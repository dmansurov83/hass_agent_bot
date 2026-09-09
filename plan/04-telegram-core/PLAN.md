# 04 — Telegram-каркас

## Цель

Telegram-бот, отвечает на команды, фильтрует пользователей по allowlist.

## Что сделать (чек-лист)

- [ ] подключить `github.com/go-telegram/bot`
- [ ] `internal/tg/bot.go` — `New(apiToken string) (*Bot, error)`, запускает long polling
- [ ] middleware: проверка `user_id` из `config.AllowUserIDs`
- [ ] обработчики команд:
  - `/start` — приветствие + что умею
  - `/help` — список команд и примеры
  - `/list` — список всех сущностей HA (entity_id + friendly_name + state)
  - `/status` — проверка связи с HA
- [ ] клавиатура: Inline клавиатура для быстрых действий (по желанию, но полезно)
- [ ] graceful shutdown на SIGINT/SIGTERM
- [ ] тесты: мок-сервер Telegram (или изоляция логики)

## Зависимости

- этапы 01–03 готовы (для /list нужен MCP-клиент HA)
- `github.com/go-telegram/bot`

## Проверка

Запустить бота, написать `/list` — получить список сущностей HA.