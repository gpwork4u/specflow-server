package config

import "github.com/specflow-n8n/internal/llm"

// LLMProviderConfig converts Config into an llm.ProviderConfig.
func (c Config) LLMProviderConfig() llm.ProviderConfig {
	return llm.ProviderConfig{
		Provider:        llm.ProviderType(c.LLMProvider),
		BaseURL:         c.LLMBaseURL,
		APIKey:          c.LLMAPIKey,
		Model:           c.LLMModel,
		AzureDeployment: c.AzureDeployment,
		AzureAPIVersion: c.AzureAPIVersion,
	}
}
