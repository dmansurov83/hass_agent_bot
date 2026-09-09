# 06 — Таймеры и расписания

## Цель

«Выключи чайник через 15 минут», «свет в 7:00 по будням».

## Контекст

Таймеры не парсим ключевыми словами — GigaChat понимает фразу и возвращает tool_call `schedule_action` (см. этап 05). Таймер — это просто отложенный вызов tool'а HA через MCP.

## Архитектура

```
GigaChat → tool_call {"name":"schedule_action",
                      "args":{"delay":"15m","action":{"domain":"switch","service":"turn_off","entity_id":"switch.kettle"}}}
     ↓
scheduler.Engine.Schedule(delay, action)
     ↓ (по истечении)
core.ExecuteIntent(action) → ha/mcp CallService → HA
     ↓
ответ в TG: «Таймер: выключить чайник через 15 мин»
```

## Что сделать (чек-лист)

- [ ] `internal/scheduler/engine.go`:
  - `Schedule(delay time.Duration, action Action) JobID`
  - `ScheduleCron(expr string, action Action) JobID`
  - `Cancel(jobID JobID)`
  - `List() []Job`
- [ ] `internal/scheduler/action.go` — тип `Action` (domain, service, entity_id, data) — переиспользуем из llm tools
- [ ] tool definition для GigaChat (`schedule_action`) — регистрируется вместе с tools HA в этапе 05:
  - вход: `delay` (parseable duration или cron-выражение через флаг `cron: true`), `action`
- [ ] персистентность:
  - сохранять активные таймеры в JSON-файл рядом с бинарником
  - восстанавливать после перезапуска
- [ ] интеграция: при срабатывании таймера → `core.ExecuteIntent(action)` → HA через MCP → ответ в TG
- [ ] команда `/timers` — список активных таймеров (отменяются кнопкой)

## Зависимости

- этап 05 (llm tools — туда добавляется tool `schedule_action`)
- библиотека: `robfig/cron/v3`

## Проверка

«Выключи чайник через 2 минуты» — GigaChat вызывает `schedule_action`, через 2 минуты чайник выключается, приходит уведомление.