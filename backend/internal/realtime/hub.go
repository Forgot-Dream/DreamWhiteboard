package realtime

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

const (
	MessageSyncStart         = "sync_start"
	MessageCheckpoint        = "checkpoint"
	MessageUpdate            = "update"
	MessageSyncComplete      = "sync_complete"
	MessageUpdateAck         = "update_ack"
	MessageAwareness         = "awareness"
	MessageCheckpointRequest = "checkpoint_request"
	MessageCheckpointAck     = "checkpoint_ack"
	MessageError             = "error"

	ProtocolVersion = 4

	defaultCheckpointEvery    = int64(100)
	defaultCheckpointInterval = 5 * time.Minute
	defaultCheckpointTimeout  = 30 * time.Second
)

var (
	ErrAlreadyJoined = errors.New("realtime client already joined")
	ErrHubClosed     = errors.New("realtime hub is closed")
)

// Message is the JSON envelope used by the realtime protocol. Data contains an
// opaque Yjs update and is encoded as base64 by encoding/json.
type Message struct {
	Type            string `json:"type"`
	Protocol        int    `json:"protocol,omitempty"`
	BoardID         string `json:"board_id,omitempty"`
	ClientID        string `json:"client_id,omitempty"`
	UserID          string `json:"user_id,omitempty"`
	CanEdit         *bool  `json:"can_edit,omitempty"`
	UpdateID        string `json:"update_id,omitempty"`
	ServerSequence  int64  `json:"server_sequence,omitempty"`
	ThroughSequence int64  `json:"through_sequence,omitempty"`
	RequestID       string `json:"request_id,omitempty"`
	Duplicate       bool   `json:"duplicate,omitempty"`
	Data            []byte `json:"data,omitempty"`
	Removed         bool   `json:"removed,omitempty"`
	Code            string `json:"code,omitempty"`
	Message         string `json:"message,omitempty"`
}

// Client represents one websocket connection. All application writes must go
// through Hub.Send or Hub.Broadcast so closing a slow client cannot race a
// sender.
type Client struct {
	ID            string
	UserID        string
	BoardID       string
	CanEdit       bool
	CanCheckpoint bool
	Send          chan []byte
	Done          chan struct{}

	closeOnce sync.Once

	// syncedThrough is guarded by the owning room mutex. It prevents an update
	// that committed immediately before the initial database read from being
	// delivered both in the initial sync and as a live broadcast.
	syncedThrough int64
}

func NewClient(id, userID, boardID string, canEdit bool, sendBuffer int) *Client {
	if sendBuffer < 1 {
		sendBuffer = 1
	}
	return &Client{
		ID:            id,
		UserID:        userID,
		BoardID:       boardID,
		CanEdit:       canEdit,
		CanCheckpoint: canEdit,
		Send:          make(chan []byte, sendBuffer),
		Done:          make(chan struct{}),
	}
}

func (c *Client) close() {
	c.closeOnce.Do(func() {
		close(c.Done)
		close(c.Send)
	})
}

type SyncState struct {
	CheckpointSequence int64
	LatestSequence     int64
}

type checkpointRequest struct {
	ID              string
	ClientID        string
	ThroughSequence int64
	ExpiresAt       time.Time
}

type queuedUpdate struct {
	message Message
	except  *Client
}

type room struct {
	mu        sync.Mutex
	clients   map[*Client]struct{}
	closed    bool
	latest    int64
	published int64
	updates   map[int64]queuedUpdate
	checked   int64
	checkedAt time.Time
	pending   *checkpointRequest
}

type Hub struct {
	mu     sync.Mutex
	rooms  map[string]*room
	closed bool

	checkpointEvery    int64
	checkpointInterval time.Duration
	now                func() time.Time
}

type Option func(*Hub)

// WithCheckpointPolicy configures when an online editor is asked to compact
// the board into a Yjs checkpoint. A zero value disables that trigger.
func WithCheckpointPolicy(everyUpdates int64, interval time.Duration) Option {
	return func(h *Hub) {
		h.checkpointEvery = everyUpdates
		h.checkpointInterval = interval
	}
}

// WithClock is intended for deterministic tests.
func WithClock(now func() time.Time) Option {
	return func(h *Hub) {
		if now != nil {
			h.now = now
		}
	}
}

func NewHub(options ...Option) *Hub {
	h := &Hub{
		rooms:              make(map[string]*room),
		checkpointEvery:    defaultCheckpointEvery,
		checkpointInterval: defaultCheckpointInterval,
		now:                time.Now,
	}
	for _, option := range options {
		option(h)
	}
	return h
}

func (h *Hub) getRoom(boardID string) *room {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[boardID] == nil {
		h.rooms[boardID] = &room{
			clients:   make(map[*Client]struct{}),
			updates:   make(map[int64]queuedUpdate),
			checkedAt: h.now(),
		}
	}
	return h.rooms[boardID]
}

// Join serializes the initial database sync with live broadcasts for this
// board. synchronize may write directly to the websocket; it must return the
// highest sequence included in that sync.
func (h *Hub) Join(client *Client, synchronize func() (SyncState, error)) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return ErrHubClosed
	}
	if h.rooms[client.BoardID] == nil {
		h.rooms[client.BoardID] = &room{
			clients:   make(map[*Client]struct{}),
			updates:   make(map[int64]queuedUpdate),
			checkedAt: h.now(),
		}
	}
	r := h.rooms[client.BoardID]
	h.mu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrHubClosed
	}
	if _, exists := r.clients[client]; exists {
		return ErrAlreadyJoined
	}
	state, err := synchronize()
	if err != nil {
		return err
	}
	client.syncedThrough = state.LatestSequence
	if state.LatestSequence > r.latest {
		r.latest = state.LatestSequence
	}
	// With no existing recipients, the database sync is authoritative for all
	// committed sequences. With existing recipients, an in-flight publisher
	// may still need to deliver an update that this joining client already read.
	if len(r.clients) == 0 && state.LatestSequence > r.published {
		r.published = state.LatestSequence
		for sequence := range r.updates {
			if sequence <= r.published {
				delete(r.updates, sequence)
			}
		}
	}
	if state.CheckpointSequence > r.checked {
		r.checked = state.CheckpointSequence
		r.checkedAt = h.now()
	}
	r.clients[client] = struct{}{}
	exclude := h.expireCheckpointLocked(r)
	if r.pending == nil && h.checkpointDueLocked(r) {
		h.requestCheckpointLocked(client.BoardID, r, exclude)
	}
	return nil
}

// Close evicts every connection. Websocket write pumps turn the closed send
// channels into close control frames before closing their network connection.
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	rooms := make([]*room, 0, len(h.rooms))
	for _, r := range h.rooms {
		rooms = append(rooms, r)
	}
	h.mu.Unlock()

	for _, r := range rooms {
		r.mu.Lock()
		r.closed = true
		for client := range r.clients {
			delete(r.clients, client)
			client.close()
		}
		r.pending = nil
		r.updates = make(map[int64]queuedUpdate)
		r.mu.Unlock()
	}
}

func (h *Hub) Leave(client *Client) {
	r := h.getRoom(client.BoardID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if !h.removeLocked(r, client) {
		return
	}
	h.announceRemovedLocked(r, []*Client{client})
	h.retryCheckpointLocked(client.BoardID, r)
}

func (h *Hub) Send(client *Client, msg Message) bool {
	r := h.getRoom(client.BoardID)
	data := Encode(msg)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.clients[client]; !ok {
		return false
	}
	select {
	case client.Send <- data:
		return true
	default:
		h.removeLocked(r, client)
		h.announceRemovedLocked(r, []*Client{client})
		h.retryCheckpointLocked(client.BoardID, r)
		return false
	}
}

func (h *Hub) Broadcast(boardID string, msg Message, except *Client) {
	r := h.getRoom(boardID)
	data := Encode(msg)
	r.mu.Lock()
	defer r.mu.Unlock()
	evicted := h.broadcastLocked(r, data, except, nil)
	h.announceRemovedLocked(r, evicted)
	h.retryCheckpointLocked(boardID, r)
}

// BroadcastUpdate publishes persisted updates in contiguous server-sequence
// order. Concurrent handlers may return from the repository in order but
// reach the hub out of order; buffering here keeps peers and checkpoint
// writers from observing sequence N before N-1. Clients whose initial sync
// already contained a sequence are skipped.
func (h *Hub) BroadcastUpdate(boardID string, msg Message, except *Client) {
	r := h.getRoom(boardID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if msg.ServerSequence > r.latest {
		r.latest = msg.ServerSequence
	}
	if msg.ServerSequence <= r.published {
		return
	}
	if _, exists := r.updates[msg.ServerSequence]; !exists {
		r.updates[msg.ServerSequence] = queuedUpdate{message: msg, except: except}
	}
	for {
		next := r.published + 1
		update, ok := r.updates[next]
		if !ok {
			break
		}
		delete(r.updates, next)
		data := Encode(update.message)
		evicted := h.broadcastLocked(r, data, update.except, func(client *Client) bool {
			return next > client.syncedThrough
		})
		r.published = next
		h.announceRemovedLocked(r, evicted)
	}
	exclude := h.expireCheckpointLocked(r)
	if r.pending == nil && h.checkpointDueLocked(r) {
		h.requestCheckpointLocked(boardID, r, exclude)
	}
	h.retryCheckpointLocked(boardID, r)
}

// ObserveUpdate records a committed sequence and checks the checkpoint policy.
// Checkpoints only cover the highest contiguously published sequence, never an
// out-of-order update that an editor may not have received yet.
func (h *Hub) ObserveUpdate(boardID string, sequence int64) {
	r := h.getRoom(boardID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if sequence > r.latest {
		r.latest = sequence
	}
	exclude := h.expireCheckpointLocked(r)
	if r.pending != nil || !h.checkpointDueLocked(r) {
		return
	}
	h.requestCheckpointLocked(boardID, r, exclude)
}

// ValidateCheckpoint ensures a response came from the editor selected by the
// hub and covers exactly the requested sequence.
func (h *Hub) ValidateCheckpoint(client *Client, requestID string, throughSequence int64) bool {
	r := h.getRoom(client.BoardID)
	r.mu.Lock()
	defer r.mu.Unlock()
	exclude := h.expireCheckpointLocked(r)
	if r.pending == nil {
		if h.checkpointDueLocked(r) {
			h.requestCheckpointLocked(client.BoardID, r, exclude)
		}
		return false
	}
	return r.pending != nil &&
		r.pending.ID == requestID &&
		r.pending.ClientID == client.ID &&
		r.pending.ThroughSequence == throughSequence
}

// CompleteCheckpoint advances compaction state after the repository has
// atomically saved the checkpoint and removed covered updates.
func (h *Hub) CompleteCheckpoint(client *Client, requestID string, throughSequence int64) bool {
	r := h.getRoom(client.BoardID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending == nil ||
		r.pending.ID != requestID ||
		r.pending.ClientID != client.ID ||
		r.pending.ThroughSequence != throughSequence {
		return false
	}
	r.pending = nil
	if throughSequence > r.checked {
		r.checked = throughSequence
	}
	r.checkedAt = h.now()
	return true
}

func (h *Hub) ClientCount(boardID string) int {
	r := h.getRoom(boardID)
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.clients)
}

// SetCanEdit refreshes a connection's role after an authorization check. If
// the selected checkpoint writer was downgraded, another editor is elected.
func (h *Hub) SetCanEdit(client *Client, canEdit bool) {
	r := h.getRoom(client.BoardID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.clients[client]; !ok {
		return
	}
	client.CanEdit = canEdit
	if !canEdit && r.pending != nil && r.pending.ClientID == client.ID {
		r.pending = nil
		if h.checkpointDueLocked(r) {
			h.requestCheckpointLocked(client.BoardID, r, client.ID)
		}
	}
}

// SetCanCheckpoint limits opaque checkpoint generation to project managers.
// A downgraded selected writer is replaced without waiting for its timeout.
func (h *Hub) SetCanCheckpoint(client *Client, canCheckpoint bool) {
	r := h.getRoom(client.BoardID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.clients[client]; !ok {
		return
	}
	client.CanCheckpoint = canCheckpoint
	if !canCheckpoint && r.pending != nil && r.pending.ClientID == client.ID {
		r.pending = nil
		if h.checkpointDueLocked(r) {
			h.requestCheckpointLocked(client.BoardID, r, client.ID)
		}
	}
}

func (h *Hub) checkpointDueLocked(r *room) bool {
	if r.published <= r.checked {
		return false
	}
	byCount := h.checkpointEvery > 0 && r.published-r.checked >= h.checkpointEvery
	byTime := h.checkpointInterval > 0 && h.now().Sub(r.checkedAt) >= h.checkpointInterval
	return byCount || byTime
}

func (h *Hub) requestCheckpointLocked(boardID string, r *room, excludeClientID string) {
	var editor *Client
	var fallback *Client
	for client := range r.clients {
		if !client.CanEdit || !client.CanCheckpoint {
			continue
		}
		if client.ID == excludeClientID {
			fallback = client
			continue
		}
		editor = client
		break
	}
	if editor == nil {
		editor = fallback
	}
	if editor == nil {
		return
	}
	request := &checkpointRequest{
		ID:              requestID(),
		ClientID:        editor.ID,
		ThroughSequence: r.published,
		ExpiresAt:       h.now().Add(defaultCheckpointTimeout),
	}
	message := Encode(Message{
		Type:            MessageCheckpointRequest,
		BoardID:         boardID,
		RequestID:       request.ID,
		ThroughSequence: request.ThroughSequence,
	})
	select {
	case editor.Send <- message:
		r.pending = request
	default:
		h.removeLocked(r, editor)
		h.announceRemovedLocked(r, []*Client{editor})
		// A single recursive retry is enough to choose the next editor; each
		// failed send removes one client, so this always terminates.
		h.requestCheckpointLocked(boardID, r, editor.ID)
	}
}

func (h *Hub) retryCheckpointLocked(boardID string, r *room) {
	if r.pending == nil {
		return
	}
	if _, present := h.clientByIDLocked(r, r.pending.ClientID); present {
		return
	}
	r.pending = nil
	if h.checkpointDueLocked(r) {
		h.requestCheckpointLocked(boardID, r, "")
	}
}

func (h *Hub) expireCheckpointLocked(r *room) string {
	if r.pending == nil || h.now().Before(r.pending.ExpiresAt) {
		return ""
	}
	exclude := r.pending.ClientID
	r.pending = nil
	return exclude
}

func (h *Hub) clientByIDLocked(r *room, id string) (*Client, bool) {
	for client := range r.clients {
		if client.ID == id {
			return client, true
		}
	}
	return nil, false
}

func (h *Hub) removeLocked(r *room, client *Client) bool {
	if _, ok := r.clients[client]; !ok {
		return false
	}
	delete(r.clients, client)
	client.close()
	return true
}

func (h *Hub) announceRemovedLocked(r *room, removed []*Client) {
	for len(removed) > 0 {
		client := removed[0]
		removed = removed[1:]
		message := Encode(Message{
			Type:     MessageAwareness,
			BoardID:  client.BoardID,
			ClientID: client.ID,
			UserID:   client.UserID,
			Removed:  true,
		})
		removed = append(removed, h.broadcastLocked(r, message, nil, nil)...)
	}
}

func (h *Hub) broadcastLocked(r *room, data []byte, except *Client, include func(*Client) bool) []*Client {
	var evicted []*Client
	for client := range r.clients {
		if client == except || (include != nil && !include(client)) {
			continue
		}
		select {
		case client.Send <- data:
		default:
			delete(r.clients, client)
			client.close()
			evicted = append(evicted, client)
		}
	}
	return evicted
}

func requestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return "cpr_" + hex.EncodeToString(value[:])
}

func Encode(msg Message) []byte {
	data, _ := json.Marshal(msg)
	return data
}
