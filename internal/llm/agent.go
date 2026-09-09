package llm

import (
	"context"
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
	case "call_service":
		domain, _ := fc.Arguments["domain"].(string)
		service, _ := fc.Arguments["service"].(string)
		data := make(map[string]any)
		if d, ok := fc.Arguments["data"].(map[string]any); ok {
			data = d
		}
		if eid, ok := fc.Arguments["entity_id"].(string); ok && data["entity_id"] == nil {
			data["entity_id"] = eid
		}
		if err := a.mcp.CallService(ctxTool, domain, service, data); err != nil {
			return "", err
		}
		return "успешно выполнено", nil

	case "get_state":
		eid, _ := fc.Arguments["entity_id"].(string)
		st, err := a.mcp.GetState(ctxTool, eid)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s: %s (friendly_name: %v)", st.EntityID, st.State, st.Attributes["friendly_name"]), nil

	case "list_entities":
		entities, err := a.mcp.ListEntities(ctxTool)
		if err != nil {
			return "", err
		}
		result := ""
		for _, e := range entities {
			result += fmt.Sprintf("%s (%s): %s\n", e.EntityID, e.FriendlyName, e.State)
		}
		return result, nil

	case "schedule_action":
		return a.handleSchedule(ctx, fc.Arguments)

	default:
		return a.mcp.CallTool(ctxTool, fc.Name, fc.Arguments)
	}
}

func (a *Agent) handleSchedule(ctx context.Context, args map[string]any) (string, error) {
	if a.sched == nil {
		return "", fmt.Errorf("scheduler не инициализирован")
	}

	actionRaw, ok := args["action"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("action is required")
	}

	act := scheduler.Action{
		Domain:  strVal(actionRaw["domain"]),
		Service: strVal(actionRaw["service"]),
	}
	if eid, ok := actionRaw["entity_id"].(string); ok {
		act.Data = map[string]any{"entity_id": eid}
	}
	if d, ok := actionRaw["data"].(map[string]any); ok {
		if act.Data == nil {
			act.Data = d
		} else {
			for k, v := range d {
				act.Data[k] = v
			}
		}
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

func strVal(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}