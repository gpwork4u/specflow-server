package main

import (
	"log"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/specflow-n8n/internal/activities"
	"github.com/specflow-n8n/internal/config"
)

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Config error: %v", err)
	}

	c, err := client.Dial(client.Options{
		HostPort: cfg.TemporalAddress,
	})
	if err != nil {
		log.Fatalf("Unable to create Temporal client: %v", err)
	}
	defer c.Close()

	w := worker.New(c, config.FrontendAgentQueue, worker.Options{
		Identity: "frontend-agent-worker",
	})

	eng := &activities.EngineerActivities{Cfg: cfg, AgentType: activities.AgentFrontend}
	w.RegisterActivity(eng.Implement)
	w.RegisterActivity(eng.FixBugs)

	log.Printf("Frontend agent worker started on queue: %s", config.FrontendAgentQueue)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("Worker failed: %v", err)
	}
}
