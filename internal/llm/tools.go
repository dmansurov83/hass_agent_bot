package llm

import (
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// ToFunction converts an MCP Tool to a GigaChat Function definition.
func ToFunction(t mcp.Tool) Function {
	fn := Function{
		Name:        t.Name,
		Description: t.Description,
		Parameters:  convertParameters(t.InputSchema),
	}
	return fn
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

// ToolResult wraps the result of a tool call back to the LLM.
type ToolResult struct {
	Name string
	Data string
}

// ConvertToAssistantMessage wraps a message for the assistant role.
func ToolResultToMessage(role string, name string, content string) Message {
	return Message{
		Role:    role,
		Content: fmt.Sprintf("Результат вызова %s:\n%s", name, content),
	}
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

Правила:
1. Отвечай кратко и понятно на русском языке, одним-двумя предложениями.
2. Если пользователь не указал конкретное устройство — спроси уточнение.
3. Не придумывай результаты — полагайся только на то, что вернули инструменты.
4. Если инструмент вернул ошибку — честно скажи об этом.
5. Для включения/выключения используй call_service с domain: light, switch, cover, climate, script, lock, scene и соответствующим service (turn_on, turn_off, trigger и т.д.).
6. Для запроса состояния используй get_state с entity_id.
7. Для списка устройств используй list_entities.
8. Если пользователь спрашивает «какая температура», «что с окнами» и т.п. — используй get_state нужного sensor/binary_sensor.
9. Передавай entity_id внутри поля "data" как объект, например для call_service: {"domain":"light","service":"turn_on","data":{"entity_id":"light.living_room"}}`,
	}
}