package core

import (
	"log/slog"

	"hass-agent-bot/internal/config"
)

type App struct {
	cfg    *config.Config
	logger *slog.Logger
}

func New(cfg *config.Config, logger *slog.Logger) *App {
	return &App{cfg: cfg, logger: logger}
}

func (a *App) Run() error {
	a.logger.Info("hass-agent-bot starting...")
	return nil
}