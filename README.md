# hass-agent-bot

Telegram-бот для управления [Home Assistant](https://www.home-assistant.io/) на естественном языке через LLM (по умолчанию [GigaChat](https://developers.sber.ru/gigachat), поддерживаются OpenAI-совместимые API). Пишете «включи свет в гостиной» — бот сам находит устройства, выполняет команду и отвечает результатом.

## Возможности

- **Естественный язык** — команды разбирает только LLM (function calling), без keyword-матчинга. **GigaChat** или **OpenAI-совместимые** (DeepSeek, Ollama, LM Studio, vLLM, ...)
- **Прямая интеграция с HA** — через MCP-клиент ко встроенному MCP Server (`/api/mcp`, тулы `HassTurnOn`, `HassLightSet`, `GetLiveContext` и др.)
- **Таймеры и расписания** — «выключи телевизор через 30 минут» или по cron
- **Уведомления из HA** — подписка на события по WebSocket, дебаунс, пауза командой `/quiet`
- **Безопасность** — доступ только для allowlist Telegram `user_id`
- **Docker-ready** — запуск 24/7 одной командой

## Архитектура

```
Telegram ──► LLM (GigaChat / OpenAI, ReAct loop) ──► MCP-клиент ──► Home Assistant MCP Server
   ▲                         │                                        │
   └─────────────────────────┴────── таймеры / уведомления (WS) ◄─────┘
```

## Быстрый старт

### Docker (рекомендуется)

```bash
cp .env.example .env      # заполни HA_URL, HA_TOKEN, TG_TOKEN, LLM_CREDENTIALS
cp config.example.yaml config.yaml   # настрой под себя
docker compose up -d
```

### Локально

```bash
go run ./cmd/bot --config config.yaml
```

> Сертификат Минцифры (`russian_trusted_root_ca_pem.crt`) уже включён в образ — нужен для API GigaChat (`ngw.devices.sberbank.ru:9443`).

## Конфигурация

Все ключи можно задать в `config.yaml` или через переменные окружения:

| Параметр | Окружение | Описание |
|---|---|---|
| `ha.url` | `HA_URL` | Адрес Home Assistant |
| `ha.token` | `HA_TOKEN` | Long-lived access token HA |
| `tg.token` | `TG_TOKEN` | Токен от @BotFather |
| `tg.allow_user_ids` | `ALLOW_USERS` | Разрешённые Telegram user_id |
| `llm.provider` | `LLM_PROVIDER` | Провайдер LLM: `gigachat` (по умолчанию) или `openai` |
| `llm.model` | `LLM_MODEL` | Модель (по умолчанию `GigaChat-2-Pro`) |
| `llm.credentials` | `LLM_CREDENTIALS` | GigaChat: Basic-ключ; OpenAI: API key (`sk-...`) |
| `llm.base_url` | `LLM_BASE_URL` | Только для OpenAI-совместимых: `https://api.deepseek.com`, `http://localhost:11434/v1`, ... |
| `notify.entities` | — | Маски entities для уведомлений (`binary_sensor.*`) |

> Легаси-совместимость: старый блок `gigachat: { credentials, model }` в корне конфига тоже работает — он подхватывается как `llm.provider: gigachat`.

### Примеры провайдеров

```yaml
# GigaChat (по умолчанию)
llm:
  provider: gigachat
  model: GigaChat-2-Pro
  credentials: "ключ авторизации"

# DeepSeek
llm:
  provider: openai
  model: deepseek-chat
  credentials: "sk-..."
  base_url: https://api.deepseek.com

# Локальная Ollama
llm:
  provider: openai
  model: llama3
  credentials: "ollama"   # любой заполнитель, Ollama не проверяет ключ
  base_url: http://localhost:11434/v1
```

## Команды

| Команда | Описание |
|---|---|
| `/start`, `/help` | Приветствие и справка |
| `/list` | Все устройства в доме |
| `/status` | Статус подключения к HA |
| `/timers` | Активные таймеры и расписания |
| `/quiet [часы]` | Пауза уведомлений (по умолчанию 1 час) |

Остальное — обычным текстом: «Поставь чайник через 10 минут» или «Что с температурой на улице?»

## Стек

Go 1.27 · [go-telegram/bot](https://github.com/go-telegram/bot) · [mcp-go](https://github.com/mark3labs/mcp-go) · GigaChat API / OpenAI-совместимые · robfig/cron · Docker

## Предупреждение

> **Весь код в этом репозитории целиком сгенерирован нейросетями (LLM).**  
> Качество, надёжность и безопасность кода не гарантируются. Перед использованием в production-среде требуется ревью и тестирование.

## Лицензия

MIT