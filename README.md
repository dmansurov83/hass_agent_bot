# hass-agent-bot

Telegram-бот для управления [Home Assistant](https://www.home-assistant.io/) на естественном языке через [GigaChat](https://developers.sber.ru/gigachat). Пишете «включи свет в гостиной» — бот сам находит устройства, выполняет команду и отвечает результатом.

## Возможности

- **Естественный язык** — команды разбирает только LLM (function calling), без keyword-матчинга
- **Прямая интеграция с HA** — через MCP-клиент ко встроенному MCP Server (`/api/mcp`, тулы `HassTurnOn`, `HassLightSet`, `GetLiveContext` и др.)
- **Таймеры и расписания** — «выключи телевизор через 30 минут» или по cron
- **Уведомления из HA** — подписка на события по WebSocket, дебаунс, пауза командой `/quiet`
- **Безопасность** — доступ только для allowlist Telegram `user_id`
- **Docker-ready** — запуск 24/7 одной командой

## Архитектура

```
Telegram ──► GigaChat (LLM, ReAct loop) ──► MCP-клиент ──► Home Assistant MCP Server
   ▲              │                                              │
   └──────────────┴──────── таймеры / уведомления (WS) ◄─────────┘
```

## Быстрый старт

### Docker (рекомендуется)

```bash
cp .env.example .env      # заполни HA_URL, HA_TOKEN, TG_TOKEN, GIGACHAT_CREDENTIALS
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
| `gigachat.credentials` | `GIGACHAT_CREDENTIALS` | Ключ авторизации GigaChat |
| `gigachat.model` | — | Модель (по умолчанию `GigaChat-2-Pro`) |
| `notify.entities` | — | Маски entities для уведомлений (`binary_sensor.*`) |

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

Go 1.27 · [go-telegram/bot](https://github.com/go-telegram/bot) · [mcp-go](https://github.com/mark3labs/mcp-go) · GigaChat API · robfig/cron · Docker

## Предупреждение

> **Весь код в этом репозитории целиком сгенерирован нейросетями (LLM).**  
> Качество, надёжность и безопасность кода не гарантируются. Перед использованием в production-среде требуется ревью и тестирование.

## Лицензия

MIT