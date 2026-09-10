package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func (f *fakeScheduler) ScheduleAt(runAt time.Time, action scheduler.Action, label string) (*scheduler.Job, error) {
	j := &scheduler.Job{ID: "fake-at", Label: label, Action: action, RunAt: runAt}
	f.jobs = append(f.jobs, *j)
	return j, nil
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

	agent := NewAgent(gigaCli, haCli, &fakeScheduler{}, "http://ha.local", nil)
	reply, err := agent.HandleMessage(WithChatID(context.Background(), 100), "включи свет в зале")
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

	agent := NewAgent(gigaCli, haCli, &fakeScheduler{}, "http://ha.local", nil)
	reply, err := agent.HandleMessage(WithChatID(context.Background(), 100), "привет")
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if !stringsContains(reply, "помочь") {
		t.Errorf("unexpected reply: %q", reply)
	}
}

func TestAgent_Reset(t *testing.T) {
	agent := NewAgent(nil, nil, nil, "", nil)
	agent.histories[100] = []Message{{Role: "user", Content: "foo"}}
	agent.Reset(100)
	if len(agent.histories[100]) != 0 {
		t.Errorf("history not reset")
	}
	if _, ok := agent.histories[100]; ok {
		t.Errorf("chat still present after reset")
	}
}

func TestAgent_Reset_Persisted(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, nil)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	agent := NewAgent(nil, nil, nil, "", store)
	agent.histories[100] = []Message{{Role: "user", Content: "foo"}, {Role: "assistant", Content: "bar"}}
	agent.saveHistory(100)

	// file exists
	if _, err := os.Stat(filepath.Join(dir, "chat_100.json")); err != nil {
		t.Fatalf("history file not written: %v", err)
	}

	agent.Reset(100)
	if _, err := os.Stat(filepath.Join(dir, "chat_100.json")); !os.IsNotExist(err) {
		t.Errorf("history file should be deleted after reset")
	}
}

func TestFileStore_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, nil)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer store.Close()

	msgs := []Message{
		{Role: "user", Content: "включи свет"},
		{Role: "assistant", Content: "Свет включён"},
	}
	if err := store.Save(context.Background(), 42, msgs); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := loaded[42]
	if len(got) != 2 || got[0].Content != "включи свет" || got[1].Content != "Свет включён" {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
}

func TestFileStore_Load_IgnoresCorrupt(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, nil)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer store.Close()

	// good file
	store.Save(context.Background(), 1, []Message{{Role: "user", Content: "hi"}})
	// corrupt file
	if err := os.WriteFile(filepath.Join(dir, "chat_2.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	// bad filename (non-numeric id)
	if err := os.WriteFile(filepath.Join(dir, "chat_abc.json"), []byte("[]"), 0o644); err != nil {
		t.Fatalf("write bad name: %v", err)
	}

	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded[1]) != 1 {
		t.Errorf("good chat lost: %+v", loaded)
	}
	if _, ok := loaded[2]; ok {
		t.Errorf("corrupt chat should be ignored")
	}
}

func TestExtractToolArgs_Map(t *testing.T) {
	args := extractToolArgs(map[string]any{
		"name":      "HassTurnOn",
		"arguments": map[string]any{"name": "Light", "area": "Туалет"},
	})
	if args["name"] != "Light" || args["area"] != "Туалет" {
		t.Errorf("wrong args: %v", args)
	}
	if len(args) != 2 {
		t.Errorf("expected 2 args, got %d: %v", len(args), args)
	}
}

func TestExtractToolArgs_JSONString(t *testing.T) {
	args := extractToolArgs(map[string]any{
		"name":      "HassTurnOn",
		"arguments": `{"name": "Light", "area": "Туалет"}`,
	})
	if args["name"] != "Light" || args["area"] != "Туалет" {
		t.Errorf("wrong args: %v", args)
	}
}

func TestExtractToolArgs_FlatFallback(t *testing.T) {
	args := extractToolArgs(map[string]any{
		"tool": "HassTurnOn",
		"name": "Light",
		"area": "Гостиная",
	})
	// name должно быть в args как аргумент HassTurnOn
	if args["name"] != "Light" || args["area"] != "Гостиная" {
		t.Errorf("wrong args: %v", args)
	}
	if _, ok := args["tool"]; ok {
		t.Errorf("'tool' should not leak into args: %v", args)
	}
}

func TestExtractToolArgs_Empty(t *testing.T) {
	// name — это аргумент, а не служебное поле; только tool убирается
	args := extractToolArgs(map[string]any{"tool": "GetDateTime"})
	if len(args) != 0 {
		t.Errorf("expected empty args, got %v", args)
	}
	// name без tool — остаётся как аргумент
	args = extractToolArgs(map[string]any{"name": "Привет"})
	if len(args) != 1 {
		t.Errorf("expected 1 arg (name), got %v", args)
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

func TestParseAtTime(t *testing.T) {
	now := time.Now()
	tm, err := parseAtTime("10:05")
	if err != nil {
		t.Fatalf("parseAtTime: %v", err)
	}
	if tm.Hour() != 10 || tm.Minute() != 5 {
		t.Errorf("time = %v", tm)
	}
	// should be today or tomorrow
	diff := tm.Sub(now)
	if diff < 0 || diff > 24*time.Hour {
		t.Errorf("unexpected time diff: %v", diff)
	}
}

func TestParseAtTime_BadFormat(t *testing.T) {
	_, err := parseAtTime("abc")
	if err == nil {
		t.Error("expected error for bad format")
	}
}

func TestNormalizeCron_5Fields(t *testing.T) {
	norm, err := normalizeCron("0 7 * * 1-5")
	if err != nil {
		t.Fatalf("normalizeCron: %v", err)
	}
	if norm != "0 0 7 * * 1-5" {
		t.Errorf("got %q, want 6-field expr", norm)
	}
}

func TestNormalizeCron_6Fields(t *testing.T) {
	norm, err := normalizeCron("0 0 7 * * 1-5")
	if err != nil {
		t.Fatalf("normalizeCron: %v", err)
	}
	if norm != "0 0 7 * * 1-5" {
		t.Errorf("got %q", norm)
	}
}

func TestNormalizeCron_WithTimePrefix(t *testing.T) {
	norm, err := normalizeCron("10:05 0 7 * * 1-5")
	if err != nil {
		t.Fatalf("normalizeCron: %v", err)
	}
	if norm != "0 0 7 * * 1-5" {
		t.Errorf("got %q", norm)
	}
}

func TestAgent_GetWeather(t *testing.T) {
	// Fake HA REST API returning weather entity
	haREST := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/states" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"entity_id": "weather.yandex_weather",
					"state":     "clear-night",
					"attributes": map[string]any{
						"friendly_name":  "Yandex Weather",
						"temperature":    14.0,
						"feels_like":     13,
						"humidity":       55,
						"pressure":       1015,
						"wind_speed":     2.88,
						"wind_bearing":   90,
						"condition":      "clear",
						"forecastHourly": []map[string]any{
							{"datetime": "2026-09-10T19:00:00+03:00", "native_temperature": 12.0, "condition": "sunny", "native_wind_speed": 0.8},
							{"datetime": "2026-09-10T20:00:00+03:00", "native_temperature": 11.0, "condition": "partlycloudy", "native_wind_speed": 1.2},
						},
					},
				},
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(haREST.Close)

	// Fake MCP server with just HassTurnOn (so ListTools works)
	mcpSrv := server.NewMCPServer("HA Test", "1.0.0")
	mcpSrv.AddTool(
		mcp.NewTool("HassTurnOn", mcp.WithDescription("turn on")),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(`{"success": true}`), nil
		},
	)
	haMCP := httptest.NewServer(server.NewStreamableHTTPServer(mcpSrv))
	t.Cleanup(haMCP.Close)

	haCli, err := hamcp.New(context.Background(), haMCP.URL, hamcp.Options{Token: "test-token"})
	if err != nil {
		t.Fatalf("ha mcp: %v", err)
	}
	t.Cleanup(func() { haCli.Close() })

	giga := newFakeGigaChat(t,
		chatResponseText("Всё в порядке"),
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

	agent := NewAgent(gigaCli, haCli, &fakeScheduler{}, haREST.URL, nil)
	result, err := agent.getWeather(context.Background(), map[string]any{"hours": 2.0})
	if err != nil {
		t.Fatalf("getWeather: %v", err)
	}
	if !stringsContains(result, "Yandex Weather") {
		t.Errorf("expected Yandex Weather in result, got: %s", result)
	}
	if !stringsContains(result, "14") {
		t.Errorf("expected temperature 14 in result, got: %s", result)
	}
	if !stringsContains(result, "19:00") {
		t.Errorf("expected hourly forecast for 19:00, got: %s", result)
	}
	if !stringsContains(result, "sunny") {
		t.Errorf("expected 'sunny' condition, got: %s", result)
	}
}

func TestAgent_GetWeather_NoEntity(t *testing.T) {
	haREST := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{})
	}))
	t.Cleanup(haREST.Close)

	mcpSrv := server.NewMCPServer("HA Test", "1.0.0")
	mcpSrv.AddTool(
		mcp.NewTool("HassTurnOn", mcp.WithDescription("turn on")),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(`{"success": true}`), nil
		},
	)
	haMCP := httptest.NewServer(server.NewStreamableHTTPServer(mcpSrv))
	t.Cleanup(haMCP.Close)
	haCli, err := hamcp.New(context.Background(), haMCP.URL, hamcp.Options{Token: "test-token"})
	if err != nil {
		t.Fatalf("ha mcp: %v", err)
	}
	t.Cleanup(func() { haCli.Close() })

	agent := NewAgent(nil, haCli, nil, haREST.URL, nil)
	result, err := agent.getWeather(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("getWeather: %v", err)
	}
	if !stringsContains(result, "нет сущностей погоды") {
		t.Errorf("expected 'no weather entities' message, got: %s", result)
	}
}

func TestNormalizeCron_TooShort(t *testing.T) {
	_, err := normalizeCron("10:05 10:05 * * *")
	if err == nil {
		t.Error("expected error for too-short cron")
	}
}