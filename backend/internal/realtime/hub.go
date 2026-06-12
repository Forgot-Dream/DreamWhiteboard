package realtime

import (
	"encoding/json"
	"sync"

	"dreamwhiteboard/backend/internal/domain"
)

type Message struct {
	Type      string                `json:"type"`
	BoardID   string                `json:"board_id,omitempty"`
	ClientID  string                `json:"client_id,omitempty"`
	UserID    string                `json:"user_id,omitempty"`
	Snapshot  *domain.BoardSnapshot `json:"snapshot,omitempty"`
	Operation *domain.Operation     `json:"operation,omitempty"`
	Payload   map[string]any        `json:"payload,omitempty"`
	Error     string                `json:"error,omitempty"`
}

type Client struct {
	ID      string
	UserID  string
	BoardID string
	Send    chan []byte
}

type Hub struct {
	mu      sync.RWMutex
	clients map[string]map[*Client]struct{}
}

func NewHub() *Hub {
	return &Hub{clients: map[string]map[*Client]struct{}{}}
}

func (h *Hub) Join(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[client.BoardID] == nil {
		h.clients[client.BoardID] = map[*Client]struct{}{}
	}
	h.clients[client.BoardID][client] = struct{}{}
}

func (h *Hub) Leave(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients[client.BoardID], client)
	if len(h.clients[client.BoardID]) == 0 {
		delete(h.clients, client.BoardID)
	}
	close(client.Send)
}

func (h *Hub) Broadcast(boardID string, msg Message, except *Client) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients[boardID] {
		if client == except {
			continue
		}
		select {
		case client.Send <- data:
		default:
		}
	}
}

func Encode(msg Message) []byte {
	data, _ := json.Marshal(msg)
	return data
}
