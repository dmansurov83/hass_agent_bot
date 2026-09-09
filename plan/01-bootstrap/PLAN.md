# 01 — Каркас проекта

## Цель

Пустой собирающийся Go-проект с модулем, main, минимальной структурой пакетов.

## Что сделать (чек-лист)

- [ ] `go mod init github.com/<user>/hass-agent-bot`
- [ ] создать `cmd/bot/main.go` — просто main, печатает "hello"
- [ ] создать заготовки пакетов (`internal/config`, `internal/ha`, `internal/tg`, `internal/intents`, `internal/scheduler`, `internal/notify`, `internal/core`) — `package x` в `internal/x/x.go`
- [ ] убедиться, что `go build ./cmd/bot` компилируется
- [ ] `go vet ./...` — без ошибок
- [ ] зафиксировать версию Go в `go.mod` (go 1.22+)
- [ ] `git init`, первый коммит с пустым проектом

## Зависимости

- `/go.mod`

## Результат

Один бинарник, который запускается и выводит `hass-agent-bot starting...`.