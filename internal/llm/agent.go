package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	hamcp "hass-agent-bot/internal/ha/mcp"
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

type Agent struct {
	llm   LLMClient
	mcp   *hamcp.Client
	sched Scheduler
	log   *slog.Logger

	mu        sync.Mutex
	histories map[int64][]Message // chatID → messages (in-memory cache)
	store     HistoryStore        // persistence (nil = don't save)
}

func NewAgent(llm LLMClient, mcpCli *hamcp.Client, sched Scheduler, store HistoryStore) *Agent {
	a := &Agent{
		llm:       llm,
		mcp:       mcpCli,
		sched:     sched,
		log:       slog.Default(),
		histories: make(map[int64][]Message),
		store:     store,
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

func (a *Agent) HandleMessage(ctx context.Context, userText string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	chatID := ChatIDFrom(ctx)
	taskMode := taskModeFrom(ctx)

	mcpTools, err := a.mcp.ListTools(ctx)
	if err != nil {
		return "", fmt.Errorf("agent: list tools: %w", err)
	}

	// В фоновом режиме schedule-тулы не отдаём LLM
	functions := AllFunctions(mcpTools)
	if taskMode {
		functions = HAOnlyFunctions(mcpTools)
	}

	history := a.histories[chatID]
	messages := make([]Message, 0, 2+len(history)+1)

	if taskMode {
		messages = append(messages, Message{Role: "system", Content: SystemPrompt().Content + "\n\nВАЖНО: Это фоновое выполнение задачи. Время уже наступило. НЕ вызывай schedule_action и schedule_ai_action — просто выполни то, что просят, прямо сейчас. Не нужно ничего планировать."})
	} else {
		messages = append(messages, SystemPrompt())
	}
	messages = append(messages, history...)
	messages = append(messages, Message{Role: "user", Content: userText})

	for i := 0; i < maxIterations; i++ {
		resp, err := a.llm.Chat(ctx, messages, functions)
		if err != nil {
			return "", fmt.Errorf("agent: chat iteration %d: %w", i, err)
		}

		choice := resp.Choices[0]
		msg := choice.Message

		if msg.FunctionCall != nil {
			fc := msg.FunctionCall
			messages = append(messages, Message{
				Role:    "assistant",
				Content: fmt.Sprintf("Calling function %s", fc.Name),
			})

			result, err := a.executeTool(ctx, fc)
			if err != nil {
				result = fmt.Sprintf("Ошибка: %v", err)
			}
			messages = append(messages, Message{
				Role:    "user",
				Content: fmt.Sprintf("Результат %s: %s", fc.Name, result),
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
	ctxTool, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	a.log.Info("agent: executing tool", "name", fc.Name, "args", fc.Arguments)

	// В фоновом режиме (AI-задача) планирование запрещено — выполнить сразу
	if taskModeFrom(ctx) && (fc.Name == "schedule_action" || fc.Name == "schedule_ai_action") {
		return "", fmt.Errorf("планирование запрещено при фоновом выполнении: %s (время уже наступило)", fc.Name)
	}

	switch fc.Name {
	case "schedule_action":
		// сбросим полные аргументы в лог для диагностики
		a.log.Info("agent: schedule_action full args", "raw_fc_arguments", fc.Arguments)
		return a.handleSchedule(ctx, fc.Arguments)

	case "schedule_ai_action":
		a.log.Info("agent: schedule_ai_action full args", "raw_fc_arguments", fc.Arguments)
		return a.handleScheduleAI(ctx, fc.Arguments)

	default:
		// All HA tools (HassTurnOn, HassLightSet, GetLiveContext, ...) proxy directly to MCP
		return a.mcp.CallTool(ctxTool, fc.Name, normalizeArgs(fc.Arguments))
	}
}

// normalizeArgs приводит аргументы к формату, который принимает HA MCP:
// domain и device_class — массивы, а GigaChat часто шлёт строкой.
func normalizeArgs(args map[string]any) map[string]any {
	normalized := args
	for _, key := range []string{"domain", "device_class"} {
		if v, ok := normalized[key].(string); ok && v != "" {
			normalized[key] = []string{v}
		}
	}
	return normalized
}

func (a *Agent) handleSchedule(ctx context.Context, args map[string]any) (string, error) {
	if a.sched == nil {
		return "", fmt.Errorf("scheduler не инициализирован")
	}

	a.log.Info("agent: schedule_action raw args", "args", args)

	// Плоский формат: tool, name, area, domain, delay/cron, label — всё в args.
	toolName, _ := args["tool"].(string)
	if toolName == "" {
		// fallback: старый формат с вложенным "action"
		if actionRaw, ok := args["action"].(map[string]any); ok {
			toolName, _ = actionRaw["tool"].(string)
			if toolName == "" {
				toolName, _ = actionRaw["name"].(string)
			}
			// merge аргументов из вложенного объекта на верхний уровень
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

	// Аргументы для инструмента: все поля, кроме служебных (tool, at, delay, cron, label)
	toolArgs := make(map[string]any)
	for k, v := range args {
		switch k {
		case "tool", "action", "at", "delay", "cron", "label":
			continue
		}
		toolArgs[k] = v
	}
	// если GigaChat всё же прислал вложенный "arguments" — забираем его
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

// extractToolArgs находит аргументы HA-инструмента в действии при разных форматах,
// которые может прислать LLM: {"arguments": {...}}, {"arguments": "<json-строка>"},
// {"args": {...}} или всё кроме "name" лежит прямо в action.
func extractToolArgs(action map[string]any) map[string]any {
	// 1. {"arguments": {...}}
	if v, ok := action["arguments"].(map[string]any); ok && len(v) > 0 {
		return v
	}
	// 2. {"arguments": "<json-строка>"}
	if v, ok := action["arguments"].(string); ok && v != "" {
		var parsed map[string]any
		if json.Unmarshal([]byte(v), &parsed) == nil {
			return parsed
		}
	}
	// 3. {"args": {...}}
	if v, ok := action["args"].(map[string]any); ok && len(v) > 0 {
		return v
	}
	// 4. Всё кроме "tool" и служебных полей лежит прямо в action (name — уже аргумент устройства)
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

func strVal(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func normalizeCron(raw string) (string, error) {
	// убираем возможные мусорные префиксы: время вида "10:05", "10:05 AM" и т.п.
	parts := strings.Fields(raw)
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		// пропускаем части с двоеточием (это время, а не cron-поле)
		if strings.Contains(p, ":") {
			continue
		}
		cleaned = append(cleaned, p)
	}

	if len(cleaned) < 5 {
		return "", fmt.Errorf("cron-выражение должно содержать минимум 5 полей, получено %q: %v", raw, parts)
	}

	if len(cleaned) == 5 {
		// robfig/cron с WithSeconds() требует 6 полей, добавляем секунды = 0
		return "0 " + strings.Join(cleaned, " "), nil
	}

	return strings.Join(cleaned, " "), nil
}

// parseAtTime разбирает время "HH:MM" и возвращает сегодняшнюю (или завтрашнюю)
// дату с этим временем. Если время уже прошло — берёт завтрашний день.
func parseAtTime(s string) (time.Time, error) {
	t, err := time.ParseInLocation("15:04", strings.TrimSpace(s), time.Local)
	if err != nil {
		// пробуем и с секундами/прочим мусором от GigaChat
		t2, err2 := time.ParseInLocation("15:04:05", strings.TrimSpace(s), time.Local)
		if err2 != nil {
			return time.Time{}, fmt.Errorf("неверный формат времени %q, ожидается HH:MM (например 10:05)", s)
		}
		t = t2
	}

	now := time.Now()
	when := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.Local)
	if !when.After(now) {
		when = when.AddDate(0, 0, 1) // на завтра, если время уже прошло
	}
	return when, nil
}