package main

import (
	"log"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/specflow-n8n/internal/config"
	wf "github.com/specflow-n8n/internal/workflow"
)

func main() {
	cfg := config.Load()

	c, err := client.Dial(client.Options{
		HostPort: cfg.TemporalAddress,
	})
	if err != nil {
		log.Fatalf("Unable to create Temporal client: %v", err)
	}
	defer c.Close()

	w := worker.New(c, config.OrchestratorQueue, worker.Options{
		Identity: "orchestrator-worker",
	})

	// Orchestrator only registers Workflows, not Activities.
	// Activities run on their respective agent workers.
	w.RegisterWorkflow(wf.SpecFlowWorkflow)

	log.Printf("Orchestrator worker started on queue: %s", config.OrchestratorQueue)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("Worker failed: %v", err)
	}
}
