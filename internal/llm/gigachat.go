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
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	defaultBaseURL = "https://api.giga.chat"            // новый URL с 17.07.2026
	legacyAuthURL  = "https://ngw.devices.sberbank.ru:9443/api/v2/oauth"
	scopePers      = "GIGACHAT_API_PERS"
	tokenTTL       = 30 * time.Minute
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type FunctionCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type Choice struct {
	Message struct {
		Role         string        `json:"role"`
		Content      string        `json:"content"`
		FunctionCall *FunctionCall `json:"function_call,omitempty"`
	} `json:"message"`
	FinishReason string `json:"finish_reason"`
}

type ChatResponse struct {
	Choices []Choice `json:"choices"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

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

// GigaChatClient is a thin HTTP client for the GigaChat REST API.
type GigaChatClient struct {
	http        *http.Client
	baseURL     string
	authURL     string
	credentials string
	model       string
	log         *slog.Logger

	mu       sync.Mutex
	token    string
	expires  time.Time
}

type Options struct {
	BaseURL     string // default https://api.giga.chat
	AuthURL     string // default legacy OAuth endpoint
	Credentials string // Authorization Key (Basic)
	Model       string // default GigaChat-Pro
	Logger      *slog.Logger
	HTTPClient  *http.Client
}

func NewGigaChatClient(opts Options) (*GigaChatClient, error) {
	if opts.Credentials == "" {
		return nil, fmt.Errorf("llm: credentials are required")
	}

	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	authURL := opts.AuthURL
	if authURL == "" {
		authURL = legacyAuthURL
	}
	model := opts.Model
	if model == "" {
		model = "GigaChat-Pro"
	}

	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}

	return &GigaChatClient{
		http:        httpClient,
		baseURL:     baseURL,
		authURL:     authURL,
		credentials: opts.Credentials,
		model:       model,
		log:         log,
	}, nil
}

// getToken returns a valid access token, refreshing it if expired.
func (c *GigaChatClient) getToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}

	body := strings.NewReader("scope=" + scopePers)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.authURL, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("RqUID", uuid.NewString())
	req.Header.Set("Authorization", "Basic "+c.credentials)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: oauth request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("llm: oauth failed: status=%d body=%s", resp.StatusCode, string(b))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresAt   int64  `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("llm: decode token: %w", err)
	}

	c.token = tokenResp.AccessToken
	// expires_at is unix seconds; be safe and refresh 1 min early.
	if tokenResp.ExpiresAt > 0 {
		c.expires = time.Unix(tokenResp.ExpiresAt, 0).Add(-time.Minute)
	} else {
		c.expires = time.Now().Add(tokenTTL)
	}

	return c.token, nil
}

// Chat sends a chat completion request. Functions (if any) are included.
func (c *GigaChatClient) Chat(ctx context.Context, messages []Message, functions []Function) (*ChatResponse, error) {
	payload := map[string]any{
		"model":       c.model,
		"messages":    messages,
		"temperature": 0.2,
	}
	if len(functions) > 0 {
		payload["functions"] = functions
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	token, err := c.getToken(ctx)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm: chat request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm: chat failed: status=%d body=%s", resp.StatusCode, string(data))
	}

	var res ChatResponse
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("llm: decode response: %w", err)
	}

	if len(res.Choices) == 0 {
		return nil, fmt.Errorf("llm: no choices in response")
	}

	c.log.Debug("llm: chat completion",
		"model", c.model,
		"tokens", res.Usage.TotalTokens,
		"function_call", res.Choices[0].Message.FunctionCall != nil,
	)

	return &res, nil
}