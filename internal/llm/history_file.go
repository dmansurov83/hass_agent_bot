package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FileStore persists chat histories as JSON files, one file per chat:
// <dir>/chat_<chatID>.json
type FileStore struct {
	dir string
	log *slog.Logger
}

// NewFileStore creates a FileStore, ensuring the directory exists.
func NewFileStore(dir string, log *slog.Logger) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("history: create dir %s: %w", dir, err)
	}
	if log == nil {
		log = slog.Default()
	}
	return &FileStore{dir: dir, log: log}, nil
}

// pathFor returns the file path for a chat.
func (s *FileStore) pathFor(chatID int64) string {
	return filepath.Join(s.dir, "chat_"+strconv.FormatInt(chatID, 10)+".json")
}

func (s *FileStore) Load(ctx context.Context) (map[int64][]Message, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return map[int64][]Message{}, fmt.Errorf("history: read dir %s: %w", s.dir, err)
	}

	histories := make(map[int64][]Message)
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "chat_") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		idStr := strings.TrimSuffix(strings.TrimPrefix(e.Name(), "chat_"), ".json")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			s.log.Warn("history: skip file with bad chat id", "file", e.Name())
			continue
		}

		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			s.log.Warn("history: read file", "file", e.Name(), "error", err)
			continue
		}
		var msgs []Message
		if err := json.Unmarshal(data, &msgs); err != nil {
			s.log.Warn("history: parse file, ignoring", "file", e.Name(), "error", err)
			continue
		}
		histories[id] = msgs
	}
	return histories, nil
}

func (s *FileStore) Save(ctx context.Context, chatID int64, msgs []Message) error {
	data, err := json.MarshalIndent(msgs, "", "  ")
	if err != nil {
		return fmt.Errorf("history: marshal chat %d: %w", chatID, err)
	}
	path := s.pathFor(chatID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("history: write chat %d: %w", chatID, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("history: rename chat %d: %w", chatID, err)
	}
	return nil
}

func (s *FileStore) Delete(ctx context.Context, chatID int64) error {
	path := s.pathFor(chatID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("history: delete chat %d: %w", chatID, err)
	}
	return nil
}

func (s *FileStore) Close() error { return nil }