package llm

import (
	"context"
	"encoding/json"
	"fmt"
)

// ToolHandler is a function that executes a tool call and returns the result.
type ToolHandler func(ctx context.Context, arguments string) (string, error)

// Agent implements the agentic loop: LLM → tool calls → LLM → ... → final response.
type Agent struct {
	client       *Client
	systemPrompt string
	tools        []Tool
	handlers     map[string]ToolHandler
	maxIter      int
}

func NewAgent(client *Client, systemPrompt string, maxIter int) *Agent {
	if maxIter <= 0 {
		maxIter = 15
	}
	return &Agent{
		client:       client,
		systemPrompt: systemPrompt,
		handlers:     make(map[string]ToolHandler),
		maxIter:      maxIter,
	}
}

// RegisterTool adds a tool the agent can use.
func (a *Agent) RegisterTool(name, description string, parameters json.RawMessage, handler ToolHandler) {
	a.tools = append(a.tools, Tool{
		Name:        name,
		Description: description,
		Parameters:  parameters,
	})
	a.handlers[name] = handler
}

// Run executes the agentic loop with the given user message.
// Returns the final text response from the LLM.
func (a *Agent) Run(ctx context.Context, userMessage string) (string, error) {
	messages := []Message{
		{Role: "user", Content: userMessage},
	}

	for i := 0; i < a.maxIter; i++ {
		resp, err := a.client.Chat(ctx, ChatRequest{
			SystemPrompt: a.systemPrompt,
			Messages:     messages,
			Tools:        a.tools,
			MaxTokens:    8192,
			Temperature:  0.2,
		})
		if err != nil {
			return "", fmt.Errorf("agent iteration %d: %w", i, err)
		}

		// No tool calls → agent is done
		if len(resp.ToolCalls) == 0 {
			return resp.Content, nil
		}

		// Append assistant message with tool calls
		messages = append(messages, Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Execute each tool call
		for _, tc := range resp.ToolCalls {
			handler, ok := a.handlers[tc.Name]
			if !ok {
				messages = append(messages, Message{
					Role:       "tool",
					Content:    fmt.Sprintf("error: unknown tool %q", tc.Name),
					ToolCallID: tc.ID,
				})
				continue
			}

			result, err := handler(ctx, tc.Arguments)
			if err != nil {
				messages = append(messages, Message{
					Role:       "tool",
					Content:    fmt.Sprintf("error: %v", err),
					ToolCallID: tc.ID,
				})
				continue
			}

			messages = append(messages, Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	return "", fmt.Errorf("agent exceeded max iterations (%d)", a.maxIter)
}
