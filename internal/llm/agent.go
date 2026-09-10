package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	hamcp "hass-agent-bot/internal/ha/mcp"
	"hass-agent-bot/internal/scheduler"
)

const maxIterations = 5

// Scheduler interface used by the agent to schedule actions.
type Scheduler interface {
	ScheduleIn(d time.Duration, action scheduler.Action, label string) (*scheduler.Job, error)
	ScheduleCron(expr string, action scheduler.Action, label string) (*scheduler.Job, error)
	Cancel(id string) bool
	List() []scheduler.Job
}

type Agent struct {
	llm  *GigaChatClient
	mcp  *hamcp.Client
	sched Scheduler
	log  *slog.Logger

	mu      sync.Mutex
	history []Message
}

func NewAgent(llm *GigaChatClient, mcpCli *hamcp.Client, sched Scheduler) *Agent {
	return &Agent{
		llm:   llm,
		mcp:   mcpCli,
		sched: sched,
		log:   slog.Default(),
	}
}

func (a *Agent) HandleMessage(ctx context.Context, userText string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	mcpTools, err := a.mcp.ListTools(ctx)
	if err != nil {
		return "", fmt.Errorf("agent: list tools: %w", err)
	}
	functions := AllFunctions(mcpTools)

	messages := make([]Message, 0, 2+len(a.history)+1)
	messages = append(messages, SystemPrompt())
	messages = append(messages, a.history...)
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
			a.history = append(a.history,
				Message{Role: "user", Content: userText},
				Message{Role: "assistant", Content: final},
			)
			if len(a.history) > 10 {
				a.history = a.history[len(a.history)-10:]
			}
			return final, nil
		}
	}

	return "", fmt.Errorf("agent: exceeded max iterations (%d)", maxIterations)
}

func (a *Agent) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.history = nil
}

func (a *Agent) executeTool(ctx context.Context, fc *FunctionCall) (string, error) {
	ctxTool, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	a.log.Info("agent: executing tool", "name", fc.Name, "args", fc.Arguments)

	switch fc.Name {
	case "schedule_action":
		return a.handleSchedule(ctx, fc.Arguments)

	default:
		// All HA tools (HassTurnOn, HassLightSet, GetLiveContext, ...) proxy directly to MCP
		return a.mcp.CallTool(ctxTool, fc.Name, fc.Arguments)
	}
}

func (a *Agent) handleSchedule(ctx context.Context, args map[string]any) (string, error) {
	if a.sched == nil {
		return "", fmt.Errorf("scheduler не инициализирован")
	}

	a.log.Info("agent: schedule_action raw args", "args", args)

	actionRaw, ok := args["action"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("action is required")
	}

	// Extract HA tool name and args from the action
	toolName, _ := actionRaw["name"].(string)
	if toolName == "" {
		return "", fmt.Errorf("у action должно быть поле 'name' с именем инструмента HA (например HassTurnOn)")
	}

	toolArgs := extractToolArgs(actionRaw)

	act := scheduler.Action{
		Tool: toolName,
		Args: toolArgs,
	}
	label := strVal(args["label"])

	delay, hasDelay := args["delay"].(string)
	cron, hasCron := args["cron"].(string)

	switch {
	case hasCron && cron != "":
		job, err := a.sched.ScheduleCron(cron, act, label)
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

	default:
		return "", fmt.Errorf("укажи delay или cron для schedule_action")
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
	// 4. Всё кроме "name" и служебных полей лежит прямо в action
	args := make(map[string]any)
	for k, v := range action {
		switch k {
		case "name", "arguments", "args":
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