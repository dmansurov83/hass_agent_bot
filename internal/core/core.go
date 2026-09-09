package core

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"hass-agent-bot/internal/config"
	hamcp "hass-agent-bot/internal/ha/mcp"
	"hass-agent-bot/internal/tg"
)

type App struct {
	cfg    *config.Config
	logger *slog.Logger
	mcp    *hamcp.Client
	tg     *tg.Bot
}

func New(cfg *config.Config, logger *slog.Logger) *App {
	return &App{cfg: cfg, logger: logger}
}

func (a *App) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a.logger.Info("hass-agent-bot starting...",
		"ha_url", a.cfg.HA.URL,
		"model", a.cfg.GigaChat.Model,
	)

	// Connect to HA via MCP
	mcpCli, err := hamcp.New(ctx, a.cfg.HA.URL+"/api/mcp", hamcp.Options{
		Token:  a.cfg.HA.Token,
		Logger: a.logger,
	})
	if err != nil {
		a.logger.Error("failed to connect to HA via MCP", "error", err)
		return err
	}
	a.mcp = mcpCli
	defer mcpCli.Close()

	// Start Telegram bot
	tgBot, err := tg.New(
		a.cfg.TG.Token,
		a.cfg.TG.AllowUserIDs,
		mcpCli,
	)
	if err != nil {
		a.logger.Error("failed to create TG bot", "error", err)
		return err
	}
	a.tg = tgBot

	// Graceful shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	go a.tg.Start(ctx)

	a.logger.Info("bot started, waiting for shutdown signal")
	<-sig
	a.logger.Info("shutting down...")
	a.tg.Close(ctx)

	return nil
}