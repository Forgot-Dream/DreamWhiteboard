package realtime

const MessagePermission = "permission"

// SynchronizeAuthorization serializes repository permission snapshots for one
// connection. A message-path refresh therefore waits for any monitor refresh
// already in flight, then obtains its own newer snapshot before proceeding.
func (c *Client) SynchronizeAuthorization(refresh func()) {
	c.authorizationMu.Lock()
	defer c.authorizationMu.Unlock()
	refresh()
}

// SetPermissions atomically refreshes a connection's edit and management
// capabilities and notifies the browser when either value changes. Permission
// delivery is ordered before any checkpoint request enabled by a promotion.
func (h *Hub) SetPermissions(client *Client, canEdit, canManage bool) bool {
	r := h.getRoom(client.BoardID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.clients[client]; !ok {
		return false
	}
	changed := client.CanEdit != canEdit || client.CanCheckpoint != canManage
	client.CanEdit = canEdit
	client.CanCheckpoint = canManage
	if changed {
		editable := canEdit
		manageable := canManage
		select {
		case client.Send <- Encode(Message{
			Type:      MessagePermission,
			BoardID:   client.BoardID,
			CanEdit:   &editable,
			CanManage: &manageable,
		}):
		default:
			h.removeLocked(r, client)
			h.announceRemovedLocked(r, []*Client{client})
			h.maintainCheckpointLocked(client.BoardID, r, client.ID)
			return false
		}
	}
	exclude := ""
	if !canEdit || !canManage {
		exclude = client.ID
	}
	h.maintainCheckpointLocked(client.BoardID, r, exclude)
	return true
}
