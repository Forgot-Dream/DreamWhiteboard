package httpapi

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"dreamwhiteboard/backend/internal/domain"
	"dreamwhiteboard/backend/internal/realtime"
	"dreamwhiteboard/backend/internal/store"
)

type checkpointSaveRepository struct {
	*wsTestRepository
	save func(string, []byte, int64) (domain.BoardDocument, error)
}

func (r *checkpointSaveRepository) SaveBoardCheckpoint(boardID string, checkpoint []byte, throughSequence int64) (domain.BoardDocument, error) {
	return r.save(boardID, checkpoint, throughSequence)
}

func TestCheckpointErrorsEchoRequestID(t *testing.T) {
	t.Run("forbidden", func(t *testing.T) {
		fixture := newWSFixtureWithAuthInterval(t, time.Hour)
		viewer, err := fixture.repo.CreateUser("checkpoint-viewer@example.com", "Checkpoint viewer", "viewer-password", domain.SystemUser)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.repo.UpsertMember(fixture.project.ID, viewer.ID, domain.RoleViewer); err != nil {
			t.Fatal(err)
		}
		client, sessionHash := joinCheckpointHandlerClient(t, fixture, viewer, false, false)
		requestID := "cpr_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		fixture.server.handleBoardWSMessage(client, fixture.board, sessionHash, wsClientMessage{
			Type: realtime.MessageCheckpoint, RequestID: requestID, ThroughSequence: 1, Data: []byte{1},
		})
		assertCheckpointError(t, readCheckpointClientMessage(t, client), "forbidden", requestID)
	})

	t.Run("invalid", func(t *testing.T) {
		fixture := newWSFixtureWithAuthInterval(t, time.Hour)
		client, sessionHash := joinCheckpointHandlerClient(t, fixture, fixture.editor, true, true)
		requestID := "cpr_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		fixture.server.handleBoardWSMessage(client, fixture.board, sessionHash, wsClientMessage{
			Type: realtime.MessageCheckpoint, RequestID: requestID, Data: []byte{1},
		})
		assertCheckpointError(t, readCheckpointClientMessage(t, client), "invalid_checkpoint", requestID)
	})

	t.Run("too large", func(t *testing.T) {
		fixture := newWSFixtureWithAuthInterval(t, time.Hour)
		client, sessionHash := joinCheckpointHandlerClient(t, fixture, fixture.editor, true, true)
		requestID := "cpr_cccccccccccccccccccccccccccccccc"
		fixture.server.handleBoardWSMessage(client, fixture.board, sessionHash, wsClientMessage{
			Type: realtime.MessageCheckpoint, RequestID: requestID, ThroughSequence: 1,
			Data: make([]byte, wsMaxCheckpointBytes+1),
		})
		assertCheckpointError(t, readCheckpointClientMessage(t, client), "checkpoint_too_large", requestID)
	})
}

func TestCheckpointErrorsDoNotEchoUntrustedRequestID(t *testing.T) {
	tests := []struct {
		name      string
		requestID string
		viewer    bool
	}{
		{name: "near-limit manager request", requestID: strings.Repeat("x", wsMaxMessageBytes-1024)},
		{name: "malformed manager request", requestID: "cpr_ABCDEF0123456789abcdef0123456789"},
		{name: "oversized viewer request", requestID: strings.Repeat("y", 1<<20), viewer: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWSFixtureWithAuthInterval(t, time.Hour)
			user := fixture.editor
			canEdit, canManage := true, true
			if test.viewer {
				var err error
				user, err = fixture.repo.CreateUser("checkpoint-id-viewer@example.com", "Checkpoint ID viewer", "viewer-password", domain.SystemUser)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.repo.UpsertMember(fixture.project.ID, user.ID, domain.RoleViewer); err != nil {
					t.Fatal(err)
				}
				canEdit, canManage = false, false
			}
			client, sessionHash := joinCheckpointHandlerClient(t, fixture, user, canEdit, canManage)
			fixture.server.handleBoardWSMessage(client, fixture.board, sessionHash, wsClientMessage{
				Type: realtime.MessageCheckpoint, RequestID: test.requestID, ThroughSequence: 1, Data: []byte{1},
			})
			payload := readCheckpointClientPayload(t, client)
			if len(payload) > 512 {
				t.Fatalf("checkpoint error response is unexpectedly large: %d bytes", len(payload))
			}
			var message realtime.Message
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatal(err)
			}
			assertCheckpointError(t, message, "invalid_checkpoint", "")
		})
	}
}

func TestWSErrorsDoNotEchoInvalidUpdateID(t *testing.T) {
	tests := []struct {
		name        string
		messageType string
		updateID    string
		viewer      bool
		wantCode    string
	}{
		{
			name: "oversized viewer update", messageType: realtime.MessageUpdate,
			updateID: strings.Repeat("u", wsMaxMessageBytes-1024), viewer: true, wantCode: "forbidden",
		},
		{
			name: "oversized editor update", messageType: realtime.MessageUpdate,
			updateID: strings.Repeat("e", wsMaxUpdateIDLength+1), wantCode: "invalid_update",
		},
		{
			name: "whitespace unsupported message", messageType: "unknown",
			updateID: " leading-space", wantCode: "unsupported_message",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWSFixtureWithAuthInterval(t, time.Hour)
			user := fixture.editor
			canEdit, canManage := true, true
			if test.viewer {
				var err error
				user, err = fixture.repo.CreateUser("update-id-viewer@example.com", "Update ID viewer", "viewer-password", domain.SystemUser)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.repo.UpsertMember(fixture.project.ID, user.ID, domain.RoleViewer); err != nil {
					t.Fatal(err)
				}
				canEdit, canManage = false, false
			}
			client, sessionHash := joinCheckpointHandlerClient(t, fixture, user, canEdit, canManage)
			fixture.server.handleBoardWSMessage(client, fixture.board, sessionHash, wsClientMessage{
				Type: test.messageType, UpdateID: test.updateID, Data: []byte{1},
			})
			payload := readCheckpointClientPayload(t, client)
			if len(payload) > 512 {
				t.Fatalf("websocket error response is unexpectedly large: %d bytes", len(payload))
			}
			var message realtime.Message
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatal(err)
			}
			if message.Type != realtime.MessageError || message.Code != test.wantCode || message.UpdateID != "" {
				t.Fatalf("websocket error = %#v, want code=%q without update_id", message, test.wantCode)
			}
		})
	}
}

func TestCheckpointPersistenceAndConflictErrorsEchoRequestID(t *testing.T) {
	t.Run("persistence", func(t *testing.T) {
		fixture, client, sessionHash, request := checkpointRequestFixture(t)
		fixture.server.repo = &checkpointSaveRepository{
			wsTestRepository: fixture.repo,
			save: func(string, []byte, int64) (domain.BoardDocument, error) {
				return domain.BoardDocument{}, errors.New("injected checkpoint persistence failure")
			},
		}
		fixture.server.handleBoardWSMessage(client, fixture.board, sessionHash, wsClientMessage{
			Type: realtime.MessageCheckpoint, RequestID: request.RequestID,
			ThroughSequence: request.ThroughSequence, Data: []byte{9},
		})
		assertCheckpointError(t, readCheckpointClientMessage(t, client), "persistence_failed", request.RequestID)
	})

	t.Run("conflict", func(t *testing.T) {
		fixture, client, sessionHash, request := checkpointRequestFixture(t)
		var injectedCompletion bool
		fixture.server.repo = &checkpointSaveRepository{
			wsTestRepository: fixture.repo,
			save: func(boardID string, checkpoint []byte, throughSequence int64) (domain.BoardDocument, error) {
				document, err := fixture.repo.SaveBoardCheckpoint(boardID, checkpoint, throughSequence)
				if err == nil {
					injectedCompletion, _ = fixture.server.hub.CompleteCheckpointAndAck(client, request.RequestID, request.ThroughSequence)
				}
				return document, err
			},
		}
		fixture.server.handleBoardWSMessage(client, fixture.board, sessionHash, wsClientMessage{
			Type: realtime.MessageCheckpoint, RequestID: request.RequestID,
			ThroughSequence: request.ThroughSequence, Data: []byte{8},
		})
		if !injectedCompletion {
			t.Fatal("failed to inject competing checkpoint completion")
		}
		if ack := readCheckpointClientMessage(t, client); ack.Type != realtime.MessageCheckpointAck || ack.RequestID != request.RequestID {
			t.Fatalf("unexpected injected checkpoint acknowledgement: %#v", ack)
		}
		assertCheckpointError(t, readCheckpointClientMessage(t, client), "checkpoint_conflict", request.RequestID)
	})
}

func checkpointRequestFixture(t *testing.T) (*wsFixture, *realtime.Client, string, realtime.Message) {
	t.Helper()
	fixture := newWSFixtureWithAuthInterval(t, time.Hour)
	fixture.server.hub = realtime.NewHub(realtime.WithCheckpointPolicy(1, time.Hour))
	client, sessionHash := joinCheckpointHandlerClient(t, fixture, fixture.editor, true, true)
	persisted, inserted, err := fixture.repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: fixture.board.ID, UpdateID: "checkpoint-source", ClientID: client.ID,
		UserID: fixture.editor.ID, Update: []byte{1}, IntroducedAssetIDs: []string{},
	})
	if err != nil || !inserted {
		t.Fatalf("persist checkpoint source: inserted=%v err=%v", inserted, err)
	}
	fixture.server.hub.BroadcastUpdate(fixture.board.ID, realtime.Message{
		Type: realtime.MessageUpdate, BoardID: fixture.board.ID,
		UpdateID: persisted.UpdateID, ServerSequence: persisted.ServerSequence, Data: persisted.Update,
	}, nil)
	if update := readCheckpointClientMessage(t, client); update.Type != realtime.MessageUpdate {
		t.Fatalf("unexpected update before checkpoint request: %#v", update)
	}
	request := readCheckpointClientMessage(t, client)
	if request.Type != realtime.MessageCheckpointRequest || request.RequestID == "" {
		t.Fatalf("unexpected checkpoint request: %#v", request)
	}
	return fixture, client, sessionHash, request
}

func joinCheckpointHandlerClient(t *testing.T, fixture *wsFixture, user domain.User, canEdit, canManage bool) (*realtime.Client, string) {
	t.Helper()
	token := randomHex(32)
	sessionHash := store.HashSessionToken(token)
	if _, err := fixture.repo.CreateSession(sessionHash, user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	client := realtime.NewClient("checkpoint-"+randomHex(8), user.ID, fixture.board.ID, canEdit, 8)
	client.CanCheckpoint = canManage
	if err := fixture.server.hub.Join(client, func() (realtime.SyncState, error) {
		return realtime.SyncState{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	return client, sessionHash
}

func readCheckpointClientMessage(t *testing.T, client *realtime.Client) realtime.Message {
	t.Helper()
	payload := readCheckpointClientPayload(t, client)
	var message realtime.Message
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatal(err)
	}
	return message
}

func readCheckpointClientPayload(t *testing.T, client *realtime.Client) []byte {
	t.Helper()
	select {
	case payload, ok := <-client.Send:
		if !ok {
			t.Fatal("checkpoint client send queue closed")
		}
		return payload
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for checkpoint client message")
		return nil
	}
}

func assertCheckpointError(t *testing.T, message realtime.Message, code, requestID string) {
	t.Helper()
	if message.Type != realtime.MessageError || message.Code != code || message.RequestID != requestID {
		t.Fatalf("checkpoint error = %#v, want code=%q request_id=%q", message, code, requestID)
	}
}
