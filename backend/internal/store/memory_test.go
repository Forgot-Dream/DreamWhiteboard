package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"dreamwhiteboard/backend/internal/domain"
)

func TestArgon2PasswordAndPasswordChange(t *testing.T) {
	if !VerifyPassword("dreamwhiteboard-dummy-password", dummyPasswordHash) {
		t.Fatal("dummy password hash used for unknown-account timing equalization is invalid")
	}
	repo := NewMemoryStore()
	admin, err := repo.EnsureSystemAdmin("ADMIN@example.com", "initial-password")
	if err != nil {
		t.Fatal(err)
	}
	if !admin.MustChangePassword {
		t.Fatal("initial admin should be required to change password")
	}
	hash := HashPassword("secret")
	if !strings.HasPrefix(hash, "$argon2id$") || !VerifyPassword("secret", hash) || VerifyPassword("wrong", hash) {
		t.Fatalf("unexpected Argon2id password behavior: %q", hash)
	}
	if _, err := repo.Authenticate("admin@example.com", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("bad password authenticated")
	}
	if _, err := repo.Authenticate("missing@example.com", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown account should return invalid credentials after dummy verification, got %v", err)
	}
	if err := repo.UpdatePassword(admin.ID, "replacement-password", false); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Authenticate("admin@example.com", "initial-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("old password authenticated after change")
	}
	updated, err := repo.Authenticate("admin@example.com", "replacement-password")
	if err != nil || updated.MustChangePassword || updated.PasswordChangedAt == nil {
		t.Fatalf("password change was not persisted: %#v", updated)
	}
}

func TestEnsureSystemAdminRejectsExistingNonAdmin(t *testing.T) {
	repo := NewMemoryStore()
	if _, err := repo.CreateUser("collision@example.com", "User", "password", domain.SystemUser); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.EnsureSystemAdmin("collision@example.com", "different-password"); !errors.Is(err, ErrConflict) {
		t.Fatalf("bootstrap email collision should fail instead of silently leaving no admin, got %v", err)
	}
}

func TestPersistentSessionLifecycle(t *testing.T) {
	repo := NewMemoryStore()
	user, err := repo.EnsureSystemAdmin("admin@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	tokenHash := HashSessionToken("raw-session-token")
	if len(tokenHash) != 64 || strings.Contains(tokenHash, "raw-session-token") {
		t.Fatalf("unexpected token hash %q", tokenHash)
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	created, err := repo.CreateSession(tokenHash, user.ID, expiresAt)
	if err != nil {
		t.Fatal(err)
	}
	accessedAt := created.LastAccessedAt.Add(time.Minute)
	loaded, err := repo.GetSession(tokenHash, accessedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.LastAccessedAt.Equal(accessedAt) {
		t.Fatalf("last access was not updated: got %s want %s", loaded.LastAccessedAt, accessedAt)
	}
	if _, err := repo.GetSession(tokenHash, expiresAt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session should be removed, got %v", err)
	}
	secondHash := HashSessionToken("second-session-token")
	if _, err := repo.CreateSession(secondHash, user.ID, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdatePassword(user.ID, "replacement-password", false); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetSession(secondHash, time.Now().UTC()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("password change should revoke sessions atomically, got %v", err)
	}
}

func TestLastProjectOwnerCannotBeRemovedOrDemoted(t *testing.T) {
	repo := NewMemoryStore()
	owner, err := repo.EnsureSystemAdmin("owner@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.CreateUser("second@example.com", "Second", "password", domain.SystemUser)
	if err != nil {
		t.Fatal(err)
	}
	project, err := repo.CreateProject("Project", "", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertMember(project.ID, owner.ID, domain.RoleAdmin); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("last owner demotion should fail, got %v", err)
	}
	if err := repo.DeleteMember(project.ID, owner.ID); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("last owner removal should fail, got %v", err)
	}
	if _, err := repo.UpsertMember(project.ID, second.ID, domain.RoleOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertMember(project.ID, owner.ID, domain.RoleAdmin); err != nil {
		t.Fatalf("owner demotion should succeed when another owner exists: %v", err)
	}
	if err := repo.DeleteMember(project.ID, second.ID); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("remaining owner removal should fail, got %v", err)
	}
}

func TestLastSystemAdministratorCannotBeDemoted(t *testing.T) {
	repo := NewMemoryStore()
	first, err := repo.EnsureSystemAdmin("first-admin@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateUser(first.ID, "", domain.SystemUser); !errors.Is(err, ErrLastSystemAdmin) {
		t.Fatalf("last system administrator demotion should fail, got %v", err)
	}
	second, err := repo.CreateUser("second-admin@example.com", "Second", "password", domain.SystemAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateUser(first.ID, "", domain.SystemUser); err != nil {
		t.Fatalf("demotion with a second administrator should succeed: %v", err)
	}
	if _, err := repo.UpdateUser(second.ID, "", domain.SystemUser); !errors.Is(err, ErrLastSystemAdmin) {
		t.Fatalf("remaining system administrator demotion should fail, got %v", err)
	}
}

func TestBoardUpdateIdempotencyAndCheckpointCompaction(t *testing.T) {
	repo := NewMemoryStore()
	user, err := repo.EnsureSystemAdmin("admin@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	project, err := repo.CreateProject("Project", "", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	board, err := repo.CreateBoard(project.ID, "Board", user.ID)
	if err != nil {
		t.Fatal(err)
	}

	firstInput := domain.BoardUpdate{
		BoardID:            board.ID,
		UpdateID:           "update-1",
		ClientID:           "client-1",
		UserID:             user.ID,
		Update:             []byte{1, 2, 3},
		IntroducedAssetIDs: []string{},
	}
	first, inserted, err := repo.AppendBoardUpdate(firstInput)
	if err != nil || !inserted || first.ServerSequence != 1 {
		t.Fatalf("unexpected first append: update=%#v inserted=%v err=%v", first, inserted, err)
	}
	duplicate, inserted, err := repo.AppendBoardUpdate(firstInput)
	if err != nil || inserted || duplicate.ServerSequence != first.ServerSequence {
		t.Fatalf("duplicate append was not idempotent: update=%#v inserted=%v err=%v", duplicate, inserted, err)
	}
	conflicting := firstInput
	conflicting.Update = []byte{9, 9, 9}
	if _, _, err := repo.AppendBoardUpdate(conflicting); !errors.Is(err, ErrUpdateIDConflict) {
		t.Fatalf("reused update id with different content should conflict, got %v", err)
	}
	second, inserted, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID:            board.ID,
		UpdateID:           "update-2",
		ClientID:           "client-1",
		UserID:             user.ID,
		Update:             []byte{4, 5, 6},
		IntroducedAssetIDs: []string{},
	})
	if err != nil || !inserted || second.ServerSequence != 2 {
		t.Fatalf("unexpected second append: update=%#v inserted=%v err=%v", second, inserted, err)
	}

	document, err := repo.SaveBoardCheckpoint(board.ID, []byte{7, 8}, first.ServerSequence)
	if err != nil || document.CheckpointSequence != 1 {
		t.Fatalf("checkpoint failed: document=%#v err=%v", document, err)
	}
	document, updates, err := repo.LoadBoardDocument(board.ID)
	if err != nil || document.CheckpointSequence != 1 || len(updates) != 1 || updates[0].UpdateID != "update-2" {
		t.Fatalf("unexpected compacted document: document=%#v updates=%#v err=%v", document, updates, err)
	}
	compactedDuplicate, inserted, err := repo.AppendBoardUpdate(firstInput)
	if err != nil || inserted || compactedDuplicate.ServerSequence != 1 || compactedDuplicate.Update != nil {
		t.Fatalf("compacted receipt did not deduplicate resend: update=%#v inserted=%v err=%v", compactedDuplicate, inserted, err)
	}
	if _, err := repo.SaveBoardCheckpoint(board.ID, []byte{1}, 3); !errors.Is(err, ErrInvalidCheckpointSequence) {
		t.Fatalf("checkpoint beyond persisted sequence should fail, got %v", err)
	}
}

func TestAssetMetadataAndProjectCascade(t *testing.T) {
	repo := NewMemoryStore()
	user, err := repo.EnsureSystemAdmin("admin@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	project, err := repo.CreateProject("Project", "", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	asset, err := repo.SaveAsset(domain.Asset{
		ProjectID:   project.ID,
		UploadedBy:  user.ID,
		FileName:    "image.png",
		ContentType: "image/png",
		Size:        123,
		Path:        "/uploads/image.png",
		StorageKey:  "random-key.png",
		SHA256:      strings.Repeat("a", 64),
		Width:       640,
		Height:      480,
	})
	if err != nil {
		t.Fatal(err)
	}
	assets, err := repo.ListAssetsByProject(project.ID)
	if err != nil || len(assets) != 1 || assets[0].ID != asset.ID {
		t.Fatalf("unexpected asset list: %#v err=%v", assets, err)
	}
	if err := repo.DeleteProject(project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetAsset(asset.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("project deletion should remove asset metadata, got %v", err)
	}
}
