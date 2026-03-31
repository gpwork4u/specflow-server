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
	"time"

	"go.temporal.io/sdk/client"

	"github.com/specflow-n8n/internal/config"
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

	port := os.Getenv("API_PORT")
	if port == "" {
		port = "8090"
	}

	log.Printf("API server starting on :%s", port)
	log.Printf("Endpoints: /api/start, /api/status, /api/approve, /api/reject, /api/cancel, /api/health, /api/webhook/github")
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
