package core

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"hass-agent-bot/internal/config"
	hamcp "hass-agent-bot/internal/ha/mcp"
	"hass-agent-bot/internal/llm"
	"hass-agent-bot/internal/scheduler"
	"hass-agent-bot/internal/tg"
)

type App struct {
	cfg    *config.Config
	logger *slog.Logger
	mcp    *hamcp.Client
	tg     *tg.Bot
	llm    *llm.Agent
	sched  *scheduler.Engine
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

	// Init GigaChat client
	gigaClient, err := llm.NewGigaChatClient(llm.Options{
		Credentials: a.cfg.GigaChat.Credentials,
		Model:       a.cfg.GigaChat.Model,
		Logger:      a.logger,
	})
	if err != nil {
		a.logger.Error("failed to create GigaChat client", "error", err)
		return err
	}

	// Scheduler engine: persists timers next to the binary
	statePath := filepath.Join(".", "scheduler_jobs.json")
	sched := scheduler.New(func(ctx context.Context, action scheduler.Action) error {
		a.logger.Info("scheduler: executing action", "action", action)
		if err := mcpCli.CallService(ctx, action.Domain, action.Service, action.Data); err != nil {
			a.logger.Error("scheduler: action failed", "error", err)
			return err
		}
		return nil
	}, statePath)
	a.sched = sched
	go sched.Run(ctx)

	// Init LLM agent (with scheduler access)
	agent := llm.NewAgent(gigaClient, mcpCli, sched)

	// Start Telegram bot
	tgBot, err := tg.New(
		a.cfg.TG.Token,
		a.cfg.TG.AllowUserIDs,
		mcpCli,
		tg.WithAgent(agent),
		tg.WithScheduler(sched),
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
	sched.Stop()

	return nil
}