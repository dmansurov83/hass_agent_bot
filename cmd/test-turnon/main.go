package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

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

	// 1. Dump full live context to see device names
	res, err := cli.Raw().CallTool(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "GetLiveContext", Arguments: map[string]any{}},
	})
	if err != nil {
		fmt.Println("GetLiveContext error:", err)
		os.Exit(1)
	}
	ctxText := ""
	for _, content := range res.Content {
		if tc, ok := content.(mcp.TextContent); ok {
			ctxText += tc.Text
		}
	}
	fmt.Println("===== FULL LIVE CONTEXT =====")
	fmt.Println(ctxText)
	fmt.Println("=============================")

	// 2. Pick first device with domain light or switch and try HassTurnOn
	toTry := []string{}
	lines := strings.Split(ctxText, "\n")
	for _, line := range lines {
		l := strings.ToLower(line)
		if strings.Contains(l, "domain: light") || strings.Contains(l, "domain: switch") || strings.Contains(l, "light") || strings.Contains(l, "switch") {
			// find "names: X" on same line or nearby
			if strings.Contains(line, "names:") {
				part := line[strings.Index(line, "names:")+6:]
				part = strings.TrimSpace(part)
				if idx := strings.Index(part, "\n"); idx >= 0 {
					part = part[:idx]
				}
				name := strings.TrimSpace(part)
				if name != "" && !strings.Contains(name, "unavailable") {
					toTry = append(toTry, name)
				}
			}
		}
	}

	// also dump raw names from lines
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "names:") {
			name := strings.TrimSpace(trimmed[6:])
			name = strings.TrimSpace(strings.Split(name, "\n")[0])
			if name != "" && !strings.Contains(strings.ToLower(name), "unavailable") {
				toTry = append(toTry, name)
			}
		}
	}

	// dedupe
	seen := map[string]bool{}
	var unique []string
	for _, n := range toTry {
		if !seen[n] {
			seen[n] = true
			unique = append(unique, n)
		}
	}

	if len(unique) == 0 {
		fmt.Println("No candidate devices found to turn on")
		return
	}

	fmt.Printf("\nTrying HassTurnOn on %d devices:\n", len(unique))
	for _, name := range unique {
		res, err := cli.Raw().CallTool(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "HassTurnOn",
				Arguments: map[string]any{"name": name},
			},
		})
		out := ""
		for _, content := range res.Content {
			if tc, ok := content.(mcp.TextContent); ok {
				out += tc.Text
			}
		}
		j, _ := json.MarshalIndent(res.StructuredContent, "", "  ")
		fmt.Printf("  [%s] err=%v isError=%v out=%s structured=%s\n", name, err, res.IsError, strings.TrimSpace(out), string(j))
	}
}