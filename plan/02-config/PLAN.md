# 02 — Конфигурация

## Цель

Бот читает конфиг: адрес HA, токен HA, токен TG, GigaChat credentials, user_id. Конфигурируемый адрес HA — ключевое требование.

## Что сделать (чек-лист)

- [ ] определить формат: `env` (переменные окружения) + `yaml` как overrides
- [ ] реализовать `internal/config.Load(path string) (*Config, error)`:

### HA
  - схема, хост, порт HA (дефолт `http://localhost:8123`)
  - long-lived access token HA
### Telegram
  - токен TG
  - `allow_user_ids []int64`
### GigaChat
  - `GIGACHAT_CREDENTIALS` (ключ авторизации, клиентский id:secret)
  - модель (дефолт `GigaChat-2-Max` или `GigaChat-2-Pro`)
  - опционально: температура, max_tokens, таймаут
### Notify (этап 07)
  - список entity_id для уведомлений, дебаунс, дефолтные метки

- [ ] env-переменные: `HA_URL`, `HA_TOKEN`, `TG_TOKEN`, `ALLOW_USERS`, `GIGACHAT_CREDENTIALS`, `GIGACHAT_MODEL`
- [ ] yaml-файл: `config.yaml` рядом с бинарником (если есть)
- [ ] env перекрывает yaml
- [ ] `internal/core` — простая шина: получает конфиг, передаёт пакетам
- [ ] написать тест на загрузку конфига
- [ ] `go vet ./...` — чисто

## Проверка

`go run ./cmd/bot --config test_config.yaml` — модель работает, при ошибках читаемые сообщения.

## Зависимости

- этап 01 готов

## Результат

Конфиг с HA URL, токеном HA, токеном TG, GigaChat credentials, allowlist. Загружается при старте.