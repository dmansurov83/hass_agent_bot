package llm

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	hamcp "hass-agent-bot/internal/ha/mcp"
	"hass-agent-bot/internal/scheduler"
)

// fakeScheduler implements the Scheduler interface for tests.
type fakeScheduler struct {
	jobs []scheduler.Job
}

func (f *fakeScheduler) ScheduleIn(d time.Duration, action scheduler.Action, label string) (*scheduler.Job, error) {
	j := &scheduler.Job{ID: "fake-timer", Label: label, Action: action, RunAt: time.Now().Add(d)}
	f.jobs = append(f.jobs, *j)
	return j, nil
}

func (f *fakeScheduler) ScheduleCron(expr string, action scheduler.Action, label string) (*scheduler.Job, error) {
	j := &scheduler.Job{ID: "fake-cron", Label: label, Action: action, CronExpr: expr}
	f.jobs = append(f.jobs, *j)
	return j, nil
}

func (f *fakeScheduler) Cancel(id string) bool { return false }
func (f *fakeScheduler) List() []scheduler.Job  { return f.jobs }

func TestAgent_HandleMessage_ToolCall(t *testing.T) {
	// Setup: fake HA MCP server with a call_service tool
	mcpSrv := server.NewMCPServer("HA Test Agent", "1.0.0")

	states := map[string]map[string]any{
		"light.living_room": {"entity_id": "light.living_room", "state": "off", "attributes": map[string]any{"friendly_name": "Свет в зале"}},
	}

	callService := mcp.NewTool("call_service",
		mcp.WithDescription("Call a service in Home Assistant"),
		mcp.WithString("domain", mcp.Required()),
		mcp.WithString("service", mcp.Required()),
	)
	mcpSrv.AddTool(callService, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		service, _ := req.RequireString("service")
		args := req.GetArguments()
		entityID, _ := args["entity_id"].(string)
		if entityID == "" {
			if d, ok := args["data"].(map[string]any); ok {
				entityID, _ = d["entity_id"].(string)
			}
		}
		if service == "turn_on" && entityID == "light.living_room" {
			states["light.living_room"]["state"] = "on"
			return mcp.NewToolResultText(`{"success": true}`), nil
		}
		return mcp.NewToolResultError("not implemented"), nil
	})

	getState := mcp.NewTool("get_state",
		mcp.WithDescription("Get state"),
		mcp.WithString("entity_id", mcp.Required()),
	)
	mcpSrv.AddTool(getState, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		data, _ := json.Marshal(states["light.living_room"])
		return mcp.NewToolResultText(string(data)), nil
	})

	haTS := httptest.NewServer(server.NewStreamableHTTPServer(mcpSrv))
	t.Cleanup(haTS.Close)

	haCli, err := hamcp.New(context.Background(), haTS.URL, hamcp.Options{Token: "test"})
	if err != nil {
		t.Fatalf("ha mcp: %v", err)
	}
	t.Cleanup(func() { haCli.Close() })

	// Setup: fake GigaChat with two responses: first tool_call, then text
	giga := newFakeGigaChat(t,
		// call 1: user says "включи свет" → GigaChat returns tool_call
		chatResponseToolCall(t, "call_service",
			map[string]any{"domain": "light", "service": "turn_on", "data": map[string]any{"entity_id": "light.living_room"}},
		),
		// call 2: after tool result, GigaChat returns text
		chatResponseText("Свет в зале включён!"),
	)

	gigaCli, err := NewGigaChatClient(Options{
		Credentials: "dGVzdDp0ZXN0",
		BaseURL:     giga.server.URL,
		AuthURL:     giga.server.URL + "/api/v2/oauth",
		Model:       "GigaChat-Test",
	})
	if err != nil {
		t.Fatalf("giga client: %v", err)
	}

	agent := NewAgent(gigaCli, haCli, &fakeScheduler{})
	reply, err := agent.HandleMessage(context.Background(), "включи свет в зале")
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if !stringsContains(reply, "Свет в зале") && !stringsContains(reply, "включ") {
		t.Errorf("unexpected reply: %q", reply)
	}
}

func TestAgent_HandleMessage_NoToolCall(t *testing.T) {
	mcpSrv := server.NewMCPServer("HA Test", "1.0.0")
	pingTool := mcp.NewTool("ping", mcp.WithDescription("ping"))
	mcpSrv.AddTool(pingTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("pong"), nil
	})
	haTS := httptest.NewServer(server.NewStreamableHTTPServer(mcpSrv))
	t.Cleanup(haTS.Close)
	haCli, err := hamcp.New(context.Background(), haTS.URL, hamcp.Options{Token: "test"})
	if err != nil {
		t.Fatalf("ha mcp: %v", err)
	}
	t.Cleanup(func() { haCli.Close() })

	giga := newFakeGigaChat(t,
		chatResponseText("Привет! Чем могу помочь?"),
	)

	gigaCli, err := NewGigaChatClient(Options{
		Credentials: "dGVzdDp0ZXN0",
		BaseURL:     giga.server.URL,
		AuthURL:     giga.server.URL + "/api/v2/oauth",
		Model:       "GigaChat-Test",
	})
	if err != nil {
		t.Fatalf("giga client: %v", err)
	}

	agent := NewAgent(gigaCli, haCli, &fakeScheduler{})
	reply, err := agent.HandleMessage(context.Background(), "привет")
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if !stringsContains(reply, "помочь") {
		t.Errorf("unexpected reply: %q", reply)
	}
}

func TestAgent_Reset(t *testing.T) {
	agent := NewAgent(nil, nil, nil)
	agent.history = append(agent.history, Message{Role: "user", Content: "foo"})
	agent.Reset()
	if len(agent.history) != 0 {
		t.Errorf("history not reset")
	}
}

func stringsContains(s, substr string) bool {
	return len(s) >= len(substr) && contains(s, substr)
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}