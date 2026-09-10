package llm

import "context"

// HistoryStore is the persistence layer for per-chat dialog history.
// Implementations: FileStore (JSON), future SQLite, Postgres, etc.
type HistoryStore interface {
	// Load reads all chat histories from the store.
	// Returns map[chatID][]Message — empty map, not nil, on error or empty store.
	Load(ctx context.Context) (map[int64][]Message, error)

	// Save persists one chat's history (overwrites).
	Save(ctx context.Context, chatID int64, msgs []Message) error

	// Delete removes one chat's history from the store.
	Delete(ctx context.Context, chatID int64) error

	// Close releases any resources held by the store.
	Close() error
}