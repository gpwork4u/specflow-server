package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"

	"github.com/specflow-n8n/internal/config"
	"github.com/specflow-n8n/internal/llm"
	wf "github.com/specflow-n8n/internal/workflow"
)

// API server provides HTTP endpoints to trigger and query SpecFlow workflows.
func main() {
	cfg := config.Load()

	c, err := client.Dial(client.Options{
		HostPort: cfg.TemporalAddress,
	})
	if err != nil {
		log.Fatalf("Unable to create Temporal client: %v", err)
	}
	defer c.Close()

	// POST /api/start — Start a new SpecFlow pipeline
	http.HandleFunc("/api/start", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}

		var input wf.SpecFlowInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if input.BaseBranch == "" {
			input.BaseBranch = "main"
		}

		workflowID := fmt.Sprintf("specflow-%s-%d", strings.ReplaceAll(input.Repo, "/", "-"), time.Now().UnixNano())

		run, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: config.OrchestratorQueue,
		}, wf.SpecFlowWorkflow, input)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]string{
			"workflowId": run.GetID(),
			"runId":      run.GetRunID(),
		})
	})

	// GET /api/status?workflowId=xxx — Query pipeline status
	http.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		workflowID := r.URL.Query().Get("workflowId")
		if workflowID == "" {
			http.Error(w, "workflowId required", http.StatusBadRequest)
			return
		}

		resp, err := c.QueryWorkflow(context.Background(), workflowID, "", "status")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var status wf.PipelineStatus
		if err := resp.Get(&status); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(status)
	})

	// POST /api/approve?workflowId=xxx — Signal approval to workflow
	http.HandleFunc("/api/approve", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}

		workflowID := r.URL.Query().Get("workflowId")
		if workflowID == "" {
			http.Error(w, "workflowId required", http.StatusBadRequest)
			return
		}

		err := c.SignalWorkflow(context.Background(), workflowID, "", "approval", true)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]string{"status": "approved"})
	})

	// POST /api/resume — Resume a pipeline from a specific phase
	// Body: {"repo":"...", "baseBranch":"...", "resumeFromPhase":"qa", "resumeData":{...}}
	http.HandleFunc("/api/resume", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}

		var input wf.SpecFlowInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if input.ResumeFromPhase == "" {
			http.Error(w, "resumeFromPhase is required (spec, plan, implement, qa, verify)", http.StatusBadRequest)
			return
		}

		workflowID := fmt.Sprintf("specflow-%s-resume-%d",
			strings.ReplaceAll(input.Repo, "/", "-"), time.Now().UnixNano())

		run, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: config.OrchestratorQueue,
		}, wf.SpecFlowWorkflow, input)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]string{
			"workflowId":      run.GetID(),
			"runId":           run.GetRunID(),
			"resumeFromPhase": input.ResumeFromPhase,
		})
	})

	// POST /api/webhook/github — GitHub Webhook handler
	// Triggers a SpecFlow pipeline when an Issue with label "specflow" is created
	webhookSecret := os.Getenv("GITHUB_WEBHOOK_SECRET")
	http.HandleFunc("/api/webhook/github", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}

		// Read body for HMAC verification
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Verify webhook signature if secret is configured
		if webhookSecret != "" {
			sig := r.Header.Get("X-Hub-Signature-256")
			if sig == "" {
				http.Error(w, "missing signature", http.StatusUnauthorized)
				return
			}
			mac := hmac.New(sha256.New, []byte(webhookSecret))
			mac.Write(body)
			expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
			if !hmac.Equal([]byte(sig), []byte(expected)) {
				http.Error(w, "invalid signature", http.StatusUnauthorized)
				return
			}
		}

		event := r.Header.Get("X-GitHub-Event")
		if event != "issues" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "ignored event: "+event)
			return
		}

		var payload struct {
			Action string `json:"action"`
			Issue  struct {
				Number int    `json:"number"`
				Title  string `json:"title"`
				Body   string `json:"body"`
				Labels []struct {
					Name string `json:"name"`
				} `json:"labels"`
			} `json:"issue"`
			Repository struct {
				FullName      string `json:"full_name"`
				DefaultBranch string `json:"default_branch"`
			} `json:"repository"`
		}

		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Only trigger on issue opened/labeled with "specflow" label
		if payload.Action != "opened" && payload.Action != "labeled" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "ignored action: "+payload.Action)
			return
		}

		hasLabel := false
		for _, l := range payload.Issue.Labels {
			if l.Name == "specflow" {
				hasLabel = true
				break
			}
		}
		if !hasLabel {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "no specflow label")
			return
		}

		// Trigger SpecFlow pipeline
		workflowID := fmt.Sprintf("specflow-%s-issue-%d-%d",
			strings.ReplaceAll(payload.Repository.FullName, "/", "-"),
			payload.Issue.Number, time.Now().UnixNano())
		baseBranch := payload.Repository.DefaultBranch
		if baseBranch == "" {
			baseBranch = "main"
		}

		input := wf.SpecFlowInput{
			Repo:            payload.Repository.FullName,
			BaseBranch:      baseBranch,
			UserRequirement: fmt.Sprintf("GitHub Issue #%d: %s\n\n%s", payload.Issue.Number, payload.Issue.Title, payload.Issue.Body),
		}

		run, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: config.OrchestratorQueue,
		}, wf.SpecFlowWorkflow, input)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("Pipeline triggered by Issue #%d: %s (workflow: %s)",
			payload.Issue.Number, payload.Issue.Title, run.GetID())

		json.NewEncoder(w).Encode(map[string]string{
			"workflowId": run.GetID(),
			"runId":      run.GetRunID(),
			"trigger":    fmt.Sprintf("issue #%d", payload.Issue.Number),
		})
	})

	// POST /api/reject?workflowId=xxx — Signal rejection to workflow
	http.HandleFunc("/api/reject", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		workflowID := r.URL.Query().Get("workflowId")
		if workflowID == "" {
			http.Error(w, "workflowId required", http.StatusBadRequest)
			return
		}
		err := c.SignalWorkflow(context.Background(), workflowID, "", "approval", false)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "rejected"})
	})

	// POST /api/cancel?workflowId=xxx — Cancel a running workflow
	http.HandleFunc("/api/cancel", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		workflowID := r.URL.Query().Get("workflowId")
		if workflowID == "" {
			http.Error(w, "workflowId required", http.StatusBadRequest)
			return
		}
		err := c.CancelWorkflow(context.Background(), workflowID, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
	})

	// GET /api/health — Health check
	http.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// ============================================
	// Spec Discussion (interactive chat)
	// ============================================

	// Chat sessions: sessionId -> ChatSession
	specSessions := make(map[string]*llm.ChatSession)
	var sessionsMu sync.Mutex

	specSystemPrompt := `你是一位資深的產品規格專家（Spec Writer）。
透過結構化的對話，將使用者的需求轉化為精確的技術規格文件。

## 對話規則
1. 不問開放式問題 — 每次提供 2-4 個選項讓使用者選擇
2. 每次最多問 3 個問題
3. 漸進式深入 — 從概覽到細節
4. 使用者確認後才進入下一個模組

## 對話階段
Phase 1: 確認專案概覽（名稱、目標使用者、核心功能）
Phase 2: 逐一深入每個功能（API、資料模型、WHEN/THEN 場景）
Phase 3: 非功能需求（效能、安全、部署）
Phase 4: 總結確認 — 輸出完整規格

## 回應格式
每次回應包含：
1. 對使用者選擇的確認摘要
2. 下一步的選擇題（以 A/B/C/D 格式呈現）
3. 目前的完成進度（例如：功能 2/5 已確認）

當所有功能都確認完畢，輸出完整的規格文件，使用 WHEN/THEN 格式。`

	// POST /api/spec/start — Start a new spec discussion session
	http.HandleFunc("/api/spec/start", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Repo        string `json:"repo"`
			Requirement string `json:"requirement"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		sessionID := fmt.Sprintf("spec-%d", time.Now().UnixNano())
		llmClient := llm.NewClientFromConfig(cfg.LLMProviderConfig())
		session := llm.NewChatSession(llmClient, specSystemPrompt)

		sessionsMu.Lock()
		specSessions[sessionID] = session
		sessionsMu.Unlock()

		// Send initial message
		initialMsg := fmt.Sprintf("我要為 %s 建立規格。需求描述：\n%s", req.Repo, req.Requirement)
		reply, err := session.Send(r.Context(), initialMsg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"sessionId": sessionID,
			"reply":     reply,
			"messages":  session.GetMessages(),
		})
	})

	// POST /api/spec/chat — Send a message in an existing spec session
	http.HandleFunc("/api/spec/chat", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			SessionID string `json:"sessionId"`
			Message   string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		sessionsMu.Lock()
		session, ok := specSessions[req.SessionID]
		sessionsMu.Unlock()
		if !ok {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}

		reply, err := session.Send(r.Context(), req.Message)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"reply":    reply,
			"messages": session.GetMessages(),
		})
	})

	// POST /api/spec/confirm — Confirm specs and start pipeline
	http.HandleFunc("/api/spec/confirm", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			SessionID  string `json:"sessionId"`
			Repo       string `json:"repo"`
			BaseBranch string `json:"baseBranch"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		sessionsMu.Lock()
		session, ok := specSessions[req.SessionID]
		sessionsMu.Unlock()
		if !ok {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}

		// Get the final specs from the last assistant message
		specs := session.GetLastAssistantMessage()
		if specs == "" {
			http.Error(w, "no specs generated yet", http.StatusBadRequest)
			return
		}

		baseBranch := req.BaseBranch
		if baseBranch == "" {
			baseBranch = "main"
		}

		// Start pipeline from Phase 2 (plan), skipping spec writer since we already have specs
		workflowID := fmt.Sprintf("specflow-%s-%d",
			strings.ReplaceAll(req.Repo, "/", "-"), time.Now().UnixNano())

		input := wf.SpecFlowInput{
			Repo:            req.Repo,
			BaseBranch:      baseBranch,
			ResumeFromPhase: "plan",
			ResumeData:      &wf.SpecFlowOutput{Specs: specs},
		}

		run, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: config.OrchestratorQueue,
		}, wf.SpecFlowWorkflow, input)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Clean up session
		sessionsMu.Lock()
		delete(specSessions, req.SessionID)
		sessionsMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"workflowId": run.GetID(),
			"runId":      run.GetRunID(),
			"specs":      specs[:min(len(specs), 500)],
		})
	})

	// GET /api/workflow/result?workflowId=xxx — Get full pipeline result (specs, QA reports, verification)
	// Used by dashboard to show QA report review before approval
	http.HandleFunc("/api/workflow/result", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			return
		}
		workflowID := r.URL.Query().Get("workflowId")
		if workflowID == "" {
			http.Error(w, "workflowId required", http.StatusBadRequest)
			return
		}

		resp, err := c.QueryWorkflow(context.Background(), workflowID, "", "result")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var result wf.SpecFlowOutput
		if err := resp.Get(&result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	// ============================================
	// Dashboard API endpoints
	// ============================================

	// GET /api/workflows — List recent SpecFlow workflows
	http.HandleFunc("/api/workflows", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			return
		}

		query := r.URL.Query().Get("query")
		if query == "" {
			query = "WorkflowType = 'SpecFlowWorkflow' ORDER BY StartTime DESC"
		}

		resp, err := c.ListWorkflow(r.Context(), &workflowservice.ListWorkflowExecutionsRequest{
			Namespace: "default",
			PageSize:  50,
			Query:     query,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		type WorkflowSummary struct {
			WorkflowID string `json:"workflowId"`
			RunID      string `json:"runId"`
			Status     string `json:"status"`
			StartTime  string `json:"startTime"`
			CloseTime  string `json:"closeTime,omitempty"`
		}

		var results []WorkflowSummary
		for _, exec := range resp.Executions {
			ws := WorkflowSummary{
				WorkflowID: exec.Execution.WorkflowId,
				RunID:      exec.Execution.RunId,
				Status:     exec.Status.String(),
				StartTime:  exec.StartTime.AsTime().Format(time.RFC3339),
			}
			if exec.CloseTime != nil {
				ws.CloseTime = exec.CloseTime.AsTime().Format(time.RFC3339)
			}
			results = append(results, ws)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	})

	// GET /api/workflow/events?workflowId=xxx — Get workflow event history
	http.HandleFunc("/api/workflow/events", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		workflowID := r.URL.Query().Get("workflowId")
		runID := r.URL.Query().Get("runId")
		if workflowID == "" {
			http.Error(w, "workflowId required", http.StatusBadRequest)
			return
		}

		iter := c.GetWorkflowHistory(r.Context(), workflowID, runID,
			false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)

		scheduledActivities := make(map[int64]string)

		type EventEntry struct {
			EventID      int64           `json:"eventId"`
			EventType    string          `json:"eventType"`
			Timestamp    string          `json:"timestamp"`
			ActivityType string          `json:"activityType,omitempty"`
			ActivityID   string          `json:"activityId,omitempty"`
			TaskQueue    string          `json:"taskQueue,omitempty"`
			Result       json.RawMessage `json:"result,omitempty"`
			FailureMsg   string          `json:"failureMessage,omitempty"`
			Identity     string          `json:"identity,omitempty"`
		}

		var events []EventEntry
		for iter.HasNext() {
			event, err := iter.Next()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			entry := EventEntry{
				EventID:   event.EventId,
				EventType: event.EventType.String(),
				Timestamp: event.EventTime.AsTime().Format(time.RFC3339Nano),
			}

			switch event.EventType {
			case enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
				attrs := event.GetActivityTaskScheduledEventAttributes()
				entry.ActivityType = attrs.ActivityType.GetName()
				entry.ActivityID = attrs.ActivityId
				entry.TaskQueue = attrs.TaskQueue.GetName()
				scheduledActivities[event.EventId] = attrs.ActivityType.GetName()
			case enumspb.EVENT_TYPE_ACTIVITY_TASK_STARTED:
				attrs := event.GetActivityTaskStartedEventAttributes()
				entry.ActivityType = scheduledActivities[attrs.ScheduledEventId]
				entry.Identity = attrs.Identity
			case enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
				attrs := event.GetActivityTaskCompletedEventAttributes()
				entry.ActivityType = scheduledActivities[attrs.ScheduledEventId]
				if attrs.Result != nil && len(attrs.Result.Payloads) > 0 {
					entry.Result = attrs.Result.Payloads[0].Data
				}
			case enumspb.EVENT_TYPE_ACTIVITY_TASK_FAILED:
				attrs := event.GetActivityTaskFailedEventAttributes()
				entry.ActivityType = scheduledActivities[attrs.ScheduledEventId]
				if attrs.Failure != nil {
					entry.FailureMsg = attrs.Failure.Message
				}
			default:
				continue // skip non-activity events for dashboard
			}

			events = append(events, entry)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(events)
	})

	// GET /api/workflow/logs?workflowId=xxx — SSE stream of real-time agent logs
	http.HandleFunc("/api/workflow/logs", func(w http.ResponseWriter, r *http.Request) {
		workflowID := r.URL.Query().Get("workflowId")
		runID := r.URL.Query().Get("runId")
		if workflowID == "" {
			http.Error(w, "workflowId required", http.StatusBadRequest)
			return
		}

		// SSE headers
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		// Long-poll: blocks until new events arrive
		iter := c.GetWorkflowHistory(r.Context(), workflowID, runID,
			true, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)

		scheduledActivities := make(map[int64]string)

		for iter.HasNext() {
			event, err := iter.Next()
			if err != nil {
				fmt.Fprintf(w, "event: error\ndata: %q\n\n", err.Error())
				flusher.Flush()
				return
			}

			logEntry := map[string]any{
				"eventId":   event.EventId,
				"eventType": event.EventType.String(),
				"timestamp": event.EventTime.AsTime().Format(time.RFC3339Nano),
			}

			emit := false
			switch event.EventType {
			case enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
				attrs := event.GetActivityTaskScheduledEventAttributes()
				scheduledActivities[event.EventId] = attrs.ActivityType.GetName()
				logEntry["activityType"] = attrs.ActivityType.GetName()
				logEntry["taskQueue"] = attrs.TaskQueue.GetName()
				logEntry["phase"] = queueToPhase(attrs.TaskQueue.GetName())
				emit = true

			case enumspb.EVENT_TYPE_ACTIVITY_TASK_STARTED:
				attrs := event.GetActivityTaskStartedEventAttributes()
				logEntry["activityType"] = scheduledActivities[attrs.ScheduledEventId]
				logEntry["identity"] = attrs.Identity
				emit = true

			case enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
				attrs := event.GetActivityTaskCompletedEventAttributes()
				logEntry["activityType"] = scheduledActivities[attrs.ScheduledEventId]
				if attrs.Result != nil && len(attrs.Result.Payloads) > 0 {
					logEntry["result"] = json.RawMessage(attrs.Result.Payloads[0].Data)
				}
				emit = true

			case enumspb.EVENT_TYPE_ACTIVITY_TASK_FAILED:
				attrs := event.GetActivityTaskFailedEventAttributes()
				logEntry["activityType"] = scheduledActivities[attrs.ScheduledEventId]
				if attrs.Failure != nil {
					logEntry["failure"] = attrs.Failure.Message
				}
				emit = true

			case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED,
				enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED,
				enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT,
				enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED:
				logEntry["terminal"] = true
				emit = true
			}

			if !emit {
				continue
			}

			data, _ := json.Marshal(logEntry)
			fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
			flusher.Flush()
		}

		fmt.Fprintf(w, "event: done\ndata: {\"status\":\"completed\"}\n\n")
		flusher.Flush()
	})

	// Serve dashboard static file
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, "web/index.html")
	})

	port := os.Getenv("API_PORT")
	if port == "" {
		port = "8090"
	}

	log.Printf("API server starting on :%s", port)
	log.Printf("Dashboard: http://localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func setCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func queueToPhase(queue string) string {
	switch queue {
	case config.SpecWriterQueue:
		return "spec"
	case config.TechLeadQueue:
		return "plan"
	case config.GolangAgentQueue, config.NestJSAgentQueue, config.FrontendAgentQueue:
		return "implement"
	case config.QAAgentQueue:
		return "qa"
	case config.VerifierQueue:
		return "verify"
	default:
		return "unknown"
	}
}
