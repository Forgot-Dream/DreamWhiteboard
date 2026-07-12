package realtime

const CloseReasonBoardDeleted = "board_deleted"
const CloseReasonForbidden = "forbidden"

// Disconnect evicts one client with a machine-readable websocket close
// reason, announces its awareness removal, and re-elects checkpoint writers.
func (h *Hub) Disconnect(client *Client, reason string) bool {
	r := h.getRoom(client.BoardID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.clients[client]; !ok {
		return false
	}
	delete(r.clients, client)
	client.closeWithReason(reason)
	h.announceRemovedLocked(r, []*Client{client})
	h.maintainCheckpointLocked(client.BoardID, r, client.ID)
	return true
}

// CloseBoard evicts every connection for a board and permanently closes its
// room. It is used after board/project deletion so stale websocket handlers
// cannot remain in a ghost room or publish more collaboration traffic.
func (h *Hub) CloseBoard(boardID string) int {
	h.mu.Lock()
	r := h.rooms[boardID]
	if r == nil {
		h.rooms[boardID] = &room{
			clients:   make(map[*Client]struct{}),
			updates:   make(map[int64]queuedUpdate),
			checkedAt: h.now(),
			closed:    true,
		}
	}
	h.mu.Unlock()
	if r == nil {
		return 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0
	}
	r.closed = true
	h.stopCheckpointTimerLocked(r)
	closed := len(r.clients)
	for client := range r.clients {
		delete(r.clients, client)
		client.closeWithReason(CloseReasonBoardDeleted)
	}
	r.pending = nil
	r.updates = make(map[int64]queuedUpdate)
	return closed
}
