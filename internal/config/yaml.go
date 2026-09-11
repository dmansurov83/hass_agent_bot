package config

import (
	"log/slog"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type yamlConfig struct {
	Version string `yaml:"version"`
	HA      struct {
		URL   string `yaml:"url"`
		Token string `yaml:"token"`
	} `yaml:"ha"`
	TG struct {
		Token        string  `yaml:"token"`
		AllowUserIDs []int64 `yaml:"allow_user_ids"`
	} `yaml:"tg"`
	LLM struct {
		Provider    string        `yaml:"provider"`
		Model       string        `yaml:"model"`
		Credentials string        `yaml:"credentials"`
		BaseURL     string        `yaml:"base_url"`
		Timeout     time.Duration `yaml:"timeout"`
	} `yaml:"llm"`
	// GigaChat — легаси-блок из старых конфигов. Используется как fallback,
	// если новый блок `llm` не задан.
	GigaChat struct {
		Credentials string `yaml:"credentials"`
		Model       string `yaml:"model"`
	} `yaml:"gigachat"`
	Notify struct {
		DebounceSeconds int      `yaml:"debounce_seconds"`
		Entities        []string `yaml:"entities"`
	} `yaml:"notify"`
}

func loadYAML(path string, cfg *Config) error {
	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Debug("config: file not found, skipping", "path", path)
			return nil
		}
		return err
	}
	if stat.IsDir() {
		slog.Debug("config: path is a directory, skipping", "path", path)
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var y yamlConfig
	if err := yaml.Unmarshal(data, &y); err != nil {
		return err
	}

	if y.Version != "" {
		cfg.Version = y.Version
	}
	if y.HA.URL != "" {
		cfg.HA.URL = y.HA.URL
	}
	if y.HA.Token != "" {
		cfg.HA.Token = y.HA.Token
	}
	if y.TG.Token != "" {
		cfg.TG.Token = y.TG.Token
	}
	if len(y.TG.AllowUserIDs) > 0 {
		cfg.TG.AllowUserIDs = y.TG.AllowUserIDs
	}
	if y.LLM.Provider != "" {
		cfg.LLM.Provider = y.LLM.Provider
	}
	if y.LLM.Model != "" {
		cfg.LLM.Model = y.LLM.Model
	}
	if y.LLM.Credentials != "" {
		cfg.LLM.Credentials = y.LLM.Credentials
	}
	if y.LLM.BaseURL != "" {
		cfg.LLM.BaseURL = y.LLM.BaseURL
	}
	if y.LLM.Timeout > 0 {
		cfg.LLM.Timeout = y.LLM.Timeout
	}

	// Легаси-совместимость: если блок llm не задан, читаем из старого gigachat:
	// credentials + model → в LLM.Credentials / LLM.Model, provider = "gigachat".
	if y.LLM.Provider == "" && y.LLM.Model == "" && y.LLM.Credentials == "" && y.LLM.BaseURL == "" {
		if y.GigaChat.Credentials != "" {
			cfg.LLM.Credentials = y.GigaChat.Credentials
		}
		if y.GigaChat.Model != "" {
			cfg.LLM.Model = y.GigaChat.Model
		}
		// Если был хоть какой-то легаси-блок, подразумеваем GigaChat
		if y.GigaChat.Credentials != "" || y.GigaChat.Model != "" {
			cfg.LLM.Provider = "gigachat"
		}
	}
	if y.Notify.DebounceSeconds > 0 {
		cfg.Notify.DebounceSeconds = y.Notify.DebounceSeconds
	}
	if len(y.Notify.Entities) > 0 {
		cfg.Notify.Entities = y.Notify.Entities
	}

	return nil
}
