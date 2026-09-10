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
	"hass-agent-bot/internal/notify"
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
	notif  *notify.Engine
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

	// Scheduler engine
	statePath := filepath.Join(a.cfg.DataDir, "scheduler_jobs.json")
	sched := scheduler.New(func(ctx context.Context, action scheduler.Action) error {
		a.logger.Info("scheduler: executing action", "tool", action.Tool, "args", action.Args)
		_, err := mcpCli.CallTool(ctx, action.Tool, action.Args)
		return err
	}, statePath)
	a.sched = sched
	go sched.Run(ctx)

	// LLM agent
	agent := llm.NewAgent(gigaClient, mcpCli, sched)

	// Notifications engine
	nf := notify.New(
		a.cfg.HA.URL,
		a.cfg.HA.Token,
		notify.Config{
			DebounceSeconds: a.cfg.Notify.DebounceSeconds,
			Entities:        a.cfg.Notify.Entities,
		},
		nil, // sender set after bot created
	)
	a.notif = nf

	// Start Telegram bot
	tgBot, err := tg.New(
		a.cfg.TG.Token,
		a.cfg.TG.AllowUserIDs,
		mcpCli,
		tg.WithAgent(agent),
		tg.WithScheduler(sched),
		tg.WithNotify(nf),
	)
	if err != nil {
		a.logger.Error("failed to create TG bot", "error", err)
		return err
	}
	a.tg = tgBot

	// Wire notification sender to TG bot
	nf.SetSender(func(ctx context.Context, text string) {
		a.tg.SendNotification(ctx, text)
	})

	// Start notification listener
	go nf.Run(ctx)

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