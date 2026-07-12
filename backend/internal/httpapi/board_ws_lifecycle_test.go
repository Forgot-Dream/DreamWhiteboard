package httpapi

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"dreamwhiteboard/backend/internal/domain"
	"dreamwhiteboard/backend/internal/realtime"
	"github.com/gorilla/websocket"
)

func TestDeletingBoardClosesItsActiveWebSockets(t *testing.T) {
	fixture := newWSFixture(t)
	connection, token := fixture.dialWithToken(fixture.editor)
	if start := readRealtimeMessage(t, connection); start.Type != realtime.MessageSyncStart {
		t.Fatalf("unexpected sync start: %#v", start)
	}
	if complete := readRealtimeMessage(t, connection); complete.Type != realtime.MessageSyncComplete {
		t.Fatalf("unexpected sync completion: %#v", complete)
	}

	cookie := &http.Cookie{Name: sessionCookieName, Value: token, Path: "/api"}
	recorder := requestJSON(t, fixture.server, http.MethodDelete, "/api/boards/"+fixture.board.ID, cookie, nil)
	assertStatus(t, recorder, http.StatusOK)
	assertWebSocketCloseReason(t, connection, time.Second, realtime.CloseReasonBoardDeleted)
	if count := fixture.server.hub.ClientCount(fixture.board.ID); count != 0 {
		t.Fatalf("deleted board retained %d websocket clients", count)
	}
}

func TestDeletingProjectClosesItsBoardWebSockets(t *testing.T) {
	fixture := newWSFixture(t)
	connection, token := fixture.dialWithToken(fixture.editor)
	if start := readRealtimeMessage(t, connection); start.Type != realtime.MessageSyncStart {
		t.Fatalf("unexpected sync start: %#v", start)
	}
	if complete := readRealtimeMessage(t, connection); complete.Type != realtime.MessageSyncComplete {
		t.Fatalf("unexpected sync completion: %#v", complete)
	}

	cookie := &http.Cookie{Name: sessionCookieName, Value: token, Path: "/api"}
	recorder := requestJSON(t, fixture.server, http.MethodDelete, "/api/projects/"+fixture.project.ID, cookie, nil)
	assertStatus(t, recorder, http.StatusOK)
	assertWebSocketCloseReason(t, connection, time.Second, realtime.CloseReasonBoardDeleted)
}

func TestRemovingMemberClosesWebSocketWithForbiddenReason(t *testing.T) {
	fixture := newWSFixture(t)
	viewer, err := fixture.repo.CreateUser("removed-close-reason@example.com", "Removed viewer", "viewer-password", domain.SystemUser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.UpsertMember(fixture.project.ID, viewer.ID, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	connection, _ := fixture.connect(viewer)
	if err := fixture.repo.DeleteMember(fixture.project.ID, viewer.ID); err != nil {
		t.Fatal(err)
	}
	assertWebSocketCloseReason(t, connection, time.Second, realtime.CloseReasonForbidden)
}

func TestRevokedAccessDetectedByClientMessageClosesWithForbiddenReason(t *testing.T) {
	fixture := newWSFixtureWithAuthInterval(t, time.Hour)
	viewer, err := fixture.repo.CreateUser("message-revoked@example.com", "Message revoked", "viewer-password", domain.SystemUser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.UpsertMember(fixture.project.ID, viewer.ID, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	connection, _ := fixture.connect(viewer)
	if err := fixture.repo.DeleteMember(fixture.project.ID, viewer.ID); err != nil {
		t.Fatal(err)
	}
	if err := connection.WriteJSON(wsClientMessage{
		Type: realtime.MessageUpdate, UpdateID: "revoked-message", Data: []byte{1}, IntroducedAssetIDs: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	assertWebSocketCloseReason(t, connection, time.Second, realtime.CloseReasonForbidden)
}

func assertWebSocketCloseReason(t *testing.T, connection *websocket.Conn, timeout time.Duration, reason string) {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := connection.ReadMessage(); err != nil {
		var closeError *websocket.CloseError
		if errors.As(err, &closeError) && closeError.Text == reason {
			return
		}
		t.Fatalf("websocket close error = %v, want reason %q", err, reason)
	}
	t.Fatal("websocket remained open")
}
