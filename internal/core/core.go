package core

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"hass-agent-bot/internal/channel"
	"hass-agent-bot/internal/config"
	"hass-agent-bot/internal/console"
	hare "hass-agent-bot/internal/ha/rest"
	"hass-agent-bot/internal/llm"
	"hass-agent-bot/internal/notify"
	"hass-agent-bot/internal/scheduler"
	"hass-agent-bot/internal/tg"
)

type App struct {
	cfg      *config.Config
	logger   *slog.Logger
	ha       *hare.Client
	channels []channel.Channel
	llm      *llm.Agent
	sched    *scheduler.Engine
	notif    *notify.Engine

	consoleEnabled bool
}

type Option func(*App)

func WithConsole(enabled bool) Option {
	return func(a *App) { a.consoleEnabled = enabled }
}

func New(cfg *config.Config, logger *slog.Logger, opts ...Option) *App {
	a := &App{cfg: cfg, logger: logger}
	for _, o := range opts {
		o(a)
	}
	return a
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

	// Build channels
	var channels []channel.Channel

	if a.cfg.TG.Token != "" {
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
		channels = append(channels, tgBot)
	}

	if a.consoleEnabled {
		channels = append(channels, console.New(agent, sched, nf, haCli, a.logger))
	}

	if len(channels) == 0 {
		return fmt.Errorf("no channels enabled")
	}
	a.channels = channels

	sendToChat = func(ctx context.Context, chatID int64, text string) {
		for _, ch := range a.channels {
			ch.SendMessage(ctx, chatID, text)
		}
	}

	nf.SetSender(func(ctx context.Context, text string) {
		for _, ch := range a.channels {
			ch.SendNotification(ctx, text)
		}
	})

	go nf.Run(ctx)

	// Start all channels
	for _, ch := range a.channels {
		go func(c channel.Channel) {
			if err := c.Start(ctx); err != nil {
				a.logger.Error("channel exited with error", "error", err)
			}
		}(ch)
	}

	// Graceful shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	a.logger.Info("bot started, waiting for shutdown signal")
	<-sig
	a.logger.Info("shutting down...")

	var wg sync.WaitGroup
	for _, ch := range a.channels {
		wg.Add(1)
		go func(c channel.Channel) {
			defer wg.Done()
			c.Close(ctx)
		}(ch)
	}
	wg.Wait()
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
