package llm

import (
	openai "github.com/sashabaranov/go-openai"
)

// azureProvider handles Azure OpenAI Service.
// Uses the same OpenAI SDK but with Azure-specific configuration.
type azureProvider struct {
	openaiProvider // embed — same logic, different config
}

func newAzureProvider(cfg ProviderConfig) Provider {
	apiVersion := cfg.AzureAPIVersion
	if apiVersion == "" {
		apiVersion = "2024-06-01"
	}

	ocfg := openai.DefaultAzureConfig(cfg.APIKey, cfg.BaseURL)
	ocfg.AzureModelMapperFunc = func(model string) string {
		if cfg.AzureDeployment != "" {
			return cfg.AzureDeployment
		}
		return model
	}
	ocfg.APIVersion = apiVersion

	return &azureProvider{
		openaiProvider: openaiProvider{
			client: openai.NewClientWithConfig(ocfg),
			model:  cfg.Model,
		},
	}
}
