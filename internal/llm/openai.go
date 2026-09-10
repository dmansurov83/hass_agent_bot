package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	defaultOpenAIBaseURL = "https://api.openai.com/v1"
	maxResponseBytes     = 4 << 20
)

// Options configures an LLM client. The same options struct works for both
// GigaChat and OpenAI-compatible providers.
type Options struct {
	BaseURL     string // default https://api.giga.chat for GigaChat, https://api.openai.com/v1 for OpenAI
	AuthURL     string // GigaChat only: legacy OAuth endpoint
	Credentials string // GigaChat: Authorization Key (Basic); OpenAI: API key
	Model       string
	Logger      *slog.Logger
	HTTPClient  *http.Client
}

// OpenAIClient is a thin HTTP client for OpenAI-compatible chat completions
// APIs (OpenAI, DeepSeek, Ollama, LM Studio, vLLM, ...).
type OpenAIClient struct {
	http    *http.Client
	baseURL string
	apiKey  string
	model   string
	log     *slog.Logger
}

// NewOpenAIClient creates an OpenAI-compatible LLM client.
func NewOpenAIClient(opts Options) (*OpenAIClient, error) {
	if opts.Credentials == "" {
		return nil, fmt.Errorf("llm: credentials are required")
	}

	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}

	model := opts.Model
	if model == "" {
		model = "gpt-4o-mini"
	}

	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}

	return &OpenAIClient{
		http:    httpClient,
		baseURL: baseURL,
		apiKey:  opts.Credentials,
		model:   model,
		log:     log,
	}, nil
}

// Chat sends a chat completion request. Functions (if any) are translated into
// OpenAI "tools" and the response is normalized to ChatResponse. Messages are
// normalized from the internal (GigaChat-style) protocol to OpenAI's wire
// format: assistant messages carrying a function_call become "tool_calls",
// and Role="function" results become Role="tool".
func (c *OpenAIClient) Chat(ctx context.Context, messages []Message, functions []Function) (*ChatResponse, error) {
	payload := map[string]any{
		"model":       c.model,
		"messages":    toOpenAIMessages(messages),
		"temperature": 0.2,
	}
	if len(functions) > 0 {
		payload["tools"] = toOpenAITools(functions)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm: chat request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm: chat failed: status=%d body=%s", resp.StatusCode, string(data))
	}

	res, err := parseOpenAIResponse(data)
	if err != nil {
		return nil, err
	}

	if len(res.Choices) == 0 {
		return nil, fmt.Errorf("llm: no choices in response")
	}

	c.log.Debug("llm: chat completion",
		"model", c.model,
		"tokens", res.Usage.TotalTokens,
		"function_call", res.Choices[0].Message.FunctionCall != nil,
	)

	return res, nil
}

type openAITool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

func toOpenAITools(functions []Function) []openAITool {
	tools := make([]openAITool, 0, len(functions))
	for _, f := range functions {
		tools = append(tools, openAITool{Type: "function", Function: f})
	}
	return tools
}

// toOpenAIMessages converts internal messages to the OpenAI wire format.
// Our agent loop passes assistant messages that include the requested
// function_call, followed by a "function" result message. OpenAI expects
// "tool_calls" on the assistant message and a "tool" role for the result;
// we translate those (including a stable tool_call_id) here. Plain
// system/user/assistant messages pass through untouched.
func toOpenAIMessages(messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	toolCallSeq := 0
	for _, m := range messages {
		msg := map[string]any{"role": m.Role, "content": m.Content}

		switch m.Role {
		case "assistant":
			if m.FunctionCall != nil {
				toolCallSeq++
				msg["tool_calls"] = []map[string]any{{
					"id":   fmt.Sprintf("call_%d", toolCallSeq),
					"type": "function",
					"function": map[string]any{
						"name":      m.FunctionCall.Name,
						"arguments": mustJSON(m.FunctionCall.Arguments),
					},
				}}
			}

		case "function":
			msg["role"] = "tool"
			msg["tool_call_id"] = fmt.Sprintf("call_%d", toolCallSeq)
			if m.Name != "" {
				msg["name"] = m.Name
			}
		}

		out = append(out, msg)
	}
	return out
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

type openAIResponse struct {
	Choices []struct {
		Message      openAIMessage `json:"message"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type openAIMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	ToolCalls []struct {
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

// parseOpenAIResponse converts an OpenAI-compatible response into ChatResponse,
// mapping "tool_calls" to a single FunctionCall (the agent loop consumes at
// most one tool call per iteration).
func parseOpenAIResponse(data []byte) (*ChatResponse, error) {
	var raw openAIResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("llm: decode response: %w", err)
	}

	res := &ChatResponse{Choices: make([]Choice, 0, len(raw.Choices))}
	res.Usage.PromptTokens = raw.Usage.PromptTokens
	res.Usage.CompletionTokens = raw.Usage.CompletionTokens
	res.Usage.TotalTokens = raw.Usage.TotalTokens

	for _, ch := range raw.Choices {
		c := Choice{FinishReason: ch.FinishReason}
		c.Message.Role = ch.Message.Role
		c.Message.Content = ch.Message.Content

		if len(ch.Message.ToolCalls) > 0 {
			tc := ch.Message.ToolCalls[0]
			args := map[string]any{}
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					// tolerate malformed arguments; pass raw string through
					args = map[string]any{"_raw": tc.Function.Arguments}
				}
			}
			c.Message.FunctionCall = &FunctionCall{
				Name:      tc.Function.Name,
				Arguments: args,
			}
		}
		res.Choices = append(res.Choices, c)
	}
	return res, nil
}