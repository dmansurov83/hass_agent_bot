package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	hare "hass-agent-bot/internal/ha/rest"
)

type cfg struct {
	HA struct {
		URL   string `yaml:"url"`
		Token string `yaml:"token"`
	} `yaml:"ha"`
}

func main() {
	data, _ := os.ReadFile("config.yaml")
	var c cfg
	if err := yaml.Unmarshal(data, &c); err != nil {
		fmt.Println("cfg:", err)
		os.Exit(1)
	}

	cli := hare.New(c.HA.URL, hare.Options{Token: c.HA.Token})

	// Try exact entity_id names from the live context
	tests := []map[string]any{
		{"entity_id": []string{"switch_hall_main"}},
		{"entity_id": []string{"light.living_room"}},
		{"entity_id": []string{"switch"}},
	}

	for _, args := range tests {
		err := cli.CallService(context.Background(), "light", "turn_on", args)
		j, _ := json.Marshal(args)
		fmt.Printf("args=%s err=%v\n", string(j), err)
	}
}