# 05 — LLM-слой (intents через GigaChat)

## Цель

Основной этап. Весь текстовый ввод идёт в GigaChat (function calling). GigaChat решает, какой tool HA вызвать, бот выполняет через MCP, возвращает результат обратно GigaChat — тот формирует ответ пользователю.

Никакого keyword-матчинга. Всё через LLM.

## Архитектура внутри этапа

```
TG текст → llm.HandleMessage(text, tools)
              │
              ├── 1. GigaChat API: chat completions + tool definitions
              │    tools: call_service, get_state, list_entities, get_live_context
              │
              ├── 2. GigaChat → tool_call (может быть несколько или цепочка)
              │
              ├── 3. Выполнение tool'ов через ha/mcp
              │
              ├── 4. Результат tool'ов → обратно GigaChat
              │
              └── 5. Финальный ответ GigaChat → в TG
```

## Что сделать (чек-лист)

### GigaChat API-клиент

- [ ] `internal/llm/gigachat.go` — структура `Client`, конструктор `New(credentials string, opts...)`
- [ ] получение токена доступа через auth endpoint (OAuth client_credential)
- [ ] `ChatCompletion(messages []Message, tools []Tool) (*Response, error)` — POST /chat/completions
- [ ] поддержка streaming (опционально, можно без него)

### Tool definitions (перевод MCP → GigaChat format)

- [ ] при старте загрузить tools из HA MCP (`internal/llm/tools.go`):
  - `BuildTools(mcpTools []mcp.Tool) []llm.Tool` — конвертация MCP-схемы в schema GigaChat
  - tools HA: `call_service`, `get_state`, `list_entities`, `get_live_context`

### Оркестрация (ReAct-like loop)

- [ ] `internal/llm/agent.go` — `Run(text string) (string, error)`:
  1. отправить сообщение + system prompt + tool_definitions в GigaChat
  2. если ответ содержит `tool_calls` — выполнить их через `MCPClient.CallTool`
  3. отправить результаты обратно GigaChat
  4. повторять шаги 2–3 пока GigaChat не ответит текстом (или до лимита итераций)
  5. вернуть финальный текст

### System prompt

- [ ] составной: «Ты помощник умного дома. У тебя есть доступ к Home Assistant. Используй инструменты чтобы управлять устройствами и отвечать на вопросы. Отвечай кратко на русском. Будь вежлив. Ничего не выдумывай — если инструмент вернул ошибку, так и скажи.»

### Интеграция с tg-ботом

- [ ] `internal/core` связывает TG-хендлеры с `llm.Agent`
- [ ] на текстовое сообщение (не команду) → `llm.Agent.Run`
- [ ] ошибки LLM (нет сети, таймаут, краш) → человекочитаемый ответ

### Команды

- [ ] `/gigachat status` — проверка доступа к GigaChat API (баланс токенов?)
- [ ] `/reset` — сброс истории диалога (если ведём history)

## Зависимости

- этап 03 (ha/mcp — для tools и выполнения)
- этап 04 (tg — приём сообщений и отправка)
- GigaChat API документация: `developers.sber.ru/docs/ru/gigachat/api`

## Проверка

«включи свет в зале» → GigaChat вызывает `call_service(light, turn_on, light.living_room)` → свет загорается. «какая температура на улице» → GigaChat вызывает `get_state(sensor.outdoor_temp)` → ответ «На улице 22°C».