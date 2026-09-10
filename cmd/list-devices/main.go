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
	_ = yaml.Unmarshal(data, &c)

	cli, err := hamcp.New(context.Background(), c.HA.URL+"/api/mcp", hamcp.Options{Token: c.HA.Token})
	if err != nil {
		fmt.Println("mcp:", err)
		os.Exit(1)
	}
	defer cli.Close()

	res, err := cli.Raw().CallTool(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "GetLiveContext", Arguments: map[string]any{}},
	})
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	ctxText := ""
	for _, content := range res.Content {
		if tc, ok := content.(mcp.TextContent); ok {
			ctxText += tc.Text
		}
	}

	// Вывести все сущности light/switch с районами
	lines := strings.Split(ctxText, "\n")
	for i, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "domain: light") || strings.Contains(lower, "domain: switch") || strings.Contains(lower, "domain: fan") {
			// Найти name в этой строке или в предыдущих
			name := ""
			for back := i; back >= 0 && back > i-3; back-- {
				trimmed := strings.TrimSpace(lines[back])
				if strings.HasPrefix(trimmed, "names:") {
					name = strings.TrimSpace(trimmed[6:])
					break
				}
			}
			// area в текущей или следующих строках
			area := ""
			for fwd := i; fwd < len(lines) && fwd < i+4; fwd++ {
				trimmed := strings.TrimSpace(lines[fwd])
				if strings.HasPrefix(trimmed, "areas:") {
					area = strings.TrimSpace(trimmed[6:])
					break
				}
			}
			state := ""
			if strings.Contains(lower, "state:") {
				idx := strings.Index(lower, "state:")
				state = strings.TrimSpace(line[idx+6:])
			}
			fmt.Printf("%-40s | %-20s | %-12s | %s\n", name, strings.TrimSpace(lower[strings.Index(lower, "domain:")+7:]), area, state)
			_ = json.Marshal
		}
	}
}