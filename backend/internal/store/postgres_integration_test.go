package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dreamwhiteboard/backend/internal/domain"
	"github.com/lib/pq"
)

var postgresTestSchemaSequence uint64

func TestPostgresMigrationsAndReadinessIntegration(t *testing.T) {
	databaseURL, schema := newPostgresTestSchema(t)

	applyPostgresTestMigrations(t, databaseURL, 1)
	legacy, err := OpenPostgres(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open store at schema version 1: %v", err)
	}
	if err := legacy.Ready(context.Background()); err == nil || !strings.Contains(err.Error(), "incomplete or incompatible") {
		_ = legacy.Close()
		t.Fatalf("schema version 1 should not be ready for v2, got %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy store: %v", err)
	}

	applyPostgresTestMigrations(t, databaseURL, LatestSchemaVersion)
	repo, err := OpenPostgres(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	t.Cleanup(func() {
		if err := repo.Close(); err != nil {
			t.Errorf("close migrated store: %v", err)
		}
	})
	if err := repo.Ready(context.Background()); err != nil {
		t.Fatalf("fully migrated schema should be ready: %v", err)
	}

	var version int
	if err := repo.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != LatestSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, LatestSchemaVersion)
	}
	assertPostgresRelationExists(t, repo.db, schema, "sessions", true)
	assertPostgresRelationExists(t, repo.db, schema, "board_documents", true)
	assertPostgresRelationExists(t, repo.db, schema, "board_updates", true)
	assertPostgresRelationExists(t, repo.db, schema, "board_operations", false)
	assertPostgresRelationExists(t, repo.db, schema, "board_snapshots", false)
	if _, err := repo.db.Exec(`UPDATE schema_migrations SET checksum='tampered' WHERE version=$1`, LatestSchemaVersion); err != nil {
		t.Fatalf("tamper migration checksum: %v", err)
	}
	if err := repo.Ready(context.Background()); err == nil || !strings.Contains(err.Error(), "incomplete or incompatible") {
		t.Fatalf("tampered migration checksum should fail readiness, got %v", err)
	}
}

func TestPostgresPersistentSessionExpiryAndTouchIntegration(t *testing.T) {
	repo, databaseURL := newMigratedPostgresTestStore(t)
	user := createPostgresTestUser(t, repo, "sessions")

	tokenHash := HashSessionToken("postgres-session-token")
	expiresAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	created, err := repo.CreateSession(tokenHash, user.ID, expiresAt)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if created.TokenHash != tokenHash || created.UserID != user.ID || !created.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("unexpected created session: %#v", created)
	}

	reopened, err := OpenPostgres(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened store: %v", err)
		}
	})
	touchedAt := created.LastAccessedAt.Add(15 * time.Minute).Truncate(time.Microsecond)
	loaded, err := reopened.GetSession(tokenHash, touchedAt)
	if err != nil {
		t.Fatalf("load persistent session: %v", err)
	}
	if !loaded.LastAccessedAt.Equal(touchedAt) {
		t.Fatalf("last_accessed_at = %s, want %s", loaded.LastAccessedAt, touchedAt)
	}

	var persistedTouch time.Time
	if err := repo.db.QueryRow(`SELECT last_accessed_at FROM sessions WHERE token_hash=$1`, tokenHash).Scan(&persistedTouch); err != nil {
		t.Fatalf("read persisted session touch: %v", err)
	}
	if !persistedTouch.Equal(touchedAt) {
		t.Fatalf("persisted last_accessed_at = %s, want %s", persistedTouch, touchedAt)
	}

	if _, err := reopened.GetSession(tokenHash, expiresAt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session should expire at expires_at, got %v", err)
	}
	var remaining int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token_hash=$1`, tokenHash).Scan(&remaining); err != nil {
		t.Fatalf("count expired sessions: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expired session row was not removed: count=%d", remaining)
	}
}

func TestPostgresLastOwnerProtectionIntegration(t *testing.T) {
	repo, _ := newMigratedPostgresTestStore(t)
	first := createPostgresTestUser(t, repo, "first-owner")
	second := createPostgresTestUser(t, repo, "second-owner")
	project, err := repo.CreateProject("Owner safety", "", first.ID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	if _, err := repo.UpsertMember(project.ID, first.ID, domain.RoleAdmin); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("demote only owner: got %v, want ErrLastOwner", err)
	}
	if err := repo.DeleteMember(project.ID, first.ID); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("remove only owner: got %v, want ErrLastOwner", err)
	}
	if role, err := repo.MemberRole(project.ID, first.ID); err != nil || role != domain.RoleOwner {
		t.Fatalf("failed owner mutations changed persisted role: role=%q err=%v", role, err)
	}
	if _, err := repo.UpsertMember(project.ID, second.ID, domain.RoleOwner); err != nil {
		t.Fatalf("add second owner: %v", err)
	}

	start := make(chan struct{})
	errorsByUser := make(chan error, 2)
	for _, userID := range []string{first.ID, second.ID} {
		userID := userID
		go func() {
			<-start
			_, err := repo.UpsertMember(project.ID, userID, domain.RoleAdmin)
			errorsByUser <- err
		}()
	}
	close(start)
	var succeeded, protected int
	for index := 0; index < 2; index++ {
		err := <-errorsByUser
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrLastOwner):
			protected++
		default:
			t.Fatalf("concurrent owner demotion returned unexpected error: %v", err)
		}
	}
	if succeeded != 1 || protected != 1 {
		t.Fatalf("concurrent demotions: succeeded=%d protected=%d, want one each", succeeded, protected)
	}
	members, err := repo.ListMembers(project.ID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	ownerCount := 0
	for _, member := range members {
		if member.Role == domain.RoleOwner {
			ownerCount++
		}
	}
	if ownerCount != 1 {
		t.Fatalf("owner count after concurrent demotions = %d, want 1", ownerCount)
	}
}

func TestPostgresLastSystemAdministratorProtectionIntegration(t *testing.T) {
	repo, _ := newMigratedPostgresTestStore(t)
	first, err := repo.CreateUser("first-system-admin@example.test", "First", "integration-test-password", domain.SystemAdmin)
	if err != nil {
		t.Fatalf("create first system administrator: %v", err)
	}
	if _, err := repo.UpdateUser(first.ID, "", domain.SystemUser); !errors.Is(err, ErrLastSystemAdmin) {
		t.Fatalf("demote only system administrator: got %v, want ErrLastSystemAdmin", err)
	}
	second, err := repo.CreateUser("second-system-admin@example.test", "Second", "integration-test-password", domain.SystemAdmin)
	if err != nil {
		t.Fatalf("create second system administrator: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, userID := range []string{first.ID, second.ID} {
		userID := userID
		go func() {
			<-start
			_, err := repo.UpdateUser(userID, "", domain.SystemUser)
			results <- err
		}()
	}
	close(start)
	var succeeded, protected int
	for index := 0; index < 2; index++ {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrLastSystemAdmin):
			protected++
		default:
			t.Fatalf("concurrent system administrator demotion returned %v", err)
		}
	}
	if succeeded != 1 || protected != 1 {
		t.Fatalf("concurrent system administrator demotions: succeeded=%d protected=%d", succeeded, protected)
	}
}

func TestPostgresBoardUpdatePersistenceAndCheckpointIntegration(t *testing.T) {
	repo, databaseURL := newMigratedPostgresTestStore(t)
	user := createPostgresTestUser(t, repo, "crdt")
	project, err := repo.CreateProject("CRDT", "", user.ID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	board, err := repo.CreateBoard(project.ID, "Shared board", user.ID)
	if err != nil {
		t.Fatalf("create board: %v", err)
	}

	firstInput := domain.BoardUpdate{
		BoardID:  board.ID,
		UpdateID: "update-1",
		ClientID: "client-a",
		UserID:   user.ID,
		Update:   []byte{1, 2, 3},
	}
	first, inserted, err := repo.AppendBoardUpdate(firstInput)
	if err != nil || !inserted || first.ServerSequence != 1 {
		t.Fatalf("append first update: update=%#v inserted=%v err=%v", first, inserted, err)
	}
	duplicate, inserted, err := repo.AppendBoardUpdate(firstInput)
	if err != nil || inserted || duplicate.ServerSequence != first.ServerSequence {
		t.Fatalf("idempotent resend: update=%#v inserted=%v err=%v", duplicate, inserted, err)
	}
	conflicting := firstInput
	conflicting.Update = []byte{9, 9, 9}
	if _, _, err := repo.AppendBoardUpdate(conflicting); !errors.Is(err, ErrUpdateIDConflict) {
		t.Fatalf("conflicting duplicate: got %v, want ErrUpdateIDConflict", err)
	}

	second := appendPostgresTestBoardUpdate(t, repo, board.ID, user.ID, "update-2", []byte{4, 5})
	third := appendPostgresTestBoardUpdate(t, repo, board.ID, user.ID, "update-3", []byte{6, 7})
	if second.ServerSequence != 2 || third.ServerSequence != 3 {
		t.Fatalf("server sequence order = (%d, %d), want (2, 3)", second.ServerSequence, third.ServerSequence)
	}
	document, updates, err := repo.LoadBoardDocument(board.ID)
	if err != nil {
		t.Fatalf("load board before checkpoint: %v", err)
	}
	if document.CheckpointSequence != 0 || len(updates) != 3 {
		t.Fatalf("unexpected pre-checkpoint state: document=%#v updates=%#v", document, updates)
	}
	for index, want := range []string{"update-1", "update-2", "update-3"} {
		if updates[index].UpdateID != want || updates[index].ServerSequence != int64(index+1) {
			t.Fatalf("update %d = %#v, want id=%q sequence=%d", index, updates[index], want, index+1)
		}
	}

	checkpoint := []byte{20, 21, 22}
	document, err = repo.SaveBoardCheckpoint(board.ID, checkpoint, second.ServerSequence)
	if err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	if document.CheckpointSequence != 2 || !bytesEqual(document.Checkpoint, checkpoint) {
		t.Fatalf("unexpected checkpoint document: %#v", document)
	}
	if _, err := repo.SaveBoardCheckpoint(board.ID, []byte{99}, first.ServerSequence); !errors.Is(err, ErrInvalidCheckpointSequence) {
		t.Fatalf("checkpoint regression: got %v, want ErrInvalidCheckpointSequence", err)
	}
	if _, err := repo.SaveBoardCheckpoint(board.ID, []byte{99}, 4); !errors.Is(err, ErrInvalidCheckpointSequence) {
		t.Fatalf("checkpoint beyond persisted sequence: got %v, want ErrInvalidCheckpointSequence", err)
	}

	var compactedRows int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM board_updates
		WHERE board_id=$1 AND server_sequence<=$2 AND update_data IS NULL AND compacted_at IS NOT NULL`, board.ID, second.ServerSequence).Scan(&compactedRows); err != nil {
		t.Fatalf("inspect compacted receipts: %v", err)
	}
	if compactedRows != 2 {
		t.Fatalf("compacted receipt count = %d, want 2", compactedRows)
	}
	compactedDuplicate, inserted, err := repo.AppendBoardUpdate(firstInput)
	if err != nil || inserted || compactedDuplicate.ServerSequence != 1 || compactedDuplicate.Update != nil || compactedDuplicate.CompactedAt == nil {
		t.Fatalf("resend after compaction: update=%#v inserted=%v err=%v", compactedDuplicate, inserted, err)
	}

	reopened, err := OpenPostgres(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("reopen store for recovery: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close recovery store: %v", err)
		}
	})
	recovered, pending, err := reopened.LoadBoardDocument(board.ID)
	if err != nil {
		t.Fatalf("recover board document: %v", err)
	}
	if recovered.CheckpointSequence != 2 || !bytesEqual(recovered.Checkpoint, checkpoint) {
		t.Fatalf("unexpected recovered checkpoint: %#v", recovered)
	}
	if len(pending) != 1 || pending[0].UpdateID != third.UpdateID || pending[0].ServerSequence != 3 {
		t.Fatalf("unexpected recovered pending updates: %#v", pending)
	}
	fourth := appendPostgresTestBoardUpdate(t, reopened, board.ID, user.ID, "update-4", []byte{8})
	if fourth.ServerSequence != 4 {
		t.Fatalf("post-recovery server sequence = %d, want 4", fourth.ServerSequence)
	}
}

func TestPostgresAssetMetadataAndProjectCascadeIntegration(t *testing.T) {
	repo, _ := newMigratedPostgresTestStore(t)
	user := createPostgresTestUser(t, repo, "assets")
	project, err := repo.CreateProject("Assets", "cascade fixture", user.ID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	board, err := repo.CreateBoard(project.ID, "Asset board", user.ID)
	if err != nil {
		t.Fatalf("create board: %v", err)
	}
	appendPostgresTestBoardUpdate(t, repo, board.ID, user.ID, "asset-update", []byte{1})

	want := domain.Asset{
		ProjectID:   project.ID,
		UploadedBy:  user.ID,
		FileName:    "diagram.png",
		ContentType: "image/png",
		Size:        9876,
		Path:        "/var/lib/dreamwhiteboard/uploads/random-key.png",
		StorageKey:  "random-key.png",
		SHA256:      strings.Repeat("a", 64),
		Width:       1920,
		Height:      1080,
	}
	saved, err := repo.SaveAsset(want)
	if err != nil {
		t.Fatalf("save asset: %v", err)
	}
	loaded, err := repo.GetAsset(saved.ID)
	if err != nil {
		t.Fatalf("get asset: %v", err)
	}
	if loaded.ID != saved.ID || loaded.ProjectID != want.ProjectID || loaded.UploadedBy != want.UploadedBy ||
		loaded.FileName != want.FileName || loaded.ContentType != want.ContentType || loaded.Size != want.Size ||
		loaded.Path != want.Path || loaded.StorageKey != want.StorageKey || loaded.SHA256 != want.SHA256 ||
		loaded.Width != want.Width || loaded.Height != want.Height || loaded.CreatedAt.IsZero() {
		t.Fatalf("asset metadata did not round-trip: got %#v want %#v", loaded, want)
	}
	assets, err := repo.ListAssetsByProject(project.ID)
	if err != nil || len(assets) != 1 || assets[0].ID != saved.ID {
		t.Fatalf("list project assets: assets=%#v err=%v", assets, err)
	}
	duplicateKey := want
	duplicateKey.ID = "different-asset-id"
	if _, err := repo.SaveAsset(duplicateKey); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate storage key: got %v, want ErrConflict", err)
	}
	invalidHash := want
	invalidHash.StorageKey = "invalid-hash.png"
	invalidHash.SHA256 = "not-a-sha256"
	if _, err := repo.SaveAsset(invalidHash); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid asset hash: got %v, want ErrInvalidInput", err)
	}

	if err := repo.DeleteProject(project.ID); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	if _, err := repo.GetAsset(saved.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("asset metadata survived project deletion: %v", err)
	}
	if _, err := repo.GetBoard(board.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("board survived project deletion: %v", err)
	}
	if _, _, err := repo.LoadBoardDocument(board.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("board document survived project deletion: %v", err)
	}
	if _, err := repo.MemberRole(project.ID, user.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("project membership survived project deletion")
	}
	if _, err := repo.GetUser(user.ID); err != nil {
		t.Fatal("project deletion should not delete its creator")
	}
}

func newMigratedPostgresTestStore(t *testing.T) (*PostgresStore, string) {
	t.Helper()
	databaseURL, _ := newPostgresTestSchema(t)
	applyPostgresTestMigrations(t, databaseURL, LatestSchemaVersion)
	repo, err := OpenPostgres(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL test store: %v", err)
	}
	t.Cleanup(func() {
		if err := repo.Close(); err != nil {
			t.Errorf("close PostgreSQL test store: %v", err)
		}
	})
	if err := repo.Ready(context.Background()); err != nil {
		t.Fatalf("PostgreSQL test store is not ready: %v", err)
	}
	return repo, databaseURL
}

func newPostgresTestSchema(t *testing.T) (string, string) {
	t.Helper()
	baseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping PostgreSQL integration test")
	}
	admin, err := sql.Open("postgres", baseURL)
	if err != nil {
		t.Fatalf("open TEST_DATABASE_URL: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	schema := postgresTestSchemaName(t.Name())
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+pq.QuoteIdentifier(schema)); err != nil {
		_ = admin.Close()
		t.Fatalf("create isolated PostgreSQL schema %q: %v", schema, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.ExecContext(cleanupCtx, `DROP SCHEMA IF EXISTS `+pq.QuoteIdentifier(schema)+` CASCADE`); err != nil {
			t.Errorf("drop PostgreSQL test schema %q: %v", schema, err)
		}
		if err := admin.Close(); err != nil {
			t.Errorf("close TEST_DATABASE_URL connection: %v", err)
		}
	})
	return postgresURLWithSearchPath(t, baseURL, schema), schema
}

func postgresURLWithSearchPath(t *testing.T, databaseURL, schema string) string {
	t.Helper()
	if strings.HasPrefix(databaseURL, "postgres://") || strings.HasPrefix(databaseURL, "postgresql://") {
		parsed, err := url.Parse(databaseURL)
		if err != nil {
			t.Fatalf("parse TEST_DATABASE_URL: %v", err)
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return strings.TrimSpace(databaseURL) + " search_path=" + schema
}

func postgresTestSchemaName(testName string) string {
	var sanitized strings.Builder
	for _, character := range strings.ToLower(testName) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			sanitized.WriteRune(character)
		} else {
			sanitized.WriteByte('_')
		}
	}
	name := strings.Trim(sanitized.String(), "_")
	if len(name) > 30 {
		name = name[:30]
	}
	return fmt.Sprintf("dwtest_%s_%d_%d", name, time.Now().UnixNano(), atomic.AddUint64(&postgresTestSchemaSequence, 1))
}

func applyPostgresTestMigrations(t *testing.T, databaseURL string, throughVersion int) {
	t.Helper()
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatalf("open isolated PostgreSQL schema for migration: %v", err)
	}
	defer db.Close()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate PostgreSQL integration test source")
	}
	migrationDirectory := filepath.Join(filepath.Dir(currentFile), "..", "..", "migrations")
	entries, err := os.ReadDir(migrationDirectory)
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	type migrationFile struct {
		version int
		name    string
		path    string
	}
	migrations := make([]migrationFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		prefix, _, found := strings.Cut(entry.Name(), "_")
		if !found {
			t.Fatalf("migration %q has no numeric prefix", entry.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			t.Fatalf("migration %q has invalid numeric prefix: %v", entry.Name(), err)
		}
		if version <= throughVersion {
			migrations = append(migrations, migrationFile{version: version, name: entry.Name(), path: filepath.Join(migrationDirectory, entry.Name())})
		}
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	for _, migration := range migrations {
		var applied bool
		err := db.QueryRow(`SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema=current_schema() AND table_name='schema_migrations'
		)`).Scan(&applied)
		if err != nil {
			t.Fatalf("inspect schema_migrations before version %d: %v", migration.version, err)
		}
		if applied {
			if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, migration.version).Scan(&applied); err != nil {
				t.Fatalf("inspect migration version %d: %v", migration.version, err)
			}
			if applied {
				continue
			}
		}
		contents, err := os.ReadFile(migration.path)
		if err != nil {
			t.Fatalf("read migration version %d: %v", migration.version, err)
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("begin migration version %d: %v", migration.version, err)
		}
		if _, err := tx.Exec(string(contents)); err != nil {
			_ = tx.Rollback()
			t.Fatalf("apply migration version %d: %v", migration.version, err)
		}
		sum := sha256.Sum256(contents)
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, name, checksum) VALUES ($1,$2,$3)`, migration.version, migration.name, hex.EncodeToString(sum[:])); err != nil {
			_ = tx.Rollback()
			t.Fatalf("record migration version %d: %v", migration.version, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit migration version %d: %v", migration.version, err)
		}
	}
}

func assertPostgresRelationExists(t *testing.T, db *sql.DB, schema, relation string, want bool) {
	t.Helper()
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, schema+"."+relation).Scan(&exists); err != nil {
		t.Fatalf("inspect relation %s.%s: %v", schema, relation, err)
	}
	if exists != want {
		t.Fatalf("relation %s.%s exists=%v, want %v", schema, relation, exists, want)
	}
}

func createPostgresTestUser(t *testing.T, repo *PostgresStore, suffix string) domain.User {
	t.Helper()
	email := fmt.Sprintf("%s-%d@example.test", suffix, atomic.AddUint64(&postgresTestSchemaSequence, 1))
	user, err := repo.CreateUser(email, suffix, "integration-test-password", domain.SystemUser)
	if err != nil {
		t.Fatalf("create PostgreSQL test user %q: %v", email, err)
	}
	return user
}

func appendPostgresTestBoardUpdate(t *testing.T, repo *PostgresStore, boardID, userID, updateID string, data []byte) domain.BoardUpdate {
	t.Helper()
	update, inserted, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID:  boardID,
		UpdateID: updateID,
		ClientID: "integration-client",
		UserID:   userID,
		Update:   data,
	})
	if err != nil || !inserted {
		t.Fatalf("append board update %q: update=%#v inserted=%v err=%v", updateID, update, inserted, err)
	}
	return update
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
