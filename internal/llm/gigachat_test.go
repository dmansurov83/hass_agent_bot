package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeGigaChat simulates the GigaChat REST API: OAuth + chat completions.
type fakeGigaChat struct {
	server    *httptest.Server
	calls     atomic.Int32
	tokenCalls atomic.Int32
	// scripted responses consumed per chat call
	responses []func(w http.ResponseWriter, body map[string]any)
}

func newFakeGigaChat(t *testing.T, responses ...func(w http.ResponseWriter, body map[string]any)) *fakeGigaChat {
	t.Helper()
	f := &fakeGigaChat{responses: responses}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/oauth", func(w http.ResponseWriter, r *http.Request) {
		f.tokenCalls.Add(1)
		// check RqUID present
		if r.Header.Get("RqUID") == "" {
			t.Error("RqUID header missing")
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
			t.Error("Basic auth header missing")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test_access_token_123",
			"expires_at":   int64(0), // forces the fixed TTL branch
		})
	})

	chatHandler := func(w http.ResponseWriter, r *http.Request) {
		i := int(f.calls.Add(1)) - 1
		if r.Header.Get("Authorization") != "Bearer test_access_token_123" {
			t.Errorf("chat: bad auth: %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		if i < len(f.responses) {
			f.responses[i](w, body)
			return
		}
		http.Error(w, "no more scripted responses", 500)
	}
	// Новый целевой хост и кастомные URL: /v1/chat/completions.
	// Legacy-хост gigachat.devices.sberbank.ru: /api/v1/chat/completions.
	mux.HandleFunc("/v1/chat/completions", chatHandler)
	mux.HandleFunc("/api/v1/chat/completions", chatHandler)

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func chatResponseText(text string) func(w http.ResponseWriter, body map[string]any) {
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

func chatResponseToolCall(t *testing.T, name string, args map[string]any) func(w http.ResponseWriter, body map[string]any) {
	return func(w http.ResponseWriter, body map[string]any) {
		// verify functions were passed to the model
		fns, ok := body["functions"].([]any)
		if !ok || len(fns) == 0 {
			t.Error("functions not passed to chat request")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"role":    "assistant",
						"content": "",
						"function_call": map[string]any{
							"name":      name,
							"arguments": args,
						},
					},
					"finish_reason": "function_call",
				},
			},
			"usage": map[string]any{"total_tokens": 20},
		})
	}
}

func TestGigaChatClient_TokenAndChat(t *testing.T) {
	f := newFakeGigaChat(t, chatResponseText("Привет!"))

	c, err := NewGigaChatClient(Options{
		Credentials: "dGVzdDp0ZXN0",
		BaseURL:     f.server.URL,
		AuthURL:     f.server.URL + "/api/v2/oauth",
		Model:       "GigaChat-Test",
	})
	if err != nil {
		t.Fatalf("NewGigaChatClient: %v", err)
	}

	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content != "Привет!" {
		t.Fatalf("unexpected response: %+v", resp.Choices)
	}
	if f.tokenCalls.Load() != 1 {
		t.Errorf("expected 1 token call, got %d", f.tokenCalls.Load())
	}
}

func TestGigaChatClient_MissingCredentials(t *testing.T) {
	_, err := NewGigaChatClient(Options{})
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
}

func TestChatPath(t *testing.T) {
	cases := []struct {
		baseURL string
		want    string
	}{
		{"https://api.giga.chat", "/v1/chat/completions"},
		{"https://api.giga.chat/", "/v1/chat/completions"},
		{"https://gigachat.devices.sberbank.ru", "/api/v1/chat/completions"},
		{"https://gigachat.devices.sberbank.ru/", "/api/v1/chat/completions"},
		{"http://127.0.0.1:8080", "/v1/chat/completions"},
	}
	for _, c := range cases {
		if got := chatPath(c.baseURL); got != c.want {
			t.Errorf("chatPath(%q) = %q, want %q", c.baseURL, got, c.want)
		}
	}
}

func TestFunctionParameters(t *testing.T) {
	p := FunctionParameters(
		map[string]any{"entity_id": map[string]any{"type": "string"}},
		[]string{"entity_id"},
	)
	if p["type"] != "object" {
		t.Errorf("type = %v", p["type"])
	}
	if _, ok := p["properties"].(map[string]any); !ok {
		t.Errorf("properties missing")
	}
	req, ok := p["required"].([]string)
	if !ok || len(req) != 1 || req[0] != "entity_id" {
		t.Errorf("required = %v", p["required"])
	}
}

func TestSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "system_prompt.txt")
	if err := os.WriteFile(path, []byte("Ты — помощник по умному дому через Home Assistant."), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	p, err := LoadSystemPrompt(path)
	if err != nil {
		t.Fatalf("LoadSystemPrompt: %v", err)
	}
	if !strings.Contains(p, "Home Assistant") {
		t.Errorf("prompt missing HA mention: %q", p)
	}

	// fallback chain: first candidate missing, second present
	used, p2, err := ResolveSystemPrompt(filepath.Join(dir, "missing.txt"), path)
	if err != nil {
		t.Fatalf("ResolveSystemPrompt fallback: %v", err)
	}
	if used != path {
		t.Errorf("expected fallback to %s, got %q", path, used)
	}
	if p2 != p {
		t.Errorf("fallback content differs")
	}

	if _, err := LoadSystemPrompt(filepath.Join(dir, "missing.txt")); err == nil {
		t.Errorf("LoadSystemPrompt should fail for missing file")
	}
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, []byte("   "), 0o644); err != nil {
		t.Fatalf("write empty prompt: %v", err)
	}
	if _, err := LoadSystemPrompt(empty); err == nil {
		t.Errorf("LoadSystemPrompt should fail for empty file")
	}
}