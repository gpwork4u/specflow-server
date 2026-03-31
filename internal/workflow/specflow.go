package workflow

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/specflow-n8n/internal/activities"
	"github.com/specflow-n8n/internal/config"
)

// SpecFlowInput is the top-level input for the SpecFlow pipeline.
type SpecFlowInput struct {
	Repo            string `json:"repo"`
	BaseBranch      string `json:"baseBranch"`
	UserRequirement string `json:"userRequirement"`
	ProjectContext  string `json:"projectContext"`
}

// SpecFlowOutput is the final result of the pipeline.
type SpecFlowOutput struct {
	Specs          string                      `json:"specs"`
	Plan           string                      `json:"plan"`
	EngineerResults []activities.EngineerOutput `json:"engineerResults"`
	QAResults      []activities.QAOutput        `json:"qaResults"`
	Verification   activities.VerifierOutput    `json:"verification"`
}

// PipelineStatus tracks the current phase for queries.
type PipelineStatus struct {
	Phase       string `json:"phase"`
	CurrentWave int    `json:"currentWave"`
	TotalWaves  int    `json:"totalWaves"`
	TasksDone   int    `json:"tasksDone"`
	TasksTotal  int    `json:"tasksTotal"`
	Message     string `json:"message"`
}

// SpecFlowWorkflow is the main orchestrator workflow.
// It routes activities to different task queues — each queue is served by
// a dedicated Docker container with its own toolchain.
func SpecFlowWorkflow(ctx workflow.Context, input SpecFlowInput) (*SpecFlowOutput, error) {
	logger := workflow.GetLogger(ctx)
	output := &SpecFlowOutput{}

	// ---- Status tracking via Query ----
	status := PipelineStatus{Phase: "init"}
	_ = workflow.SetQueryHandler(ctx, "status", func() (PipelineStatus, error) {
		return status, nil
	})

	// ---- Human approval Signal ----
	approvalCh := workflow.GetSignalChannel(ctx, "approval")

	retryPolicy := &temporal.RetryPolicy{
		MaximumAttempts: 3,
	}

	// ====================================================
	// Phase 1: Spec Writer
	// ====================================================
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
		return nil, fmt.Errorf("spec writer: %w", err)
	}
	output.Specs = specResult.Specs

	// ====================================================
	// Phase 2: Tech Lead Planning
	// ====================================================
	status.Phase = "plan"
	status.Message = "技術規劃中..."
	logger.Info("Phase 2: Tech Lead")

	planCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		TaskQueue:           config.TechLeadQueue,
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         retryPolicy,
	})

	var planResult activities.TechLeadOutput
	err = workflow.ExecuteActivity(planCtx, "Plan", activities.TechLeadInput{
		Repo:       input.Repo,
		BaseBranch: input.BaseBranch,
		Specs:      specResult.Specs,
	}).Get(ctx, &planResult)
	if err != nil {
		return nil, fmt.Errorf("tech lead: %w", err)
	}
	output.Plan = planResult.Plan

	status.TotalWaves = len(planResult.Waves)
	status.TasksTotal = len(planResult.Tasks)

	// ====================================================
	// Phase 3: Implementation (parallel by wave)
	// ====================================================
	status.Phase = "implement"
	logger.Info("Phase 3: Implementation", "waves", len(planResult.Waves))

	for _, wave := range planResult.Waves {
		status.CurrentWave = wave.Wave
		status.Message = fmt.Sprintf("Wave %d/%d 實作中...", wave.Wave, len(planResult.Waves))

		// Find tasks in this wave
		var waveTasks []activities.TaskDef
		for _, taskID := range wave.Tasks {
			for _, t := range planResult.Tasks {
				if t.ID == taskID {
					waveTasks = append(waveTasks, t)
				}
			}
		}

		// Fan-out: launch all tasks in this wave in parallel
		var futures []workflow.Future
		for _, task := range waveTasks {
			// Route to the correct agent queue based on agentType
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
				Specs:           specResult.Specs,
				Plan:            planResult.Plan,
			})
			futures = append(futures, future)
		}

		// Fan-in: wait for all tasks in this wave
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

	// ====================================================
	// Phase 4: QA Review
	// ====================================================
	status.Phase = "qa"
	status.Message = "QA 審查中..."
	logger.Info("Phase 4: QA")

	var qaFutures []workflow.Future
	for _, eng := range output.EngineerResults {
		if eng.PRNumber == 0 {
			continue
		}
		qaCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			TaskQueue:           config.QAAgentQueue,
			StartToCloseTimeout: 5 * time.Minute,
			RetryPolicy:         retryPolicy,
		})
		future := workflow.ExecuteActivity(qaCtx, "Review", activities.QAInput{
			Repo:          input.Repo,
			BaseBranch:    input.BaseBranch,
			FeatureBranch: eng.Branch,
			PRNumber:      eng.PRNumber,
			Specs:         specResult.Specs,
		})
		qaFutures = append(qaFutures, future)
	}

	for _, future := range qaFutures {
		var result activities.QAOutput
		if err := future.Get(ctx, &result); err != nil {
			logger.Error("QA failed", "error", err)
			continue
		}
		output.QAResults = append(output.QAResults, result)
	}

	// ====================================================
	// Phase 5: Verification
	// ====================================================
	status.Phase = "verify"
	status.Message = "三維度驗證中..."
	logger.Info("Phase 5: Verification")

	qaReportSummary := ""
	for _, qa := range output.QAResults {
		qaReportSummary += qa.Summary + "\n---\n"
	}

	verifyCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		TaskQueue:           config.VerifierQueue,
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         retryPolicy,
	})

	err = workflow.ExecuteActivity(verifyCtx, "Verify", activities.VerifierInput{
		Repo:     input.Repo,
		Branch:   input.BaseBranch,
		Specs:    specResult.Specs,
		Plan:     planResult.Plan,
		QAReport: qaReportSummary,
	}).Get(ctx, &output.Verification)
	if err != nil {
		return nil, fmt.Errorf("verifier: %w", err)
	}

	// ====================================================
	// Phase 6: Approval (human-in-the-loop)
	// ====================================================
	status.Phase = "approval"
	status.Message = fmt.Sprintf("驗證結果: %s — 等待人工確認 (24h timeout)", output.Verification.Verdict)
	logger.Info("Phase 6: Waiting for approval", "verdict", output.Verification.Verdict)

	// Wait for approval with 24-hour timeout
	var approved bool
	timerCtx, cancel := workflow.WithCancel(ctx)
	defer cancel()

	timerFuture := workflow.NewTimer(timerCtx, 24*time.Hour)
	selector := workflow.NewSelector(ctx)

	selector.AddReceive(approvalCh, func(ch workflow.ReceiveChannel, more bool) {
		ch.Receive(ctx, &approved)
	})
	selector.AddFuture(timerFuture, func(f workflow.Future) {
		// Timeout — auto-reject
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
