package notify

import (
	"testing"
	"time"
)

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern  string
		entityID string
		match    bool
	}{
		{"*", "any.entity", true},
		{"binary_sensor.*", "binary_sensor.door", true},
		{"binary_sensor.*", "sensor.temp", false},
		{"sensor.outdoor_temp", "sensor.outdoor_temp", true},
		{"sensor.outdoor_temp", "sensor.indoor_temp", false},
		{"light.*", "light.living_room", true},
	}
	for _, tt := range tests {
		got := matchPattern(tt.pattern, tt.entityID)
		if got != tt.match {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.pattern, tt.entityID, got, tt.match)
		}
	}
}

func TestMatchesFilter(t *testing.T) {
	e := New("", "", Config{Entities: []string{"binary_sensor.*", "sensor.temp"}}, nil)

	if !e.matchesFilter("binary_sensor.door") {
		t.Error("binary_sensor.door should match")
	}
	if !e.matchesFilter("sensor.temp") {
		t.Error("sensor.temp should match")
	}
	if e.matchesFilter("light.kitchen") {
		t.Error("light.kitchen should not match")
	}
}

func TestDebounce(t *testing.T) {
	e := New("", "", Config{DebounceSeconds: 60}, nil)

	if e.isDebounced("sensor.test") {
		t.Error("should not be debounced initially")
	}

	e.markDebounced("sensor.test")
	if !e.isDebounced("sensor.test") {
		t.Error("should be debounced after mark")
	}

	// Not debounced for a different entity
	if e.isDebounced("sensor.other") {
		t.Error("other entity should not be debounced")
	}
}

// Test helpers
func (e *Engine) isDebounced(entityID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	last, exists := e.debounce[entityID]
	if !exists {
		return false
	}
	debounceSec := time.Duration(e.config.DebounceSeconds) * time.Second
	return time.Now().Sub(last) < debounceSec
}

func (e *Engine) markDebounced(entityID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.debounce[entityID] = time.Now()
}