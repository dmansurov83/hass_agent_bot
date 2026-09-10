package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	hare "hass-agent-bot/internal/ha/rest"
	"hass-agent-bot/internal/scheduler"
)

const maxIterations = 5

// ctxKey is a private type for context values to avoid collisions.
type ctxKey int

const (
	ctxKeyChatID ctxKey = iota
	ctxKeyTaskMode
)

// WithChatID returns a context carrying the TG chat ID (used by AI tasks).
func WithChatID(ctx context.Context, chatID int64) context.Context {
	return context.WithValue(ctx, ctxKeyChatID, chatID)
}

// ChatIDFrom returns the TG chat ID stored in ctx (0 if absent).
func ChatIDFrom(ctx context.Context) int64 {
	v, _ := ctx.Value(ctxKeyChatID).(int64)
	return v
}

// WithTaskMode marks the context as a background AI task execution: the agent
// must NOT schedule anything again, just perform the request now.
func WithTaskMode(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeyTaskMode, true)
}

func taskModeFrom(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeyTaskMode).(bool)
	return v
}

// Scheduler interface used by the agent to schedule actions.
type Scheduler interface {
	ScheduleAt(runAt time.Time, action scheduler.Action, label string) (*scheduler.Job, error)
	ScheduleIn(d time.Duration, action scheduler.Action, label string) (*scheduler.Job, error)
	ScheduleCron(expr string, action scheduler.Action, label string) (*scheduler.Job, error)
	Cancel(id string) bool
	List() []scheduler.Job
}

// HAClient is the subset of the HA REST client used by the agent. Defined as an
// interface so tests can substitute a fake.
type HAClient interface {
	Token() string
	BaseURL() string
	States(ctx context.Context) ([]hare.State, error)
	CallService(ctx context.Context, domain, service string, data map[string]any) error
	History(ctx context.Context, entityID string, start, end time.Time) ([]hare.State, error)
}

var _ HAClient = (*hare.Client)(nil)

// HAStateAliases is a map from an entity or friendly name (case-insensitive) to
// its entity_id. Populated from GetLiveContext/States at runtime.
type haIndex struct {
	byName   map[string]string // lower(friendly_name) / lower(entity_id) / lower(alias) → entity_id
	byArea   map[string][]string
	stateClass map[string]string // entity_id → state_class
	unit     map[string]string // entity_id → unit_of_measurement
	friendly map[string]string // entity_id → friendly_name
	have     bool
}

type Agent struct {
	llm   LLMClient
	ha    HAClient
	sched Scheduler
	log   *slog.Logger

	index haIndex

	mu        sync.Mutex
	histories map[int64][]Message // chatID → messages (in-memory cache)
	store     HistoryStore        // persistence (nil = don't save)
	memory    MemoryStore         // durable memory (nil = disabled)
}

func NewAgent(llm LLMClient, ha HAClient, sched Scheduler, store HistoryStore, mem MemoryStore) *Agent {
	a := &Agent{
		llm:       llm,
		ha:        ha,
		sched:     sched,
		log:       slog.Default(),
		histories: make(map[int64][]Message),
		index:     haIndex{byName: map[string]string{}, byArea: map[string][]string{}, stateClass: map[string]string{}, unit: map[string]string{}, friendly: map[string]string{}},
		store:     store,
		memory:    mem,
	}
	if store != nil {
		loaded, err := store.Load(context.Background())
		if err != nil {
			a.log.Warn("agent: load history", "error", err)
		} else {
			a.histories = loaded
		}
	}
	return a
}

// EnsureIndex populates the entity index from HA states (once per message call).
func (a *Agent) EnsureIndex(ctx context.Context) error {
	if a.index.have {
		return nil
	}
	states, err := a.ha.States(ctx)
	if err != nil {
		return fmt.Errorf("agent: load states: %w", err)
	}
	a.rebuildIndex(states)
	return nil
}

func (a *Agent) rebuildIndex(states []hare.State) {
	for k := range a.index.byName {
		delete(a.index.byName, k)
	}
	clear(a.index.byArea)
	clear(a.index.stateClass)
	clear(a.index.unit)
	clear(a.index.friendly)

	for _, s := range states {
		eid := s.EntityID
		a.index.friendly[eid] = friendlyName(s)
		a.index.byName[strings.ToLower(eid)] = eid
		if fn := friendlyName(s); fn != "" && fn != eid {
			a.index.byName[strings.ToLower(fn)] = eid
		}
		if uom, ok := s.Attributes["unit_of_measurement"].(string); ok {
			a.index.unit[eid] = uom
		}
		if sc, ok := s.Attributes["state_class"].(string); ok {
			a.index.stateClass[eid] = sc
		}
	}
	// area mapping via WS registries (best-effort; ignore errors)
	areas, entities, devices, err := hare.WSRegistries(context.Background(), a.ha.BaseURL(), a.ha.Token())
	if err == nil {
		areaName := map[string]string{}
		for _, ar := range areas {
			areaName[ar.AreaID] = ar.Name
			for _, al := range ar.Aliases {
				areaName[al] = ar.Name
			}
		}
		devArea := map[string]string{}
		for _, d := range devices {
			devArea[d.DeviceID] = d.AreaID
		}
		for _, e := range entities {
			aid := e.AreaID
			if aid == "" {
				aid = devArea[e.DeviceID]
			}
			an := areaName[aid]
			if an == "" {
				continue
			}
			a.index.byArea[strings.ToLower(an)] = append(a.index.byArea[strings.ToLower(an)], e.EntityID)
		}
	}
	a.index.have = true
}

// resolveEntity turns a human name / alias / area into concrete entity IDs.
// Returns the resolved entity IDs (may be many when an area matches).
func (a *Agent) resolveEntity(ctx context.Context, name, area string) ([]string, error) {
	if err := a.EnsureIndex(ctx); err != nil {
		return nil, err
	}
	var ids []string
	seen := map[string]bool{}

	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			ids = append(ids, s)
		}
	}

	if name != "" {
		key := strings.ToLower(strings.TrimSpace(name))
		if eid, ok := a.index.byName[key]; ok {
			add(eid)
		} else {
			// substring match as fallback
			for k, v := range a.index.byName {
				if strings.Contains(k, key) {
					add(v)
				}
			}
			if len(ids) == 0 {
				return nil, fmt.Errorf("устройство %q не найдено в Home Assistant", name)
			}
		}
	}

	if area != "" {
		key := strings.ToLower(strings.TrimSpace(area))
		if ents, ok := a.index.byArea[key]; ok {
			for _, e := range ents {
				add(e)
			}
		} else {
			// fallback: match by area substring
			for k, ents := range a.index.byArea {
				if strings.Contains(k, key) {
					for _, e := range ents {
						add(e)
					}
				}
			}
			if len(ids) == 0 {
				return nil, fmt.Errorf("зона %q не найдена в Home Assistant", area)
			}
		}
	}

	if name == "" && area == "" {
		return nil, fmt.Errorf("укажи name или area")
	}
	return ids, nil
}

func (a *Agent) HandleMessage(ctx context.Context, userText string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	chatID := ChatIDFrom(ctx)
	taskMode := taskModeFrom(ctx)

	functions := AllFunctions()
	if taskMode {
		functions = HAOnlyFunctions()
	}

	history := a.histories[chatID]
	messages := make([]Message, 0, 2+len(history)+1)

	sys := SystemPrompt().Content
	now := time.Now()
	sys += "\n\nТекущая дата и время: " + now.Format("02.01.2006 15:04") + " (" + now.Format("Monday") + ")"
	if memBlock := a.memoryBlock(chatID); memBlock != "" {
		sys += "\n\n=== Запомненное о пользователе (память) ===\n" + memBlock + "\n=== Конец памяти ==="
	}

	if taskMode {
		messages = append(messages, Message{Role: "system", Content: sys + "\n\nВАЖНО: Это фоновое выполнение задачи. Время уже наступило. НЕ вызывай schedule_action и schedule_ai_action — просто выполни то, что просят, прямо сейчас. Не нужно ничего планировать."})
	} else {
		messages = append(messages, Message{Role: "system", Content: sys})
	}
	messages = append(messages, history...)
	messages = append(messages, Message{Role: "user", Content: userText})

	lastToolCalls := map[string]int{}

	for i := 0; i < maxIterations; i++ {
		resp, err := a.llm.Chat(ctx, messages, functions)
		if err != nil {
			return "", fmt.Errorf("agent: chat iteration %d: %w", i, err)
		}

		choice := resp.Choices[0]
		msg := choice.Message

		if msg.FunctionCall != nil {
			fc := msg.FunctionCall

			a.log.Debug("agent: llm wants tool",
				"iter", i,
				"name", fc.Name,
				"args", fc.Arguments,
				"state", msg.FunctionsStateID,
			)

			// Loop guard: if the model keeps asking for the exact same
			// tool+args it already got a result for, it is going in circles.
			// Count repeats instead of breaking on the first one — the model
			// may legitimately retry and then switch to another tool (e.g.
			// GetDateTime -> HassGetHistory). Break only after 3 identical
			// calls in this turn.
			if sig := fc.Signature(); sig != "" {
				lastToolCalls[sig]++
				if lastToolCalls[sig] >= 3 {
					a.log.Warn("agent: repeated tool call, breaking loop", "name", fc.Name, "iter", i)
					final := fmt.Sprintf("Не удалось получить ответ: модель повторно вызывает инструмент %s. Попробуй переформулировать запрос.", fc.Name)
					return final, nil
				}
			}

			// Correct protocol: pass the assistant's function_call back to the
			// model and append the tool result as a Role=function message.
			// (GigaChat: message.function_call + role "function"; OpenAI is
			// normalized in the provider client.)
			messages = append(messages, Message{
				Role:             "assistant",
				Content:          msg.Content,
				FunctionCall:     fc,
				FunctionsStateID: msg.FunctionsStateID,
			})

			result, err := a.executeTool(ctx, fc)
			if err != nil {
				result = fmt.Sprintf("Ошибка: %v", err)
			}
			a.log.Info("agent: tool result",
				"name", fc.Name,
				"args", fc.Arguments,
				"result", result,
				"err", err,
			)
			messages = append(messages, Message{
				Role:    "function",
				Name:    fc.Name,
				Content: wrapToolResult(result),
			})
		} else {
			final := msg.Content

			// save history only for interactive dialogs, not background AI tasks
			if !taskMode {
				history = append(history,
					Message{Role: "user", Content: userText},
					Message{Role: "assistant", Content: final},
				)
				if len(history) > 10 {
					history = history[len(history)-10:]
				}
				a.histories[chatID] = history
				a.saveHistory(chatID)
			}

			return final, nil
		}
	}

	return "", fmt.Errorf("agent: exceeded max iterations (%d)", maxIterations)
}

func (a *Agent) saveHistory(chatID int64) {
	if a.store == nil {
		return
	}
	msgs := a.histories[chatID]
	// don't persist empty histories
	if len(msgs) == 0 {
		return
	}
	if err := a.store.Save(context.Background(), chatID, msgs); err != nil {
		a.log.Error("agent: save history", "chat_id", chatID, "error", err)
	}
}

// memoryBlock renders the durable memories of a chat as a readable text block
// injected into the system prompt. Empty string when memory is disabled.
func (a *Agent) memoryBlock(chatID int64) string {
	if a.memory == nil {
		return ""
	}
	mems, err := a.memory.Load(context.Background())
	if err != nil {
		a.log.Warn("agent: load memory", "error", err)
		return ""
	}
	var lines []string
	for _, m := range mems[chatID] {
		line := "- " + m.Text
		if m.Category != "" && m.Category != "other" {
			line = "- [" + m.Category + "] " + m.Text
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// Reset clears history for a specific chat (removes from cache + store).
func (a *Agent) Reset(chatID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.store != nil {
		if err := a.store.Delete(context.Background(), chatID); err != nil {
			a.log.Error("agent: delete history", "chat_id", chatID, "error", err)
		}
	}
	delete(a.histories, chatID)
}

// ResetAll clears all chat histories.
func (a *Agent) ResetAll() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.store != nil {
		for chatID := range a.histories {
			if err := a.store.Delete(context.Background(), chatID); err != nil {
				a.log.Error("agent: delete history", "chat_id", chatID, "error", err)
			}
		}
	}
	a.histories = make(map[int64][]Message)
}

func (a *Agent) executeTool(ctx context.Context, fc *FunctionCall) (string, error) {
	ctxTool, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	a.log.Info("agent: executing tool", "name", fc.Name, "args", fc.Arguments)

	// В фоновом режиме (AI-задача) планирование запрещено — выполнить сразу
	if taskModeFrom(ctx) && (fc.Name == "schedule_action" || fc.Name == "schedule_ai_action") {
		return "", fmt.Errorf("планирование запрещено при фоновом выполнении: %s (время уже наступило)", fc.Name)
	}

	switch fc.Name {
	case "schedule_action":
		a.log.Info("agent: schedule_action full args", "raw_fc_arguments", fc.Arguments)
		return a.handleSchedule(ctx, fc.Arguments)

	case "schedule_ai_action":
		a.log.Info("agent: schedule_ai_action full args", "raw_fc_arguments", fc.Arguments)
		return a.handleScheduleAI(ctx, fc.Arguments)

	case "HassGetWeather":
		return a.getWeather(ctxTool, fc.Arguments)

	case "HassGetHistory":
		return a.getHistory(ctxTool, fc.Arguments)

	case "HassListSensors":
		return a.listSensors(ctxTool)

	case "HassCancelAllTimers":
		if a.sched == nil {
			return "", fmt.Errorf("scheduler не инициализирован")
		}
		n := 0
		for _, j := range a.sched.List() {
			if a.sched.Cancel(j.ID) {
				n++
			}
		}
		return fmt.Sprintf("Отменено таймеров: %d", n), nil

	case "remember":
		return a.handleRemember(ctx, fc.Arguments)

	case "recall":
		return a.handleRecall(ctx, fc.Arguments)

	case "forget":
		return a.handleForget(ctx, fc.Arguments)

	default:
		// All Hass* tools proxy to native handlers
		return a.handleHassTool(ctxTool, fc.Name, fc.Arguments)
	}
}

// wrapToolResult makes sure the tool result sent back to the LLM is a valid
// JSON string, which GigaChat strictly requires for function results. Textual
// tool outputs and error messages are JSON-encoded under a "result" key.
func wrapToolResult(result string) string {
	if result == "" {
		return `{"result":""}`
	}
	if json.Valid([]byte(result)) {
		return result
	}
	b, err := json.Marshal(map[string]string{"result": result})
	if err != nil {
		return `{"result":"<unserializable>"}`
	}
	return string(b)
}

// ExecuteToolPublic runs a scheduled or admin tool call by name+args (used by
// the scheduler executor and command handlers). Returns the tool output text.
func (a *Agent) ExecuteToolPublic(ctx context.Context, name string, args map[string]any) (string, error) {
	return a.executeTool(ctx, &FunctionCall{Name: name, Arguments: args})
}

// handleRemember saves a durable memory for the current chat.
func (a *Agent) handleRemember(ctx context.Context, args map[string]any) (string, error) {
	text, _ := args["text"].(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("укажи text — что запомнить")
	}
	if a.memory == nil {
		return "", fmt.Errorf("память отключена (memory store не настроен)")
	}

	cat, _ := args["category"].(string)
	cat = strings.ToLower(strings.TrimSpace(cat))
	switch cat {
	case "", "user", "preference", "habit", "fact", "other":
	default:
		cat = "other"
	}

	m := &Memory{
		ChatID:    ChatIDFrom(ctx),
		Category:  cat,
		Text:      text,
		UpdatedAt: time.Now(),
	}
	if err := a.memory.Add(ctx, m); err != nil {
		return "", fmt.Errorf("не удалось сохранить в память: %w", err)
	}
	a.log.Info("agent: remembered", "chat_id", m.ChatID, "category", cat, "text", text)
	return fmt.Sprintf("Запомнил: %s", text), nil
}

// handleRecall returns memories that match the query (case-insensitive
// substring over text+category), or all memories when query is empty.
func (a *Agent) handleRecall(ctx context.Context, args map[string]any) (string, error) {
	if a.memory == nil {
		return "", fmt.Errorf("память отключена (memory store не настроен)")
	}
	mems, err := a.memory.Load(ctx)
	if err != nil {
		return "", fmt.Errorf("не удалось прочитать память: %w", err)
	}

	chatID := ChatIDFrom(ctx)
	query := strings.ToLower(strings.TrimSpace(strVal(args["query"])))

	var lines []string
	for _, m := range mems[chatID] {
		if query != "" && !strings.Contains(strings.ToLower(m.Text), query) &&
			!strings.Contains(strings.ToLower(m.Category), query) {
			continue
		}
		line := fmt.Sprintf("[%s] %s", m.ID, m.Text)
		if m.Category != "" && m.Category != "other" {
			line = fmt.Sprintf("[%s] (%s) %s", m.ID, m.Category, m.Text)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		if query != "" {
			return "В памяти ничего не найдено по запросу.", nil
		}
		return "В памяти пока ничего нет.", nil
	}
	return strings.Join(lines, "\n"), nil
}

// handleForget deletes one memory by ID.
func (a *Agent) handleForget(ctx context.Context, args map[string]any) (string, error) {
	id, _ := args["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("укажи id памяти (получи через recall)")
	}
	if a.memory == nil {
		return "", fmt.Errorf("память отключена (memory store не настроен)")
	}

	if err := a.memory.Delete(ctx, ChatIDFrom(ctx), id); err != nil {
		return "", fmt.Errorf("не удалось удалить: %w", err)
	}
	a.log.Info("agent: forgot", "chat_id", ChatIDFrom(ctx), "id", id)
	return fmt.Sprintf("Забыл память %s.", id), nil
}

// handleHassTool implements the HA device tools natively via REST.
func (a *Agent) handleHassTool(ctx context.Context, name string, args map[string]any) (string, error) {
	switch name {
	case "HassTurnOn":
		return a.setEntities(ctx, args, true, false)
	case "HassTurnOff":
		return a.setEntities(ctx, args, false, false)
	case "HassLightSet":
		return a.lightSet(ctx, args)
	case "HassClimateSetTemperature":
		return a.climateTemp(ctx, args)
	case "HassSetVolume":
		return a.mediaVolume(ctx, args, false)
	case "HassSetVolumeRelative":
		return a.mediaVolume(ctx, args, true)
	case "HassMediaPause":
		return a.mediaAction(ctx, args, "media_pause")
	case "HassMediaUnpause":
		return a.mediaAction(ctx, args, "media_play")
	case "HassMediaNext":
		return a.mediaAction(ctx, args, "media_next_track")
	case "HassMediaPrevious":
		return a.mediaAction(ctx, args, "media_previous_track")
	case "HassMediaPlayerMute":
		return a.mediaMute(ctx, args, true)
	case "HassMediaPlayerUnmute":
		return a.mediaMute(ctx, args, false)
	case "HassMediaSearchAndPlay":
		return a.mediaSearchPlay(ctx, args)
	case "HassFanSetSpeed":
		return a.fanSetSpeed(ctx, args)
	case "HassBroadcast":
		return a.broadcast(ctx, args)
	case "GetLiveContext":
		return a.liveContext(ctx, args)
	default:
		return "", fmt.Errorf("неизвестный инструмент: %s", name)
	}
}

func (a *Agent) setEntities(ctx context.Context, args map[string]any, turnOn bool, toggle bool) (string, error) {
	name, _ := args["name"].(string)
	area, _ := args["area"].(string)
	domain := args["domain"]
	deviceClass := args["device_class"]

	ids, err := a.resolveEntity(ctx, name, area)
	if err != nil {
		// if name/area absent but domain given — operate on whole domain
		if dom, ok := asStringSlice(domain); ok && len(dom) > 0 {
			ids, err = a.resolveDomain(ctx, dom)
			if err != nil {
				return "", err
			}
		} else if dc, ok := asStringSlice(deviceClass); ok && len(dc) > 0 {
			ids, err = a.resolveDeviceClass(ctx, dc)
			if err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}

	action := "turn_on"
	if !turnOn {
		action = "turn_off"
	}

	// group by domain for the service call
	byDomain := map[string][]string{}
	for _, id := range ids {
		dom := strings.SplitN(id, ".", 2)[0]
		byDomain[dom] = append(byDomain[dom], id)
	}

	var done []string
	for dom, ents := range byDomain {
		data := map[string]any{"entity_id": ents}
		if err := a.ha.CallService(ctx, dom, action, data); err != nil {
			return "", fmt.Errorf("не удалось %s %s: %w", action, strings.Join(ents, ", "), err)
		}
		done = append(done, ents...)
	}
	if len(done) == 0 {
		return "Устройства не найдены.", nil
	}
	return fmt.Sprintf("%s: %s", map[bool]string{true: "Включено", false: "Выключено"}[turnOn], strings.Join(done, ", ")), nil
}

func (a *Agent) resolveDomain(ctx context.Context, domains []string) ([]string, error) {
	if err := a.EnsureIndex(ctx); err != nil {
		return nil, err
	}
	var ids []string
	states, err := a.ha.States(ctx)
	if err != nil {
		return nil, err
	}
	allow := map[string]bool{}
	for _, d := range domains {
		allow[d] = true
	}
	for _, s := range states {
		dom := strings.SplitN(s.EntityID, ".", 2)[0]
		if allow[dom] {
			ids = append(ids, s.EntityID)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("нет устройств домена %v", domains)
	}
	return ids, nil
}

func (a *Agent) resolveDeviceClass(ctx context.Context, classes []string) ([]string, error) {
	if err := a.EnsureIndex(ctx); err != nil {
		return nil, err
	}
	states, err := a.ha.States(ctx)
	if err != nil {
		return nil, err
	}
	allow := map[string]bool{}
	for _, c := range classes {
		allow[strings.ToLower(c)] = true
	}
	var ids []string
	for _, s := range states {
		if dc, ok := s.Attributes["device_class"].(string); ok && allow[strings.ToLower(dc)] {
			ids = append(ids, s.EntityID)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("нет устройств с device_class %v", classes)
	}
	return ids, nil
}

func (a *Agent) lightSet(ctx context.Context, args map[string]any) (string, error) {
	ids, err := a.resolveEntity(ctx, strVal(args["name"]), strVal(args["area"]))
	if err != nil {
		return "", err
	}
	data := map[string]any{"entity_id": ids}
	if b, ok := args["brightness"].(float64); ok {
		data["brightness_pct"] = int(b)
	}
	if c, ok := args["color"].(string); ok && c != "" {
		data["color_name"] = c
	}
	if t, ok := args["temperature"].(float64); ok {
		data["color_temp_kelvin"] = int(t)
	}
	if err := a.ha.CallService(ctx, "light", "turn_on", data); err != nil {
		return "", err
	}
	return fmt.Sprintf("Свет настроен: %s", strings.Join(ids, ", ")), nil
}

func (a *Agent) climateTemp(ctx context.Context, args map[string]any) (string, error) {
	ids, err := a.resolveEntity(ctx, strVal(args["name"]), strVal(args["area"]))
	if err != nil {
		return "", err
	}
	temp, ok := args["temperature"].(float64)
	if !ok {
		return "", fmt.Errorf("укажи temperature для HassClimateSetTemperature")
	}
	data := map[string]any{"entity_id": ids, "temperature": temp}
	if err := a.ha.CallService(ctx, "climate", "set_temperature", data); err != nil {
		return "", err
	}
	return fmt.Sprintf("Температура установлена: %s → %g°C", strings.Join(ids, ", "), temp), nil
}

func (a *Agent) mediaVolume(ctx context.Context, args map[string]any, relative bool) (string, error) {
	ids, err := a.resolveEntity(ctx, strVal(args["name"]), strVal(args["area"]))
	if err != nil {
		return "", err
	}
	if relative {
		step, _ := args["volume_step"].(string)
		if step == "" {
			if v, ok := args["volume_step"].(float64); ok {
				if v > 0 {
					step = "up"
				} else {
					step = "down"
				}
			}
		}
		if step == "" {
			return "", fmt.Errorf("укажи volume_step: up/down или число")
		}
		svc := "volume_down"
		if step == "up" {
			svc = "volume_up"
		}
		if err := a.ha.CallService(ctx, "media_player", svc, map[string]any{"entity_id": ids}); err != nil {
			return "", err
		}
		return fmt.Sprintf("Громкость %s: %s", step, strings.Join(ids, ", ")), nil
	}

	vol, ok := args["volume_level"].(float64)
	if !ok {
		return "", fmt.Errorf("укажи volume_level (0-100)")
	}
	// HA expects 0..1
	if vol > 1 {
		vol = vol / 100
	}
	data := map[string]any{"entity_id": ids, "volume_level": vol}
	if err := a.ha.CallService(ctx, "media_player", "volume_set", data); err != nil {
		return "", err
	}
	return fmt.Sprintf("Громкость установлена: %s → %d%%", strings.Join(ids, ", "), int(vol*100)), nil
}

func (a *Agent) mediaAction(ctx context.Context, args map[string]any, service string) (string, error) {
	ids, err := a.resolveEntity(ctx, strVal(args["name"]), strVal(args["area"]))
	if err != nil {
		return "", err
	}
	if err := a.ha.CallService(ctx, "media_player", service, map[string]any{"entity_id": ids}); err != nil {
		return "", err
	}
	return fmt.Sprintf("media %s: %s", service, strings.Join(ids, ", ")), nil
}

func (a *Agent) mediaMute(ctx context.Context, args map[string]any, mute bool) (string, error) {
	ids, err := a.resolveEntity(ctx, strVal(args["name"]), strVal(args["area"]))
	if err != nil {
		return "", err
	}
	var muted bool
	if v, ok := args["is_volume_muted"].(string); ok {
		muted = v == "true" || v == "1" || v == "on"
	} else {
		muted = mute
	}
	data := map[string]any{"entity_id": ids, "is_volume_muted": muted}
	if err := a.ha.CallService(ctx, "media_player", "volume_mute", data); err != nil {
		return "", err
	}
	return fmt.Sprintf("Звук %s: %s", map[bool]string{true: "выключен", false: "включён"}[muted], strings.Join(ids, ", ")), nil
}

func (a *Agent) mediaSearchPlay(ctx context.Context, args map[string]any) (string, error) {
	ids, err := a.resolveEntity(ctx, strVal(args["name"]), strVal(args["area"]))
	if err != nil {
		return "", err
	}
	q, _ := args["search_query"].(string)
	if q == "" {
		return "", fmt.Errorf("укажи search_query")
	}
	data := map[string]any{
		"entity_id":          ids,
		"media_content_id":   q,
		"media_content_type": "music",
	}
	if err := a.ha.CallService(ctx, "media_player", "play_media", data); err != nil {
		return "", err
	}
	return fmt.Sprintf("Играет: %s на %s", q, strings.Join(ids, ", ")), nil
}

func (a *Agent) fanSetSpeed(ctx context.Context, args map[string]any) (string, error) {
	ids, err := a.resolveEntity(ctx, strVal(args["name"]), strVal(args["area"]))
	if err != nil {
		return "", err
	}
	pct, ok := args["percentage"].(float64)
	if !ok {
		return "", fmt.Errorf("укажи percentage (0-100)")
	}
	data := map[string]any{"entity_id": ids, "percentage": int(pct)}
	if err := a.ha.CallService(ctx, "fan", "set_percentage", data); err != nil {
		return "", err
	}
	return fmt.Sprintf("Скорость вентилятора: %s → %d%%", strings.Join(ids, ", "), int(pct)), nil
}

// liveContext returns a text overview of all devices/sensors (like the old
// GetLiveContext MCP tool), optionally filtered by name/domain/area.
func (a *Agent) liveContext(ctx context.Context, args map[string]any) (string, error) {
	states, err := a.ha.States(ctx)
	if err != nil {
		return "", err
	}

	name, _ := args["name"].(string)
	area, _ := args["area"].(string)
	var domains []string
	if d, ok := asStringSlice(args["domain"]); ok {
		domains = d
	}

	// When filtering by area, resolve it through the HA area registry
	// (entities are assigned to areas in HA, not by name substring).
	areaSet := map[string]bool{}
	if area != "" {
		if err := a.EnsureIndex(ctx); err != nil {
			return "", err
		}
		key := strings.ToLower(strings.TrimSpace(area))
		for k, ents := range a.index.byArea {
			if k == key || strings.Contains(k, key) {
				for _, e := range ents {
					areaSet[e] = true
				}
			}
		}
	}

	filter := func(s hare.State) bool {
		if area != "" && !areaSet[s.EntityID] {
			return false
		}
		if name != "" && !strings.Contains(strings.ToLower(friendlyName(s)), strings.ToLower(name)) &&
			!strings.Contains(strings.ToLower(s.EntityID), strings.ToLower(name)) {
			return false
		}
		if len(domains) > 0 {
			dom := strings.SplitN(s.EntityID, ".", 2)[0]
			ok := false
			for _, d := range domains {
				if d == dom {
					ok = true
					break
				}
			}
			if !ok {
				return false
			}
		}
		return true
	}

	var lines []string
	lines = append(lines, "Live Context: обзор областей и устройств умного дома:")
	for _, s := range states {
		if !filter(s) {
			continue
		}
		fn := friendlyName(s)
		attrs := s.Attributes
		unit, _ := attrs["unit_of_measurement"].(string)
		devClass, _ := attrs["device_class"].(string)
		stateStr := s.State
		if unit != "" {
			stateStr = stateStr + " " + unit
		}
		line := fmt.Sprintf("- names: %s\n  domain: %s\n  state: %s", fn, strings.SplitN(s.EntityID, ".", 2)[0], stateStr)
		if devClass != "" {
			line += fmt.Sprintf("\n  device_class: %s", devClass)
		}
		// area lookup (best-effort)
		line += "\n  entity_id: " + s.EntityID
		lines = append(lines, line)
	}
	if area != "" {
		lines = append(lines, fmt.Sprintf("\nФильтр по зоне: %s (см. entity_id для адресации)", area))
	}
	return strings.Join(lines, "\n"), nil
}

// listSensors returns a list of ALL sensors with current values from /api/states.
func (a *Agent) listSensors(ctx context.Context) (string, error) {
	states, err := a.ha.States(ctx)
	if err != nil {
		return "", err
	}
	var sensors []hare.State
	for _, s := range states {
		if strings.HasPrefix(s.EntityID, "sensor.") {
			sensors = append(sensors, s)
		}
	}
	if len(sensors) == 0 {
		return "В Home Assistant нет сенсоров (sensor.*).", nil
	}
	sort.Slice(sensors, func(i, j int) bool { return sensors[i].EntityID < sensors[j].EntityID })

	var lines []string
	lines = append(lines, fmt.Sprintf("Всего сенсоров: %d", len(sensors)))
	for _, s := range sensors {
		fn := friendlyName(s)
		unit, _ := s.Attributes["unit_of_measurement"].(string)
		val := s.State
		if val == "unknown" || val == "unavailable" {
			val = "(недоступно)"
		} else if unit != "" {
			val = val + " " + unit
		}
		lines = append(lines, fmt.Sprintf("— %s (%s): %s", fn, s.EntityID, val))
	}
	return strings.Join(lines, "\n"), nil
}

// getWeather returns current weather + hourly forecast from /api/states/weather.*
func (a *Agent) getWeather(ctx context.Context, args map[string]any) (string, error) {
	hours := 12
	if h, ok := args["hours"].(float64); ok && h > 0 {
		hours = int(h)
	}
	if hours > 48 {
		hours = 48
	}

	states, err := a.ha.States(ctx)
	if err != nil {
		return "", err
	}
	var weather []hare.State
	for _, s := range states {
		if strings.HasPrefix(s.EntityID, "weather.") {
			weather = append(weather, s)
		}
	}
	if len(weather) == 0 {
		return "В Home Assistant нет сущностей погоды (weather).", nil
	}

	var lines []string
	for _, ws := range weather[:min(len(weather), 2)] {
		name := friendlyName(ws)
		lines = append(lines, fmt.Sprintf("— %s (%s): %s", name, ws.EntityID, ws.State))

		var attrs []string
		for _, k := range []string{"temperature", "feels_like", "condition", "humidity", "pressure", "wind_speed", "wind_bearing", "visibility", "precipitation"} {
			if v, ok := ws.Attributes[k]; ok {
				attrs = append(attrs, fmt.Sprintf("%s=%v", k, v))
			}
		}
		if len(attrs) > 0 {
			lines = append(lines, "  "+strings.Join(attrs, ", "))
		}

		if fh, ok := ws.Attributes["forecastHourly"].([]any); ok && len(fh) > 0 {
			lines = append(lines, "  Почасовой прогноз:")
			n := min(hours, len(fh))
			for _, item := range fh[:n] {
				m, _ := item.(map[string]any)
				if m == nil {
					continue
				}
				dt, _ := m["datetime"].(string)
				temp, _ := m["native_temperature"].(float64)
				cond, _ := m["condition"].(string)
				wind, _ := m["native_wind_speed"].(float64)
				t, err := time.Parse("2006-01-02T15:04:05-07:00", dt)
				if err != nil {
					if t2, err2 := time.Parse(time.RFC3339, dt); err2 == nil {
						t = t2
					}
				}
				when := dt
				if !t.IsZero() {
					when = t.Format("2006-01-02 15:04")
				}
				lines = append(lines, fmt.Sprintf("    %s: %g°C, %s, ветер %g км/ч", when, temp, cond, wind))
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}

// getHistory returns history/statistics of an entity for a period.
func (a *Agent) getHistory(ctx context.Context, args map[string]any) (string, error) {
	entity, _ := args["entity"].(string)
	if entity == "" {
		return "", fmt.Errorf("параметр 'entity' обязателен")
	}
	// Try to resolve a friendly name to an entity_id if the LLM passed a name.
	if !strings.Contains(entity, ".") {
		if err := a.EnsureIndex(ctx); err != nil {
			return "", err
		}
		if eid, ok := a.index.byName[strings.ToLower(entity)]; ok {
			entity = eid
		}
	}

	period := "week"
	if p, ok := args["period"].(string); ok && p != "" {
		period = p
	}
	aggregate := "sum"
	if ag, ok := args["aggregate"].(string); ok && ag != "" {
		aggregate = ag
	}

	now := time.Now()
	var start time.Time
	switch period {
	case "day":
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case "week":
		start = now.AddDate(0, 0, -7)
	case "month":
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	case "year":
		start = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
	default:
		t, err := time.ParseInLocation("2006-01-02", period, now.Location())
		if err != nil {
			return "", fmt.Errorf("неверный период %q: используй day/week/month/year или YYYY-MM-DD", period)
		}
		start = t
	}

	points, err := a.ha.History(ctx, entity, start, now)
	if err != nil {
		return "", err
	}
	if len(points) == 0 {
		return fmt.Sprintf("Нет данных по %s за указанный период.", entity), nil
	}

	friendlyName := friendlyName(points[0])
	unit, _ := points[0].Attributes["unit_of_measurement"].(string)
	stateClass, _ := points[0].Attributes["state_class"].(string)
	isCounter := stateClass == "total_increasing" || stateClass == "total"

	if aggregate == "raw" {
		var sb strings.Builder
		fmt.Fprintf(&sb, "История %s (%s):\n", friendlyName, entity)
		n := min(50, len(points))
		for _, p := range points[:n] {
			t, _ := time.Parse(time.RFC3339, p.LastChanged)
			if t.IsZero() {
				sb.WriteString(fmt.Sprintf("  %s\n", p.State))
			} else {
				sb.WriteString(fmt.Sprintf("  %s: %s%s\n", t.Format("2006-01-02 15:04"), p.State, unitStr(unit)))
			}
		}
		if len(points) > 50 {
			sb.WriteString(fmt.Sprintf("  ... и ещё %d записей\n", len(points)-50))
		}
		return sb.String(), nil
	}

	// numeric values
	var values []float64
	type numP struct {
		State string
	}
	var validPoints []numP
	for _, p := range points {
		s := strings.TrimSpace(p.State)
		if s == "" || s == "unknown" || s == "unavailable" || s == "none" {
			continue
		}
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			values = append(values, v)
			validPoints = append(validPoints, numP{State: s})
		}
	}
	if len(values) == 0 {
		return fmt.Sprintf("Нет числовых данных по %s за указанный период.", friendlyName), nil
	}

	if isCounter && aggregate == "sum" {
		firstVal, lastVal := values[0], values[len(values)-1]
		diff := lastVal - firstVal
		if diff < 0 {
			return fmt.Sprintf("%s за %s (%s): показания: %.2f%s", friendlyName, period, entity, lastVal, unitStr(unit)), nil
		}
		return fmt.Sprintf("%s за %s (%s): потребление: %.2f%s (показания: %.2f → %.2f%s)", friendlyName, period, entity, diff, unitStr(unit), firstVal, lastVal, unitStr(unit)), nil
	}

	var result string
	switch aggregate {
	case "sum":
		result = fmt.Sprintf("Сумма: %.2f%s", sumValues(values), unitStr(unit))
	case "mean":
		result = fmt.Sprintf("Среднее: %.2f%s", sumValues(values)/float64(len(values)), unitStr(unit))
	case "min":
		mn := values[0]
		for _, v := range values[1:] {
			if v < mn {
				mn = v
			}
		}
		result = fmt.Sprintf("Минимум: %.2f%s", mn, unitStr(unit))
	case "max":
		mx := values[0]
		for _, v := range values[1:] {
			if v > mx {
				mx = v
			}
		}
		result = fmt.Sprintf("Максимум: %.2f%s", mx, unitStr(unit))
	case "latest":
		result = fmt.Sprintf("Последнее: %s%s", validPoints[len(validPoints)-1].State, unitStr(unit))
	default:
		result = fmt.Sprintf("Сумма: %.2f%s", sumValues(values), unitStr(unit))
	}
	return fmt.Sprintf("%s за %s (%s): %s", friendlyName, period, entity, result), nil
}

func (a *Agent) broadcast(ctx context.Context, args map[string]any) (string, error) {
	msg, _ := args["message"].(string)
	if msg == "" {
		return "", fmt.Errorf("укажи message для HassBroadcast")
	}

	// find TTS-capable media players (speakers). Prefer yandex_station via tts.
	states, err := a.ha.States(ctx)
	if err != nil {
		return "", err
	}
	var players []string
	for _, s := range states {
		if strings.HasPrefix(s.EntityID, "media_player.") {
			players = append(players, s.EntityID)
		}
	}
	if len(players) == 0 {
		return "", fmt.Errorf("нет media_player для озвучки")
	}

	// Preferred: tts.speak (modern) — target any tts entity; fallback to cloud_say per player.
	// We call tts.speak with the target entity list to broadcast.
	data := map[string]any{
		"media_player_entity_id": players,
		"message":                msg,
	}
	if err := a.ha.CallService(ctx, "tts", "speak", data); err != nil {
		// Try per-player fallback: media_player.play_media with TTS URL is complex;
		// instead call tts.cloud_say for each player.
		var errs []string
		for _, p := range players {
			if err2 := a.ha.CallService(ctx, "tts", "cloud_say", map[string]any{"entity_id": p, "message": msg}); err2 != nil {
				errs = append(errs, err2.Error())
			}
		}
		if len(errs) == len(players) {
			return "", fmt.Errorf("не удалось озвучить: %v", strings.Join(errs, "; "))
		}
	}
	return fmt.Sprintf("Озвучено через колонки (%d): %s", len(players), msg), nil
}

// --- helpers ---

func friendlyName(s hare.State) string {
	if fn, ok := s.Attributes["friendly_name"].(string); ok && fn != "" {
		return fn
	}
	return s.EntityID
}

func unitStr(u string) string {
	if u == "" {
		return ""
	}
	return " " + u
}

func sumValues(vals []float64) float64 {
	s := 0.0
	for _, v := range vals {
		s += v
	}
	return s
}

func asStringSlice(v any) ([]string, bool) {
	switch t := v.(type) {
	case string:
		return []string{t}, t != ""
	case []string:
		return t, len(t) > 0
	case []any:
		var out []string
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out, len(out) > 0
	default:
		return nil, false
	}
}

func strVal(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func (a *Agent) handleSchedule(ctx context.Context, args map[string]any) (string, error) {
	if a.sched == nil {
		return "", fmt.Errorf("scheduler не инициализирован")
	}

	a.log.Info("agent: schedule_action raw args", "args", args)

	toolName, _ := args["tool"].(string)
	if toolName == "" {
		if actionRaw, ok := args["action"].(map[string]any); ok {
			toolName, _ = actionRaw["tool"].(string)
			if toolName == "" {
				toolName, _ = actionRaw["name"].(string)
			}
			for k, v := range actionRaw {
				if _, exists := args[k]; !exists {
					args[k] = v
				}
			}
		}
	}

	if toolName == "" {
		return "", fmt.Errorf("укажи поле 'tool' с именем инструмента HA (например HassTurnOn)")
	}

	toolArgs := make(map[string]any)
	for k, v := range args {
		switch k {
		case "tool", "action", "at", "delay", "cron", "label":
			continue
		}
		toolArgs[k] = v
	}
	if len(toolArgs) == 0 {
		toolArgs = extractToolArgs(args)
	}

	act := scheduler.Action{
		Tool: toolName,
		Args: toolArgs,
	}
	label := strVal(args["label"])

	at, hasAt := args["at"].(string)
	delay, hasDelay := args["delay"].(string)
	cron, hasCron := args["cron"].(string)

	switch {
	case hasCron && cron != "":
		norm, err := normalizeCron(cron)
		if err != nil {
			return "", err
		}
		job, err := a.sched.ScheduleCron(norm, act, label)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Создано расписание: %s (ID: %s)", job.Label, job.ID), nil

	case hasDelay && delay != "":
		d, err := time.ParseDuration(delay)
		if err != nil {
			return "", fmt.Errorf("неверный формат задержки %q: %w", delay, err)
		}
		job, err := a.sched.ScheduleIn(d, act, label)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Таймер на %s: %s (ID: %s)", delay, job.Label, job.ID), nil

	case hasAt && at != "":
		t, err := parseAtTime(at)
		if err != nil {
			return "", err
		}
		job, err := a.sched.ScheduleAt(t, act, label)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Таймер на %s: %s (ID: %s)", t.Format("15:04"), job.Label, job.ID), nil

	default:
		return "", fmt.Errorf("укажи at, delay или cron для schedule_action")
	}
}

func (a *Agent) handleScheduleAI(ctx context.Context, args map[string]any) (string, error) {
	if a.sched == nil {
		return "", fmt.Errorf("scheduler не инициализирован")
	}

	prompt, _ := args["prompt"].(string)
	if prompt == "" {
		return "", fmt.Errorf("укажи поле 'prompt' с инструкцией для AI")
	}

	chatID := ChatIDFrom(ctx)

	act := scheduler.Action{
		AgentPrompt: prompt,
		ChatID:      chatID,
	}
	label := strVal(args["label"])

	at, hasAt := args["at"].(string)
	delay, hasDelay := args["delay"].(string)
	cron, hasCron := args["cron"].(string)

	switch {
	case hasCron && cron != "":
		norm, err := normalizeCron(cron)
		if err != nil {
			return "", err
		}
		job, err := a.sched.ScheduleCron(norm, act, label)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Создана AI-задача: %s (ID: %s)", job.Label, job.ID), nil

	case hasDelay && delay != "":
		d, err := time.ParseDuration(delay)
		if err != nil {
			return "", fmt.Errorf("неверный формат задержки %q: %w", delay, err)
		}
		job, err := a.sched.ScheduleIn(d, act, label)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("AI-задача через %s: %s (ID: %s)", delay, job.Label, job.ID), nil

	case hasAt && at != "":
		t, err := parseAtTime(at)
		if err != nil {
			return "", err
		}
		job, err := a.sched.ScheduleAt(t, act, label)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("AI-задача на %s: %s (ID: %s)", t.Format("15:04"), job.Label, job.ID), nil

	default:
		return "", fmt.Errorf("укажи at, delay или cron для schedule_ai_action")
	}
}

func extractToolArgs(action map[string]any) map[string]any {
	if v, ok := action["arguments"].(map[string]any); ok && len(v) > 0 {
		return v
	}
	if v, ok := action["arguments"].(string); ok && v != "" {
		var parsed map[string]any
		if json.Unmarshal([]byte(v), &parsed) == nil {
			return parsed
		}
	}
	if v, ok := action["args"].(map[string]any); ok && len(v) > 0 {
		return v
	}
	args := make(map[string]any)
	for k, v := range action {
		switch k {
		case "tool", "arguments", "args", "at", "delay", "cron", "label":
			continue
		}
		args[k] = v
	}
	return args
}

func normalizeCron(raw string) (string, error) {
	parts := strings.Fields(raw)
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.Contains(p, ":") {
			continue
		}
		cleaned = append(cleaned, p)
	}

	if len(cleaned) < 5 {
		return "", fmt.Errorf("cron-выражение должно содержать минимум 5 полей, получено %q: %v", raw, parts)
	}

	if len(cleaned) == 5 {
		return "0 " + strings.Join(cleaned, " "), nil
	}

	return strings.Join(cleaned, " "), nil
}

func parseAtTime(s string) (time.Time, error) {
	t, err := time.ParseInLocation("15:04", strings.TrimSpace(s), time.Local)
	if err != nil {
		t2, err2 := time.ParseInLocation("15:04:05", strings.TrimSpace(s), time.Local)
		if err2 != nil {
			return time.Time{}, fmt.Errorf("неверный формат времени %q, ожидается HH:MM (например 10:05)", s)
		}
		t = t2
	}

	now := time.Now()
	when := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.Local)
	if !when.After(now) {
		when = when.AddDate(0, 0, 1)
	}
	return when, nil
}

var _ = url.QueryEscape // keep import if unused