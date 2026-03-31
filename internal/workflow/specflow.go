package workflow

import (
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/specflow-n8n/internal/activities"
	"github.com/specflow-n8n/internal/config"
)

const MaxBugFixAttempts = 3

// SpecFlowInput is the top-level input for the SpecFlow pipeline.
type SpecFlowInput struct {
	Repo            string `json:"repo"`
	BaseBranch      string `json:"baseBranch"`
	UserRequirement string `json:"userRequirement"`
	ProjectContext  string `json:"projectContext"`
	// ResumeFromPhase allows restarting from a specific phase (e.g. "implement", "qa").
	// Leave empty to run full pipeline.
	ResumeFromPhase string `json:"resumeFromPhase,omitempty"`
	// ResumeData carries data from previous run when resuming.
	ResumeData *SpecFlowOutput `json:"resumeData,omitempty"`
}

// SpecFlowOutput is the final result of the pipeline.
type SpecFlowOutput struct {
	Specs           string                      `json:"specs"`
	Plan            string                      `json:"plan"`
	Tasks           []activities.TaskDef         `json:"tasks"`
	Waves           []activities.WaveDef         `json:"waves"`
	EngineerResults []activities.EngineerOutput  `json:"engineerResults"`
	QAResults       []activities.QAOutput        `json:"qaResults"`
	BugFixResults   []activities.BugFixOutput    `json:"bugFixResults"`
	Verification    activities.VerifierOutput    `json:"verification"`
}

// PipelineStatus tracks the current phase for queries.
type PipelineStatus struct {
	Phase          string `json:"phase"`
	CurrentWave    int    `json:"currentWave"`
	TotalWaves     int    `json:"totalWaves"`
	TasksDone      int    `json:"tasksDone"`
	TasksTotal     int    `json:"tasksTotal"`
	BugFixAttempt  int    `json:"bugFixAttempt"`
	BugsRemaining  int    `json:"bugsRemaining"`
	Message        string `json:"message"`
}

// SpecFlowWorkflow is the main orchestrator workflow.
func SpecFlowWorkflow(ctx workflow.Context, input SpecFlowInput) (*SpecFlowOutput, error) {
	logger := workflow.GetLogger(ctx)

	// Initialize output (or resume from previous run)
	output := &SpecFlowOutput{}
	if input.ResumeData != nil {
		output = input.ResumeData
	}

	// ---- Status tracking via Query ----
	status := PipelineStatus{Phase: "init"}
	_ = workflow.SetQueryHandler(ctx, "status", func() (PipelineStatus, error) {
		return status, nil
	})

	// ---- Signals ----
	approvalCh := workflow.GetSignalChannel(ctx, "approval")
	// "resume" signal: send any value to retry the current phase
	resumeCh := workflow.GetSignalChannel(ctx, "resume")
	_ = resumeCh // used in bug fix loop

	retryPolicy := &temporal.RetryPolicy{
		MaximumAttempts: 3,
	}

	// Determine starting phase
	startPhase := phaseOrder(input.ResumeFromPhase)

	// ====================================================
	// Phase 1: Spec Writer
	// ====================================================
	if startPhase <= 1 {
		status.Phase = "spec"
		status.Message = "收集需求中..."
		logger.Info("Phase 1: Spec Writer")

		specCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			TaskQueue:           config.SpecWriterQueue,
			StartToCloseTimeout: 5 * time.Minute,
			RetryPolicy:         retryPolicy,
		})

		var specResult activities.SpecWriterOutput
		err := workflow.ExecuteActivity(specCtx, "WriteSpec", activities.SpecWriterInput{
			Repo:            input.Repo,
			UserRequirement: input.UserRequirement,
			ProjectContext:  input.ProjectContext,
		}).Get(ctx, &specResult)
		if err != nil {
			return output, fmt.Errorf("spec writer: %w", err)
		}
		output.Specs = specResult.Specs
	}

	// ====================================================
	// Phase 2: Tech Lead Planning
	// ====================================================
	if startPhase <= 2 {
		status.Phase = "plan"
		status.Message = "技術規劃中..."
		logger.Info("Phase 2: Tech Lead")

		planCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			TaskQueue:           config.TechLeadQueue,
			StartToCloseTimeout: 5 * time.Minute,
			RetryPolicy:         retryPolicy,
		})

		var planResult activities.TechLeadOutput
		err := workflow.ExecuteActivity(planCtx, "Plan", activities.TechLeadInput{
			Repo:       input.Repo,
			BaseBranch: input.BaseBranch,
			Specs:      output.Specs,
		}).Get(ctx, &planResult)
		if err != nil {
			return output, fmt.Errorf("tech lead: %w", err)
		}
		output.Plan = planResult.Plan
		output.Tasks = planResult.Tasks
		output.Waves = planResult.Waves
	}

	status.TotalWaves = len(output.Waves)
	status.TasksTotal = len(output.Tasks)

	// ====================================================
	// Phase 3: Implementation (parallel by wave)
	// ====================================================
	if startPhase <= 3 {
		status.Phase = "implement"
		logger.Info("Phase 3: Implementation", "waves", len(output.Waves))

		// Build task lookup map
		taskMap := make(map[string]activities.TaskDef)
		for _, t := range output.Tasks {
			taskMap[t.ID] = t
		}

		for _, wave := range output.Waves {
			status.CurrentWave = wave.Wave
			status.Message = fmt.Sprintf("Wave %d/%d 實作中...", wave.Wave, len(output.Waves))

			var futures []workflow.Future
			for _, taskID := range wave.Tasks {
				task, ok := taskMap[taskID]
				if !ok {
					logger.Warn("Task not found", "taskId", taskID)
					continue
				}

				queue := agentTypeToQueue(task.AgentType)
				engCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
					TaskQueue:           queue,
					StartToCloseTimeout: 10 * time.Minute,
					HeartbeatTimeout:    2 * time.Minute,
					RetryPolicy:         retryPolicy,
				})

				future := workflow.ExecuteActivity(engCtx, "Implement", activities.EngineerInput{
					Repo:            input.Repo,
					BaseBranch:      input.BaseBranch,
					TaskID:          task.ID,
					TaskDescription: task.Description,
					Specs:           output.Specs,
					Plan:            output.Plan,
				})
				futures = append(futures, future)
			}

			for _, future := range futures {
				var result activities.EngineerOutput
				if err := future.Get(ctx, &result); err != nil {
					logger.Error("Task failed", "error", err)
					continue
				}
				output.EngineerResults = append(output.EngineerResults, result)
				status.TasksDone++
			}
		}
	}

	// ====================================================
	// Phase 4: QA Review + Bug Fix Loop
	// ====================================================
	if startPhase <= 4 {
		status.Phase = "qa"
		logger.Info("Phase 4: QA + Bug Fix Loop")

		// Run QA → Bug Fix → QA cycle for each PR
		for i, eng := range output.EngineerResults {
			if eng.PRNumber == 0 {
				continue
			}

			qaResult, err := runQABugFixLoop(ctx, qaLoopInput{
				repo:          input.Repo,
				baseBranch:    input.BaseBranch,
				eng:           eng,
				specs:         output.Specs,
				taskIndex:     i,
				status:        &status,
				retryPolicy:   retryPolicy,
				logger:        logger,
			})
			if err != nil {
				logger.Error("QA loop failed", "pr", eng.PRNumber, "error", err)
			}
			output.QAResults = append(output.QAResults, qaResult)
		}
	}

	// ====================================================
	// Phase 5: Verification
	// ====================================================
	if startPhase <= 5 {
		status.Phase = "verify"
		status.Message = "三維度驗證中..."
		logger.Info("Phase 5: Verification")

		var sb strings.Builder
		for _, qa := range output.QAResults {
			sb.WriteString(qa.Summary)
			sb.WriteString("\n---\n")
		}

		verifyCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			TaskQueue:           config.VerifierQueue,
			StartToCloseTimeout: 5 * time.Minute,
			RetryPolicy:         retryPolicy,
		})

		err := workflow.ExecuteActivity(verifyCtx, "Verify", activities.VerifierInput{
			Repo:     input.Repo,
			Branch:   input.BaseBranch,
			Specs:    output.Specs,
			Plan:     output.Plan,
			QAReport: sb.String(),
		}).Get(ctx, &output.Verification)
		if err != nil {
			return output, fmt.Errorf("verifier: %w", err)
		}
	}

	// ====================================================
	// Phase 6: Approval (human-in-the-loop)
	// ====================================================
	status.Phase = "approval"
	status.Message = fmt.Sprintf("驗證結果: %s — 等待人工確認 (24h timeout)", output.Verification.Verdict)
	logger.Info("Phase 6: Waiting for approval", "verdict", output.Verification.Verdict)

	var approved bool
	timerCtx, cancel := workflow.WithCancel(ctx)
	defer cancel()

	timerFuture := workflow.NewTimer(timerCtx, 24*time.Hour)
	selector := workflow.NewSelector(ctx)

	selector.AddReceive(approvalCh, func(ch workflow.ReceiveChannel, more bool) {
		ch.Receive(ctx, &approved)
	})
	selector.AddFuture(timerFuture, func(f workflow.Future) {
		approved = false
		logger.Warn("Approval timed out after 24 hours")
	})
	selector.Select(ctx)

	if !approved {
		status.Phase = "rejected"
		status.Message = "已拒絕（或超時）"
		return output, fmt.Errorf("pipeline rejected or timed out")
	}

	status.Phase = "done"
	status.Message = "完成!"
	return output, nil
}

// ---- Bug Fix Loop ----

type qaLoopInput struct {
	repo        string
	baseBranch  string
	eng         activities.EngineerOutput
	specs       string
	taskIndex   int
	status      *PipelineStatus
	retryPolicy *temporal.RetryPolicy
	logger      log.Logger
}

// runQABugFixLoop runs QA, and if bugs are found, sends them to engineer for fixing,
// then re-runs QA. Repeats up to MaxBugFixAttempts times.
func runQABugFixLoop(ctx workflow.Context, in qaLoopInput) (activities.QAOutput, error) {
	var lastQA activities.QAOutput

	for attempt := 0; attempt <= MaxBugFixAttempts; attempt++ {
		// ---- Run QA ----
		in.status.Message = fmt.Sprintf("QA 審查 PR #%d (attempt %d)...", in.eng.PRNumber, attempt+1)
		in.logger.Info("QA review", "pr", in.eng.PRNumber, "attempt", attempt+1)

		qaCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			TaskQueue:           config.QAAgentQueue,
			StartToCloseTimeout: 5 * time.Minute,
			RetryPolicy:         in.retryPolicy,
		})

		err := workflow.ExecuteActivity(qaCtx, "Review", activities.QAInput{
			Repo:          in.repo,
			BaseBranch:    in.baseBranch,
			FeatureBranch: in.eng.Branch,
			PRNumber:      in.eng.PRNumber,
			Specs:         in.specs,
			TaskID:        fmt.Sprintf("task-%d", in.taskIndex),
		}).Get(ctx, &lastQA)
		if err != nil {
			return lastQA, fmt.Errorf("qa review: %w", err)
		}

		// ---- Check if QA passed ----
		criticalOrMajor := countSevereBugs(lastQA.BugsFound)
		in.status.BugsRemaining = len(lastQA.BugsFound)

		if lastQA.Status == "PASS" || criticalOrMajor == 0 {
			in.logger.Info("QA passed", "pr", in.eng.PRNumber)
			return lastQA, nil
		}

		// ---- QA failed: attempt bug fix ----
		if attempt >= MaxBugFixAttempts {
			in.logger.Warn("Max bug fix attempts reached", "pr", in.eng.PRNumber, "bugs", len(lastQA.BugsFound))
			lastQA.Summary += fmt.Sprintf("\n\n⚠️ 達到最大修復次數 (%d)，仍有 %d 個 bug 未修復，需人工介入。",
				MaxBugFixAttempts, len(lastQA.BugsFound))
			return lastQA, nil
		}

		in.status.Phase = "bugfix"
		in.status.BugFixAttempt = attempt + 1
		in.status.Message = fmt.Sprintf("修復 Bug (第 %d/%d 次): PR #%d, %d 個 bug",
			attempt+1, MaxBugFixAttempts, in.eng.PRNumber, len(lastQA.BugsFound))
		in.logger.Info("Bug fix attempt", "pr", in.eng.PRNumber, "attempt", attempt+1, "bugs", len(lastQA.BugsFound))

		// Route to the same agent type that created the PR
		// (inferred from task; default to golang)
		fixCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			TaskQueue:           config.GolangAgentQueue, // TODO: route based on original agentType
			StartToCloseTimeout: 10 * time.Minute,
			HeartbeatTimeout:    2 * time.Minute,
			RetryPolicy:         in.retryPolicy,
		})

		var fixResult activities.BugFixOutput
		err = workflow.ExecuteActivity(fixCtx, "FixBugs", activities.BugFixInput{
			Repo:          in.repo,
			BaseBranch:    in.baseBranch,
			FeatureBranch: in.eng.Branch,
			PRNumber:      in.eng.PRNumber,
			TaskID:        fmt.Sprintf("task-%d", in.taskIndex),
			Bugs:          lastQA.BugsFound,
			Specs:         in.specs,
			Attempt:       attempt + 1,
		}).Get(ctx, &fixResult)
		if err != nil {
			in.logger.Error("Bug fix failed", "error", err)
			lastQA.Summary += fmt.Sprintf("\n\n❌ Bug fix 第 %d 次嘗試失敗: %v", attempt+1, err)
			return lastQA, nil
		}

		in.logger.Info("Bug fix completed", "fixed", len(fixResult.FixedBugs), "files", len(fixResult.FilesChanged))
		// Loop back to QA
	}

	return lastQA, nil
}

func countSevereBugs(bugs []activities.BugDef) int {
	count := 0
	for _, b := range bugs {
		if b.Severity == "critical" || b.Severity == "major" {
			count++
		}
	}
	return count
}

func phaseOrder(phase string) int {
	switch phase {
	case "spec":
		return 1
	case "plan":
		return 2
	case "implement":
		return 3
	case "qa":
		return 4
	case "verify":
		return 5
	default:
		return 0 // run all
	}
}

func agentTypeToQueue(agentType activities.AgentType) string {
	switch agentType {
	case activities.AgentGolang:
		return config.GolangAgentQueue
	case activities.AgentNestJS:
		return config.NestJSAgentQueue
	case activities.AgentFrontend:
		return config.FrontendAgentQueue
	default:
		return config.GolangAgentQueue
	}
}
