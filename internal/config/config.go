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
	Version string // версия приложения (для docker-тегов и логов)
	HA      HAConfig
	TG      TGConfig
	LLM     LLMConfig
	Notify  NotifyConfig
	DataDir string // директория для персистентности (таймеры и т.д.)
}

type HAConfig struct {
	URL   string
	Token string
}

type TGConfig struct {
	Token        string
	AllowUserIDs []int64
}

type LLMConfig struct {
	Provider    string // gigachat | openai
	Model       string
	Credentials string // Basic auth для GigaChat, API key для OpenAI
	BaseURL     string // для OpenAI-совместимых (по умолчанию api.openai.com)
	Timeout     time.Duration
}

type NotifyConfig struct {
	DebounceSeconds int
	Entities        []string
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		Version: "0.5.0",
		HA: HAConfig{
			URL: "http://localhost:8123",
		},
		TG: TGConfig{},
		LLM: LLMConfig{
			Provider: "gigachat",
			Model:    "GigaChat-2-Pro",
			Timeout:  30 * time.Second,
		},
		Notify: NotifyConfig{
			DebounceSeconds: 30,
		},
		DataDir: "data", // по умолчанию подпапка data рядом с бинарником
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
	if v := os.Getenv("APP_VERSION"); v != "" {
		cfg.Version = v
	}
	if v := os.Getenv("HA_URL"); v != "" {
		cfg.HA.URL = v
	}
	if v := os.Getenv("HA_TOKEN"); v != "" {
		cfg.HA.Token = v
	}
	if v := os.Getenv("TG_TOKEN"); v != "" {
		cfg.TG.Token = v
	}
	if v := os.Getenv("LLM_PROVIDER"); v != "" {
		cfg.LLM.Provider = v
	}
	if v := os.Getenv("LLM_CREDENTIALS"); v != "" {
		cfg.LLM.Credentials = v
	}
	if v := os.Getenv("LLM_MODEL"); v != "" {
		cfg.LLM.Model = v
	}
	if v := os.Getenv("LLM_BASE_URL"); v != "" {
		cfg.LLM.BaseURL = v
	}
	if v := os.Getenv("ALLOW_USERS"); v != "" {
		cfg.TG.AllowUserIDs = parseIDs(v)
	}
	if v := os.Getenv("DATA_DIR"); v != "" {
		cfg.DataDir = v
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
