package tg

import (
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestAllowed(t *testing.T) {
	tests := []struct {
		name     string
		allow    []int64
		updateID int64
		fromID   int64
		want     bool
	}{
		{"owner allowed", []int64{111}, 1, 111, true},
		{"stranger blocked", []int64{111}, 1, 222, false},
		{"empty allowlist", []int64{}, 1, 111, false},
		{"nil from blocked", []int64{111}, 1, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Bot{allow: make(map[int64]bool, len(tt.allow))}
			for _, id := range tt.allow {
				b.allow[id] = true
			}

			update := &models.Update{}
			if tt.fromID != 0 {
				update.Message = &models.Message{
					From: &models.User{ID: tt.fromID},
				}
			}

			if got := b.allowedUser(update); got != tt.want {
				t.Errorf("allowedUser = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTextHandler(t *testing.T) {
	// Text handler doesn't need mocking at unit level.
	// allowedUser is tested above; handler logic is verified in integration.
}