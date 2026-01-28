package api

import (
	"encoding/json"
	"net/http"

	"github.com/corazawaf/coraza/v3/internal/app/store"
	"github.com/gorilla/websocket"
)

type API struct {
	Store *store.Store
	// Upgrader for websockets
	Upgrader websocket.Upgrader
}

func NewAPI(s *store.Store) *API {
	return &API{
		Store: s,
		Upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all for dev
			},
		},
	}
}

func (a *API) HandleLogin(w http.ResponseWriter, r *http.Request) {
	// Mock login
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"token": "obsidian-secure-token", "user": {"name": "Admin", "role": "Sentinel"}}`))
}

func (a *API) HandleStats(w http.ResponseWriter, r *http.Request) {
	stats := a.Store.GetStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (a *API) HandleLogs(w http.ResponseWriter, r *http.Request) {
	logs := a.Store.GetLogs()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

func (a *API) HandleRules(w http.ResponseWriter, r *http.Request) {
	rules := a.Store.GetRules()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rules)
}

func (a *API) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := a.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Simple loop to keep connection alive and send periodic updates or just event driven.
	// For now, let's just push stats every 5 seconds.
	for {
		stats := a.Store.GetStats()
		msg := map[string]interface{}{
			"type": "stats_update",
			"data": stats,
		}
		if err := conn.WriteJSON(msg); err != nil {
			break
		}
		// Also invoke logs?
		// In a real system we'd use a channel to broadcast events.
		// We'll sleep to avoid CPU spin
		// This is a naive implementation; in main.go we will link the logger to the hub.
		// So this specific handler might primarily be for receiving or just initial state?
		// Let's rely on the structure in main.go for broadcasting.
		// Actually, let's just return here and let the BroadcastHub in main.go handle the connection if we were passing it around.
		// But since we are decoupling, let's just keep this simple: read junk, ignore.
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}
