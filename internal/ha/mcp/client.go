package mcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

type Client struct {
	mcp   *client.Client
	token string
	log   *slog.Logger
}

type Options struct {
	Token string
	Logger *slog.Logger
}

func New(ctx context.Context, baseURL string, opts Options) (*Client, error) {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}

	if baseURL == "" {
		return nil, fmt.Errorf("mcp: base URL is required")
	}

	mcpClient, err := client.NewStreamableHttpClient(
		baseURL,
		transport.WithHTTPHeaders(map[string]string{
			"Authorization": "Bearer " + opts.Token,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("mcp: create client: %w", err)
	}

	c := &Client{mcp: mcpClient, token: opts.Token, log: log}

	if err := mcpClient.Start(ctx); err != nil {
		return nil, fmt.Errorf("mcp: start client: %w", err)
	}

	if _, err := mcpClient.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		return nil, fmt.Errorf("mcp: initialize: %w", err)
	}

	log.Info("mcp: connected to HA", "url", baseURL)
	return c, nil
}

func (c *Client) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	result, err := c.mcp.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("mcp: list tools: %w", err)
	}
	return result.Tools, nil
}

func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	}

	result, err := c.mcp.CallTool(ctx, req)
	if err != nil {
		return "", fmt.Errorf("mcp: call tool %s: %w", name, err)
	}

	var out string
	for _, content := range result.Content {
		if tc, ok := content.(mcp.TextContent); ok {
			out += tc.Text
		}
	}

	if result.IsError {
		return out, fmt.Errorf("mcp: tool %s returned error: %s", name, out)
	}

	return out, nil
}

func (c *Client) Close() error {
	if c.mcp == nil {
		return nil
	}
	return c.mcp.Close()
}

// Raw returns the underlying MCP client for advanced use.
func (c *Client) Raw() *client.Client {
	return c.mcp
}

// Token returns the HA long-lived access token used by this client.
func (c *Client) Token() string {
	return c.token
}