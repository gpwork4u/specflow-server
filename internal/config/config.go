package config

import (
	"fmt"
	"os"
	"strconv"
)

// Task queue names — each agent worker polls its own queue
const (
	OrchestratorQueue  = "orchestrator-queue"
	SpecWriterQueue    = "spec-writer-queue"
	TechLeadQueue      = "tech-lead-queue"
	GolangAgentQueue   = "golang-agent-queue"
	NestJSAgentQueue   = "nestjs-agent-queue"
	FrontendAgentQueue = "frontend-agent-queue"
	QAAgentQueue       = "qa-agent-queue"
	VerifierQueue      = "verifier-queue"
)

type Config struct {
	TemporalAddress string
	TaskQueue       string

	// LLM — supports multiple providers
	LLMProvider         string // "openai" (default), "anthropic", "azure"
	LLMBaseURL          string
	LLMAPIKey           string
	LLMModel            string
	AzureDeployment     string // Azure OpenAI deployment name
	AzureAPIVersion     string // Azure API version

	// GitHub
	GitHubToken string

	// Sandbox
	WorkspaceDir       string
	DockerNetwork      string
	SandboxMemory      string
	SandboxCPUs        string
	SandboxTimeoutSecs int
}

func Load() Config {
	timeout, _ := strconv.Atoi(envOrDefault("SANDBOX_TIMEOUT_SECS", "600"))
	return Config{
		TemporalAddress:    envOrDefault("TEMPORAL_ADDRESS", "localhost:7233"),
		TaskQueue:          envOrDefault("TASK_QUEUE", OrchestratorQueue),
		LLMProvider:        envOrDefault("LLM_PROVIDER", "openai"),
		LLMBaseURL:         envOrDefault("LLM_BASE_URL", "http://ollama:11434/v1"),
		LLMAPIKey:          envOrDefault("LLM_API_KEY", "ollama"),
		LLMModel:           envOrDefault("LLM_MODEL", "qwen2.5-coder:32b"),
		AzureDeployment:    os.Getenv("AZURE_DEPLOYMENT"),
		AzureAPIVersion:    envOrDefault("AZURE_API_VERSION", "2024-06-01"),
		GitHubToken:        os.Getenv("GITHUB_TOKEN"),
		WorkspaceDir:       envOrDefault("WORKSPACE_DIR", "/workspace"),
		DockerNetwork:      envOrDefault("DOCKER_NETWORK", "specflow-server_specflow"),
		SandboxMemory:      envOrDefault("SANDBOX_MEMORY", "2g"),
		SandboxCPUs:        envOrDefault("SANDBOX_CPUS", "2"),
		SandboxTimeoutSecs: timeout,
	}
}

// Validate checks that all required configuration is present.
func (c Config) Validate() error {
	if c.GitHubToken == "" {
		return fmt.Errorf("GITHUB_TOKEN is required")
	}
	if c.LLMProvider == "anthropic" && c.LLMAPIKey == "" {
		return fmt.Errorf("LLM_API_KEY is required for Anthropic provider")
	}
	if c.LLMProvider == "azure" && c.LLMBaseURL == "" {
		return fmt.Errorf("LLM_BASE_URL is required for Azure provider (your Azure endpoint)")
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
