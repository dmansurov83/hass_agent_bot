# AGENTS.md — статус разработки

> Этот файл для AI-агентов, работающих над проектом. Обновляй при переходе между этапами.

## Текущий этап

**Этап 03 — MCP-клиент HA (завершён)**

`internal/ha/mcp` готов: подключение к HA MCP Server (Streamable HTTP + Bearer), tools ListEntities/GetState/CallService, 4 теста с in-process MCP-сервером. Коммит `bfa6e66`.

## Что сейчас в разработке

Этап 04 — Telegram-каркас: go-telegram/bot, allowlist, команды /start /help /list /status.

## Прогресс по этапам

| # | Этап | Статус |
|---|---|---|
| 01 | bootstrap — каркас Go-проекта | ✅ завершён |
| 02 | config — env + yaml (HA, GigaChat, TG) | ✅ завершён |
| 03 | ha/mcp — MCP-клиент к HA MCP Server | ✅ завершён |
| 04 | telegram-core — TG-бот, команды | 🔄 в работе |
| 05 | llm — GigaChat + function calling + оркестрация | ⬜ не начат |
| 06 | scheduler — таймеры, cron через LLM | ⬜ не начат |
| 07 | notify — уведомления из HA (WS) | ⬜ не начат |
| 08 | deploy — systemd/docker, 24/7 | ⬜ не начат |

## Главные решения (не менять без обсуждения)

| Решение | Значение |
|---|---|
| Язык | Go 1.22+ |
| TG-библиотека | `github.com/go-telegram/bot` |
| LLM | **GigaChat API** (Freemium, 1M токенов/мес, 0 ₽) |
| Доступ к HA | **MCP-клиент** через `github.com/mark3labs/mcp-go` к встроенному MCP Server HA (`/api/mcp`) |
| Разбор текста | **Только LLM** (GigaChat function calling). Никакого keyword-матчинга |
| Схема работы | **Вариант A**: Go-бот оркестрирует: GigaChat → tool_call → бот выполняет через MCP → результат обратно |
| Безопасность | allowlist по `user_id` |
| Логи | `log/slog` |
| Уведомления | WS REST API напрямую (MCP notifications не поддерживаются HA) |

## Структура проекта на диске

```
hass_agent_bot/
├── plan/
│   ├── PLAN.md                   # архитектура, решения, сроки (v2)
│   ├── AGENTS.md                 ← этот файл
│   ├── 01-bootstrap/PLAN.md
│   ├── 02-config/PLAN.md
│   ├── 03-hass-client/PLAN.md
│   ├── 04-telegram-core/PLAN.md
│   ├── 05-intents/PLAN.md
│   ├── 06-scheduler/PLAN.md
│   ├── 07-notify/PLAN.md
│   └── 08-deploy/PLAN.md
├── cmd/bot/main.go
├── internal/
│   ├── config/                   # загрузка конфига
│   ├── ha/mcp/                   # MCP-клиент Home Assistant
│   ├── tg/                       # Telegram-бот
│   ├── llm/                      # GigaChat клиент + оркестрация
│   ├── scheduler/                # таймеры и расписания
│   ├── notify/                   # уведомления из HA
│   └── core/                     # шина
├── go.mod
└── go.sum
```

## Развилки

- **Таймеры (06)**: персистентность в JSON-файл (пока так)
- **LLM-слой (05)**: ReAct-like loop — GigaChat с function calling. При ошибках — 2–3 попытки перевызова.
- **Голосовые команды**: не в текущем плане
- **GigaChat model**: `GigaChat-2-Max` или `GigaChat-2-Pro`, решим на этапе 05