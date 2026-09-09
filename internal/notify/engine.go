package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const (
	reconnectBase = time.Second
	reconnectMax  = 30 * time.Second
)

// Event represents a state_changed event from HA.
type Event struct {
	EntityID string `json:"entity_id"`
	OldState string `json:"old_state"`
	NewState string `json:"new_state"`
}

// Sender is called to send a notification message to the user.
type Sender func(ctx context.Context, text string)

type Engine struct {
	baseURL string
	token   string
	config  Config
	sender  Sender
	log     *slog.Logger

	mu      sync.Mutex
	quietUntil time.Time
	debounce   map[string]time.Time
}

type Config struct {
	DebounceSeconds int
	Entities        []string // entity_id patterns, e.g. ["binary_sensor.*", "sensor.outdoor_temp"]
}

func New(baseURL, token string, config Config, sender Sender) *Engine {
	return &Engine{
		baseURL:  baseURL,
		token:    token,
		config:   config,
		sender:   sender,
		log:      slog.Default(),
		debounce: make(map[string]time.Time),
	}
}

// SetSender updates the sender callback (used when wiring after construction).
func (e *Engine) SetSender(sender Sender) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sender = sender
}

// Run connects to HA WebSocket and listens for events. Blocks until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	e.log.Info("notify: starting")

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := e.connectAndListen(ctx); err != nil {
			e.log.Error("notify: connection error, reconnecting", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		default:
		}
		time.Sleep(reconnectBase)
	}
}

// SetQuiet pauses notifications until the given time.
func (e *Engine) SetQuiet(until time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.quietUntil = until
	e.log.Info("notify: quiet mode until", "until", until)
}

// IsQuiet returns true if notifications are currently paused.
func (e *Engine) IsQuiet() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return time.Now().Before(e.quietUntil)
}

func (e *Engine) connectAndListen(ctx context.Context) error {
	u, err := url.Parse(e.baseURL)
	if err != nil {
		return fmt.Errorf("parse base URL: %w", err)
	}
	u.Scheme = "ws"
	u.Path = "/api/websocket"

	wsConn, _, err := websocket.Dial(ctx, u.String(), nil)
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}
	defer wsConn.CloseNow()

	// Step 1: auth phase
	var authMsg struct {
		Type string `json:"type"`
	}
	if err := wsjson.Read(ctx, wsConn, &authMsg); err != nil {
		return fmt.Errorf("ws read auth: %w", err)
	}
	if authMsg.Type != "auth_required" {
		return fmt.Errorf("ws unexpected auth type: %s", authMsg.Type)
	}

	authReq := struct {
		Type        string `json:"type"`
		AccessToken string `json:"access_token"`
	}{Type: "auth", AccessToken: e.token}
	if err := wsjson.Write(ctx, wsConn, authReq); err != nil {
		return fmt.Errorf("ws send auth: %w", err)
	}

	var authResult struct {
		Type      string `json:"type"`
		HAVersion string `json:"ha_version,omitempty"`
		Message   string `json:"message,omitempty"`
	}
	if err := wsjson.Read(ctx, wsConn, &authResult); err != nil {
		return fmt.Errorf("ws read auth result: %w", err)
	}
	if authResult.Type != "auth_ok" {
		return fmt.Errorf("ws auth failed: %s (%s)", authResult.Type, authResult.Message)
	}

	e.log.Info("notify: authenticated", "ha_version", authResult.HAVersion)

	// Step 2: subscribe to state_changed events
	subReq := struct {
		ID         int    `json:"id"`
		Type       string `json:"type"`
		EventType  string `json:"event_type"`
	}{ID: 1, Type: "subscribe_events", EventType: "state_changed"}
	if err := wsjson.Write(ctx, wsConn, subReq); err != nil {
		return fmt.Errorf("ws subscribe: %w", err)
	}

	if err := e.listenEvents(ctx, wsConn); err != nil {
		return err
	}

	return nil
}

func (e *Engine) listenEvents(ctx context.Context, wsConn *websocket.Conn) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var raw json.RawMessage
		if err := wsjson.Read(ctx, wsConn, &raw); err != nil {
			return fmt.Errorf("ws read event: %w", err)
		}

		var header struct {
			Type string `json:"type"`
			ID   int    `json:"id,omitempty"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			continue
		}

		if header.Type == "event" {
			var ev struct {
				Event struct {
					EntityID      string          `json:"entity_id"`
					OldState             *stateSnapshot `json:"old_state"`
					NewState             *stateSnapshot `json:"new_state"`
				} `json:"event"`
			}
			if err := json.Unmarshal(raw, &ev); err != nil {
				continue
			}

			e.handleEvent(ctx, ev.Event.EntityID, ev.Event.OldState, ev.Event.NewState)
		}

		// Ignore result/subscription messages
	}
}

type stateSnapshot struct {
	State string `json:"state"`
}

func (e *Engine) handleEvent(ctx context.Context, entityID string, oldState, newState *stateSnapshot) {
	if newState == nil || oldState == nil {
		return
	}

	// Filter by entity pattern
	if !e.matchesFilter(entityID) {
		return
	}

	// Debounce
	now := time.Now()
	e.mu.Lock()
	last, exists := e.debounce[entityID]
	debounceSec := time.Duration(e.config.DebounceSeconds) * time.Second
	if debounceSec == 0 {
		debounceSec = 30 * time.Second
	}
	e.debounce[entityID] = now
	e.mu.Unlock()
	if exists && now.Sub(last) < debounceSec {
		return
	}

	if oldState.State == newState.State {
		return
	}

	// Quiet mode
	if e.IsQuiet() {
		return
	}

	text := fmt.Sprintf("%s: %s → %s", entityID, oldState.State, newState.State)

	if e.sender != nil {
		e.sender(ctx, text)
	}
}

func (e *Engine) matchesFilter(entityID string) bool {
	if len(e.config.Entities) == 0 {
		return false
	}
	for _, pattern := range e.config.Entities {
		if matchPattern(pattern, entityID) {
			return true
		}
	}
	return false
}

// matchPattern supports wildcard "*" suffix, e.g. "binary_sensor.*"
func matchPattern(pattern, entityID string) bool {
	if pattern == "*" {
		return true
	}
	for i := 0; i < len(pattern) && i < len(entityID); i++ {
		if pattern[i] == '*' {
			return true
		}
		if pattern[i] != entityID[i] {
			return false
		}
	}
	return len(pattern) == len(entityID)
}