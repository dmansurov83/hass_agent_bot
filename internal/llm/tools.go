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
		props[key] = sanitizeSchema(raw)
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

// sanitizeSchema converts a JSON Schema node from HA MCP into a form GigaChat
// function-calling accepts. GigaChat (like OpenAI) rejects anyOf/oneOf/union
// types inside tool parameters, which HA emits for e.g. volume_step. We collapse
// such unions to a permissive "string" (safe: LLM just passes a raw value).
func sanitizeSchema(node any) any {
	m, ok := node.(map[string]any)
	if !ok {
		return node
	}

	// Descend into nested property objects
	if props, ok := m["properties"].(map[string]any); ok {
		for k := range props {
			props[k] = sanitizeSchema(props[k])
		}
	}

	// Items: arrays like {"items": {...}, "type": "array"} — recurse into items
	if items, ok := m["items"].(map[string]any); ok {
		m["items"] = sanitizeSchema(items)
	}

	// GigaChat requires an object type to declare its "properties" (even empty).
	if t, _ := m["type"].(string); t == "object" {
		if _, ok := m["properties"]; !ok {
			m["properties"] = map[string]any{}
		}
	}

	// Collapse unions to a plain "string" type
	if _, hasAnyOf := m["anyOf"]; hasAnyOf {
		return map[string]any{"type": "string", "description": orDescription(m)}
	}
	if _, hasOneOf := m["oneOf"]; hasOneOf {
		return map[string]any{"type": "string", "description": orDescription(m)}
	}

	return m
}

func orDescription(m map[string]any) string {
	if d, ok := m["description"].(string); ok && d != "" {
		return d
	}
	return ""
}

// SchedulerFunction returns the function definition for the built-in scheduler
// tool that lets the LLM schedule delayed or recurring actions.
// Поля — плоские (без вложенных объектов): GigaChat надёжнее заполняет
// верхнеуровневые поля, чем объекты внутри объектов.
func SchedulerFunction() Function {
	return Function{
		Name:        "schedule_action",
		Description: "Запланировать выполнение действия в будущем. Используй at для одноразового времени (например '10:05'), delay для задержки ('15m'), cron для повторения ('0 0 7 * * 1-5'). Укажи ровно одно из трёх: at ИЛИ delay ИЛИ cron.",
		Parameters: FunctionParameters(
			map[string]any{
				"at": map[string]any{
					"type":        "string",
					"description": "Время одноразового выполнения в формате 'HH:MM' (например '10:05' — выполнить сегодня в 10:05). Не для cron!",
				},
				"delay": map[string]any{
					"type":        "string",
					"description": "Задержка перед выполнением, например '15m', '2h', '1h30m'. Не для cron!",
				},
				"cron": map[string]any{
					"type":        "string",
					"description": "Cron-выражение из 5-6 полей для ПОВТОРЯЮЩИХСЯ действий. Например '0 7 * * 1-5' — каждый будний день в 7:00. Не для одноразового времени!",
				},
				"tool": map[string]any{
					"type":        "string",
					"description": "Имя инструмента HA: HassTurnOn, HassTurnOff, HassLightSet, HassClimateSetTemperature, HassSetVolume, HassBroadcast.",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Название устройства из GetLiveContext (например 'switch_hall_main', 'Light'). Не выдумывай, только из HA!",
				},
				"area": map[string]any{
					"type":        "string",
					"description": "Зона/комната (например 'Гостиная', 'Комната 1', 'Туалет').",
				},
				"domain": map[string]any{
					"type":        "string",
					"description": "Домен устройства (light, switch, fan, climate).",
				},
				"label": map[string]any{
					"type":        "string",
					"description": "Понятное название таймера для списка /timers.",
				},
			},
			[]string{"tool"},
		),
	}
}

// SchedulerAIFunction returns the function definition for scheduling a background
// AI task: at fire time the full agent (ReAct loop) runs and the result is sent
// to the chat that created the task.
func SchedulerAIFunction() Function {
	return Function{
		Name:        "schedule_ai_action",
		Description: "Запланировать любую задачу, где AI должен САМ подумать и написать ответ в чат: рассказать что-то, проверить датчики и сделать вывод, напомнить, прислать сводку. Не используй HassBroadcast для ответа в чат — это только для озвучки через колонки!",
		Parameters: FunctionParameters(
			map[string]any{
				"at": map[string]any{
					"type":        "string",
					"description": "Время одноразового выполнения в формате 'HH:MM' (например '10:05'). Не для cron!",
				},
				"delay": map[string]any{
					"type":        "string",
					"description": "Задержка перед выполнением, например '15m', '2h', '1h30m'. Не для cron!",
				},
				"cron": map[string]any{
					"type":        "string",
					"description": "Cron-выражение из 5-6 полей для ПОВТОРЯЮЩИХСЯ задач. Например '0 0 7 * * *' — каждый день в 7:00. Не для одноразового времени!",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "Что нужно сделать при срабатывании — просто повтори просьбу пользователя словами, без переформулировок. Примеры: 'расскажи анекдот', 'проверь заряд батарей, напиши если ниже 20%', 'напомни выключить чайник', 'какая погода на улице'.",
				},
				"label": map[string]any{
					"type":        "string",
					"description": "Понятное название задачи для списка /timers.",
				},
			},
			[]string{"prompt"},
		),
	}
}

// HAOnlyFunctions returns only HA tools (no scheduler tools). Used during background
// AI task execution where scheduling is not allowed.
func HAOnlyFunctions(mcpTools []mcp.Tool) []Function {
	fns := make([]Function, 0, len(mcpTools))
	for _, t := range mcpTools {
		fns = append(fns, ToFunction(t))
	}
	return fns
}

// AllFunctions returns HA tools + the scheduler tools for the LLM agent.
func AllFunctions(mcpTools []mcp.Tool) []Function {
	fns := make([]Function, 0, len(mcpTools)+2)
	for _, t := range mcpTools {
		fns = append(fns, ToFunction(t))
	}
	fns = append(fns, SchedulerFunction())
	fns = append(fns, SchedulerAIFunction())
	return fns
}

// SystemPrompt returns the default system prompt for the home assistant agent.
func SystemPrompt() Message {
	return Message{
		Role: "system",
		Content: `Ты — помощник для управления умным домом через Home Assistant.

У тебя есть доступ к инструментам Home Assistant (все имена с заглавной буквы, вызывай их как tool_call):
- HassTurnOn — включить/открыть/активировать устройство (аргументы: name, area, domain)
- HassTurnOff — выключить/закрыть устройство
- HassLightSet — установить яркость (%) или цвет света
- HassClimateSetTemperature — установить температуру климата
- HassSetVolume / HassSetVolumeRelative — громкость медиа
- HassMediaPause / HassMediaUnpause / HassMediaNext / HassMediaPrevious — управление медиа
- HassBroadcast — озвучить сообщение через умный дом (колонки/динамики). Только для голосового объявления в доме, НЕ для ответа в чат!
- HassCancelAllTimers — отменить все таймеры
- GetLiveContext — получить ТЕКУЩЕЕ состояние устройств, датчиков, областей (аргументы: name, domain, area)
- GetDateTime — текущие дата и время
- schedule_action — запланировать действие в будущем (at — одноразово в время HH:MM, delay — через N минут, cron — по расписанию)
- schedule_ai_action — запланировать ФОНОВУЮ AI-задачу: в заданное время ты сам проверишь состояние дома и пришлёшь результат в чат (поле prompt — что проверить и при каком условии писать)

Правила:
1. Отвечай кратко и понятно на русском, одним-двумя предложениями.
2. Имена устройств в Home Assistant — ТЕХНИЧЕСКИЕ, case-sensitive. Например "switch_hall_main", "Light", "Table-Led table-led-light". НИКОГДА не выдумывай имена!
3. ВСЕГДА сначала вызывай GetLiveContext, чтобы узнать точные имена (поля "names") и зоны ("areas"). Никогда не угадывай name.
4. Поле "domain" принимает строку, бот сам превратит в массив.
5. Включать/выключать можно ТРЕМЯ способами:
   a) По area (зона) — HassTurnOn с полем area, без name. Включит ВСЕ устройства в зоне.
   b) По точному name из GetLiveContext — HassTurnOn с name и по желанию area.
   c) По domain — например HassTurnOn с domain "light".
6. Примеры правильного вызова HassTurnOn:
   - {"name": "Table-Led table-led-light"} — конкретное устройство
   - {"area": "Гостиная"} — все устройства в зоне (работает!)
   - {"area": "Комната 1"} — все устройства в комнате 1
7. Не придумывай результаты — полагайся на ответ инструментов. Если инструмент вернул ошибку — честно скажи об этом.
8. Для отложенных действий используй schedule_action с плоскими полями:
   - "в 10:05" (одноразово) → поле at: "10:05", БЕЗ cron!
   - "через 15 минут" → поле delay: "15m"
   - "каждый день в 7:00" / "по будням в 7:00" (повторение) → поле cron: "0 0 7 * * *" / "0 0 7 * * 1-5" (с секундами)
   Пример одноразового: {"tool":"HassTurnOff","area":"Комната 1","at":"10:05","label":"Выключить свет в комнате 1"} + tool-параметры (name/area/domain).
   Пример cron: {"tool":"HassTurnOff","name":"Light","cron":"0 0 7 * * 1-5","label":"Выключить свет по будням в 7:00"}.
9. Для фоновой задачи, где нужно САМОМУ подумать и написать в чат (рассказать, проверить, напомнить, прислать сводку) — используй schedule_ai_action с полем prompt, повторяющим просьбу пользователя:
   Пример: {"prompt":"проверь заряд батарей всех датчиков, напиши если какой-то ниже 20%","cron":"0 0 7 * * *","label":"Проверка батарей"}
   Пример: {"prompt":"расскажи мне анекдот","delay":"1m","label":"Анекдот"}
   Пример: {"prompt":"напомни выключить чайник","at":"18:30","label":"Напоминание"}
   НИКОГДА не используй HassBroadcast для ответа в чат — он только озвучивает через колонки. Всё, что пользователь просит «расскажи/напиши/пришли/сообщи/напомни» в будущем — schedule_ai_action.
10. Если пользователь не указал, какое именно устройство — уточни. Никогда не придумывай name самостоятельно.`,
	}
}