package llm

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	hamcp "hass-agent-bot/internal/ha/mcp"
)

const maxIterations = 5

type Agent struct {
	llm  *GigaChatClient
	mcp  *hamcp.Client
	log  *slog.Logger

	mu      sync.Mutex
	history []Message // conversation history (per-chat, simplified)
}

func NewAgent(llm *GigaChatClient, mcpCli *hamcp.Client) *Agent {
	return &Agent{
		llm: llm,
		mcp: mcpCli,
		log: slog.Default(),
	}
}

// HandleMessage processes a user text message and returns the text to send back.
func (a *Agent) HandleMessage(ctx context.Context, userText string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Load MCP tools and convert to GigaChat functions
	mcpTools, err := a.mcp.ListTools(ctx)
	if err != nil {
		return "", fmt.Errorf("agent: list tools: %w", err)
	}
	functions := make([]Function, 0, len(mcpTools))
	for _, t := range mcpTools {
		functions = append(functions, ToFunction(t))
	}

	// Build message list: system prompt + history + new user message
	messages := make([]Message, 0, 2+len(a.history)+1)
	messages = append(messages, SystemPrompt())
	messages = append(messages, a.history...)
	messages = append(messages, Message{Role: "user", Content: userText})

	// ReAct loop
	for i := 0; i < maxIterations; i++ {
		resp, err := a.llm.Chat(ctx, messages, functions)
		if err != nil {
			return "", fmt.Errorf("agent: chat iteration %d: %w", i, err)
		}

		choice := resp.Choices[0]
		msg := choice.Message

		if msg.FunctionCall != nil {
			fc := msg.FunctionCall

			// Update history: assistant message with function_call
			fcMsg := Message{Role: "assistant", Content: fmt.Sprintf("Calling function %s", fc.Name)}
			messages = append(messages, fcMsg)

			// Execute the tool
			result, err := a.executeTool(ctx, fc)
			if err != nil {
				result = fmt.Sprintf("Ошибка: %v", err)
			}

			// Add tool result as a "user" message (GigaChat format uses user/assistant roles)
			messages = append(messages, Message{
				Role:    "user",
				Content: fmt.Sprintf("Результат %s: %s", fc.Name, result),
			})
		} else {
			// Text response — done
			final := msg.Content

			// Save to history (truncate to last 10 messages to avoid context overflow)
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

// Reset clears conversation history.
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
		// HAMCP also accepts entity_id at top level
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

	default:
		// Generic MCP tool call fallback
		return a.mcp.CallTool(ctxTool, fc.Name, fc.Arguments)
	}
}