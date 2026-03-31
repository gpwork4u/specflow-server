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

	c, err := client.Dial(client.Options{
		HostPort: cfg.TemporalAddress,
	})
	if err != nil {
		log.Fatalf("Unable to create Temporal client: %v", err)
	}
	defer c.Close()

	w := worker.New(c, config.NestJSAgentQueue, worker.Options{
		Identity: "nestjs-agent-worker",
	})

	eng := &activities.EngineerActivities{Cfg: cfg, AgentType: activities.AgentNestJS}
	w.RegisterActivity(eng.Implement)

	log.Printf("NestJS agent worker started on queue: %s", config.NestJSAgentQueue)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("Worker failed: %v", err)
	}
}
