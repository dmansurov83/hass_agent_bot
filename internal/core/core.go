package core

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"hass-agent-bot/internal/config"
	hare "hass-agent-bot/internal/ha/rest"
	"hass-agent-bot/internal/llm"
	"hass-agent-bot/internal/notify"
	"hass-agent-bot/internal/scheduler"
	"hass-agent-bot/internal/tg"
)

type App struct {
	cfg    *config.Config
	logger *slog.Logger
	ha     *hare.Client
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
		"llm_provider", a.cfg.LLM.Provider,
		"model", a.cfg.LLM.Model,
	)

	// Connect to HA via REST (replaces MCP — REST sees ALL entities, not just Assist-exposed)
	haCli := hare.New(a.cfg.HA.URL, hare.Options{
		Token:  a.cfg.HA.Token,
		Logger: a.logger,
	})
	// Quick connectivity check
	_, err := haCli.States(ctx)
	if err != nil {
		a.logger.Warn("HA REST connectivity check", "error", err)
		// non-fatal: may be transient
	}
	a.ha = haCli

	// Init LLM client (provider chosen from config)
	llmClient, err := newLLMClient(&a.cfg.LLM, a.logger)
	if err != nil {
		a.logger.Error("failed to create LLM client", "provider", a.cfg.LLM.Provider, "error", err)
		return err
	}

	// Scheduler engine
	statePath := filepath.Join(a.cfg.DataDir, "scheduler_jobs.json")

	// Late-bound hooks
	var agentExecutor func(ctx context.Context, prompt string) (string, error)
	var sendToChat func(ctx context.Context, chatID int64, text string)
	var agentRef *llm.Agent

	sched := scheduler.New(func(ctx context.Context, action scheduler.Action) error {
		if action.AgentPrompt != "" {
			if agentExecutor == nil {
				return fmt.Errorf("AI agent not initialized")
			}
			result, err := agentExecutor(llm.WithTaskMode(ctx), action.AgentPrompt)
			if err != nil {
				return fmt.Errorf("AI task error: %w", err)
			}
			if result != "" && sendToChat != nil {
				chatID := action.ChatID
				if chatID == 0 {
					chatID = tgOwnerChatID(a.cfg)
				}
				sendToChat(ctx, chatID, result)
			}
			return nil
		}
		a.logger.Info("scheduler: executing action", "tool", action.Tool, "args", action.Args)
		if agentRef == nil {
			return fmt.Errorf("agent not initialized")
		}
		_, err := agentRef.ExecuteToolPublic(ctx, action.Tool, action.Args)
		return err
	}, statePath)
	a.sched = sched
	go sched.Run(ctx)

	// LLM agent
	histStore, err := newHistoryStore(a.cfg, a.logger)
	if err != nil {
		a.logger.Warn("history store init", "error", err)
	}
	if histStore != nil {
		defer histStore.Close()
	}
	memStore, err := newMemoryStore(a.cfg, a.logger)
	if err != nil {
		a.logger.Warn("memory store init", "error", err)
	}
	if memStore != nil {
		defer memStore.Close()
	}
	agent := llm.NewAgent(llmClient, haCli, sched, histStore, memStore)
	if err := agent.SetSystemPromptPath(
		filepath.Join(a.cfg.DataDir, "system_prompt.txt"),
		"system_prompt.txt",
	); err != nil {
		a.logger.Error("failed to load system prompt", "error", err)
		return err
	}
	agentRef = agent
	agentExecutor = func(ctx context.Context, prompt string) (string, error) {
		return agent.HandleMessage(ctx, prompt)
	}

	// Notifications engine
	nf := notify.New(
		a.cfg.HA.URL,
		a.cfg.HA.Token,
		notify.Config{
			DebounceSeconds: a.cfg.Notify.DebounceSeconds,
			Entities:        a.cfg.Notify.Entities,
		},
		nil,
	)
	a.notif = nf

	// Start Telegram bot
	tgBot, err := tg.New(
		a.cfg.TG.Token,
		a.cfg.TG.AllowUserIDs,
		haCli,
		tg.WithAgent(agent),
		tg.WithScheduler(sched),
		tg.WithNotify(nf),
	)
	if err != nil {
		a.logger.Error("failed to create TG bot", "error", err)
		return err
	}
	a.tg = tgBot

	sendToChat = func(ctx context.Context, chatID int64, text string) {
		tgBot.SendMessage(ctx, chatID, text)
	}

	nf.SetSender(func(ctx context.Context, text string) {
		a.tg.SendNotification(ctx, text)
	})

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

// newHistoryStore creates the history persistence layer for dialogs.
// Currently a JSON-file store; future backends (SQLite, Postgres) plug in here
// without touching the agent.
func newHistoryStore(cfg *config.Config, log *slog.Logger) (llm.HistoryStore, error) {
	return llm.NewFileStore(filepath.Join(cfg.DataDir, "history"), log)
}

// newMemoryStore creates the memory persistence layer for agent memories.
func newMemoryStore(cfg *config.Config, log *slog.Logger) (llm.MemoryStore, error) {
	return llm.NewFileMemoryStore(filepath.Join(cfg.DataDir, "memory"), log)
}

func tgOwnerChatID(cfg *config.Config) int64 {
	if len(cfg.TG.AllowUserIDs) == 0 {
		return 0
	}
	return cfg.TG.AllowUserIDs[0]
}

func newLLMClient(cfg *config.LLMConfig, log *slog.Logger) (llm.LLMClient, error) {
	switch cfg.Provider {
	case "gigachat":
		return llm.NewGigaChatClient(llm.Options{
			Credentials: cfg.Credentials,
			BaseURL:     cfg.BaseURL,
			Model:       cfg.Model,
			Logger:      log,
		})
	case "openai":
		return llm.NewOpenAIClient(llm.Options{
			BaseURL:     cfg.BaseURL,
			Credentials: cfg.Credentials,
			Model:       cfg.Model,
			Logger:      log,
		})
	default:
		return nil, fmt.Errorf("unknown llm provider: %s (supported: gigachat, openai)", cfg.Provider)
	}
}