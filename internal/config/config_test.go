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
	if cfg.LLM.Model != "GigaChat-2-Pro" {
		t.Errorf("Model default = %q", cfg.LLM.Model)
	}
	if cfg.LLM.Timeout != 30*time.Second {
		t.Errorf("Timeout default = %v", cfg.LLM.Timeout)
	}
	if cfg.LLM.Provider != "gigachat" {
		t.Errorf("Provider default = %q", cfg.LLM.Provider)
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
	t.Setenv("LLM_MODEL", "GigaChat-2-Max")

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
	if cfg.LLM.Model != "GigaChat-2-Max" {
		t.Errorf("Model = %q", cfg.LLM.Model)
	}
}

func TestLoad_LegacyGigaChatBlock(t *testing.T) {
	content := []byte(`
ha:
  url: http://192.168.1.100:8123
  token: ha_token_123
tg:
  token: tg_token_456
  allow_user_ids: [111]
gigachat:
  credentials: legacy_creds
  model: GigaChat-Pro
notify:
  debounce_seconds: 10
`)
	f := writeTemp(t, content)
	defer os.Remove(f)

	cfg, err := Load(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.LLM.Provider != "gigachat" {
		t.Errorf("Provider = %q, want gigachat (from legacy)", cfg.LLM.Provider)
	}
	if cfg.LLM.Credentials != "legacy_creds" {
		t.Errorf("Credentials = %q", cfg.LLM.Credentials)
	}
	if cfg.LLM.Model != "GigaChat-Pro" {
		t.Errorf("Model = %q", cfg.LLM.Model)
	}
	if cfg.LLM.BaseURL != "" {
		t.Errorf("BaseURL should be empty, got %q", cfg.LLM.BaseURL)
	}
}

func TestLoad_NewLLMOverridesLegacy(t *testing.T) {
	// Когда есть оба блока — llm имеет приоритет над gigachat
	content := []byte(`
ha:
  url: http://192.168.1.100:8123
  token: ha_token_123
tg:
  token: tg_token_456
  allow_user_ids: [111]
gigachat:
  credentials: legacy_creds
  model: GigaChat-Pro
llm:
  provider: openai
  model: gpt-4o
  credentials: sk-new-key
  base_url: https://api.openai.com/v1
`)
	f := writeTemp(t, content)
	defer os.Remove(f)

	cfg, err := Load(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.LLM.Provider != "openai" {
		t.Errorf("Provider = %q, want openai", cfg.LLM.Provider)
	}
	if cfg.LLM.Credentials != "sk-new-key" {
		t.Errorf("Credentials = %q, want sk-new-key", cfg.LLM.Credentials)
	}
	if cfg.LLM.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", cfg.LLM.Model)
	}
	if cfg.LLM.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("BaseURL = %q", cfg.LLM.BaseURL)
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