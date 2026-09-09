package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func newFakeHAServer(t *testing.T) *httptest.Server {
	t.Helper()

	s := server.NewMCPServer("HA Test", "1.0.0")

	states := map[string]map[string]any{
		"light.living_room": {"entity_id": "light.living_room", "state": "off", "attributes": map[string]any{"friendly_name": "Свет в зале"}},
		"sensor.outdoor_temp": {"entity_id": "sensor.outdoor_temp", "state": "22.5", "attributes": map[string]any{"friendly_name": "Уличная температура", "unit_of_measurement": "°C"}},
		"switch.kettle":     {"entity_id": "switch.kettle", "state": "off", "attributes": map[string]any{"friendly_name": "Чайник"}},
	}

	listEntities := mcp.NewTool("list_entities",
		mcp.WithDescription("List all exposed entities in Home Assistant"),
	)
	s.AddTool(listEntities, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		list := make([]any, 0, len(states))
		for _, v := range states {
			list = append(list, v)
		}
		data, _ := json.Marshal(list)
		return mcp.NewToolResultText(string(data)), nil
	})

	getState := mcp.NewTool("get_state",
		mcp.WithDescription("Get the state of a specific entity"),
		mcp.WithString("entity_id", mcp.Required()),
	)
	s.AddTool(getState, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		entityID, _ := req.RequireString("entity_id")
		st, ok := states[entityID]
		if !ok {
			return mcp.NewToolResultError("entity not found: " + entityID), nil
		}
		data, _ := json.Marshal(st)
		return mcp.NewToolResultText(string(data)), nil
	})

	callService := mcp.NewTool("call_service",
		mcp.WithDescription("Call a service in Home Assistant"),
		mcp.WithString("domain", mcp.Required()),
		mcp.WithString("service", mcp.Required()),
	)
	s.AddTool(callService, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		service, _ := req.RequireString("service")
		args := req.GetArguments()
		entityID, _ := args["entity_id"].(string)
		if entityID == "" {
			if d, ok := args["data"].(map[string]any); ok {
				entityID, _ = d["entity_id"].(string)
			}
		}
		if entityID != "" {
			st, ok := states[entityID]
			if !ok {
				return mcp.NewToolResultError("entity not found: " + entityID), nil
			}
			if service == "turn_on" {
				st["state"] = "on"
			} else {
				st["state"] = "off"
			}
		}
		return mcp.NewToolResultText(`{"success": true}`), nil
	})

	ts := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(ts.Close)
	return ts
}

func TestClient_ListEntities(t *testing.T) {
	ts := newFakeHAServer(t)

	c, err := New(context.Background(), ts.URL, Options{Token: "test-token"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	entities, err := c.ListEntities(context.Background())
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}
	if len(entities) != 3 {
		t.Fatalf("expected 3 entities, got %d: %+v", len(entities), entities)
	}

	byID := map[string]Entity{}
	for _, e := range entities {
		byID[e.EntityID] = e
	}

	light := byID["light.living_room"]
	if light.FriendlyName != "Свет в зале" {
		t.Errorf("friendly name = %q", light.FriendlyName)
	}
	if light.Domain != "light" {
		t.Errorf("domain = %q", light.Domain)
	}
	if light.State != "off" {
		t.Errorf("state = %q", light.State)
	}
}

func TestClient_GetState(t *testing.T) {
	ts := newFakeHAServer(t)

	c, err := New(context.Background(), ts.URL, Options{Token: "test-token"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	st, err := c.GetState(context.Background(), "sensor.outdoor_temp")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if st.State != "22.5" {
		t.Errorf("state = %q", st.State)
	}
	if st.Attributes["friendly_name"] != "Уличная температура" {
		t.Errorf("friendly_name = %v", st.Attributes["friendly_name"])
	}
}

func TestClient_CallService(t *testing.T) {
	ts := newFakeHAServer(t)

	c, err := New(context.Background(), ts.URL, Options{Token: "test-token"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	err = c.CallService(context.Background(), "light", "turn_on", map[string]any{"entity_id": "light.living_room"})
	if err != nil {
		t.Fatalf("CallService: %v", err)
	}

	st, err := c.GetState(context.Background(), "light.living_room")
	if err != nil {
		t.Fatalf("GetState after: %v", err)
	}
	if st.State != "on" {
		t.Errorf("state after turn_on = %q, want on", st.State)
	}
}

func TestClient_CallService_EntityNotFound(t *testing.T) {
	ts := newFakeHAServer(t)

	c, err := New(context.Background(), ts.URL, Options{Token: "test-token"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	err = c.CallService(context.Background(), "light", "turn_on", map[string]any{"entity_id": "light.nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent entity")
	}
	if !strings.Contains(err.Error(), "entity not found") {
		t.Errorf("unexpected error: %v", err)
	}
}