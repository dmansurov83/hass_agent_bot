package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

type State struct {
	EntityID string         `json:"entity_id"`
	State    string         `json:"state"`
	Attributes map[string]any `json:"attributes"`
	LastChanged string      `json:"last_changed"`
	LastUpdated string      `json:"last_updated"`
}

type Entity struct {
	EntityID    string
	FriendlyName string
	Domain      string
	State       string
	Attributes  map[string]any
}

const (
	toolCallService  = "call_service"
	toolGetState     = "get_state"
	toolListEntities = "list_entities"
	toolQuery        = "query"
	toolGetLiveContext = "get_live_context"
)

func (c *Client) CallService(ctx context.Context, domain, service string, data map[string]any) error {
	args := map[string]any{
		"domain":  domain,
		"service": service,
	}
	if len(data) > 0 {
		args["data"] = data
	}
	if _, err := c.CallTool(ctx, toolCallService, args); err != nil {
		return err
	}
	return nil
}

func (c *Client) GetState(ctx context.Context, entityID string) (*State, error) {
	out, err := c.CallTool(ctx, toolGetState, map[string]any{"entity_id": entityID})
	if err != nil {
		return nil, err
	}

	var s State
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		return nil, fmt.Errorf("mcp: parse state for %s: %w", entityID, err)
	}
	return &s, nil
}

func (c *Client) ListEntities(ctx context.Context) ([]Entity, error) {
	out, err := c.CallTool(ctx, toolListEntities, map[string]any{})
	if err != nil {
		return nil, err
	}

	var raw []State
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("mcp: parse entities: %w", err)
	}

	entities := make([]Entity, 0, len(raw))
	for _, s := range raw {
		domain := ""
		if i := indexByte(s.EntityID, '.'); i > 0 {
			domain = s.EntityID[:i]
		}
		entities = append(entities, Entity{
			EntityID:     s.EntityID,
			FriendlyName: friendlyName(s.Attributes),
			Domain:       domain,
			State:        s.State,
			Attributes:   s.Attributes,
		})
	}
	return entities, nil
}

func (c *Client) Query(ctx context.Context, query string) (string, error) {
	return c.CallTool(ctx, toolQuery, map[string]any{"query": query})
}

func friendlyName(attrs map[string]any) string {
	if attrs == nil {
		return ""
	}
	if v, ok := attrs["friendly_name"].(string); ok {
		return v
	}
	return ""
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}