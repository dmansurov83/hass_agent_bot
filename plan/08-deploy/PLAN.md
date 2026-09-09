# 08 — Запуск 24/7 и сопровождение

## Цель

Бот работает непрерывно, запускается при старте системы, обновляется без проблем.

## Что сделать (чек-лист)

### systemd (если на Linux-сервере/mini-PC)

- [ ] unit-файл `/etc/systemd/system/hass-agent-bot.service`:
  - `ExecStart=/usr/local/bin/hass-agent-bot`
  - `WorkingDirectory=/opt/hass-agent-bot`
  - `Restart=always`
  - `User=homeassistant`
- [ ] `systemctl enable --now hass-agent-bot`

### Docker (альтернатива)

- [ ] `Dockerfile` — multistage: `golang:1.22` → `scratch`
- [ ] `docker-compose.yml` с env-переменными
  - `restart: unless-stopped`
  - volumes: config.yaml + данные таймеров

### Логи

- [ ] `log/slog` в JSON формате (для парсинга) и человекочитаемый в консоль
- [ ] ротация логов — systemd journal или Docker logs (не нужна отдельная)

### Обновление

- [ ] сценарий: `git pull && go build -o /usr/local/bin/hass-agent-bot ./cmd/bot && systemctl restart hass-agent-bot`
- [ ] или: `docker compose pull && docker compose up -d`

### Healthcheck

- [ ] `/status` команда в TG — время работы, статус HA, кол-во таймеров

## Зависимости

- этап 04 минимум (чтобы можно было уже запустить ядро)

## Проверка

Перезагрузка машины — бот стартует автоматически, отвечает на команды.