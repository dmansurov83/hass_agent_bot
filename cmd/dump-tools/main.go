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

	states, err := cli.States(context.Background())
	if err != nil {
		fmt.Println("states:", err)
		os.Exit(1)
	}

	fmt.Println("TOTAL ENTITIES:", len(states))
	// Print first 20 to inspect structure
	n := len(states)
	if n > 20 {
		n = 20
	}
	for _, s := range states[:n] {
		b, _ := json.MarshalIndent(s, "", "  ")
		fmt.Printf("%s\n", string(b))
	}
}