package llm

import (
	"github.com/mark3labs/mcp-go/mcp"
)

// ToFunction converts an MCP Tool to a GigaChat Function definition.
func ToFunction(t mcp.Tool) Function {
	return Function{
		Name:        t.Name,
		Description: t.Description,
		Parameters:  convertParameters(t.InputSchema),
	}
}

func convertParameters(schema mcp.ToolInputSchema) map[string]any {
	if len(schema.Properties) == 0 {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	props := make(map[string]any, len(schema.Properties))
	for key, raw := range schema.Properties {
		props[key] = raw // keep as-is (already matches JSON Schema)
	}

	result := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(schema.Required) > 0 {
		result["required"] = schema.Required
	}

	return result
}

// SchedulerFunction returns the function definition for the built-in scheduler
// tool that lets the LLM schedule delayed or recurring actions.
func SchedulerFunction() Function {
	return Function{
		Name:        "schedule_action",
		Description: "Запланировать выполнение действия в будущем (однократный таймер с задержкой или по cron-расписанию)",
		Parameters: FunctionParameters(
			map[string]any{
				"delay": map[string]any{
					"type":        "string",
					"description": "Задержка перед выполнением, например '15m', '2h', '1h30m'. Не используется для cron.",
				},
				"cron": map[string]any{
					"type":        "string",
					"description": "Cron-выражение из 6 полей: секунды минуты часы день месяца месяц день недели. Например '0 0 7 * * 1-5' — каждый будний день в 7:00. Не используется для delay.",
				},
				"action": map[string]any{
					"type":        "object",
					"description": "Действие, которое нужно выполнить",
					"properties": map[string]any{
						"domain":    map[string]any{"type": "string", "description": "Домен HA (light, switch, cover, climate, script, scene)"},
						"service":   map[string]any{"type": "string", "description": "Сервис (turn_on, turn_off, trigger и т.д.)"},
						"entity_id": map[string]any{"type": "string", "description": "ID сущности"},
						"data":      map[string]any{"type": "object", "description": "Дополнительные данные"},
					},
					"required": []string{"domain", "service"},
				},
				"label": map[string]any{
					"type":        "string",
					"description": "Название таймера для отображения в списке",
				},
			},
			[]string{"action"},
		),
	}
}

// AllFunctions returns HA tools + the scheduler tool for the LLM agent.
func AllFunctions(mcpTools []mcp.Tool) []Function {
	fns := make([]Function, 0, len(mcpTools)+1)
	for _, t := range mcpTools {
		fns = append(fns, ToFunction(t))
	}
	fns = append(fns, SchedulerFunction())
	return fns
}

// SystemPrompt returns the default system prompt for the home assistant agent.
func SystemPrompt() Message {
	return Message{
		Role: "system",
		Content: `Ты — помощник для управления умным домом через Home Assistant.

У тебя есть доступ к инструментам Home Assistant. Используй их чтобы:
- Включать и выключать устройства (свет, розетки, чайники и т.д.)
- Получать состояние устройств и датчиков
- Вызывать сценарии (скрипты)
- Запланировать действия на будущее (через schedule_action)

Правила:
1. Отвечай кратко и понятно на русском языке, одним-двумя предложениями.
2. Если пользователь не указал конкретное устройство — спроси уточнение.
3. Не придумывай результаты — полагайся только на то, что вернули инструменты.
4. Если инструмент вернул ошибку — честно скажи об этом.
5. Для включения/выключения используй call_service с domain: light, switch, cover, climate, script, lock, scene и соответствующим service (turn_on, turn_off, trigger и т.д.).
6. Для запроса состояния используй get_state с entity_id.
7. Для списка устройств используй list_entities.
8. Если пользователь спрашивает «какая температура», «что с окнами» и т.п. — используй get_state нужного sensor/binary_sensor.
9. Передавай entity_id внутри поля "data" как объект, например для call_service: {"domain":"light","service":"turn_on","data":{"entity_id":"light.living_room"}}
10. Для отложенных действий используй schedule_action. Укажи delay (например "15m", "2h") или cron (например "0 0 7 * * 1-5" для будильника по будням). Действие должно содержать domain, service и entity_id.`,
	}
}