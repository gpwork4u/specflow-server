package config

import "os"

// Task queue names — each agent worker polls its own queue
const (
	OrchestratorQueue = "orchestrator-queue"
	SpecWriterQueue   = "spec-writer-queue"
	TechLeadQueue     = "tech-lead-queue"
	GolangAgentQueue  = "golang-agent-queue"
	NestJSAgentQueue  = "nestjs-agent-queue"
	FrontendAgentQueue = "frontend-agent-queue"
	QAAgentQueue      = "qa-agent-queue"
	VerifierQueue     = "verifier-queue"
)

type Config struct {
	TemporalAddress string
	TaskQueue       string
	LLMBaseURL      string
	LLMAPIKey       string
	LLMModel        string
	GitHubToken     string
	WorkspaceDir    string
}

func Load() Config {
	return Config{
		TemporalAddress: envOrDefault("TEMPORAL_ADDRESS", "localhost:7233"),
		TaskQueue:       envOrDefault("TASK_QUEUE", OrchestratorQueue),
		LLMBaseURL:      envOrDefault("LLM_BASE_URL", "http://ollama:11434/v1"),
		LLMAPIKey:       envOrDefault("LLM_API_KEY", "ollama"),
		LLMModel:        envOrDefault("LLM_MODEL", "qwen2.5-coder:32b"),
		GitHubToken:     os.Getenv("GITHUB_TOKEN"),
		WorkspaceDir:    envOrDefault("WORKSPACE_DIR", "/workspace"),
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
