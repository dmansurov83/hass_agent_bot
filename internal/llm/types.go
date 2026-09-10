package llm

import (
	"context"
)

// Message is a chat message exchanged with the LLM.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// FunctionCall is a tool invocation requested by the LLM.
type FunctionCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// Choice is a single completion choice returned by the LLM.
type Choice struct {
	Message struct {
		Role         string        `json:"role"`
		Content      string        `json:"content"`
		FunctionCall *FunctionCall `json:"function_call,omitempty"`
	} `json:"message"`
	FinishReason string `json:"finish_reason"`
}

// ChatResponse is a chat completion response from the LLM.
type ChatResponse struct {
	Choices []Choice `json:"choices"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// Function is a tool/function definition declared to the LLM.
type Function struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// FunctionParameters is a helper to build JSON Schema parameters.
func FunctionParameters(props map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

// LLMClient is the common contract every supported LLM provider implements.
type LLMClient interface {
	// Chat sends a chat completion request with optional function definitions
	// and returns a parsed chat response.
	Chat(ctx context.Context, messages []Message, functions []Function) (*ChatResponse, error)
}