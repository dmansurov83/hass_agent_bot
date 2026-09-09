# 03 — MCP-клиент Home Assistant

## Цель

Go-бот подключается к встроенному MCP Server HA через Streamable HTTP и получает доступ к его tools (call_service, query, list_entities и т.д.).

## Контекст

- HA с версии 2025.2 имеет встроенную интеграцию MCP Server: `Settings → Devices & services → MCP Server`, endpoint `/api/mcp`
- Управление устройствами ограничено сущностями, которые **exposed** для Assist (Настройки → Голосовые помощники → Exposed entities)
- Аутентификация: Long-lived access token (Профиль → Безопасность → Токены) или OAuth
- MCP transport: Streamable HTTP (stateless), для локальной сети + токен — самый простой вариант

## Что сделать (чек-лист)

- [ ] `internal/ha/mcp/client.go` — структура `Client`, конструктор `New(baseURL, token string, opts...)`
- [ ] подключение по Streamable HTTP:
  - подключение и `initialize` handshake
  - получение списка tools (`tools/list`)
  - выполнение вызовов (`tools/call`)
- [ ] `internal/ha/mcp/tools.go` — обёртки над стандартными tools HA:
  - `CallService(domain, service string, data map[string]any) (Result, error)`
  - `GetState(entityID string) (*State, error)`
  - `ListEntities() ([]Entity, error)`
  - `Query(query string) (Result, error)` — Assist query (если поддерживается)
- [ ] типы `State`, `Entity`, `Attributes`
- [ ] reconnect с exponential backoff при разрыве
- [ ] кэш состояний (для мгновенных ответов и уведомлений)
- [ ] тесты с мок-сервером MCP

## Зависимости

- этапы 01–02 готовы
- библиотека: `github.com/mark3labs/mcp-go` (клиент MCP для Go, Streamable HTTP)

## Проверка

Подключение к реальному HA, вывод списка tools и первых 5 состояний (тривиальная main).

## Примечание

- Кэш состояний из `ListEntities`/`GetState` пригодится в этапе 07 (уведомления)
- MCP-notifications от HA пока не поддерживаются (этап 07 — через REST API)