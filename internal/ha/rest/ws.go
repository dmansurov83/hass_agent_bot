package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// WSRegistries fetches the HA area/entity/device registries over the WebSocket
// API (these are not exposed via REST on modern HA versions). A single
// connection is used and closed afterwards.
func WSRegistries(ctx context.Context, baseURL, token string) (areas []AreaInfo, entities []EntityEntry, devices []DeviceEntry, err error) {
	u := baseURL + "/api/websocket"
	ws, _, err := websocket.Dial(ctx, u, nil)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("ws dial: %w", err)
	}
	defer ws.CloseNow()
	// HA registry lists (especially entity_registry) can exceed the default
	// 32 KiB read limit of coder/websocket, which aborts with
	// "message too big". Raise it to 16 MiB.
	ws.SetReadLimit(16 << 20)

	// auth handshake
	var authMsg struct {
		Type string `json:"type"`
	}
	if err := wsjson.Read(ctx, ws, &authMsg); err != nil {
		return nil, nil, nil, fmt.Errorf("ws read auth: %w", err)
	}
	if authMsg.Type != "auth_required" {
		return nil, nil, nil, fmt.Errorf("ws unexpected auth type: %s", authMsg.Type)
	}
	if err := wsjson.Write(ctx, ws, struct {
		Type        string `json:"type"`
		AccessToken string `json:"access_token"`
	}{Type: "auth", AccessToken: token}); err != nil {
		return nil, nil, nil, fmt.Errorf("ws send auth: %w", err)
	}

	var authResult struct {
		Type    string `json:"type"`
		Message string `json:"message,omitempty"`
	}
	if err := wsjson.Read(ctx, ws, &authResult); err != nil {
		return nil, nil, nil, fmt.Errorf("ws read auth result: %w", err)
	}
	if authResult.Type != "auth_ok" {
		return nil, nil, nil, fmt.Errorf("ws auth failed: %s (%s)", authResult.Type, authResult.Message)
	}

	// request sequence: area_registry, entity_registry, device_registry
	requests := []struct {
		id   int
		typ  string
		dest any
	}{
		{1, "config/area_registry/list", &areas},
		{2, "config/entity_registry/list", &entities},
		{3, "config/device_registry/list", &devices},
	}

	// deadline guard for the whole exchange
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	pending := map[int]*any{}
	for i := range requests {
		pending[requests[i].id] = &requests[i].dest
		if err := wsjson.Write(ctx, ws, struct {
			ID   int    `json:"id"`
			Type string `json:"type"`
		}{ID: requests[i].id, Type: requests[i].typ}); err != nil {
			return nil, nil, nil, fmt.Errorf("ws send %s: %w", requests[i].typ, err)
		}
	}

	for len(pending) > 0 {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, ws, &raw); err != nil {
			return nil, nil, nil, fmt.Errorf("ws read result: %w", err)
		}
		var header struct {
			Type string          `json:"type"`
			ID   int             `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			continue
		}
		if header.Type != "result" {
			continue
		}
		dest, ok := pending[header.ID]
		if !ok {
			continue
		}
		if err := json.Unmarshal(header.Result, *dest); err != nil {
			return nil, nil, nil, fmt.Errorf("ws parse result %d: %w", header.ID, err)
		}
		delete(pending, header.ID)
	}

	return areas, entities, devices, nil
}

// AreaInfo is one entry of the area registry.
type AreaInfo struct {
	AreaID  string   `json:"area_id"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
}

// EntityEntry is one entry of the entity registry.
type EntityEntry struct {
	EntityID string   `json:"entity_id"`
	DeviceID string   `json:"device_id,omitempty"`
	AreaID   string   `json:"area_id,omitempty"`
	Name     string   `json:"name,omitempty"`
	Aliases  []string `json:"aliases"`
}

// DeviceEntry is one entry of the device registry.
type DeviceEntry struct {
	DeviceID string `json:"id"`
	AreaID   string `json:"area_id,omitempty"`
}