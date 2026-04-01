package llm

import (
	"context"
	"sync"
)

// ChatSession manages a multi-turn conversation with the LLM.
// Used for interactive spec discussion before starting the pipeline.
type ChatSession struct {
	mu       sync.Mutex
	client   *Client
	system   string
	messages []Message
}

func NewChatSession(client *Client, systemPrompt string) *ChatSession {
	return &ChatSession{
		client: client,
		system: systemPrompt,
	}
}

// Send sends a user message and returns the assistant's response.
func (s *ChatSession) Send(ctx context.Context, userMessage string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages = append(s.messages, Message{Role: "user", Content: userMessage})

	resp, err := s.client.Chat(ctx, ChatRequest{
		SystemPrompt: s.system,
		Messages:     s.messages,
		MaxTokens:    4096,
		Temperature:  0.3,
	})
	if err != nil {
		return "", err
	}

	s.messages = append(s.messages, Message{Role: "assistant", Content: resp.Content})
	return resp.Content, nil
}

// GetMessages returns the full conversation history.
func (s *ChatSession) GetMessages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, len(s.messages))
	copy(out, s.messages)
	return out
}

// GetLastAssistantMessage returns the specs output (last assistant message).
func (s *ChatSession) GetLastAssistantMessage() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.messages) - 1; i >= 0; i-- {
		if s.messages[i].Role == "assistant" {
			return s.messages[i].Content
		}
	}
	return ""
}
