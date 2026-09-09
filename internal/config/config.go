package config

import "log/slog"

type Config struct {
	HAURL  string
	HAToken string
	TGToken string
	GPToken string
	AllowUserIDs []int64
}

func Load(path string) (*Config, error) {
	slog.Debug("config: loading", "path", path)
	return &Config{}, nil
}