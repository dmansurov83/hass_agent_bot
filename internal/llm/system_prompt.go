package llm

import (
	"fmt"
	"os"
	"strings"
)

// LoadSystemPrompt reads the system prompt text from the first candidate path
// that exists and is non-empty. Pass the primary location (e.g.
// <DataDir>/system_prompt.txt) first, then fallbacks (e.g. a copy shipped next
// to the binary / in the container root). The prompt is required: when no
// candidate is readable the call fails loudly instead of silently running with
// a wrong prompt. Trailing whitespace is trimmed.
func LoadSystemPrompt(paths ...string) (string, error) {
	_, text, err := ResolveSystemPrompt(paths...)
	return text, err
}

// ResolveSystemPrompt is like LoadSystemPrompt but also reports which path was
// actually used.
func ResolveSystemPrompt(paths ...string) (string, string, error) {
	if len(paths) == 0 {
		return "", "", fmt.Errorf("system prompt: no candidate paths")
	}
	var tried []string
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				tried = append(tried, path+" (missing)")
				continue
			}
			return "", "", fmt.Errorf("system prompt: read %s: %w", path, err)
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			tried = append(tried, path+" (empty)")
			continue
		}
		return path, text, nil
	}
	return "", "", fmt.Errorf("system prompt: no readable prompt file (tried: %s)", strings.Join(tried, ", "))
}