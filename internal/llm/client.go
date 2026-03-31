package llm

import (
	"context"
	"encoding/json"
)

// Client wraps an LLM provider with a unified interface.
// Supports: OpenAI, Anthropic (Claude), Azure OpenAI, Ollama, vLLM, and any OpenAI-compatible API.
type Client struct {
	provider Provider
	model    string
}

// NewClient creates an LLM client using the OpenAI-compatible provider (backward compatible).
func NewClient(baseURL, apiKey, model string) *Client {
	return NewClientFromConfig(ProviderConfig{
		Provider: ProviderOpenAI,
		BaseURL:  baseURL,
		APIKey:   apiKey,
		Model:    model,
	})
}

// NewClientFromConfig creates an LLM client from a full provider config.
func NewClientFromConfig(cfg ProviderConfig) *Client {
	return &Client{
		provider: NewProvider(cfg),
		model:    cfg.Model,
	}
}

// Tool defines a function the LLM can call.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema
}

// Message wraps a chat message.
type Message struct {
	Role       string
	Content    string
	ToolCallID string
	ToolCalls  []ToolCall
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type ChatRequest struct {
	SystemPrompt string
	Messages     []Message
	Tools        []Tool
	MaxTokens    int
	Temperature  float32
}

type ChatResponse struct {
	Content   string
	ToolCalls []ToolCall
}

// Chat sends a chat completion request to the underlying LLM provider.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	return c.provider.Chat(ctx, req)
}
