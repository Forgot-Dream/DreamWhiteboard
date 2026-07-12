package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"dreamwhiteboard/backend/internal/domain"
	"dreamwhiteboard/backend/internal/realtime"
	"dreamwhiteboard/backend/internal/store"
)

type wsTestRepository struct {
	*store.MemoryStore
}

// These tests exercise session authentication without also exercising the
// first-login password flow, which has its own HTTP tests.
func (r *wsTestRepository) GetUser(id string) (domain.User, error) {
	user, err := r.MemoryStore.GetUser(id)
	user.MustChangePassword = false
	return user, err
}

type wsFixture struct {
	t       *testing.T
	repo    *wsTestRepository
	server  *Server
	http    *httptest.Server
	project domain.Project
	board   domain.Board
	editor  domain.User
}

func newWSFixture(t *testing.T) *wsFixture {
	return newWSFixtureWithAuthInterval(t, 20*time.Millisecond)
}

func newWSFixtureWithAuthInterval(t *testing.T, authInterval time.Duration) *wsFixture {
	t.Helper()
	repo := &wsTestRepository{MemoryStore: store.NewMemoryStore()}
	editor, err := repo.CreateUser("editor@example.com", "Editor", "editor-password", domain.SystemUser)
	if err != nil {
		t.Fatal(err)
	}
	project, err := repo.CreateProject("Realtime", "", editor.ID)
	if err != nil {
		t.Fatal(err)
	}
	board, err := repo.CreateBoard(project.ID, "Board", editor.ID)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig(t.TempDir())
	config.CookieSecure = false
	config.WSAuthInterval = authInterval
	config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServerWithConfig(repo, config)
	httpServer := httptest.NewServer(server)
	t.Cleanup(func() {
		server.Close()
		httpServer.Close()
	})
	return &wsFixture{
		t: t, repo: repo, server: server, http: httpServer,
		project: project, board: board, editor: editor,
	}
}

func (f *wsFixture) dial(user domain.User) *websocket.Conn {
	f.t.Helper()
	connection, _ := f.dialWithToken(user)
	return connection
}

func (f *wsFixture) dialWithToken(user domain.User) (*websocket.Conn, string) {
	f.t.Helper()
	token := randomHex(32)
	if _, err := f.repo.CreateSession(store.HashSessionToken(token), user.ID, time.Now().Add(time.Hour)); err != nil {
		f.t.Fatal(err)
	}
	header := http.Header{}
	header.Set("Cookie", (&http.Cookie{Name: sessionCookieName, Value: token, Path: "/api"}).String())
	wsURL := "ws" + strings.TrimPrefix(f.http.URL, "http") + "/api/boards/" + f.board.ID + "/ws"
	connection, response, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		if response != nil {
			f.t.Fatalf("websocket dial: %v (status %d)", err, response.StatusCode)
		}
		f.t.Fatalf("websocket dial: %v", err)
	}
	f.t.Cleanup(func() { _ = connection.Close() })
	return connection, token
}

func (f *wsFixture) connect(user domain.User) (*websocket.Conn, realtime.Message) {
	f.t.Helper()
	connection := f.dial(user)
	start := readRealtimeMessage(f.t, connection)
	if start.Type != realtime.MessageSyncStart || start.Protocol != realtime.ProtocolVersion || start.ClientID == "" {
		f.t.Fatalf("unexpected sync start: %#v", start)
	}
	complete := readRealtimeMessage(f.t, connection)
	if complete.Type != realtime.MessageSyncComplete {
		f.t.Fatalf("unexpected sync completion: %#v", complete)
	}
	return connection, start
}

func TestBoardWebSocketInitialSyncReplaysCheckpointThenUpdates(t *testing.T) {
	fixture := newWSFixture(t)
	first, inserted, err := fixture.repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: fixture.board.ID, UpdateID: "first", ClientID: "old-client",
		UserID: fixture.editor.ID, Update: []byte{1}, IntroducedAssetIDs: []string{},
	})
	if err != nil || !inserted {
		t.Fatalf("append first update: inserted=%v err=%v", inserted, err)
	}
	checkpoint := []byte{7, 7, 7}
	if _, err := fixture.repo.SaveBoardCheckpoint(fixture.board.ID, checkpoint, first.ServerSequence); err != nil {
		t.Fatal(err)
	}
	second, inserted, err := fixture.repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: fixture.board.ID, UpdateID: "second", ClientID: "old-client",
		UserID: fixture.editor.ID, Update: []byte{2}, IntroducedAssetIDs: []string{},
	})
	if err != nil || !inserted {
		t.Fatalf("append second update: inserted=%v err=%v", inserted, err)
	}

	connection := fixture.dial(fixture.editor)
	start := readRealtimeMessage(t, connection)
	checkpointMessage := readRealtimeMessage(t, connection)
	updateMessage := readRealtimeMessage(t, connection)
	complete := readRealtimeMessage(t, connection)
	if start.Type != realtime.MessageSyncStart ||
		checkpointMessage.Type != realtime.MessageCheckpoint ||
		checkpointMessage.ServerSequence != first.ServerSequence ||
		string(checkpointMessage.Data) != string(checkpoint) ||
		updateMessage.Type != realtime.MessageUpdate ||
		updateMessage.UpdateID != second.UpdateID ||
		updateMessage.ServerSequence != second.ServerSequence ||
		complete.Type != realtime.MessageSyncComplete ||
		complete.ServerSequence != second.ServerSequence {
		t.Fatalf("unexpected initial sync: start=%#v checkpoint=%#v update=%#v complete=%#v", start, checkpointMessage, updateMessage, complete)
	}
}

func TestBoardWebSocketPersistsAcknowledgesAndBroadcastsUpdate(t *testing.T) {
	fixture := newWSFixture(t)
	sender, _ := fixture.connect(fixture.editor)
	peer, _ := fixture.connect(fixture.editor)
	update := []byte{1, 2, 3, 4}
	if err := sender.WriteJSON(wsClientMessage{
		Type: realtime.MessageUpdate, UpdateID: "update-1", Data: update, IntroducedAssetIDs: []string{},
	}); err != nil {
		t.Fatal(err)
	}

	ack := readRealtimeMessage(t, sender)
	if ack.Type != realtime.MessageUpdateAck || ack.UpdateID != "update-1" || ack.ServerSequence != 1 || ack.Duplicate {
		t.Fatalf("unexpected update ack: %#v", ack)
	}
	broadcast := readRealtimeMessage(t, peer)
	if broadcast.Type != realtime.MessageUpdate || broadcast.UpdateID != "update-1" || broadcast.ServerSequence != 1 || string(broadcast.Data) != string(update) {
		t.Fatalf("unexpected update broadcast: %#v", broadcast)
	}
	document, updates, err := fixture.repo.LoadBoardDocument(fixture.board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if document.CheckpointSequence != 0 || len(updates) != 1 || updates[0].UpdateID != "update-1" {
		t.Fatalf("update was not persisted before broadcast: document=%#v updates=%#v", document, updates)
	}

	if err := sender.WriteJSON(wsClientMessage{
		Type: realtime.MessageUpdate, UpdateID: "update-1", Data: update, IntroducedAssetIDs: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	duplicateAck := readRealtimeMessage(t, sender)
	if duplicateAck.Type != realtime.MessageUpdateAck || !duplicateAck.Duplicate || duplicateAck.ServerSequence != 1 {
		t.Fatalf("unexpected duplicate ack: %#v", duplicateAck)
	}
	if err := peer.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := peer.ReadMessage(); err == nil {
		t.Fatal("duplicate update was broadcast to peer")
	}
}

func TestBoardWebSocketRejectsViewerDocumentUpdate(t *testing.T) {
	fixture := newWSFixture(t)
	viewer, err := fixture.repo.CreateUser("viewer@example.com", "Viewer", "viewer-password", domain.SystemUser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.UpsertMember(fixture.project.ID, viewer.ID, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	connection, start := fixture.connect(viewer)
	if start.CanEdit == nil || *start.CanEdit {
		t.Fatalf("viewer sync permissions are wrong: %#v", start)
	}
	if err := connection.WriteJSON(wsClientMessage{
		Type: realtime.MessageUpdate, UpdateID: "viewer-update", Data: []byte{1},
	}); err != nil {
		t.Fatal(err)
	}
	message := readRealtimeMessage(t, connection)
	if message.Type != realtime.MessageError || message.Code != "forbidden" || message.UpdateID != "viewer-update" {
		t.Fatalf("unexpected viewer response: %#v", message)
	}
	_, updates, err := fixture.repo.LoadBoardDocument(fixture.board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 0 {
		t.Fatalf("viewer update was persisted: %#v", updates)
	}
}

func TestBoardWebSocketRevokedSessionIsEvictedWithoutClientTraffic(t *testing.T) {
	fixture := newWSFixture(t)
	connection, token := fixture.dialWithToken(fixture.editor)
	if message := readRealtimeMessage(t, connection); message.Type != realtime.MessageSyncStart {
		t.Fatalf("unexpected sync start: %#v", message)
	}
	if message := readRealtimeMessage(t, connection); message.Type != realtime.MessageSyncComplete {
		t.Fatalf("unexpected sync completion: %#v", message)
	}
	if err := fixture.repo.DeleteSession(store.HashSessionToken(token)); err != nil {
		t.Fatal(err)
	}
	assertWebSocketClosed(t, connection, time.Second)
}

func TestBoardWebSocketRemovedMemberIsEvictedWithoutClientTraffic(t *testing.T) {
	fixture := newWSFixture(t)
	viewer, err := fixture.repo.CreateUser("passive@example.com", "Passive", "passive-password", domain.SystemUser)
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
	assertWebSocketClosed(t, connection, time.Second)
}

func TestServerCloseEvictsWebSocketsAndWaitsForHandlers(t *testing.T) {
	fixture := newWSFixture(t)
	connection, _ := fixture.connect(fixture.editor)
	if err := fixture.server.Close(); err != nil {
		t.Fatalf("close server with active websocket: %v", err)
	}
	assertWebSocketClosed(t, connection, time.Second)
}

func TestBoardWebSocketRoleDowngradeTakesEffectOnNextMessage(t *testing.T) {
	fixture := newWSFixture(t)
	editor, err := fixture.repo.CreateUser("second-editor@example.com", "Second editor", "second-editor-password", domain.SystemUser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.UpsertMember(fixture.project.ID, editor.ID, domain.RoleEditor); err != nil {
		t.Fatal(err)
	}
	connection, _ := fixture.connect(editor)
	if _, err := fixture.repo.UpsertMember(fixture.project.ID, editor.ID, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	permission := readRealtimeMessage(t, connection)
	if permission.Type != realtime.MessagePermission || permission.CanEdit == nil || *permission.CanEdit {
		t.Fatalf("unexpected permission downgrade: %#v", permission)
	}
	if err := connection.WriteJSON(wsClientMessage{
		Type: realtime.MessageUpdate, UpdateID: "after-downgrade", Data: []byte{1},
	}); err != nil {
		t.Fatal(err)
	}
	message := readRealtimeMessage(t, connection)
	if message.Type != realtime.MessageError || message.Code != "forbidden" || message.UpdateID != "after-downgrade" {
		t.Fatalf("unexpected response after role downgrade: %#v", message)
	}
	_, updates, err := fixture.repo.LoadBoardDocument(fixture.board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 0 {
		t.Fatalf("downgraded editor update was persisted: %#v", updates)
	}
}

func TestBoardWebSocketRejectsUpdateIDReuseWithDifferentData(t *testing.T) {
	fixture := newWSFixture(t)
	connection, _ := fixture.connect(fixture.editor)
	if err := connection.WriteJSON(wsClientMessage{
		Type: realtime.MessageUpdate, UpdateID: "same-id", Data: []byte{1}, IntroducedAssetIDs: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	if ack := readRealtimeMessage(t, connection); ack.Type != realtime.MessageUpdateAck {
		t.Fatalf("unexpected update ack: %#v", ack)
	}
	if err := connection.WriteJSON(wsClientMessage{
		Type: realtime.MessageUpdate, UpdateID: "same-id", Data: []byte{2}, IntroducedAssetIDs: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	conflict := readRealtimeMessage(t, connection)
	if conflict.Type != realtime.MessageError || conflict.Code != "update_id_conflict" || conflict.UpdateID != "same-id" {
		t.Fatalf("unexpected update ID conflict: %#v", conflict)
	}
}

func TestBoardWebSocketAdvertisesAssetReferenceProtocolV5(t *testing.T) {
	if realtime.ProtocolVersion != 5 {
		t.Fatalf("realtime protocol constant = %d, want 5", realtime.ProtocolVersion)
	}
	fixture := newWSFixture(t)
	connection := fixture.dial(fixture.editor)
	start := readRealtimeMessage(t, connection)
	if start.Type != realtime.MessageSyncStart || start.Protocol != realtime.ProtocolVersion {
		t.Fatalf("unexpected sync start protocol: %#v", start)
	}
	if complete := readRealtimeMessage(t, connection); complete.Type != realtime.MessageSyncComplete {
		t.Fatalf("unexpected sync completion: %#v", complete)
	}
}

func TestBoardWebSocketProtocolV5RejectsMissingAssetClaims(t *testing.T) {
	fixture := newWSFixture(t)
	connection, _ := fixture.connect(fixture.editor)
	if err := connection.WriteJSON(map[string]any{
		"type":      realtime.MessageUpdate,
		"update_id": "missing-claims",
		"data":      []byte{1, 2, 3},
	}); err != nil {
		t.Fatal(err)
	}
	message := readRealtimeMessage(t, connection)
	if message.Type != realtime.MessageError || message.Code != "asset_claims_required" || message.UpdateID != "missing-claims" ||
		message.Message != "introduced_asset_ids is required by collaboration protocol v5" {
		t.Fatalf("unexpected missing claims response: %#v", message)
	}
	_, updates, err := fixture.repo.LoadBoardDocument(fixture.board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 0 {
		t.Fatalf("missing-claims update was persisted: %#v", updates)
	}
}

func TestBoardWebSocketPersistsAssetReferenceManifest(t *testing.T) {
	fixture := newWSFixture(t)
	asset := saveWSAsset(t, fixture, fixture.project, "manifest")
	connection, _ := fixture.connect(fixture.editor)
	if err := connection.WriteJSON(map[string]any{
		"type":                    realtime.MessageUpdate,
		"update_id":               "manifest-update",
		"data":                    []byte{1, 2, 3},
		"reference_base_sequence": int64(0),
		"asset_ids":               []string{asset.ID},
		"introduced_asset_ids":    []string{asset.ID},
	}); err != nil {
		t.Fatal(err)
	}
	if ack := readRealtimeMessage(t, connection); ack.Type != realtime.MessageUpdateAck || ack.UpdateID != "manifest-update" {
		t.Fatalf("unexpected update ack: %#v", ack)
	}

	_, updates, err := fixture.repo.LoadBoardDocument(fixture.board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || updates[0].ReferenceBaseSequence == nil || *updates[0].ReferenceBaseSequence != 0 || len(updates[0].AssetIDs) != 1 || updates[0].AssetIDs[0] != asset.ID || len(updates[0].IntroducedAssetIDs) != 1 || updates[0].IntroducedAssetIDs[0] != asset.ID {
		t.Fatalf("asset reference manifest was not persisted: %#v", updates)
	}
}

func TestBoardWebSocketRejectsCrossProjectAssetReference(t *testing.T) {
	fixture := newWSFixture(t)
	otherProject, err := fixture.repo.CreateProject("Other realtime project", "", fixture.editor.ID)
	if err != nil {
		t.Fatal(err)
	}
	foreignAsset := saveWSAsset(t, fixture, otherProject, "foreign")
	connection, _ := fixture.connect(fixture.editor)
	if err := connection.WriteJSON(map[string]any{
		"type":                    realtime.MessageUpdate,
		"update_id":               "foreign-asset-update",
		"data":                    []byte{1},
		"reference_base_sequence": int64(0),
		"asset_ids":               []string{foreignAsset.ID},
		"introduced_asset_ids":    []string{foreignAsset.ID},
	}); err != nil {
		t.Fatal(err)
	}
	message := readRealtimeMessage(t, connection)
	if message.Type != realtime.MessageError || message.Code != "invalid_asset_reference" || message.UpdateID != "foreign-asset-update" {
		t.Fatalf("unexpected invalid asset response: %#v", message)
	}
	_, updates, err := fixture.repo.LoadBoardDocument(fixture.board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 0 {
		t.Fatalf("invalid asset update was persisted: %#v", updates)
	}
}

func TestBoardWebSocketRejectsUpdateIDReuseWithDifferentAssetManifest(t *testing.T) {
	fixture := newWSFixture(t)
	first := saveWSAsset(t, fixture, fixture.project, "manifest-first")
	second := saveWSAsset(t, fixture, fixture.project, "manifest-second")
	connection, _ := fixture.connect(fixture.editor)
	update := map[string]any{
		"type":                    realtime.MessageUpdate,
		"update_id":               "same-manifest-id",
		"data":                    []byte{7, 8, 9},
		"reference_base_sequence": int64(0),
		"asset_ids":               []string{first.ID},
		"introduced_asset_ids":    []string{first.ID},
	}
	if err := connection.WriteJSON(update); err != nil {
		t.Fatal(err)
	}
	if ack := readRealtimeMessage(t, connection); ack.Type != realtime.MessageUpdateAck {
		t.Fatalf("unexpected update ack: %#v", ack)
	}

	update["asset_ids"] = []string{second.ID}
	update["introduced_asset_ids"] = []string{second.ID}
	if err := connection.WriteJSON(update); err != nil {
		t.Fatal(err)
	}
	conflict := readRealtimeMessage(t, connection)
	if conflict.Type != realtime.MessageError || conflict.Code != "update_id_conflict" || conflict.UpdateID != "same-manifest-id" {
		t.Fatalf("unexpected manifest conflict: %#v", conflict)
	}
}

func TestClaimsOnlyWebSocketUpdateMakesAssetDeletionStale(t *testing.T) {
	fixture := newWSFixture(t)
	asset := saveWSAsset(t, fixture, fixture.project, "legacy-stale")
	connection, token := fixture.dialWithToken(fixture.editor)
	if start := readRealtimeMessage(t, connection); start.Type != realtime.MessageSyncStart {
		t.Fatalf("unexpected sync start: %#v", start)
	}
	if complete := readRealtimeMessage(t, connection); complete.Type != realtime.MessageSyncComplete {
		t.Fatalf("unexpected sync completion: %#v", complete)
	}

	// Claims have been required since protocol v4. Without a trusted full
	// manifest, the server still cannot prove that the document omits an asset.
	if err := connection.WriteJSON(wsClientMessage{
		Type: realtime.MessageUpdate, UpdateID: "claims-without-manifest", Data: []byte{4, 5, 6}, IntroducedAssetIDs: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	if ack := readRealtimeMessage(t, connection); ack.Type != realtime.MessageUpdateAck {
		t.Fatalf("unexpected legacy update ack: %#v", ack)
	}

	cookie := &http.Cookie{Name: sessionCookieName, Value: token, Path: "/api"}
	rec := requestJSON(t, fixture.server, http.MethodDelete, "/api/assets/"+asset.ID, cookie, nil)
	assertStatus(t, rec, http.StatusConflict)
	assertErrorCode(t, rec, "asset_reference_index_stale")
	if _, err := fixture.repo.GetAsset(asset.ID); err != nil {
		t.Fatalf("stale reference index allowed asset deletion: %v", err)
	}
}

func TestBoardWebSocketBroadcastsAwarenessRemovalOnDisconnect(t *testing.T) {
	fixture := newWSFixture(t)
	leaving, leavingStart := fixture.connect(fixture.editor)
	peer, peerStart := fixture.connect(fixture.editor)
	awareness := encodeTestAwareness(t, testAwarenessEntry{
		ID: 301, Clock: 1,
		State: map[string]any{"user": map[string]any{"id": "forged", "name": "Forged"}},
	})
	if err := leaving.WriteJSON(wsClientMessage{Type: realtime.MessageAwareness, Data: awareness}); err != nil {
		t.Fatal(err)
	}
	announced := readRealtimeMessage(t, peer)
	if announced.Type != realtime.MessageAwareness || len(announced.AwarenessIDs) != 1 || announced.AwarenessIDs[0] != 301 {
		t.Fatalf("unexpected awareness announcement: %#v", announced)
	}
	if err := leaving.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	_ = leaving.Close()

	removed := readRealtimeMessage(t, peer)
	if removed.Type != realtime.MessageAwareness || !removed.Removed || removed.ClientID != leavingStart.ClientID || removed.UserID != fixture.editor.ID {
		t.Fatalf("unexpected awareness removal: %#v", removed)
	}
	waitForAwarenessOwner(t, fixture.server.awareness, fixture.board.ID, 301, "")
	if err := peer.WriteJSON(wsClientMessage{Type: realtime.MessageAwareness, Data: encodeTestAwareness(t, testAwarenessEntry{
		ID: 301, Clock: 2, State: map[string]any{"user": map[string]any{"id": "second-forgery"}},
	})}); err != nil {
		t.Fatal(err)
	}
	waitForAwarenessOwner(t, fixture.server.awareness, fixture.board.ID, 301, peerStart.ClientID)
}

func TestBoardWebSocketBindsAwarenessIdentityAndRejectsCrossConnectionID(t *testing.T) {
	fixture := newWSFixture(t)
	viewer, err := fixture.repo.CreateUser("awareness-viewer@example.com", "Trusted Viewer", "viewer-password", domain.SystemUser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.UpsertMember(fixture.project.ID, viewer.ID, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	editorConnection, editorStart := fixture.connect(fixture.editor)
	viewerConnection, viewerStart := fixture.connect(viewer)

	editorAwareness := encodeTestAwareness(t, testAwarenessEntry{
		ID: 401, Clock: 1,
		State: map[string]any{"user": map[string]any{"id": viewer.ID, "name": "Forged Viewer", "color": "#123456"}},
	})
	if err := editorConnection.WriteJSON(wsClientMessage{Type: realtime.MessageAwareness, Data: editorAwareness}); err != nil {
		t.Fatal(err)
	}
	fromEditor := readRealtimeMessage(t, viewerConnection)
	if fromEditor.Type != realtime.MessageAwareness || fromEditor.ClientID != editorStart.ClientID ||
		fromEditor.UserID != fixture.editor.ID || fromEditor.UserName != fixture.editor.Name ||
		len(fromEditor.AwarenessIDs) != 1 || fromEditor.AwarenessIDs[0] != 401 {
		t.Fatalf("editor awareness was not bound to its authenticated identity: %#v", fromEditor)
	}

	viewerAwareness := encodeTestAwareness(t, testAwarenessEntry{
		ID: 402, Clock: 1,
		State: map[string]any{"user": map[string]any{"id": fixture.editor.ID, "name": "Forged Editor"}},
	})
	if err := viewerConnection.WriteJSON(wsClientMessage{Type: realtime.MessageAwareness, Data: viewerAwareness}); err != nil {
		t.Fatal(err)
	}
	fromViewer := readRealtimeMessage(t, editorConnection)
	if fromViewer.Type != realtime.MessageAwareness || fromViewer.ClientID != viewerStart.ClientID ||
		fromViewer.UserID != viewer.ID || fromViewer.UserName != viewer.Name ||
		len(fromViewer.AwarenessIDs) != 1 || fromViewer.AwarenessIDs[0] != 402 {
		t.Fatalf("viewer awareness was not bound to its authenticated identity: %#v", fromViewer)
	}

	stolenID := encodeTestAwareness(t, testAwarenessEntry{
		ID: 401, Clock: 2,
		State: map[string]any{"user": map[string]any{"id": fixture.editor.ID, "name": "Impersonated Editor"}},
	})
	if err := viewerConnection.WriteJSON(wsClientMessage{Type: realtime.MessageAwareness, Data: stolenID}); err != nil {
		t.Fatal(err)
	}
	rejected := readRealtimeMessage(t, viewerConnection)
	if rejected.Type != realtime.MessageError || rejected.Code != "awareness_id_conflict" {
		t.Fatalf("cross-connection awareness ID was not rejected: %#v", rejected)
	}
}

func TestBoardWebSocketDuplicateRemainsIdempotentAfterCheckpoint(t *testing.T) {
	fixture := newWSFixture(t)
	fixture.server.hub = realtime.NewHub(realtime.WithCheckpointPolicy(1, time.Hour))
	connection, _ := fixture.connect(fixture.editor)
	update := []byte{9, 8, 7}
	if err := connection.WriteJSON(wsClientMessage{
		Type: realtime.MessageUpdate, UpdateID: "compacted-update", Data: update, IntroducedAssetIDs: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	ack := readRealtimeMessage(t, connection)
	if ack.Type != realtime.MessageUpdateAck || ack.ServerSequence != 1 {
		t.Fatalf("unexpected update ack: %#v", ack)
	}
	request := readRealtimeMessage(t, connection)
	if request.Type != realtime.MessageCheckpointRequest || request.RequestID == "" || request.ThroughSequence != 1 {
		t.Fatalf("unexpected checkpoint request: %#v", request)
	}
	checkpoint := []byte{5, 4, 3, 2, 1}
	if err := connection.WriteJSON(wsClientMessage{
		Type: realtime.MessageCheckpoint, RequestID: request.RequestID,
		ThroughSequence: request.ThroughSequence, Data: checkpoint,
	}); err != nil {
		t.Fatal(err)
	}
	checkpointAck := readRealtimeMessage(t, connection)
	if checkpointAck.Type != realtime.MessageCheckpointAck || checkpointAck.ThroughSequence != 1 {
		t.Fatalf("unexpected checkpoint ack: %#v", checkpointAck)
	}
	document, updates, err := fixture.repo.LoadBoardDocument(fixture.board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if document.CheckpointSequence != 1 || string(document.Checkpoint) != string(checkpoint) || len(updates) != 0 {
		t.Fatalf("checkpoint did not compact updates: document=%#v updates=%#v", document, updates)
	}

	// This models a lost ACK: the page still has the original update in its
	// pending queue and reconnect/resend happens after compaction.
	if err := connection.WriteJSON(wsClientMessage{
		Type: realtime.MessageUpdate, UpdateID: "compacted-update", Data: update, IntroducedAssetIDs: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	duplicateAck := readRealtimeMessage(t, connection)
	if duplicateAck.Type != realtime.MessageUpdateAck || !duplicateAck.Duplicate || duplicateAck.ServerSequence != 1 {
		t.Fatalf("compacted duplicate was not idempotent: %#v", duplicateAck)
	}
	documentAfter, updatesAfter, err := fixture.repo.LoadBoardDocument(fixture.board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if documentAfter.CheckpointSequence != 1 || len(updatesAfter) != 0 {
		t.Fatalf("duplicate reappeared after checkpoint: document=%#v updates=%#v", documentAfter, updatesAfter)
	}
}

func readRealtimeMessage(t *testing.T, connection *websocket.Conn) realtime.Message {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var message realtime.Message
	if err := connection.ReadJSON(&message); err != nil {
		t.Fatalf("read realtime message: %v", err)
	}
	return message
}

func assertWebSocketClosed(t *testing.T, connection *websocket.Conn, timeout time.Duration) {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := connection.ReadMessage(); err == nil {
		t.Fatal("websocket remained open after authorization was revoked")
	}
}

func saveWSAsset(t *testing.T, fixture *wsFixture, project domain.Project, suffix string) domain.Asset {
	t.Helper()
	asset, err := fixture.repo.SaveAsset(domain.Asset{
		ID:          "ast_" + suffix,
		ProjectID:   project.ID,
		UploadedBy:  fixture.editor.ID,
		FileName:    suffix + ".png",
		ContentType: "image/png",
		Size:        1,
		StorageKey:  suffix + ".png",
		SHA256:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Width:       1,
		Height:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return asset
}

type testAwarenessEntry struct {
	ID    uint64
	Clock uint64
	State any
}

func encodeTestAwareness(t *testing.T, entries ...testAwarenessEntry) []byte {
	t.Helper()
	encoded := appendTestVarUint(nil, uint64(len(entries)))
	for _, entry := range entries {
		state, err := json.Marshal(entry.State)
		if err != nil {
			t.Fatal(err)
		}
		encoded = appendTestVarUint(encoded, entry.ID)
		encoded = appendTestVarUint(encoded, entry.Clock)
		encoded = appendTestVarUint(encoded, uint64(len(state)))
		encoded = append(encoded, state...)
	}
	return encoded
}

func appendTestVarUint(target []byte, value uint64) []byte {
	for value > 0x7f {
		target = append(target, byte(value&0x7f)|0x80)
		value /= 128
	}
	return append(target, byte(value))
}

func waitForAwarenessOwner(t *testing.T, registry *awarenessRegistry, boardID string, awarenessID uint64, expected string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		registry.mu.Lock()
		owner := registry.owners[awarenessOwnerKey{boardID: boardID, awarenessID: awarenessID}]
		registry.mu.Unlock()
		if owner == expected {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("awareness owner=%q, want %q", owner, expected)
		}
		time.Sleep(time.Millisecond)
	}
}
