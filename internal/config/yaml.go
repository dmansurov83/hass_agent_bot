package config

import (
	"log/slog"
	"os"

	"gopkg.in/yaml.v3"
)

type yamlConfig struct {
	Version  string `yaml:"version"`
	HA struct {
		URL   string `yaml:"url"`
		Token string `yaml:"token"`
	} `yaml:"ha"`
	TG struct {
		Token        string  `yaml:"token"`
		AllowUserIDs []int64 `yaml:"allow_user_ids"`
	} `yaml:"tg"`
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
	if y.GigaChat.Credentials != "" {
		cfg.GigaChat.Credentials = y.GigaChat.Credentials
	}
	if y.GigaChat.Model != "" {
		cfg.GigaChat.Model = y.GigaChat.Model
	}
	if y.Notify.DebounceSeconds > 0 {
		cfg.Notify.DebounceSeconds = y.Notify.DebounceSeconds
	}
	if len(y.Notify.Entities) > 0 {
		cfg.Notify.Entities = y.Notify.Entities
	}

	return nil
}