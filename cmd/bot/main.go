package main

import (
	"flag"
	"log/slog"
	"os"

	"hass-agent-bot/internal/config"
	"hass-agent-bot/internal/core"
)

func main() {
	cfgPath := flag.String("config", "", "path to config.yaml")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	app := core.New(cfg, logger)

	if err := app.Run(); err != nil {
		slog.Error("application error", "error", err)
		os.Exit(1)
	}
}