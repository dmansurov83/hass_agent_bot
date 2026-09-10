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
	// Setup: fake HA MCP server with a HassTurnOn tool (like real HA MCP)
	mcpSrv := server.NewMCPServer("HA Test Agent", "1.0.0")

	states := map[string]map[string]any{
		"light.living_room": {"entity_id": "light.living_room", "state": "off", "attributes": map[string]any{"friendly_name": "Свет в зале"}},
	}

	hasTurnOn := mcp.NewTool("HassTurnOn",
		mcp.WithDescription("Turns on/opens/presses a device or entity"),
		mcp.WithString("name", mcp.Required()),
		mcp.WithString("area"),
	)
	mcpSrv.AddTool(hasTurnOn, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, _ := req.RequireString("name")
		if name != "" {
			states["light.living_room"]["state"] = "on"
			return mcp.NewToolResultText(`{"success": true}`), nil
		}
		return mcp.NewToolResultError("device not found"), nil
	})

	getLiveCtx := mcp.NewTool("GetLiveContext",
		mcp.WithDescription("Provides real-time information about the CURRENT state of devices"),
		mcp.WithString("name"),
	)
	mcpSrv.AddTool(getLiveCtx, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		// call 1: user says "включи свет" → GigaChat returns tool_call HassTurnOn
		chatResponseToolCall(t, "HassTurnOn",
			map[string]any{"name": "Свет в зале"},
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
	// Verify the state actually changed to on
	if states["light.living_room"]["state"] != "on" {
		t.Errorf("state = %v, want on (HassTurnOn should have executed)", states["light.living_room"]["state"])
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