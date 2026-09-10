package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
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
	_ = yaml.Unmarshal(data, &c)

	cli, err := hamcp.New(context.Background(), c.HA.URL+"/api/mcp", hamcp.Options{Token: c.HA.Token})
	if err != nil {
		fmt.Println("mcp:", err)
		os.Exit(1)
	}
	defer cli.Close()

	// Try exact entity_id names from the live context
	tests := []map[string]any{
		{"name": "switch_hall_main"},
		{"name": "switch_hall_main", "domain": "light"},
		{"name": "my_kitchen_light"},
		{"name": "Light", "area": "Туалет"},
		{"name": "Table-Led table-led-light"},
	}

	for _, args := range tests {
		res, err := cli.Raw().CallTool(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "HassTurnOn", Arguments: args},
		})
		out := ""
		if res != nil {
			for _, content := range res.Content {
				if tc, ok := content.(mcp.TextContent); ok {
					out += tc.Text
				}
			}
		}
		j, _ := json.Marshal(args)
		fmt.Printf("args=%s err=%v isError=%v out=%s\n", string(j), err, res != nil && res.IsError, out)
	}
}