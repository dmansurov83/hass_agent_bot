package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HA       HAConfig
	TG       TGConfig
	GigaChat GigaChatConfig
	Notify   NotifyConfig
}

type HAConfig struct {
	URL   string
	Token string
}

type TGConfig struct {
	Token        string
	AllowUserIDs []int64
}

type GigaChatConfig struct {
	Credentials string
	Model       string
	Timeout     time.Duration
}

type NotifyConfig struct {
	DebounceSeconds int
	Entities        []string
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		HA: HAConfig{
			URL: "http://localhost:8123",
		},
		TG: TGConfig{},
		GigaChat: GigaChatConfig{
			Model:   "GigaChat-2-Pro",
			Timeout: 30 * time.Second,
		},
		Notify: NotifyConfig{
			DebounceSeconds: 30,
		},
	}

	if path != "" {
		if err := loadYAML(path, cfg); err != nil {
			return nil, err
		}
	}

	applyEnv(cfg)
	validate(cfg)
	return cfg, nil
}

func validate(cfg *Config) error {
	if cfg.HA.URL == "" {
		return fmt.Errorf("ha.url is required")
	}
	if cfg.TG.Token == "" {
		return fmt.Errorf("tg.token is required (set TG_TOKEN env or config.yaml)")
	}
	if len(cfg.TG.AllowUserIDs) == 0 {
		return fmt.Errorf("tg.allow_user_ids is required (at least one user)")
	}
	return nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("HA_URL"); v != "" {
		cfg.HA.URL = v
	}
	if v := os.Getenv("HA_TOKEN"); v != "" {
		cfg.HA.Token = v
	}
	if v := os.Getenv("TG_TOKEN"); v != "" {
		cfg.TG.Token = v
	}
	if v := os.Getenv("GIGACHAT_CREDENTIALS"); v != "" {
		cfg.GigaChat.Credentials = v
	}
	if v := os.Getenv("GIGACHAT_MODEL"); v != "" {
		cfg.GigaChat.Model = v
	}
	if v := os.Getenv("ALLOW_USERS"); v != "" {
		cfg.TG.AllowUserIDs = parseIDs(v)
	}
}

func parseIDs(s string) []int64 {
	parts := strings.Split(s, ",")
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			slog.Warn("config: skipping invalid user id", "value", p)
			continue
		}
		ids = append(ids, id)
	}
	return ids
}