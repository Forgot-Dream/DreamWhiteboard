package realtime

import (
	"testing"
	"time"
)

func TestSetPermissionsNotifiesOnceAndUpdatesClient(t *testing.T) {
	hub := NewHub(WithCheckpointPolicy(0, 0))
	client := NewClient("client", "user", "board", true, 4)
	client.CanCheckpoint = true
	joinForTest(t, hub, client)

	if !hub.SetPermissions(client, false, false) {
		t.Fatal("permission update evicted client")
	}
	message := readMessage(t, client.Send)
	if message.Type != MessagePermission || message.CanEdit == nil || *message.CanEdit || message.CanManage == nil || *message.CanManage {
		t.Fatalf("unexpected permission message: %#v", message)
	}
	if client.CanEdit || client.CanCheckpoint {
		t.Fatalf("client permissions were not updated: edit=%v manage=%v", client.CanEdit, client.CanCheckpoint)
	}
	if !hub.SetPermissions(client, false, false) {
		t.Fatal("unchanged permission refresh evicted client")
	}
	assertNoMessageNow(t, client.Send)
}

func TestPermissionPromotionPrecedesNewCheckpointRequest(t *testing.T) {
	hub := NewHub(WithCheckpointPolicy(1, time.Hour))
	client := NewClient("client", "user", "board", false, 4)
	client.CanCheckpoint = false
	joinForTest(t, hub, client)
	hub.BroadcastUpdate("board", Message{Type: MessageUpdate, ServerSequence: 1}, nil)
	_ = readMessage(t, client.Send)

	if !hub.SetPermissions(client, true, true) {
		t.Fatal("permission promotion evicted client")
	}
	permission := readMessage(t, client.Send)
	request := readMessage(t, client.Send)
	if permission.Type != MessagePermission || permission.CanManage == nil || !*permission.CanManage {
		t.Fatalf("promotion was not delivered first: %#v", permission)
	}
	if request.Type != MessageCheckpointRequest || request.ThroughSequence != 1 {
		t.Fatalf("promotion did not enable checkpointing: %#v", request)
	}
}
