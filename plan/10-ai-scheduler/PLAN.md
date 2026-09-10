# 10 — Фоновые AI-задачи (AI Scheduler)

## Цель

Возможность создавать фоновые задачи, которые при срабатывании запускают **полноценный AI-диалог** (ReAct loop), а не просто выполняют один фиксированный tool. Например:

> «Каждый день в 7:00 проверяй заряд батарей всех датчиков и напиши, если что-то скоро сядет»

## Чем отличается от обычного schedule_action

| Сейчас (schedule_action) | Нужно (schedule_ai_action) |
|---|---|
| Триггер → один tool HA (HassTurnOn, HassLightSet…) | Триггер → LLM ReAct-цикл (GetLiveContext, анализ, вывод) |
| Результат — успех/ошибка вызова | Результат — текстовый ответ LLM, отправленный в Telegram |
| Не может анализировать и рассуждать | Может: проверить батареи, сравнить температуры, обобщить |

## Архитектура

```
Пользователь: "Проверяй батареи каждый день"
       │
       ▼
LLM → schedule_ai_action(prompt=..., cron=..., chat_id=...)
       │  chat_id проставляется автоматически из контекста TG-сообщения
       ▼
scheduler.Engine → через N(time/cron) → executor
       │
       ▼
core.executor: если action.AgentPrompt ≠ "" →
       ├── agent.HandleMessage(ctx, action.AgentPrompt)  ← ReAct-цикл
       ├── результат (текст)
       └── tg.SendMessage(ctx, action.ChatID, результат)
```

## Изменения в коде

### 1. `internal/scheduler/engine.go` — Action.ChatID + AgentPrompt

```go
type Action struct {
    Tool        string         `json:"tool"`
    Args        map[string]any `json:"args"`
    Text        string         `json:"text,omitempty"`
    AgentPrompt string         `json:"agent_prompt,omitempty"` // prompt для AI-задачи
    ChatID      int64          `json:"chat_id,omitempty"`      // куда ответить
}
```

### 2. `internal/llm/tools.go` — новый tool schedule_ai_action

- Добавить `SchedulerAIFunction()`:
  - `prompt` (string, required) — инструкция для AI
  - `at` / `delay` / `cron` (одно из трёх)
  - `label` (string) — название задачи
  - `chat_id` — НЕ показываем LLM (проставляется автоматически)
- Обновить `AllFunctions()` — добавить SchedulerAIFunction в список

### 3. `internal/llm/agent.go` — chatID через context + обработчик

- Определяем ключ контекста: `type ctxKey int; const ctxKeyChatID ctxKey = iota`
- В `executeTool()` добавить case `"schedule_ai_action"` → `handleScheduleAI(ctx, args)`
- `handleScheduleAI()` достаёт `chatID` из `ctx.Value(ctxKeyChatID)` и кладёт в `Action.ChatID`
```go
func (a *Agent) handleScheduleAI(ctx context.Context, args map[string]any) (string, error) {
    chatID, _ := ctx.Value(ctxKeyChatID).(int64)
    prompt, _ := args["prompt"].(string)
    // ...
    act := scheduler.Action{
        AgentPrompt: prompt,
        ChatID:      chatID,
    }
    // ScheduleCron/In/At ...
}
```

### 4. `internal/tg/tg.go` — прокидываем chatID в context

В `textHandler` перед вызовом agent:
```go
ctx = context.WithValue(ctx, ctxKeyChatID, chatID)
reply, err := b.agent.HandleMessage(ctx, text)
```

### 5. `internal/core/core.go` — executor с AI

Executor-замыкание: если `action.AgentPrompt != ""` → вызвать `agent.HandleMessage`, результат отправить в `action.ChatID`.

Проблема: scheduler создаётся раньше agent и tg. Решение — переменные-замыкания:

```go
var agentExecutor func(ctx context.Context, prompt string) (string, error)

sched := scheduler.New(func(ctx context.Context, action scheduler.Action) error {
    if action.AgentPrompt != "" {
        if agentExecutor == nil {
            return fmt.Errorf("AI agent not initialized")
        }
        result, err := agentExecutor(ctx, action.AgentPrompt)
        if err != nil {
            return err
        }
        if result != "" && action.ChatID != 0 {
            // отправка через tg (см. п.6)
        }
        return nil
    }
    // regular tool call
    normalizeArgs(action.Args)
    _, err := mcpCli.CallTool(ctx, action.Tool, action.Args)
    return err
}, statePath)
```

### 6. `internal/core/core.go` — отложенная инициализация отправки

Нужен sender, доступный после создания tg-бота:

```go
var sendToChat func(ctx context.Context, chatID int64, text string)

// после создания tgBot:
sendToChat = func(ctx context.Context, chatID int64, text string) {
    tgBot.SendMessage(ctx, chatID, text)
}
```

В executor-e:
```go
if result != "" && action.ChatID != 0 && sendToChat != nil {
    sendToChat(ctx, action.ChatID, result)
}
```

### 7. SystemPrompt — упомянуть schedule_ai_action

Обновить `SystemPrompt()` в `tools.go` — добавить описание `schedule_ai_action` для AI-задач.

### 8. Безопасность: fallback chatID

Если `chat_id` в задаче пуст (0) — старая задача, созданная до этой фичи, или failure — отправлять в `b.first` (первый пользователь allowlist), как раньше:

```go
if result != "" {
    if action.ChatID != 0 {
        sendToChat(ctx, action.ChatID, result)
    } else {
        sendToChat(ctx, b.first, result) // fallback
    }
}
```

### 9. Команда /ai-tasks (опционально)

Просмотр активных AI-задач. Можно показать вместе с /timers (они уже в одном списке с `Type=cron`).

## Примеры использования

1. **Ежедневная проверка батарей:**
   > "Проверяй каждый день в 7 утра заряд батарей датчиков. Если какой-то ниже 20%, напиши мне"
   → schedule_ai_action(prompt=..., cron="0 0 7 * * *")

2. **Разовый анализ:**
   > "Через 30 минут проверь, все ли окна закрыты"
   → schedule_ai_action(prompt=..., delay="30m")

3. **Вечерний дайджест:**
   > "Каждый вечер в 21:00 пиши мне сводку: какая температура в доме и на улице, открыты ли окна"
   → schedule_ai_action(prompt=..., cron="0 0 21 * * *")

## Проверка

1. Создать AI задачу: "Проверяй каждую минуту температуру и пиши мне если выше 30"
   → задача создаётся, появляется в /timers
2. Дождаться срабатывания → приходит сообщение с анализом
3. Удалить задачу через /timers (или отмена из LLM)
4. Перезапуск бота — cron-задачи восстанавливаются из scheduler_jobs.json

## Зависимости

- Этап 06 (scheduler) — уже готов
- Нет новых библиотек