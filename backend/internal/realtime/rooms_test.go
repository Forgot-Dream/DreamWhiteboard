package realtime

import (
	"testing"
	"time"
)

func TestCloseBoardEvictsOnlyTargetRoomAndRejectsNewJoins(t *testing.T) {
	hub := NewHub(WithCheckpointPolicy(0, time.Hour))
	first := NewClient("first", "user-1", "board-1", true, 2)
	second := NewClient("second", "user-2", "board-1", true, 2)
	other := NewClient("other", "user-3", "board-2", true, 2)
	joinForTest(t, hub, first)
	joinForTest(t, hub, second)
	joinForTest(t, hub, other)

	if closed := hub.CloseBoard("board-1"); closed != 2 {
		t.Fatalf("closed clients = %d, want 2", closed)
	}
	for _, client := range []*Client{first, second} {
		select {
		case <-client.Done:
		case <-time.After(time.Second):
			t.Fatalf("client %q was not closed", client.ID)
		}
	}
	select {
	case <-other.Done:
		t.Fatal("closing board-1 evicted a board-2 client")
	default:
	}
	if closed := hub.CloseBoard("board-1"); closed != 0 {
		t.Fatalf("second close evicted %d clients", closed)
	}

	replacement := NewClient("replacement", "user-4", "board-1", true, 2)
	if err := hub.Join(replacement, func() (SyncState, error) { return SyncState{}, nil }); err != ErrHubClosed {
		t.Fatalf("join closed board error = %v, want %v", err, ErrHubClosed)
	}
}

func TestCloseBoardBeforeRoomExistsRejectsFutureJoin(t *testing.T) {
	hub := NewHub(WithCheckpointPolicy(0, 0))
	t.Cleanup(hub.Close)
	if closed := hub.CloseBoard("board-never-opened"); closed != 0 {
		t.Fatalf("closed clients = %d, want 0", closed)
	}
	client := NewClient("client", "user", "board-never-opened", true, 2)
	if err := hub.Join(client, func() (SyncState, error) { return SyncState{}, nil }); err != ErrHubClosed {
		t.Fatalf("join pre-closed board error = %v, want %v", err, ErrHubClosed)
	}
}

func TestClosedRoomIgnoresLateCollaborationEvents(t *testing.T) {
	hub := NewHub(WithCheckpointPolicy(1, time.Hour))
	client := NewClient("client", "user", "board-1", true, 4)
	joinForTest(t, hub, client)
	if closed := hub.CloseBoard("board-1"); closed != 1 {
		t.Fatalf("closed clients = %d, want 1", closed)
	}

	hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 1}, nil)
	hub.ObserveUpdate("board-1", 2)
	hub.Broadcast("board-1", Message{Type: MessageAwareness}, nil)

	hub.mu.Lock()
	r := hub.rooms["board-1"]
	hub.mu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.latest != 0 || r.published != 0 || len(r.updates) != 0 || r.pending != nil || r.checkpointTimer != nil {
		t.Fatalf("late events mutated closed room: latest=%d published=%d updates=%d pending=%#v timer=%#v",
			r.latest, r.published, len(r.updates), r.pending, r.checkpointTimer)
	}
}
