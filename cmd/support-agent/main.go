package main

import (
	"log"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/specflow-n8n/internal/activities"
	"github.com/specflow-n8n/internal/config"
)

// support-agent handles lightweight agents that don't need specialized toolchains:
// - Spec Writer (spec-writer-queue)
// - Tech Lead (tech-lead-queue)
// - Verifier (verifier-queue)
//
// Each runs as a separate Temporal worker polling its own queue,
// but they share the same Docker image and binary.
func main() {
	cfg := config.Load()

	c, err := client.Dial(client.Options{
		HostPort: cfg.TemporalAddress,
	})
	if err != nil {
		log.Fatalf("Unable to create Temporal client: %v", err)
	}
	defer c.Close()

	// Spec Writer worker
	specW := worker.New(c, config.SpecWriterQueue, worker.Options{
		Identity: "spec-writer-worker",
	})
	specWriter := &activities.SpecWriterActivities{Cfg: cfg}
	specW.RegisterActivity(specWriter.WriteSpec)

	// Tech Lead worker
	techW := worker.New(c, config.TechLeadQueue, worker.Options{
		Identity: "tech-lead-worker",
	})
	techLead := &activities.TechLeadActivities{Cfg: cfg}
	techW.RegisterActivity(techLead.Plan)

	// Verifier worker
	verifyW := worker.New(c, config.VerifierQueue, worker.Options{
		Identity: "verifier-worker",
	})
	verifier := &activities.VerifierActivities{Cfg: cfg}
	verifyW.RegisterActivity(verifier.Verify)

	// Start all three workers in the same process
	errCh := make(chan error, 3)
	go func() { errCh <- specW.Run(worker.InterruptCh()) }()
	go func() { errCh <- techW.Run(worker.InterruptCh()) }()
	go func() { errCh <- verifyW.Run(worker.InterruptCh()) }()

	log.Printf("Support agent started: spec-writer, tech-lead, verifier queues")

	if err := <-errCh; err != nil {
		log.Fatalf("Worker failed: %v", err)
	}
}
