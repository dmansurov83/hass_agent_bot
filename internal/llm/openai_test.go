package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeOpenAI simulates an OpenAI-compatible chat completions API.
type fakeOpenAI struct {
	server *httptest.Server
	calls  atomic.Int32
	responses []func(w http.ResponseWriter, body map[string]any)
	checkAuth func(r *http.Request)
}

func newFakeOpenAI(t *testing.T, responses ...func(w http.ResponseWriter, body map[string]any)) *fakeOpenAI {
	t.Helper()
	f := &fakeOpenAI{responses: responses}

	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		i := int(f.calls.Add(1)) - 1
		if f.checkAuth != nil {
			f.checkAuth(r)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		if i < len(f.responses) {
			f.responses[i](w, body)
			return
		}
		http.Error(w, "no more scripted responses", 500)
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func openAIChatResponseText(text string) func(w http.ResponseWriter, body map[string]any) {
	return func(w http.ResponseWriter, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{
				map[string]any{
					"message":       map[string]any{"role": "assistant", "content": text},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}
}

func openAIChatResponseToolCall(t *testing.T, name string, args map[string]any) func(w http.ResponseWriter, body map[string]any) {
	return func(w http.ResponseWriter, body map[string]any) {
		tools, ok := body["tools"].([]any)
		if !ok || len(tools) == 0 {
			t.Error("tools not passed to chat request")
		}
		argsJSON, _ := json.Marshal(args)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"role":    "assistant",
						"content": "",
						"tool_calls": []any{
							map[string]any{
								"id":       "call_123",
								"type":     "function",
								"function": map[string]any{
									"name":      name,
									"arguments": string(argsJSON),
								},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
			"usage": map[string]any{"total_tokens": 20},
		})
	}
}

func TestOpenAIClient_ChatText(t *testing.T) {
	f := newFakeOpenAI(t, openAIChatResponseText("Привет из OpenAI!"))

	c, err := NewOpenAIClient(Options{
		Credentials: "sk-test-key",
		BaseURL:     f.server.URL,
		Model:       "gpt-4o-mini",
	})
	if err != nil {
		t.Fatalf("NewOpenAIClient: %v", err)
	}

	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content != "Привет из OpenAI!" {
		t.Fatalf("unexpected response: %+v", resp.Choices)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestOpenAIClient_ChatToolCall(t *testing.T) {
	f := newFakeOpenAI(t, openAIChatResponseToolCall(t, "HassTurnOn", map[string]any{"name": "Light"}))

	c, err := NewOpenAIClient(Options{
		Credentials: "sk-test-key",
		BaseURL:     f.server.URL,
		Model:       "gpt-4o-mini",
	})
	if err != nil {
		t.Fatalf("NewOpenAIClient: %v", err)
	}

	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "включи свет"}}, []Function{
		{Name: "HassTurnOn", Description: "turns on", Parameters: map[string]any{"type": "object"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Choices[0].Message.FunctionCall == nil {
		t.Fatal("expected function_call")
	}
	fc := resp.Choices[0].Message.FunctionCall
	if fc.Name != "HassTurnOn" {
		t.Errorf("fc.Name = %q", fc.Name)
	}
	if fc.Arguments["name"] != "Light" {
		t.Errorf("fc.Arguments = %v", fc.Arguments)
	}
}

func TestOpenAIClient_MissingCredentials(t *testing.T) {
	_, err := NewOpenAIClient(Options{})
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
}

func TestOpenAIClient_BearerAuth(t *testing.T) {
	f := newFakeOpenAI(t, openAIChatResponseText("ok"))
	f.checkAuth = func(r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test-key" {
			t.Errorf("bad auth: %q", r.Header.Get("Authorization"))
		}
	}

	c, err := NewOpenAIClient(Options{
		Credentials: "sk-test-key",
		BaseURL:     f.server.URL,
	})
	if err != nil {
		t.Fatalf("NewOpenAIClient: %v", err)
	}
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestOpenAIClient_DefaultBaseURL(t *testing.T) {
	c, err := NewOpenAIClient(Options{Credentials: "sk-test-key"})
	if err != nil {
		t.Fatalf("NewOpenAIClient: %v", err)
	}
	if c.baseURL != defaultOpenAIBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, defaultOpenAIBaseURL)
	}
}

func TestParseOpenAIResponse_Empty(t *testing.T) {
	_, err := parseOpenAIResponse([]byte(`{"choices": []}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
}

func TestParseOpenAIResponse_MalformedArgs(t *testing.T) {
	data := []byte(`{
		"choices": [{
			"message": {
				"role": "assistant",
				"content": "",
				"tool_calls": [{
					"id": "call_1",
					"function": {"name": "Foo", "arguments": "not json"}
				}]
			},
			"finish_reason": "tool_calls"
		}]
	}`)
	res, err := parseOpenAIResponse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Choices[0].Message.FunctionCall.Name != "Foo" {
		t.Errorf("name = %q", res.Choices[0].Message.FunctionCall.Name)
	}
	if _, ok := res.Choices[0].Message.FunctionCall.Arguments["_raw"]; !ok {
		t.Errorf("expected _raw fallback, got %v", res.Choices[0].Message.FunctionCall.Arguments)
	}
}

func TestStringTrimRight(t *testing.T) {
	if strings.TrimRight("https://api.deepseek.com/", "/") != "https://api.deepseek.com" {
		t.Error("TrimRight failed")
	}
	if strings.TrimRight("", "/") != "" {
		t.Error("TrimRight empty failed")
	}
}