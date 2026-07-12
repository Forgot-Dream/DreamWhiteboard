package store

import (
	"errors"
	"sync"
	"testing"

	"dreamwhiteboard/backend/internal/domain"
)

func TestPostgresProtocolV4RequiresClaimsButAcceptsLegacyDuplicateIntegration(t *testing.T) {
	repo, _ := newMigratedPostgresTestStore(t)
	user := createPostgresTestUser(t, repo, "v4-claims")
	project, _ := repo.CreateProject("Protocol v4", "", user.ID)
	board, _ := repo.CreateBoard(project.ID, "Board", user.ID)
	legacyBase := int64(0)
	legacyInput := domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "legacy-v3", ClientID: "legacy-client", UserID: user.ID,
		Update: []byte{1, 2, 3}, ReferenceBaseSequence: &legacyBase, AssetIDs: []string{},
	}
	legacyHash := hashBoardUpdate(legacyInput.Update, legacyInput.ReferenceBaseSequence, legacyInput.AssetIDs, nil)
	if _, err := repo.db.Exec(`INSERT INTO board_updates
		(board_id, server_sequence, update_id, client_id, user_id, update_data, update_hash, created_at)
		VALUES ($1,1,$2,$3,$4,$5,$6,now())`,
		board.ID, legacyInput.UpdateID, legacyInput.ClientID, user.ID, legacyInput.Update, legacyHash); err != nil {
		t.Fatalf("seed protocol v3 receipt: %v", err)
	}

	duplicate, inserted, err := repo.AppendBoardUpdate(legacyInput)
	if err != nil || inserted || duplicate.ServerSequence != 1 || duplicate.IntroducedAssetIDs != nil {
		t.Fatalf("legacy duplicate after protocol upgrade: update=%#v inserted=%v err=%v", duplicate, inserted, err)
	}
	legacyInput.UpdateID = "new-legacy-v3"
	if _, _, err := repo.AppendBoardUpdate(legacyInput); !errors.Is(err, ErrAssetClaimsRequired) {
		t.Fatalf("new update without introduced_asset_ids = %v, want ErrAssetClaimsRequired", err)
	}
	var count int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM board_updates WHERE board_id=$1`, board.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("missing-claims update was persisted: update count=%d", count)
	}
}

func TestPostgresProtocolV4IntroducedClaimsParticipateInIdempotencyIntegration(t *testing.T) {
	repo, _ := newMigratedPostgresTestStore(t)
	user := createPostgresTestUser(t, repo, "v4-conflict")
	project, _ := repo.CreateProject("Claims conflict", "", user.ID)
	board, _ := repo.CreateBoard(project.ID, "Board", user.ID)
	firstAsset := savePostgresReferenceTestAsset(t, repo, project.ID, user.ID, "claims-first.png")
	secondAsset := savePostgresReferenceTestAsset(t, repo, project.ID, user.ID, "claims-second.png")
	base := int64(0)
	input := domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "same-update", ClientID: "client", UserID: user.ID,
		Update: []byte{4, 5, 6}, ReferenceBaseSequence: &base,
		AssetIDs: []string{firstAsset.ID, secondAsset.ID}, IntroducedAssetIDs: []string{firstAsset.ID},
	}
	if _, inserted, err := repo.AppendBoardUpdate(input); err != nil || !inserted {
		t.Fatalf("append first claims: inserted=%v err=%v", inserted, err)
	}
	_, updates, err := repo.LoadBoardDocument(board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || len(updates[0].IntroducedAssetIDs) != 1 || updates[0].IntroducedAssetIDs[0] != firstAsset.ID {
		t.Fatalf("introduced claims were not restored from PostgreSQL: %#v", updates)
	}
	input.IntroducedAssetIDs = []string{secondAsset.ID}
	if _, _, err := repo.AppendBoardUpdate(input); !errors.Is(err, ErrUpdateIDConflict) {
		t.Fatalf("same update ID with different introduced claims = %v, want ErrUpdateIDConflict", err)
	}
}

func TestPostgresProtocolV4AcceptsStaleEmptyClaimsIntegration(t *testing.T) {
	repo, _ := newMigratedPostgresTestStore(t)
	user := createPostgresTestUser(t, repo, "v4-stale-empty")
	project, _ := repo.CreateProject("Stale empty claims", "", user.ID)
	board, _ := repo.CreateBoard(project.ID, "Board", user.ID)
	base := int64(0)
	if _, _, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "advance", ClientID: "manager", UserID: user.ID,
		Update: []byte{1}, ReferenceBaseSequence: &base, AssetIDs: []string{},
		IntroducedAssetIDs: []string{}, AssetManifestTrusted: true,
	}); err != nil {
		t.Fatal(err)
	}
	update, inserted, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "stale-empty", ClientID: "offline", UserID: user.ID,
		Update: []byte{2}, ReferenceBaseSequence: &base, AssetIDs: []string{}, IntroducedAssetIDs: []string{},
	})
	if err != nil || !inserted || update.ServerSequence != 2 {
		t.Fatalf("stale empty claims: update=%#v inserted=%v err=%v", update, inserted, err)
	}
}

func TestPostgresUntrustedCurrentManifestMayContainDeletedAssetWithEmptyClaimsIntegration(t *testing.T) {
	repo, _ := newMigratedPostgresTestStore(t)
	user := createPostgresTestUser(t, repo, "v4-current-deleted")
	project, _ := repo.CreateProject("Current deleted manifest", "", user.ID)
	board, _ := repo.CreateBoard(project.ID, "Board", user.ID)
	asset := savePostgresReferenceTestAsset(t, repo, project.ID, user.ID, "deleted-before-text-update.png")
	if err := repo.DeleteAsset(asset.ID); err != nil {
		t.Fatalf("delete unreferenced asset: %v", err)
	}
	base := int64(0)
	update, inserted, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "unrelated-text", ClientID: "editor", UserID: user.ID,
		Update: []byte{1}, ReferenceBaseSequence: &base, AssetIDs: []string{asset.ID}, IntroducedAssetIDs: []string{},
	})
	if err != nil || !inserted || update.ServerSequence != 1 {
		t.Fatalf("untrusted unrelated update with deleted manifest asset: update=%#v inserted=%v err=%v", update, inserted, err)
	}
}

func TestPostgresAssetReferenceLifecycleIntegration(t *testing.T) {
	repo, _ := newMigratedPostgresTestStore(t)
	user := createPostgresTestUser(t, repo, "asset-refs")
	project, err := repo.CreateProject("Asset references", "", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	board, err := repo.CreateBoard(project.ID, "Board", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	referenced := savePostgresReferenceTestAsset(t, repo, project.ID, user.ID, "referenced.png")
	unreferenced := savePostgresReferenceTestAsset(t, repo, project.ID, user.ID, "unreferenced.png")

	base := int64(0)
	input := domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "manifest-update", ClientID: "client", UserID: user.ID,
		Update: []byte{1}, ReferenceBaseSequence: &base, AssetIDs: []string{referenced.ID, referenced.ID},
		IntroducedAssetIDs: []string{referenced.ID}, AssetManifestTrusted: true,
	}
	first, inserted, err := repo.AppendBoardUpdate(input)
	if err != nil || !inserted || first.ServerSequence != 1 {
		t.Fatalf("append manifest update: update=%#v inserted=%v err=%v", first, inserted, err)
	}
	input.AssetIDs = []string{referenced.ID}
	if duplicate, inserted, err := repo.AppendBoardUpdate(input); err != nil || inserted || duplicate.ServerSequence != 1 {
		t.Fatalf("canonical manifest duplicate: update=%#v inserted=%v err=%v", duplicate, inserted, err)
	}
	differentBase := int64(1)
	conflictingUpdateID := input
	conflictingUpdateID.ReferenceBaseSequence = &differentBase
	if _, _, err := repo.AppendBoardUpdate(conflictingUpdateID); !errors.Is(err, ErrUpdateIDConflict) {
		t.Fatalf("same update id with different manifest metadata: %v", err)
	}
	if err := repo.DeleteAsset(referenced.ID); !errors.Is(err, ErrAssetInUse) {
		t.Fatalf("referenced asset deletion = %v, want ErrAssetInUse", err)
	}

	foreignProject, err := repo.CreateProject("Foreign", "", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	foreign := savePostgresReferenceTestAsset(t, repo, foreignProject.ID, user.ID, "foreign.png")
	invalid := domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "foreign-update", ClientID: "client", UserID: user.ID,
		Update: []byte{2}, ReferenceBaseSequence: &differentBase, AssetIDs: []string{foreign.ID},
		IntroducedAssetIDs: []string{foreign.ID}, AssetManifestTrusted: true,
	}
	if _, _, err := repo.AppendBoardUpdate(invalid); !errors.Is(err, ErrInvalidAssetReference) {
		t.Fatalf("cross-project update manifest = %v, want ErrInvalidAssetReference", err)
	}

	staleBase := int64(0)
	stale, inserted, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "stale-base", ClientID: "client", UserID: user.ID,
		Update: []byte{3}, ReferenceBaseSequence: &staleBase, AssetIDs: []string{referenced.ID}, IntroducedAssetIDs: []string{},
	})
	if err != nil || !inserted || stale.ServerSequence != 2 {
		t.Fatalf("append stale-base update: update=%#v inserted=%v err=%v", stale, inserted, err)
	}
	if err := repo.DeleteAsset(unreferenced.ID); !errors.Is(err, ErrAssetReferenceIndexStale) {
		t.Fatalf("delete with stale board index = %v, want ErrAssetReferenceIndexStale", err)
	}
	if _, err := repo.SaveBoardAssetReferences(board.ID, user.ID, 2, []string{foreign.ID}); !errors.Is(err, ErrInvalidAssetReference) {
		t.Fatalf("cross-project full manifest = %v, want ErrInvalidAssetReference", err)
	}
	if _, err := repo.SaveBoardAssetReferences(board.ID, user.ID, 1, []string{referenced.ID}); !errors.Is(err, ErrAssetReferenceIndexStale) {
		t.Fatalf("outdated full manifest = %v, want ErrAssetReferenceIndexStale", err)
	}
	if _, err := repo.SaveBoardAssetReferences(board.ID, user.ID, 2, []string{referenced.ID}); err != nil {
		t.Fatalf("reconcile full manifest: %v", err)
	}
	if err := repo.DeleteAsset(unreferenced.ID); err != nil {
		t.Fatalf("delete fresh unreferenced asset: %v", err)
	}

	conflictAsset := savePostgresReferenceTestAsset(t, repo, project.ID, user.ID, "conflict.png")
	if _, err := repo.SaveBoardAssetReferences(board.ID, user.ID, 2, []string{referenced.ID, conflictAsset.ID}); !errors.Is(err, ErrAssetReferenceConflict) {
		t.Fatalf("same-sequence differing manifest = %v, want ErrAssetReferenceConflict", err)
	}
	if err := repo.DeleteAsset(conflictAsset.ID); !errors.Is(err, ErrAssetReferenceIndexStale) {
		t.Fatalf("delete while index conflicted = %v, want ErrAssetReferenceIndexStale", err)
	}
}

func TestPostgresClaimsOnlyUpdateMakesAssetIndexStaleIntegration(t *testing.T) {
	repo, _ := newMigratedPostgresTestStore(t)
	user := createPostgresTestUser(t, repo, "legacy-refs")
	project, _ := repo.CreateProject("Legacy", "", user.ID)
	board, _ := repo.CreateBoard(project.ID, "Board", user.ID)
	asset := savePostgresReferenceTestAsset(t, repo, project.ID, user.ID, "legacy.png")
	if _, _, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "claims-only", ClientID: "client", UserID: user.ID,
		Update: []byte{1}, IntroducedAssetIDs: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteAsset(asset.ID); !errors.Is(err, ErrAssetReferenceIndexStale) {
		t.Fatalf("legacy client must make index stale, got %v", err)
	}
}

func TestPostgresStaleManifestDoesNotRejectUnrelatedOfflineUpdateIntegration(t *testing.T) {
	repo, _ := newMigratedPostgresTestStore(t)
	user := createPostgresTestUser(t, repo, "offline-refs")
	project, _ := repo.CreateProject("Offline", "", user.ID)
	board, _ := repo.CreateBoard(project.ID, "Board", user.ID)
	asset := savePostgresReferenceTestAsset(t, repo, project.ID, user.ID, "offline.png")

	base := int64(0)
	if _, _, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "add-image", ClientID: "manager", UserID: user.ID,
		Update: []byte{1}, ReferenceBaseSequence: &base, AssetIDs: []string{asset.ID},
		IntroducedAssetIDs: []string{asset.ID}, AssetManifestTrusted: true,
	}); err != nil {
		t.Fatal(err)
	}
	base = 1
	if _, _, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "remove-image", ClientID: "manager", UserID: user.ID,
		Update: []byte{2}, ReferenceBaseSequence: &base, AssetIDs: []string{},
		IntroducedAssetIDs: []string{}, AssetManifestTrusted: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteAsset(asset.ID); err != nil {
		t.Fatalf("delete reconciled image asset: %v", err)
	}

	offlineBase := int64(1)
	update, inserted, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "offline-text", ClientID: "offline-editor", UserID: user.ID,
		Update: []byte{3}, ReferenceBaseSequence: &offlineBase, AssetIDs: []string{asset.ID}, IntroducedAssetIDs: []string{},
	})
	if err != nil || !inserted || update.ServerSequence != 3 {
		t.Fatalf("stale manifest rejected unrelated offline update: update=%#v inserted=%v err=%v", update, inserted, err)
	}
}

func TestPostgresUntrustedEditorManifestCannotAuthorizeAssetDeletionIntegration(t *testing.T) {
	repo, _ := newMigratedPostgresTestStore(t)
	user := createPostgresTestUser(t, repo, "untrusted-refs")
	project, _ := repo.CreateProject("Untrusted", "", user.ID)
	board, _ := repo.CreateBoard(project.ID, "Board", user.ID)
	asset := savePostgresReferenceTestAsset(t, repo, project.ID, user.ID, "protected.png")
	base := int64(0)
	if _, _, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "lying-editor", ClientID: "editor", UserID: user.ID,
		Update: []byte{1}, ReferenceBaseSequence: &base, AssetIDs: []string{}, IntroducedAssetIDs: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteAsset(asset.ID); !errors.Is(err, ErrAssetReferenceIndexStale) {
		t.Fatalf("untrusted manifest authorized deletion: %v", err)
	}
}

func TestPostgresFreshUntrustedManifestRejectsDeletedAssetIntegration(t *testing.T) {
	repo, _ := newMigratedPostgresTestStore(t)
	user := createPostgresTestUser(t, repo, "deleted-asset")
	project, _ := repo.CreateProject("Deleted asset", "", user.ID)
	board, _ := repo.CreateBoard(project.ID, "Board", user.ID)
	asset := savePostgresReferenceTestAsset(t, repo, project.ID, user.ID, "deleted-before-update.png")
	if err := repo.DeleteAsset(asset.ID); err != nil {
		t.Fatalf("delete unreferenced asset: %v", err)
	}
	base := int64(0)
	if _, _, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "reference-deleted", ClientID: "editor", UserID: user.ID,
		Update: []byte{1}, ReferenceBaseSequence: &base, AssetIDs: []string{asset.ID}, IntroducedAssetIDs: []string{asset.ID},
	}); !errors.Is(err, ErrInvalidAssetReference) {
		t.Fatalf("fresh editor manifest referencing a deleted asset = %v, want ErrInvalidAssetReference", err)
	}
}

func TestPostgresAssetDeleteAndEditorUpdateLockOrderingIntegration(t *testing.T) {
	repo, _ := newMigratedPostgresTestStore(t)
	user := createPostgresTestUser(t, repo, "asset-locks")
	project, _ := repo.CreateProject("Locks", "", user.ID)
	board, _ := repo.CreateBoard(project.ID, "Board", user.ID)
	asset := savePostgresReferenceTestAsset(t, repo, project.ID, user.ID, "race.png")
	base := int64(0)
	start := make(chan struct{})
	var updateErr, deleteErr error
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		_, _, updateErr = repo.AppendBoardUpdate(domain.BoardUpdate{
			BoardID: board.ID, UpdateID: "race", ClientID: "client", UserID: user.ID,
			Update: []byte{1}, ReferenceBaseSequence: &base, AssetIDs: []string{asset.ID},
			IntroducedAssetIDs: []string{asset.ID},
		})
	}()
	go func() {
		defer wait.Done()
		<-start
		deleteErr = repo.DeleteAsset(asset.ID)
	}()
	close(start)
	wait.Wait()
	if updateErr == nil && deleteErr == nil {
		t.Fatal("update and delete both committed")
	}
	if updateErr == nil && !errors.Is(deleteErr, ErrAssetReferenceIndexStale) {
		t.Fatalf("editor update won but delete error = %v, want ErrAssetReferenceIndexStale", deleteErr)
	}
	if deleteErr == nil && !errors.Is(updateErr, ErrInvalidAssetReference) {
		t.Fatalf("delete won but update error = %v, want ErrInvalidAssetReference", updateErr)
	}
}

func savePostgresReferenceTestAsset(t *testing.T, repo *PostgresStore, projectID, userID, storageKey string) domain.Asset {
	t.Helper()
	asset, err := repo.SaveAsset(domain.Asset{
		ProjectID: projectID, UploadedBy: userID, FileName: storageKey,
		ContentType: "image/png", StorageKey: storageKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	return asset
}
