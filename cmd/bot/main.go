package main

import (
	"flag"
	"log/slog"
	"os"
	"path/filepath"

	"hass-agent-bot/internal/config"
	"hass-agent-bot/internal/core"
)

func main() {
	cfgPath := flag.String("config", "", "path to config.yaml")
	console := flag.Bool("console", false, "enable console channel (stdin/stdout)")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	var logger *slog.Logger
	if *console {
		logPath := filepath.Join(cfg.DataDir, "bot.log")
		if err := os.MkdirAll(cfg.DataDir, 0755); err == nil {
			f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err == nil {
				logger = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo}))
			}
		}
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	slog.SetDefault(logger)

	app := core.New(cfg, logger, core.WithConsole(*console))

	if err := app.Run(); err != nil {
		logger.Error("application error", "error", err)
		os.Exit(1)
	}
}
