package realtime

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBroadcastUpdateSkipsSequenceIncludedInInitialSync(t *testing.T) {
	hub := NewHub(WithCheckpointPolicy(0, 0))
	client := NewClient("client-1", "user-1", "board-1", true, 4)
	if err := hub.Join(client, func() (SyncState, error) {
		return SyncState{CheckpointSequence: 3, LatestSequence: 5}, nil
	}); err != nil {
		t.Fatal(err)
	}

	hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 5}, nil)
	assertNoMessage(t, client.Send)

	hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 6}, nil)
	message := readMessage(t, client.Send)
	if message.Type != MessageUpdate || message.ServerSequence != 6 {
		t.Fatalf("unexpected message: %#v", message)
	}
}

func TestBroadcastUpdatePublishesConcurrentSequencesInOrder(t *testing.T) {
	hub := NewHub(WithCheckpointPolicy(0, 0))
	client := NewClient("client-1", "user-1", "board-1", true, 4)
	joinForTest(t, hub, client)

	hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 2, UpdateID: "two"}, nil)
	assertNoMessage(t, client.Send)
	hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 1, UpdateID: "one"}, nil)
	first := readMessage(t, client.Send)
	second := readMessage(t, client.Send)
	if first.ServerSequence != 1 || first.UpdateID != "one" || second.ServerSequence != 2 || second.UpdateID != "two" {
		t.Fatalf("updates published out of order: first=%#v second=%#v", first, second)
	}
}

func TestJoinSerializesInitialSyncWithLiveBroadcast(t *testing.T) {
	hub := NewHub(WithCheckpointPolicy(0, 0))
	client := NewClient("client-1", "user-1", "board-1", true, 4)
	syncStarted := make(chan struct{})
	releaseSync := make(chan struct{})
	joinDone := make(chan error, 1)
	go func() {
		joinDone <- hub.Join(client, func() (SyncState, error) {
			close(syncStarted)
			<-releaseSync
			return SyncState{LatestSequence: 9}, nil
		})
	}()
	<-syncStarted

	broadcastDone := make(chan struct{})
	go func() {
		hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 9}, nil)
		close(broadcastDone)
	}()
	select {
	case <-broadcastDone:
		t.Fatal("broadcast passed the initial sync barrier")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseSync)
	if err := <-joinDone; err != nil {
		t.Fatal(err)
	}
	<-broadcastDone
	assertNoMessage(t, client.Send)
}

func TestSlowClientIsEvictedAndAwarenessIsRemoved(t *testing.T) {
	hub := NewHub(WithCheckpointPolicy(0, 0))
	slow := NewClient("slow", "user-slow", "board-1", false, 1)
	fast := NewClient("fast", "user-fast", "board-1", true, 4)
	joinForTest(t, hub, slow)
	joinForTest(t, hub, fast)

	hub.Broadcast("board-1", Message{Type: MessageAwareness, Data: []byte{1}}, fast)
	hub.Broadcast("board-1", Message{Type: MessageAwareness, Data: []byte{2}}, fast)

	select {
	case <-slow.Done:
	case <-time.After(time.Second):
		t.Fatal("slow client was not evicted")
	}
	if got := hub.ClientCount("board-1"); got != 1 {
		t.Fatalf("client count = %d, want 1", got)
	}
	removed := readMessage(t, fast.Send)
	if removed.Type != MessageAwareness || !removed.Removed || removed.ClientID != slow.ID {
		t.Fatalf("unexpected removal message: %#v", removed)
	}
}

func TestLeaveBroadcastsAwarenessRemoval(t *testing.T) {
	hub := NewHub(WithCheckpointPolicy(0, 0))
	leaving := NewClient("leaving", "user-1", "board-1", true, 2)
	peer := NewClient("peer", "user-2", "board-1", true, 2)
	joinForTest(t, hub, leaving)
	joinForTest(t, hub, peer)

	hub.Leave(leaving)
	message := readMessage(t, peer.Send)
	if message.Type != MessageAwareness || !message.Removed || message.ClientID != leaving.ID || message.UserID != leaving.UserID {
		t.Fatalf("unexpected removal message: %#v", message)
	}
}

func TestCheckpointPolicyChoosesEditorAndValidatesResponse(t *testing.T) {
	now := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	hub := NewHub(
		WithCheckpointPolicy(2, time.Hour),
		WithClock(func() time.Time { return now }),
	)
	viewer := NewClient("viewer", "user-viewer", "board-1", false, 2)
	editor := NewClient("editor", "user-editor", "board-1", true, 2)
	joinForTest(t, hub, viewer)
	joinForTest(t, hub, editor)

	hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 1}, nil)
	_ = readMessage(t, editor.Send)
	_ = readMessage(t, viewer.Send)
	assertNoMessage(t, editor.Send)
	hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 2}, nil)
	_ = readMessage(t, editor.Send)
	_ = readMessage(t, viewer.Send)
	request := readMessage(t, editor.Send)
	if request.Type != MessageCheckpointRequest || request.RequestID == "" || request.ThroughSequence != 2 {
		t.Fatalf("unexpected checkpoint request: %#v", request)
	}
	assertNoMessage(t, viewer.Send)
	if hub.ValidateCheckpoint(viewer, request.RequestID, 2) {
		t.Fatal("viewer validated another client's checkpoint request")
	}
	if hub.ValidateCheckpoint(editor, request.RequestID, 1) {
		t.Fatal("checkpoint with the wrong sequence was accepted")
	}
	if !hub.ValidateCheckpoint(editor, request.RequestID, 2) {
		t.Fatal("valid checkpoint was rejected")
	}
	if !hub.CompleteCheckpoint(editor, request.RequestID, 2) {
		t.Fatal("valid checkpoint completion was rejected")
	}
	if hub.ValidateCheckpoint(editor, request.RequestID, 2) {
		t.Fatal("completed checkpoint request remained valid")
	}
}

func TestCheckpointTimePolicy(t *testing.T) {
	now := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	hub := NewHub(
		WithCheckpointPolicy(0, time.Minute),
		WithClock(func() time.Time { return now }),
	)
	editor := NewClient("editor", "user-editor", "board-1", true, 2)
	joinForTest(t, hub, editor)

	now = now.Add(time.Minute)
	hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 1}, nil)
	_ = readMessage(t, editor.Send)
	request := readMessage(t, editor.Send)
	if request.Type != MessageCheckpointRequest || request.ThroughSequence != 1 {
		t.Fatalf("unexpected checkpoint request: %#v", request)
	}
}

func TestCheckpointDueWithoutEditorIsRequestedWhenEditorJoins(t *testing.T) {
	hub := NewHub(WithCheckpointPolicy(1, time.Hour))
	hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 1}, nil)

	viewer := NewClient("viewer", "user-viewer", "board-1", false, 2)
	joinForTest(t, hub, viewer)
	assertNoMessage(t, viewer.Send)
	editor := NewClient("editor", "user-editor", "board-1", true, 2)
	joinForTest(t, hub, editor)
	request := readMessage(t, editor.Send)
	if request.Type != MessageCheckpointRequest || request.ThroughSequence != 1 {
		t.Fatalf("unexpected checkpoint request: %#v", request)
	}
}

func TestExpiredCheckpointRequestIsReissued(t *testing.T) {
	now := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	hub := NewHub(
		WithCheckpointPolicy(1, time.Hour),
		WithClock(func() time.Time { return now }),
	)
	editor := NewClient("editor", "user-editor", "board-1", true, 4)
	joinForTest(t, hub, editor)
	hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 1}, nil)
	_ = readMessage(t, editor.Send)
	first := readMessage(t, editor.Send)

	now = now.Add(defaultCheckpointTimeout)
	if hub.ValidateCheckpoint(editor, first.RequestID, first.ThroughSequence) {
		t.Fatal("expired checkpoint request remained valid")
	}
	second := readMessage(t, editor.Send)
	if second.Type != MessageCheckpointRequest || second.RequestID == first.RequestID || second.ThroughSequence != first.ThroughSequence {
		t.Fatalf("unexpected replacement checkpoint request: %#v", second)
	}
}

func TestCloseEvictsClientsAndRejectsNewJoins(t *testing.T) {
	hub := NewHub(WithCheckpointPolicy(0, 0))
	client := NewClient("client-1", "user-1", "board-1", true, 2)
	joinForTest(t, hub, client)

	hub.Close()
	select {
	case <-client.Done:
	case <-time.After(time.Second):
		t.Fatal("client was not closed")
	}
	newClient := NewClient("client-2", "user-2", "board-1", true, 2)
	if err := hub.Join(newClient, func() (SyncState, error) { return SyncState{}, nil }); err != ErrHubClosed {
		t.Fatalf("Join after Close error = %v, want %v", err, ErrHubClosed)
	}
}

func joinForTest(t *testing.T, hub *Hub, client *Client) {
	t.Helper()
	if err := hub.Join(client, func() (SyncState, error) { return SyncState{}, nil }); err != nil {
		t.Fatal(err)
	}
}

func readMessage(t *testing.T, messages <-chan []byte) Message {
	t.Helper()
	select {
	case data, ok := <-messages:
		if !ok {
			t.Fatal("message channel closed")
		}
		var message Message
		if err := json.Unmarshal(data, &message); err != nil {
			t.Fatalf("decode message: %v", err)
		}
		return message
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
		return Message{}
	}
}

func assertNoMessage(t *testing.T, messages <-chan []byte) {
	t.Helper()
	select {
	case data, ok := <-messages:
		if !ok {
			t.Fatal("message channel unexpectedly closed")
		}
		t.Fatalf("unexpected message: %s", data)
	case <-time.After(20 * time.Millisecond):
	}
}
