package realtime

import (
	"encoding/json"
	"sync"
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
	completed, acknowledged := hub.CompleteCheckpointAndAck(editor, request.RequestID, 2)
	if !completed || !acknowledged {
		t.Fatalf("valid checkpoint completion: completed=%v acknowledged=%v", completed, acknowledged)
	}
	ack := readMessage(t, editor.Send)
	if ack.Type != MessageCheckpointAck || ack.RequestID != request.RequestID || ack.ThroughSequence != 2 {
		t.Fatalf("unexpected checkpoint acknowledgement: %#v", ack)
	}
	if hub.ValidateCheckpoint(editor, request.RequestID, 2) {
		t.Fatal("completed checkpoint request remained valid")
	}
}

func TestCheckpointPolicySkipsNonManagerEditors(t *testing.T) {
	hub := NewHub(WithCheckpointPolicy(1, time.Hour))
	nonManager := NewClient("editor", "user-editor", "board-1", true, 3)
	nonManager.CanCheckpoint = false
	manager := NewClient("manager", "user-manager", "board-1", true, 3)
	joinForTest(t, hub, nonManager)
	joinForTest(t, hub, manager)

	hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 1}, nil)
	_ = readMessage(t, nonManager.Send)
	_ = readMessage(t, manager.Send)
	request := readMessage(t, manager.Send)
	if request.Type != MessageCheckpointRequest || request.ThroughSequence != 1 {
		t.Fatalf("manager did not receive checkpoint request: %#v", request)
	}
	assertNoMessage(t, nonManager.Send)
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

func TestCheckpointTimePolicyFiresWithoutFurtherRoomEvents(t *testing.T) {
	clock := newManualCheckpointClock(time.Date(2026, 7, 11, 1, 0, 0, 0, time.UTC))
	hub := NewHub(
		WithCheckpointPolicy(0, time.Minute),
		WithClock(clock.Now),
		withCheckpointTimerFactory(clock.AfterFunc),
	)
	editor := NewClient("editor", "user-editor", "board-1", true, 4)
	joinForTest(t, hub, editor)

	hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 1}, nil)
	_ = readMessage(t, editor.Send)
	assertNoMessageNow(t, editor.Send)
	if active := clock.ActiveTimers(); active != 1 {
		t.Fatalf("active timers = %d, want 1", active)
	}
	hub.Broadcast("board-1", Message{Type: MessageAwareness}, editor)
	if created := clock.TimerCreations(); created != 1 {
		t.Fatalf("unchanged deadline created %d timers, want 1", created)
	}

	clock.Advance(time.Minute - time.Nanosecond)
	assertNoMessageNow(t, editor.Send)
	clock.Advance(time.Nanosecond)
	request := readMessage(t, editor.Send)
	if request.Type != MessageCheckpointRequest || request.ThroughSequence != 1 {
		t.Fatalf("quiet room did not receive timed checkpoint request: %#v", request)
	}
	completed, acknowledged := hub.CompleteCheckpointAndAck(editor, request.RequestID, request.ThroughSequence)
	if !completed || !acknowledged {
		t.Fatalf("timed checkpoint completion: completed=%v acknowledged=%v", completed, acknowledged)
	}
	if ack := readMessage(t, editor.Send); ack.Type != MessageCheckpointAck || ack.RequestID != request.RequestID {
		t.Fatalf("unexpected timed checkpoint acknowledgement: %#v", ack)
	}
	if active := clock.ActiveTimers(); active != 0 {
		t.Fatalf("completed checkpoint retained %d timers", active)
	}
}

func TestCheckpointTimePolicyFiresAfterInitialSyncWithoutBroadcast(t *testing.T) {
	clock := newManualCheckpointClock(time.Date(2026, 7, 11, 1, 30, 0, 0, time.UTC))
	hub := NewHub(
		WithCheckpointPolicy(0, time.Minute),
		WithClock(clock.Now),
		withCheckpointTimerFactory(clock.AfterFunc),
	)
	t.Cleanup(hub.Close)
	editor := NewClient("editor", "user-editor", "board-1", true, 4)
	if err := hub.Join(editor, func() (SyncState, error) {
		return SyncState{CheckpointSequence: 3, LatestSequence: 5}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if active := clock.ActiveTimers(); active != 1 {
		t.Fatalf("initial sync active timers = %d, want 1", active)
	}

	clock.Advance(time.Minute)
	request := readMessage(t, editor.Send)
	if request.Type != MessageCheckpointRequest || request.ThroughSequence != 5 {
		t.Fatalf("initially synced quiet room did not receive timed request: %#v", request)
	}
}

func TestCheckpointRequestTimeoutAutomaticallyReissues(t *testing.T) {
	clock := newManualCheckpointClock(time.Date(2026, 7, 11, 2, 0, 0, 0, time.UTC))
	hub := NewHub(
		WithCheckpointPolicy(1, time.Hour),
		WithClock(clock.Now),
		withCheckpointTimerFactory(clock.AfterFunc),
	)
	editor := NewClient("editor", "user-editor", "board-1", true, 4)
	joinForTest(t, hub, editor)

	hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 1}, nil)
	_ = readMessage(t, editor.Send)
	first := readMessage(t, editor.Send)
	replacement := NewClient("replacement", "user-replacement", "board-1", true, 4)
	joinForTest(t, hub, replacement)
	clock.Advance(defaultCheckpointTimeout - time.Nanosecond)
	assertNoMessageNow(t, editor.Send)
	clock.Advance(time.Nanosecond)
	second := readMessage(t, replacement.Send)
	if second.Type != MessageCheckpointRequest || second.RequestID == first.RequestID || second.ThroughSequence != first.ThroughSequence {
		t.Fatalf("unexpected automatic replacement checkpoint request: %#v", second)
	}
	assertNoMessageNow(t, editor.Send)
}

func TestCheckpointTimePolicyWaitsForEligibleWriter(t *testing.T) {
	clock := newManualCheckpointClock(time.Date(2026, 7, 11, 3, 0, 0, 0, time.UTC))
	hub := NewHub(
		WithCheckpointPolicy(0, time.Minute),
		WithClock(clock.Now),
		withCheckpointTimerFactory(clock.AfterFunc),
	)
	editor := NewClient("editor", "user-editor", "board-1", true, 4)
	editor.CanCheckpoint = false
	joinForTest(t, hub, editor)

	hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 1}, nil)
	_ = readMessage(t, editor.Send)
	if active := clock.ActiveTimers(); active != 0 {
		t.Fatalf("ineligible room retained %d checkpoint timers", active)
	}
	clock.Advance(time.Minute)
	assertNoMessageNow(t, editor.Send)

	hub.SetCanCheckpoint(editor, true)
	request := readMessage(t, editor.Send)
	if request.Type != MessageCheckpointRequest || request.ThroughSequence != 1 {
		t.Fatalf("newly eligible writer did not receive overdue request: %#v", request)
	}
}

func TestCheckpointRequestMovesWhenSelectedWriterLosesPermission(t *testing.T) {
	hub := NewHub(WithCheckpointPolicy(1, time.Hour))
	selected := NewClient("selected", "user-selected", "board-1", true, 4)
	joinForTest(t, hub, selected)
	hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 1}, nil)
	_ = readMessage(t, selected.Send)
	first := readMessage(t, selected.Send)

	replacement := NewClient("replacement", "user-replacement", "board-1", true, 4)
	joinForTest(t, hub, replacement)
	hub.SetCanCheckpoint(selected, false)
	second := readMessage(t, replacement.Send)
	if second.Type != MessageCheckpointRequest || second.RequestID == first.RequestID || second.ThroughSequence != first.ThroughSequence {
		t.Fatalf("unexpected replacement writer request: %#v", second)
	}
	assertNoMessageNow(t, selected.Send)
}

func TestCheckpointCompletionQueuesAckBeforeNextRequest(t *testing.T) {
	hub := NewHub(WithCheckpointPolicy(1, time.Hour))
	editor := NewClient("editor", "user-editor", "board-1", true, 8)
	joinForTest(t, hub, editor)
	hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 1}, nil)
	_ = readMessage(t, editor.Send)
	first := readMessage(t, editor.Send)

	hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 2}, nil)
	_ = readMessage(t, editor.Send)
	assertNoMessageNow(t, editor.Send)
	completed, acknowledged := hub.CompleteCheckpointAndAck(editor, first.RequestID, first.ThroughSequence)
	if !completed || !acknowledged {
		t.Fatalf("first checkpoint completion: completed=%v acknowledged=%v", completed, acknowledged)
	}
	ack := readMessage(t, editor.Send)
	second := readMessage(t, editor.Send)
	if ack.Type != MessageCheckpointAck || ack.RequestID != first.RequestID {
		t.Fatalf("checkpoint acknowledgement was not queued first: %#v", ack)
	}
	if second.Type != MessageCheckpointRequest || second.ThroughSequence != 2 {
		t.Fatalf("count policy was not rechecked after completion: %#v", second)
	}
}

func TestCheckpointTimerStopsWhenRoomEmptiesOrHubCloses(t *testing.T) {
	for _, test := range []struct {
		name string
		stop func(*Hub, *Client)
	}{
		{name: "room empty", stop: func(hub *Hub, client *Client) { hub.Leave(client) }},
		{name: "board close", stop: func(hub *Hub, client *Client) { hub.CloseBoard(client.BoardID) }},
		{name: "hub close", stop: func(hub *Hub, _ *Client) { hub.Close() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			clock := newManualCheckpointClock(time.Date(2026, 7, 11, 4, 0, 0, 0, time.UTC))
			hub := NewHub(
				WithCheckpointPolicy(0, time.Minute),
				WithClock(clock.Now),
				withCheckpointTimerFactory(clock.AfterFunc),
			)
			editor := NewClient("editor", "user-editor", "board-1", true, 4)
			joinForTest(t, hub, editor)
			hub.BroadcastUpdate("board-1", Message{Type: MessageUpdate, ServerSequence: 1}, nil)
			_ = readMessage(t, editor.Send)
			if active := clock.ActiveTimers(); active != 1 {
				t.Fatalf("active timers before stop = %d, want 1", active)
			}

			test.stop(hub, editor)
			if active := clock.ActiveTimers(); active != 0 {
				t.Fatalf("active timers after stop = %d, want 0", active)
			}
			clock.Advance(time.Minute)
			if active := clock.ActiveTimers(); active != 0 {
				t.Fatalf("stopped room rescheduled %d timers", active)
			}
		})
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
	t.Cleanup(hub.Close)
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

func assertNoMessageNow(t *testing.T, messages <-chan []byte) {
	t.Helper()
	select {
	case data, ok := <-messages:
		if !ok {
			t.Fatal("message channel unexpectedly closed")
		}
		t.Fatalf("unexpected message: %s", data)
	default:
	}
}

type manualCheckpointClock struct {
	mu      sync.Mutex
	now     time.Time
	timers  map[*manualCheckpointTimer]struct{}
	created int
}

type manualCheckpointTimer struct {
	clock    *manualCheckpointClock
	deadline time.Time
	callback func()
	stopped  bool
	fired    bool
}

func newManualCheckpointClock(now time.Time) *manualCheckpointClock {
	return &manualCheckpointClock{now: now, timers: make(map[*manualCheckpointTimer]struct{})}
}

func (c *manualCheckpointClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualCheckpointClock) AfterFunc(delay time.Duration, callback func()) checkpointTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.created++
	timer := &manualCheckpointTimer{
		clock:    c,
		deadline: c.now.Add(delay),
		callback: callback,
	}
	c.timers[timer] = struct{}{}
	return timer
}

func (c *manualCheckpointClock) Advance(elapsed time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(elapsed)
	c.mu.Unlock()
	for {
		var callbacks []func()
		c.mu.Lock()
		for timer := range c.timers {
			if timer.stopped || timer.fired || timer.deadline.After(c.now) {
				continue
			}
			timer.fired = true
			delete(c.timers, timer)
			callbacks = append(callbacks, timer.callback)
		}
		c.mu.Unlock()
		if len(callbacks) == 0 {
			return
		}
		for _, callback := range callbacks {
			callback()
		}
	}
}

func (c *manualCheckpointClock) ActiveTimers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

func (c *manualCheckpointClock) TimerCreations() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.created
}

func (t *manualCheckpointTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	delete(t.clock.timers, t)
	return true
}
