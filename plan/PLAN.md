# Telegram-бот для Home Assistant (Go) — v2 (LLM + MCP)

Личный бот: общаешься с ним в Telegram текстом, он через GigaChat понимает запросы и через MCP управляет Home Assistant. Стек — Go. HA адрес конфигурируемый.

## Что должен уметь (5 функций)

1. **Управление устройствами** — «включи свет в гостиной», «выключи чайник»
2. **Запрос состояния** — «какая температура в спальне», «что с окнами»
3. **Сценарии (scripts HA)** — «запусти режим кино», «режим охраны»
4. **Расписания / таймеры** — «выключи чайник через 15 минут», «свет в 7:00 по будням»
5. **Уведомления** — бот сам пишет при событии (дверь открыта, сработал датчик)

## Архитектура (вариант A)

```
Telegram (пользователь)
        │  text
        ▼
┌───────────────────┐
│  tg/adapter       │ приём/отправка, клавиатуры, allowlist
└─────────┬─────────┘
          ▼
┌──────────────────────────────────────────────┐
│  llm (intents)                               │
│  → GigaChat API (function calling)           │
│  → GigaChat решает: какой tool вызвать       │
│  → бот выполняет tool через MCP              │
│  → результат → GigaChat → ответ пользователю │
└─────────┬────────────────────────────────────┘
          │              │
          ▼              ▼
┌──────────────┐  ┌──────────────┐
│  ha/mcp      │  │  scheduler   │ таймеры, cron
│  MCP-клиент  │  │  (через LLM) │
│  к HA MCP    │  └──────────────┘
│  Server      │
└──────┬───────┘
       │ MCP (Streamable HTTP)
       ▼
┌──────────────────┐
│  Home Assistant  │ встроенный MCP Server (/api/mcp)
│  (локальная сеть)│ tools: call_service, get_state, list_entities...
└──────────────────┘
       ▲
┌──────┴───────┐
│  notify      │ подписка на события HA → сообщения в TG
│  (REST/WS)   │ (MCP notifications пока не поддерживаются)
└──────────────┘

  config — env + yaml, всё конфигурируемое
  gigachat — GigaChat API (credentials, model, params)
```

Слои: `cmd/bot` (точка входа), `internal/config`, `internal/ha/mcp`, `internal/tg`, `internal/llm`, `internal/scheduler`, `internal/notify`, `internal/core`.

## Цикл обработки сообщения

1. Пользователь пишет «включи свет в гостиной»
2. Go-бот отправляет запрос в GigaChat API (chat completions) с описанием доступных tools
3. GigaChat возвращает `tool_call`: `{name: "call_service", args: {domain: "light", service: "turn_on", entity_id: "light.living_room"}}`
4. Go-бот выполняет tool через HA MCP Server (Streamable HTTP)
5. Результат (`{"success": true}`) отправляется обратно GigaChat
6. GigaChat формирует ответ: «Свет в гостиной включён» → бот отправляет в Telegram

## Ключевые решения

| Вопрос | Решение | Почему |
|---|---|---|
| Язык | Go 1.22+ | выбрано |
| TG-библиотека | `github.com/go-telegram/bot` | активно ведётся, стабильный API |
| LLM | GigaChat API (Freemium) | бесплатно (1M токенов/мес), русский, РФ |
| Доступ к HA | **MCP-клиент** через `mcp-go` к встроенному MCP Server HA | HA с версии 2025.2 имеет встроенный MCP-сервер, tools из коробки |
| Адрес HA | конфигурируемый: схема + host:port + токен | требование |
| Разбор текста | **Только LLM** (GigaChat function calling) | GigaChat сам парсит NL, синонимы, неоднозначности |
| Безопасность | allowlist `user_id` в конфиге | доступ к дому — строго |
| Запуск | один бинарник + systemd/docker | простота |
| Логи | `log/slog` | встроено в Go |

## Этапы и зависимости

```
01 bootstrap ─▶ 02 config ─▶ 03 ha/mcp ─▶ 04 tg/core ─▶ 05 llm (intents)
                                                         └──▶ 06 scheduler
                                                 03 ──────└──▶ 07 notify (REST)
08 deploy — в любой момент после 04
```

| № | Этап | Результат |
|---|---|---|
| [01](01-bootstrap/PLAN.md) | Каркас проекта | пустой собирающийся проект |
| [02](02-config/PLAN.md) | Конфигурация | env + yaml: HA URL, GigaChat credentials, TG токен |
| [03](03-hass-client/PLAN.md) | MCP-клиент HA | подключение к HA MCP Server, получение tools, вызовы |
| [04](04-telegram-core/PLAN.md) | Каркас бота | /start /help /list, allowlist |
| [05](05-intents/PLAN.md) | LLM-слой (intents) | GigaChat + function calling + MCP оркестрация |
| [06](06-scheduler/PLAN.md) | Таймеры | «через 15 минут», cron, персистентность |
| [07](07-notify/PLAN.md) | Уведомления | события HA → сообщения в TG (через REST API) |
| [08](08-deploy/PLAN.md) | Запуск 24/7 | systemd/docker, логи, обновление |

## Что ломается и запасной ход

- **GigaChat API недоступен** → бот отвечает «Извини, проблема с подключением к ИИ. Попробуй позже».
- **HA перезагрузился** → MCP reconnect с exponential backoff.
- **Токены протухли** → ошибка в логе при старте, правка конфига + рестарт.
- **GigaChat ошибся в entity_id** → tool_call возвращает ошибку от HA, GigaChat пробует другой entity или просит уточнить.
- **Спам уведомлениями** → дебаунс, кнопка «пауза на час».
- **Нет интернета (нельзя вызвать GigaChat)** → «Извини, нет связи с ИИ. Попробуй позже» — без fallback на keyword-матчинг.

## Открытые вопросы

- [ ] HA URL, HA long-lived access token
- [ ] GigaChat API credentials (ключ авторизации)
- [ ] токен Telegram-бота от @BotFather
- [ ] твой Telegram `user_id` (для allowlist)

## Оценка времени

| Этап | Экономно | Комфортно |
|---|---|---|
| 01–02 | 1 вечер | 2 вечера |
| 03 | 1–2 вечера | 3 вечера |
| 04 | 1 вечер | 2 вечера |
| 05 | 2–3 вечера | 4 вечера |
| 06 | 1 вечер | 2 вечера |
| 07 | 1 вечер | 2 вечера |
| 08 | 1 вечер | 2 вечера |
| **Итого** | **~9–10 вечеров** | **~17 вечеров** |

Оценка на сентябрь 2026.