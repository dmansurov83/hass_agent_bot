package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hare "hass-agent-bot/internal/ha/rest"
	"hass-agent-bot/internal/scheduler"
)

// fakeHAClient implements HAClient for tests.
type fakeHAClient struct {
	statesFn func(ctx context.Context) ([]hare.State, error)
	callSvc  func(ctx context.Context, domain, service string, data map[string]any) error
	histFn   func(ctx context.Context, entityID string, start, end time.Time) ([]hare.State, error)
}

func (f *fakeHAClient) Token() string                           { return "test-token" }
func (f *fakeHAClient) BaseURL() string                         { return "http://ha.local" }
func (f *fakeHAClient) States(ctx context.Context) ([]hare.State, error)   { return f.statesFn(ctx) }
func (f *fakeHAClient) CallService(ctx context.Context, d, s string, data map[string]any) error { return f.callSvc(ctx, d, s, data) }
func (f *fakeHAClient) History(ctx context.Context, eid string, start, end time.Time) ([]hare.State, error) { return f.histFn(ctx, eid, start, end) }

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
	called := false
	ha := &fakeHAClient{
		statesFn: func(ctx context.Context) ([]hare.State, error) {
			return []hare.State{
				{EntityID: "light.living_room", State: "off", Attributes: map[string]any{"friendly_name": "Свет в зале"}},
			}, nil
		},
		callSvc: func(ctx context.Context, domain, service string, data map[string]any) error {
			called = true
			if domain != "light" || service != "turn_on" {
				return fmt.Errorf("unexpected call: %s.%s", domain, service)
			}
			return nil
		},
	}

	giga := newFakeGigaChat(t,
		chatResponseToolCall(t, "HassTurnOn",
			map[string]any{"name": "Свет в зале"},
		),
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

	agent := NewAgent(gigaCli, ha, &fakeScheduler{}, nil, nil)
	reply, err := agent.HandleMessage(WithChatID(context.Background(), 100), "включи свет в зале")
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if !stringsContains(reply, "Свет в зале") && !stringsContains(reply, "включ") {
		t.Errorf("unexpected reply: %q", reply)
	}
	if !called {
		t.Error("HassTurnOn should have been called (CallService)")
	}
}

func TestAgent_HandleMessage_NoToolCall(t *testing.T) {
	ha := &fakeHAClient{
		statesFn: func(ctx context.Context) ([]hare.State, error) {
			return []hare.State{
				{EntityID: "weather.test", State: "sunny", Attributes: map[string]any{"friendly_name": "Test"}},
			}, nil
		},
	}

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

	agent := NewAgent(gigaCli, ha, &fakeScheduler{}, nil, nil)
	reply, err := agent.HandleMessage(WithChatID(context.Background(), 100), "привет")
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if !stringsContains(reply, "помочь") {
		t.Errorf("unexpected reply: %q", reply)
	}
}

func TestAgent_Reset(t *testing.T) {
	agent := NewAgent(nil, nil, nil, nil, nil)
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
	agent := NewAgent(nil, nil, nil, store, nil)
	agent.histories[100] = []Message{{Role: "user", Content: "foo"}, {Role: "assistant", Content: "bar"}}
	agent.saveHistory(100)

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

	store.Save(context.Background(), 1, []Message{{Role: "user", Content: "hi"}})
	if err := os.WriteFile(filepath.Join(dir, "chat_2.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
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

func TestAgent_GetWeather(t *testing.T) {
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

	ha := hare.New(haREST.URL, hare.Options{Token: "test-token"})
	agent := NewAgent(nil, ha, nil, nil, nil)
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

	ha := hare.New(haREST.URL, hare.Options{Token: "test-token"})
	agent := NewAgent(nil, ha, nil, nil, nil)
	result, err := agent.getWeather(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("getWeather: %v", err)
	}
	if !stringsContains(result, "нет сущностей погоды") {
		t.Errorf("expected 'no weather entities' message, got: %s", result)
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
	if args["name"] != "Light" || args["area"] != "Гостиная" {
		t.Errorf("wrong args: %v", args)
	}
	if _, ok := args["tool"]; ok {
		t.Errorf("'tool' should not leak into args: %v", args)
	}
}

func TestExtractToolArgs_Empty(t *testing.T) {
	args := extractToolArgs(map[string]any{"tool": "GetDateTime"})
	if len(args) != 0 {
		t.Errorf("expected empty args, got %v", args)
	}
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

func TestMemoryStore_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileMemoryStore(dir, nil)
	if err != nil {
		t.Fatalf("NewFileMemoryStore: %v", err)
	}
	defer store.Close()

	m := Memory{ChatID: 42, Category: "preference", Text: "Свет в спальне всегда выключать в 23:00"}
	if err := store.Add(context.Background(), &m); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if m.ID == "" {
		t.Error("expected an ID assigned")
	}
	if _, err := os.Stat(filepath.Join(dir, "mem_42.json")); err != nil {
		t.Fatalf("memory file not written: %v", err)
	}

	mems, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := mems[42]
	if len(got) != 1 || got[0].Text != "Свет в спальне всегда выключать в 23:00" || got[0].ChatID != 42 {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
}

func TestMemoryStore_Dedup(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileMemoryStore(dir, nil)
	if err != nil {
		t.Fatalf("NewFileMemoryStore: %v", err)
	}
	defer store.Close()

	m1 := Memory{ChatID: 42, Category: "preference", Text: "Свет в спальне всегда выключать в 23:00"}
	if err := store.Add(context.Background(), &m1); err != nil {
		t.Fatalf("Add 1: %v", err)
	}
	firstID := m1.ID

	m2 := Memory{ChatID: 42, Category: "preference", Text: "свет в спальне всегда выключать в 23:00"}
	if err := store.Add(context.Background(), &m2); err != nil {
		t.Fatalf("Add 2: %v", err)
	}

	mems, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := mems[42]
	if len(got) != 1 {
		t.Fatalf("expected dedup to 1, got %d: %+v", len(got), got)
	}
	if got[0].ID != firstID {
		t.Errorf("expected same ID after dedup, got %s != %s", got[0].ID, firstID)
	}
}

func TestMemoryStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileMemoryStore(dir, nil)
	if err != nil {
		t.Fatalf("NewFileMemoryStore: %v", err)
	}
	defer store.Close()

	m := Memory{ChatID: 42, Text: "Имя пользователя — Иван"}
	if err := store.Add(context.Background(), &m); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.Delete(context.Background(), 42, m.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mem_42.json")); !os.IsNotExist(err) {
		t.Errorf("memory file should be removed when empty")
	}
	mems, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(mems) != 0 {
		t.Errorf("expected empty after delete, got %+v", mems)
	}
}

func TestMemoryStore_PerChatFiles(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileMemoryStore(dir, nil)
	if err != nil {
		t.Fatalf("NewFileMemoryStore: %v", err)
	}
	defer store.Close()

	m1 := Memory{ChatID: 42, Text: "Факт чата 42"}
	if err := store.Add(context.Background(), &m1); err != nil {
		t.Fatalf("Add 42: %v", err)
	}
	m2 := Memory{ChatID: 43, Text: "Факт чата 43"}
	if err := store.Add(context.Background(), &m2); err != nil {
		t.Fatalf("Add 43: %v", err)
	}

	for _, id := range []string{"mem_42.json", "mem_43.json"} {
		if _, err := os.Stat(filepath.Join(dir, id)); err != nil {
			t.Fatalf("file %s not written: %v", id, err)
		}
	}

	mems, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(mems) != 2 || len(mems[42]) != 1 || len(mems[43]) != 1 {
		t.Errorf("unexpected map: %+v", mems)
	}

	// deleting from chat 42 must not touch chat 43
	if err := store.Delete(context.Background(), 42, m1.ID); err != nil {
		t.Fatalf("Delete 42: %v", err)
	}
	mems, err = store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(mems[43]) != 1 {
		t.Errorf("chat 43 memory lost: %+v", mems)
	}
}

func TestAgent_MemoryTools(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileMemoryStore(dir, nil)
	if err != nil {
		t.Fatalf("NewFileMemoryStore: %v", err)
	}
	defer store.Close()

	agent := NewAgent(nil, nil, nil, nil, store)
	ctx := WithChatID(context.Background(), 100)

	res, err := agent.handleRemember(ctx, map[string]any{"text": "Пользователя зовут Иван", "category": "user"})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if !stringsContains(res, "Иван") {
		t.Errorf("remember reply: %q", res)
	}

	res, err = agent.handleRecall(ctx, map[string]any{"query": "Иван"})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !stringsContains(res, "Иван") || !stringsContains(res, "mem_") {
		t.Errorf("recall reply: %q", res)
	}

	// isolation: another chat must not see this memory
	other := WithChatID(context.Background(), 200)
	res, err = agent.handleRecall(other, map[string]any{})
	if err != nil {
		t.Fatalf("recall other: %v", err)
	}
	if !stringsContains(res, "ничего нет") {
		t.Errorf("expected empty memory for other chat, got %q", res)
	}

	// forget by ID extracted from recall
	res, err = agent.handleRecall(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("recall all: %v", err)
	}
	id := strings.TrimPrefix(strings.SplitN(strings.TrimPrefix(res, "["), "]", 2)[0], "[")
	if id == "" {
		t.Fatalf("could not parse id from recall: %q", res)
	}
	res, err = agent.handleForget(ctx, map[string]any{"id": id})
	if err != nil {
		t.Fatalf("forget: %v", err)
	}
	if !stringsContains(res, "Забыл") {
		t.Errorf("forget reply: %q", res)
	}

	res, err = agent.handleRecall(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("recall after forget: %v", err)
	}
	if !stringsContains(res, "ничего нет") {
		t.Errorf("expected empty memory after forget, got %q", res)
	}
}

func TestAgent_MemoryBlockInject(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileMemoryStore(dir, nil)
	if err != nil {
		t.Fatalf("NewFileMemoryStore: %v", err)
	}
	defer store.Close()

	agent := NewAgent(nil, nil, nil, nil, store)
	ctx := WithChatID(context.Background(), 100)
	if _, err := agent.handleRemember(ctx, map[string]any{"text": "Любимая температура 24°C", "category": "preference"}); err != nil {
		t.Fatalf("remember: %v", err)
	}

	block := agent.memoryBlock(100)
	if block == "" || !stringsContains(block, "24") {
		t.Errorf("memory block not injected: %q", block)
	}
	// other chat must not see it
	if other := agent.memoryBlock(200); other != "" {
		t.Errorf("other chat sees memory: %q", other)
	}
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

func TestNormalizeCron_TooShort(t *testing.T) {
	_, err := normalizeCron("10:05 10:05 * * *")
	if err == nil {
		t.Error("expected error for too-short cron")
	}
}
