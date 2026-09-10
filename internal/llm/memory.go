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
	"sync"
	"time"
)

// Memory is a single durable fact/preference remembered by the agent.
type Memory struct {
	ID        string    `json:"id"`
	ChatID    int64     `json:"chat_id"`
	Category  string    `json:"category"` // user | preference | fact | habit | other
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MemoryStore is the persistence layer for agent memories.
// Implementations: FileMemoryStore (JSON, one file per chat), future SQLite, Postgres, etc.
type MemoryStore interface {
	// Load reads all memories from the store.
	// Returns map[chatID][]Memory — empty map, not nil, on error or empty store.
	Load(ctx context.Context) (map[int64][]Memory, error)

	// Add inserts or replaces a memory with the same ChatID+Text (dedup).
	// The ID is assigned on first insert and written back to m.
	Add(ctx context.Context, m *Memory) error

	// Delete removes one memory of a chat by ID.
	Delete(ctx context.Context, chatID int64, id string) error

	// Close releases any resources held by the store.
	Close() error
}

// FileMemoryStore persists memories as JSON files, one file per chat:
// <dir>/mem_<chatID>.json
type FileMemoryStore struct {
	dir string
	log *slog.Logger

	mu sync.Mutex
}

// NewFileMemoryStore creates a FileMemoryStore, ensuring the directory exists.
func NewFileMemoryStore(dir string, log *slog.Logger) (*FileMemoryStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("memory: create dir %s: %w", dir, err)
	}
	if log == nil {
		log = slog.Default()
	}
	return &FileMemoryStore{dir: dir, log: log}, nil
}

// pathFor returns the file path for a chat.
func (s *FileMemoryStore) pathFor(chatID int64) string {
	return filepath.Join(s.dir, "mem_"+strconv.FormatInt(chatID, 10)+".json")
}

// loadChat reads one chat's memories; empty slice when the file is missing.
func (s *FileMemoryStore) loadChat(chatID int64) ([]Memory, error) {
	data, err := os.ReadFile(s.pathFor(chatID))
	if err != nil {
		if os.IsNotExist(err) {
			return []Memory{}, nil
		}
		return nil, fmt.Errorf("memory: read chat %d: %w", chatID, err)
	}
	var ms []Memory
	if err := json.Unmarshal(data, &ms); err != nil {
		return nil, fmt.Errorf("memory: parse chat %d: %w", chatID, err)
	}
	return ms, nil
}

// saveChat writes one chat's memories atomically.
func (s *FileMemoryStore) saveChat(chatID int64, ms []Memory) error {
	data, err := json.MarshalIndent(ms, "", "  ")
	if err != nil {
		return fmt.Errorf("memory: marshal chat %d: %w", chatID, err)
	}
	path := s.pathFor(chatID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("memory: write chat %d: %w", chatID, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("memory: rename chat %d: %w", chatID, err)
	}
	return nil
}

func (s *FileMemoryStore) Load(ctx context.Context) (map[int64][]Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return map[int64][]Memory{}, fmt.Errorf("memory: read dir %s: %w", s.dir, err)
	}

	mems := make(map[int64][]Memory)
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "mem_") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		idStr := strings.TrimSuffix(strings.TrimPrefix(e.Name(), "mem_"), ".json")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			s.log.Warn("memory: skip file with bad chat id", "file", e.Name())
			continue
		}
		ms, err := s.loadChat(id)
		if err != nil {
			s.log.Warn("memory: read file", "file", e.Name(), "error", err)
			continue
		}
		mems[id] = ms
	}
	return mems, nil
}

func (s *FileMemoryStore) Add(ctx context.Context, m *Memory) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ms, err := s.loadChat(m.ChatID)
	if err != nil {
		return err
	}

	key := strings.ToLower(strings.TrimSpace(m.Text))
	for i := range ms {
		if strings.ToLower(strings.TrimSpace(ms[i].Text)) == key {
			// replace existing memory (dedup), keep original ID
			m.ID = ms[i].ID
			m.CreatedAt = ms[i].CreatedAt
			ms[i] = *m
			return s.saveChat(m.ChatID, ms)
		}
	}

	m.ID = "mem_" + strconv.Itoa(len(ms)+1)
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	m.UpdatedAt = time.Now()
	ms = append(ms, *m)
	return s.saveChat(m.ChatID, ms)
}

func (s *FileMemoryStore) Delete(ctx context.Context, chatID int64, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ms, err := s.loadChat(chatID)
	if err != nil {
		return err
	}
	out := ms[:0]
	removed := false
	for _, m := range ms {
		if m.ID == id {
			removed = true
			continue
		}
		out = append(out, m)
	}
	if !removed {
		return nil
	}
	if len(out) == 0 {
		// remove the file entirely instead of leaving "[]"
		if err := os.Remove(s.pathFor(chatID)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("memory: delete chat %d file: %w", chatID, err)
		}
		return nil
	}
	return s.saveChat(chatID, out)
}

func (s *FileMemoryStore) Close() error { return nil }