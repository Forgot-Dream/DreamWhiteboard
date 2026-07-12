package httpapi

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"dreamwhiteboard/backend/internal/domain"
	"dreamwhiteboard/backend/internal/realtime"
	"dreamwhiteboard/backend/internal/store"
)

type blockingMemberRoleRepository struct {
	*wsTestRepository
	calls    atomic.Int32
	captured chan struct{}
	release  chan struct{}
}

func (r *blockingMemberRoleRepository) MemberRole(projectID, userID string) (string, error) {
	role, err := r.wsTestRepository.MemberRole(projectID, userID)
	if r.calls.Add(1) == 1 {
		close(r.captured)
		<-r.release
	}
	return role, err
}

func TestWriteBoardSyncRechecksViewPermission(t *testing.T) {
	fixture := newWSFixtureWithAuthInterval(t, time.Hour)
	outsider, err := fixture.repo.CreateUser("sync-outsider@example.com", "Sync outsider", "outsider-password", domain.SystemUser)
	if err != nil {
		t.Fatal(err)
	}
	client := realtime.NewClient("sync-outsider", outsider.ID, fixture.board.ID, false, 1)
	if _, err := fixture.server.writeBoardSync(nil, outsider, fixture.board, client); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("sync without current view permission = %v, want ErrForbidden", err)
	}
}

func TestBoardWebSocketPublishesInitialAndDynamicPermissions(t *testing.T) {
	fixture := newWSFixture(t)
	member, err := fixture.repo.CreateUser("dynamic-permissions@example.com", "Dynamic member", "member-password", domain.SystemUser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.UpsertMember(fixture.project.ID, member.ID, domain.RoleEditor); err != nil {
		t.Fatal(err)
	}
	connection, start := fixture.connect(member)
	if start.CanEdit == nil || !*start.CanEdit || start.CanManage == nil || *start.CanManage {
		t.Fatalf("unexpected initial editor permissions: %#v", start)
	}

	if _, err := fixture.repo.UpsertMember(fixture.project.ID, member.ID, domain.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	promoted := readRealtimeMessage(t, connection)
	if promoted.Type != realtime.MessagePermission || promoted.CanEdit == nil || !*promoted.CanEdit || promoted.CanManage == nil || !*promoted.CanManage {
		t.Fatalf("unexpected promotion message: %#v", promoted)
	}

	if _, err := fixture.repo.UpsertMember(fixture.project.ID, member.ID, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	downgraded := readRealtimeMessage(t, connection)
	if downgraded.Type != realtime.MessagePermission || downgraded.CanEdit == nil || *downgraded.CanEdit || downgraded.CanManage == nil || *downgraded.CanManage {
		t.Fatalf("unexpected downgrade message: %#v", downgraded)
	}
	if err := connection.WriteJSON(wsClientMessage{
		Type: realtime.MessageUpdate, UpdateID: "after-dynamic-downgrade", Data: []byte{1}, IntroducedAssetIDs: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	rejected := readRealtimeMessage(t, connection)
	if rejected.Type != realtime.MessageError || rejected.Code != "forbidden" {
		t.Fatalf("downgraded viewer update was not rejected: %#v", rejected)
	}
}

func TestPermissionDeliveryEvictionPreventsFurtherMessagePersistence(t *testing.T) {
	fixture := newWSFixtureWithAuthInterval(t, time.Hour)
	member, err := fixture.repo.CreateUser("slow-permission@example.com", "Slow permission", "member-password", domain.SystemUser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.UpsertMember(fixture.project.ID, member.ID, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	token := randomHex(32)
	sessionHash := store.HashSessionToken(token)
	if _, err := fixture.repo.CreateSession(sessionHash, member.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	client := realtime.NewClient("slow-permission-client", member.ID, fixture.board.ID, false, 1)
	client.CanCheckpoint = false
	if err := fixture.server.hub.Join(client, func() (realtime.SyncState, error) {
		return realtime.SyncState{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	client.Send <- realtime.Encode(realtime.Message{Type: realtime.MessageAwareness})
	if _, err := fixture.repo.UpsertMember(fixture.project.ID, member.ID, domain.RoleEditor); err != nil {
		t.Fatal(err)
	}

	if fixture.server.handleBoardWSMessage(client, fixture.board, sessionHash, wsClientMessage{
		Type: realtime.MessageUpdate, UpdateID: "must-not-persist", Data: []byte{1}, IntroducedAssetIDs: []string{},
	}) {
		t.Fatal("message handling continued after permission delivery evicted the client")
	}
	select {
	case <-client.Done:
	default:
		t.Fatal("permission delivery failure did not evict the client")
	}
	_, updates, err := fixture.repo.LoadBoardDocument(fixture.board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 0 {
		t.Fatalf("evicted client update was persisted: %#v", updates)
	}
}

func TestAuthorizationRefreshSerializesMonitorAndMessageSnapshots(t *testing.T) {
	fixture := newWSFixtureWithAuthInterval(t, time.Hour)
	member, err := fixture.repo.CreateUser("serialized-auth@example.com", "Serialized auth", "member-password", domain.SystemUser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.UpsertMember(fixture.project.ID, member.ID, domain.RoleEditor); err != nil {
		t.Fatal(err)
	}
	token := randomHex(32)
	sessionHash := store.HashSessionToken(token)
	if _, err := fixture.repo.CreateSession(sessionHash, member.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	client := realtime.NewClient("serialized-auth-client", member.ID, fixture.board.ID, true, 4)
	client.CanCheckpoint = false
	if err := fixture.server.hub.Join(client, func() (realtime.SyncState, error) {
		return realtime.SyncState{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	blocking := &blockingMemberRoleRepository{
		wsTestRepository: fixture.repo,
		captured:         make(chan struct{}),
		release:          make(chan struct{}),
	}
	fixture.server.repo = blocking

	type result struct {
		canEdit    bool
		authorized bool
		err        error
	}
	monitorDone := make(chan result, 1)
	go func() {
		_, canEdit, _, authorized, err := fixture.server.refreshBoardWSAuthorization(client, fixture.board, sessionHash)
		monitorDone <- result{canEdit: canEdit, authorized: authorized, err: err}
	}()
	<-blocking.captured
	released := false
	defer func() {
		if !released {
			close(blocking.release)
		}
	}()
	if _, err := fixture.repo.UpsertMember(fixture.project.ID, member.ID, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	messageDone := make(chan result, 1)
	go func() {
		_, canEdit, _, authorized, err := fixture.server.refreshBoardWSAuthorization(client, fixture.board, sessionHash)
		messageDone <- result{canEdit: canEdit, authorized: authorized, err: err}
	}()
	select {
	case stale := <-messageDone:
		t.Fatalf("message refresh bypassed the in-flight monitor snapshot: %#v", stale)
	case <-time.After(50 * time.Millisecond):
	}
	close(blocking.release)
	released = true

	monitor := <-monitorDone
	message := <-messageDone
	if monitor.err != nil || !monitor.authorized || !monitor.canEdit {
		t.Fatalf("unexpected captured monitor snapshot: %#v", monitor)
	}
	if message.err != nil || !message.authorized || message.canEdit {
		t.Fatalf("message path did not recheck the newer viewer role: %#v", message)
	}
	if client.CanEdit || client.CanCheckpoint {
		t.Fatalf("stale monitor snapshot won: edit=%v manage=%v", client.CanEdit, client.CanCheckpoint)
	}
}
