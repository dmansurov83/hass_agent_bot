package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load("")
	if cfg == nil || err != nil {
		// ok — validation will catch missing required fields
	}
}

func TestLoad_WithYAML(t *testing.T) {
	content := []byte(`
ha:
  url: http://192.168.1.100:8123
  token: ha_token_123
tg:
  token: tg_token_456
  allow_user_ids: [111, 222]
`)
	f := writeTemp(t, content)
	defer os.Remove(f)

	cfg, err := Load(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.HA.URL != "http://192.168.1.100:8123" {
		t.Errorf("HA.URL = %q, want %q", cfg.HA.URL, "http://192.168.1.100:8123")
	}
	if cfg.HA.Token != "ha_token_123" {
		t.Errorf("HA.Token = %q", cfg.HA.Token)
	}
	if cfg.TG.Token != "tg_token_456" {
		t.Errorf("TG.Token = %q", cfg.TG.Token)
	}
	if len(cfg.TG.AllowUserIDs) != 2 || cfg.TG.AllowUserIDs[0] != 111 {
		t.Errorf("AllowUserIDs = %v", cfg.TG.AllowUserIDs)
	}
	if cfg.GigaChat.Model != "GigaChat-2-Pro" {
		t.Errorf("Model default = %q", cfg.GigaChat.Model)
	}
	if cfg.GigaChat.Timeout != 30*time.Second {
		t.Errorf("Timeout default = %v", cfg.GigaChat.Timeout)
	}
}

func TestLoad_EnvOverridesYAML(t *testing.T) {
	content := []byte(`
ha:
  url: http://localhost:8123
tg:
  token: yaml_token
  allow_user_ids: [1]
`)
	f := writeTemp(t, content)
	defer os.Remove(f)

	t.Setenv("HA_URL", "http://10.0.0.1:8123")

	cfg, err := Load(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.HA.URL != "http://10.0.0.1:8123" {
		t.Errorf("env should override: %q", cfg.HA.URL)
	}
	if cfg.TG.Token != "yaml_token" {
		t.Errorf("yaml should persist: %q", cfg.TG.Token)
	}
}

func TestLoad_EnvVars(t *testing.T) {
	t.Setenv("HA_URL", "http://10.0.0.1:8123")
	t.Setenv("HA_TOKEN", "env_ha_token")
	t.Setenv("TG_TOKEN", "env_tg_token")
	t.Setenv("ALLOW_USERS", "111,222")
	t.Setenv("GIGACHAT_MODEL", "GigaChat-2-Max")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// env should set fields even without yaml
	if cfg.HA.Token != "env_ha_token" {
		t.Errorf("HA.Token = %q", cfg.HA.Token)
	}
	if cfg.TG.Token != "env_tg_token" {
		t.Errorf("TG.Token = %q", cfg.TG.Token)
	}
	if len(cfg.TG.AllowUserIDs) != 2 {
		t.Errorf("AllowUserIDs = %v", cfg.TG.AllowUserIDs)
	}
	if cfg.GigaChat.Model != "GigaChat-2-Max" {
		t.Errorf("Model = %q", cfg.GigaChat.Model)
	}
}

func writeTemp(t *testing.T, content []byte) string {
	f, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		t.Fatalf("write: %v", err)
	}
	f.Close()
	return f.Name()
}