package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	hamcp "hass-agent-bot/internal/ha/mcp"
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

	cli, err := hamcp.New(context.Background(), c.HA.URL+"/api/mcp", hamcp.Options{Token: c.HA.Token})
	if err != nil {
		fmt.Println("mcp:", err)
		os.Exit(1)
	}
	defer cli.Close()

	tools, err := cli.ListTools(context.Background())
	if err != nil {
		fmt.Println("list:", err)
		os.Exit(1)
	}

	fmt.Println("TOOLS COUNT:", len(tools))
	for _, t := range tools {
		b, _ := json.Marshal(t)
		fmt.Printf("--- %s ---\n%s\n\n", t.Name, string(b))
	}
}