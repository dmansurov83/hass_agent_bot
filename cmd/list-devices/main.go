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

	states, err := cli.States(context.Background())
	if err != nil {
		fmt.Println("states:", err)
		os.Exit(1)
	}

	// Вывести все сущности light/switch/fan с районами (friendly_name + entity_id)
	fmt.Printf("%-45s | %-30s | %-12s | %s\n", "entity_id", "friendly_name", "state", "device_class")
	for _, s := range states {
		lower := strings.ToLower(s.EntityID)
		if strings.HasPrefix(lower, "light.") || strings.HasPrefix(lower, "switch.") || strings.HasPrefix(lower, "fan.") {
			fn := ""
			if f, ok := s.Attributes["friendly_name"].(string); ok {
				fn = f
			}
			dc := ""
			if d, ok := s.Attributes["device_class"].(string); ok {
				dc = d
			}
			fmt.Printf("%-45s | %-30s | %-12s | %s\n", s.EntityID, fn, s.State, dc)
		}
	}
}