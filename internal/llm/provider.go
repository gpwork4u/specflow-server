package llm

import "context"

// Provider is the interface for LLM backends.
// All providers must support chat completion with tool calling.
type Provider interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

// ProviderType identifies the LLM backend.
type ProviderType string

const (
	ProviderOpenAI    ProviderType = "openai"    // OpenAI, Ollama, vLLM, LM Studio, Groq, Together, Mistral
	ProviderAnthropic ProviderType = "anthropic" // Claude (Messages API)
	ProviderAzure     ProviderType = "azure"     // Azure OpenAI
)

// ProviderConfig holds configuration for creating an LLM provider.
type ProviderConfig struct {
	Provider    ProviderType
	BaseURL     string
	APIKey      string
	Model       string
	MaxTokens   int
	Temperature float32

	// Azure-specific
	AzureDeployment string
	AzureAPIVersion string
}

// NewProvider creates an LLM provider based on the config.
func NewProvider(cfg ProviderConfig) Provider {
	switch cfg.Provider {
	case ProviderAnthropic:
		return newAnthropicProvider(cfg)
	case ProviderAzure:
		return newAzureProvider(cfg)
	default:
		// OpenAI-compatible: covers OpenAI, Ollama, vLLM, LM Studio, Groq, Together, Mistral, LocalAI
		return newOpenAIProvider(cfg)
	}
}
