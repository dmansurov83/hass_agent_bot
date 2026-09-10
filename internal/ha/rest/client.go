package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a thin Home Assistant REST API client. It replaces the HA MCP client:
// HA's MCP server only exposes entities that are exposed to Assist, while the
// REST API returns every entity. All read and write operations go through the
// same long-lived access token.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
	log     *slog.Logger
}

// Options configures the REST client.
type Options struct {
	BaseURL string
	Token   string
	Logger  *slog.Logger
}

// New creates a REST client for the given HA base URL, e.g. http://192.168.1.2:8123.
func New(baseURL string, opts Options) *Client {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   opts.Token,
		http:    &http.Client{Timeout: 15 * time.Second},
		log:     log,
	}
}

// Token returns the HA access token (used by helpers that call REST directly).
func (c *Client) Token() string { return c.token }

// BaseURL returns the HA base URL without a trailing slash.
func (c *Client) BaseURL() string { return c.baseURL }

// get performs a GET request and returns the raw body. Non-2xx status codes
// become errors with the response body included.
func (c *Client) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HA REST %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("HA REST %s: read body: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HA REST %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// post performs a POST request with a JSON body (a HA service call).
func (c *Client) post(ctx context.Context, path string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("HA REST: marshal payload: %w", err)
	}
	u := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HA REST %s: %w", path, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("HA REST %s: read body: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HA REST %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// State is a single entity state from /api/states.
type State struct {
	EntityID   string         `json:"entity_id"`
	State      string         `json:"state"`
	Attributes map[string]any `json:"attributes"`
	LastChanged string        `json:"last_changed"`
	LastUpdated string        `json:"last_updated"`
}

// States returns all entity states from /api/states.
func (c *Client) States(ctx context.Context) ([]State, error) {
	body, err := c.get(ctx, "/api/states", nil)
	if err != nil {
		return nil, err
	}
	var states []State
	if err := json.Unmarshal(body, &states); err != nil {
		return nil, fmt.Errorf("HA REST: parse states: %w", err)
	}
	return states, nil
}

// StateByID returns a single entity state from /api/states/<entity_id>.
func (c *Client) StateByID(ctx context.Context, entityID string) (*State, error) {
	body, err := c.get(ctx, "/api/states/"+url.PathEscape(entityID), nil)
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(body, &st); err != nil {
		return nil, fmt.Errorf("HA REST: parse state: %w", err)
	}
	return &st, nil
}

// CallService calls a domain service, e.g. CallService("light", "turn_on", data).
// data may contain "entity_id", "area_id" (supports one or many), and any service
// fields (brightness, volume_level, ...).
func (c *Client) CallService(ctx context.Context, domain, service string, data map[string]any) error {
	_, err := c.post(ctx, "/api/services/"+domain+"/"+service, data)
	return err
}

// History returns raw state-change history for one entity from
// /api/history/period/<start>. start must be a local time (timezone ignored),
// the API accepts ISO strings without an offset.
func (c *Client) History(ctx context.Context, entityID string, start, end time.Time) ([]State, error) {
	q := url.Values{}
	q.Set("filter_entity_id", entityID)
	q.Set("end_time", end.Format("2006-01-02T15:04:05"))
	q.Set("minimal_response", "")
	q.Set("significant_changes_only", "false")

	path := "/api/history/period/" + start.Format("2006-01-02T15:04:05")
	body, err := c.get(ctx, path, q)
	if err != nil {
		return nil, err
	}

	// Response is an array of per-entity arrays.
	var raw []json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("HA REST: parse history: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var pts []State
	if err := json.Unmarshal(raw[0], &pts); err != nil {
		return nil, fmt.Errorf("HA REST: parse history points: %w", err)
	}
	return pts, nil
}