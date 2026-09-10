package main

import (
	"context"
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

	states, err := cli.States(context.Background())
	if err != nil {
		fmt.Println("states:", err)
		os.Exit(1)
	}

	// Dump everything: entity_id, friendly_name, state, unit, device_class
	for _, s := range states {
		fn := ""
		if f, ok := s.Attributes["friendly_name"].(string); ok {
			fn = f
		}
		unit := ""
		if u, ok := s.Attributes["unit_of_measurement"].(string); ok {
			unit = u
		}
		dc := ""
		if d, ok := s.Attributes["device_class"].(string); ok {
			dc = d
		}
		fmt.Printf("%s | %s | %s | %s | %s\n", s.EntityID, fn, s.State, unit, dc)
	}
}