package main

import (
	"context"
	"fmt"
	"os"
	"strings"

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

	// 1. Dump all states to see device names
	states, err := cli.States(context.Background())
	if err != nil {
		fmt.Println("states error:", err)
		os.Exit(1)
	}
	fmt.Println("===== ALL ENTITIES =====")
	for _, s := range states {
		fmt.Printf("%s | %s | %s\n", s.EntityID, s.State, friendly(s))
	}
	fmt.Println("========================")

	// 2. Try turning on first light/switch/fan
	var target string
	for _, s := range states {
		if strings.HasPrefix(s.EntityID, "light.") || strings.HasPrefix(s.EntityID, "switch.") {
			target = s.EntityID
			break
		}
	}
	if target == "" {
		fmt.Println("No light/switch entities found")
		return
	}

	fmt.Printf("\nTrying turn_on %s\n", target)
	if err := cli.CallService(context.Background(), strings.SplitN(target, ".", 2)[0], "turn_on", map[string]any{"entity_id": []string{target}}); err != nil {
		fmt.Println("turn_on error:", err)
	} else {
		fmt.Println("turn_on OK")
	}
}

func friendly(s hare.State) string {
	if f, ok := s.Attributes["friendly_name"].(string); ok {
		return f
	}
	return ""
}