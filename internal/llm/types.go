package llm

import (
	"context"
	"encoding/json"
)

// Message is a chat message exchanged with the LLM.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// FunctionCall carries the assistant's tool invocation (GigaChat-style
	// protocol). Populated on messages sent back after the model asked to
	// call a function; the result then follows as Role="function" with Name.
	FunctionCall *FunctionCall `json:"function_call,omitempty"`
	// Name is the function name for result messages (Role="function").
	Name string `json:"name,omitempty"`
	// FunctionsStateID is returned by GigaChat together with a function_call.
	// It must be echoed back in the assistant message when sending the tool
	// result, otherwise the model does not link the result to its call and
	// keeps re-invoking the same function.
	FunctionsStateID string `json:"functions_state_id,omitempty"`
}

// FunctionCall is a tool invocation requested by the LLM.
type FunctionCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// Signature returns a stable key identifying this exact tool invocation
// (name + canonical arguments), or "" when the call cannot be keyed.
func (fc *FunctionCall) Signature() string {
	if fc == nil || fc.Name == "" {
		return ""
	}
	b, err := json.Marshal(fc.Arguments)
	if err != nil {
		return fc.Name
	}
	return fc.Name + "|" + string(b)
}

// Choice is a single completion choice returned by the LLM.
type Choice struct {
	Message struct {
		Role              string        `json:"role"`
		Content           string        `json:"content"`
		FunctionCall      *FunctionCall `json:"function_call,omitempty"`
		FunctionsStateID  string        `json:"functions_state_id,omitempty"`
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
	// GigaChat 3 Ultra (xgrammar) требует, чтобы required был массивом,
	// а не null — иначе 422 "required must be an array".
	if len(required) == 0 {
		required = []string{}
	}
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