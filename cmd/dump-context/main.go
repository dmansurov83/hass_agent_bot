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

	for _, name := range []string{"GetLiveContext", "HassTurnOn", "list_entities", "get_state"} {
		res, err := cli.Raw().CallTool(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: name, Arguments: map[string]any{}},
		})
		if err != nil {
			fmt.Printf("[%s] call error: %v\n\n", name, err)
			continue
		}
		fmt.Printf("[%s] IsError=%v Structured=%v len(content)=%d\n", name, res.IsError, res.StructuredContent != nil, len(res.Content))
		if res.StructuredContent != nil {
			b, _ := json.MarshalIndent(res.StructuredContent, "", "  ")
			fmt.Printf("%s\n", string(b))
		} else if len(res.Content) > 0 {
			switch c := res.Content[0].(type) {
			case mcp.TextContent:
				fmt.Printf("TEXT: %.400s\n", c.Text)
			default:
				b, _ := json.Marshal(res.Content[0])
				fmt.Printf("OTHER: %.400s\n", string(b))
			}
		}
		fmt.Println()
	}
}