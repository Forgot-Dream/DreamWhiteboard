package store

import (
	"errors"
	"sync"
	"testing"

	"dreamwhiteboard/backend/internal/domain"
)

func TestMemoryAssetReferenceLifecycle(t *testing.T) {
	repo := NewMemoryStore()
	user, err := repo.EnsureSystemAdmin("memory-asset-refs@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	project, err := repo.CreateProject("Asset references", "", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	board, err := repo.CreateBoard(project.ID, "Board", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	initial := repo.boardAssetReferenceStates[board.ID]
	if initial.IndexedThroughSequence != 0 || initial.RefsHash != hashAssetReferences([]string{}) || initial.Conflicted {
		t.Fatalf("new board reference state = %#v", initial)
	}
	referenced := saveMemoryReferenceTestAsset(t, repo, project.ID, user.ID, "referenced.png")
	unreferenced := saveMemoryReferenceTestAsset(t, repo, project.ID, user.ID, "unreferenced.png")

	base := int64(0)
	input := domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "manifest-update", ClientID: "client", UserID: user.ID,
		Update: []byte{1}, ReferenceBaseSequence: &base, AssetIDs: []string{referenced.ID, referenced.ID}, AssetManifestTrusted: true,
	}
	first, inserted, err := repo.AppendBoardUpdate(input)
	if err != nil || !inserted || first.ServerSequence != 1 || len(first.AssetIDs) != 1 || first.AssetIDs[0] != referenced.ID {
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
	foreign := saveMemoryReferenceTestAsset(t, repo, foreignProject.ID, user.ID, "foreign.png")
	invalid := domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "foreign-update", ClientID: "client", UserID: user.ID,
		Update: []byte{2}, ReferenceBaseSequence: &differentBase, AssetIDs: []string{foreign.ID}, AssetManifestTrusted: true,
	}
	if _, _, err := repo.AppendBoardUpdate(invalid); !errors.Is(err, ErrInvalidAssetReference) {
		t.Fatalf("cross-project update manifest = %v, want ErrInvalidAssetReference", err)
	}

	staleBase := int64(0)
	stale, inserted, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "stale-base", ClientID: "client", UserID: user.ID,
		Update: []byte{3}, ReferenceBaseSequence: &staleBase, AssetIDs: []string{referenced.ID},
	})
	if err != nil || !inserted || stale.ServerSequence != 2 {
		t.Fatalf("append stale-base update: update=%#v inserted=%v err=%v", stale, inserted, err)
	}
	if err := repo.DeleteAsset(unreferenced.ID); !errors.Is(err, ErrAssetReferenceIndexStale) {
		t.Fatalf("delete with stale board index = %v, want ErrAssetReferenceIndexStale", err)
	}
	if _, err := repo.SaveBoardAssetReferences(board.ID, user.ID, 2, nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("nil full manifest = %v, want ErrInvalidInput", err)
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

	conflictAsset := saveMemoryReferenceTestAsset(t, repo, project.ID, user.ID, "conflict.png")
	state, err := repo.SaveBoardAssetReferences(board.ID, user.ID, 2, []string{referenced.ID, conflictAsset.ID})
	if !errors.Is(err, ErrAssetReferenceConflict) || !state.Conflicted {
		t.Fatalf("same-sequence differing manifest: state=%#v err=%v", state, err)
	}
	if err := repo.DeleteAsset(conflictAsset.ID); !errors.Is(err, ErrAssetReferenceIndexStale) {
		t.Fatalf("delete while index conflicted = %v, want ErrAssetReferenceIndexStale", err)
	}

	recoveryBase := int64(2)
	if _, inserted, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "resolve-conflict", ClientID: "client", UserID: user.ID,
		Update: []byte{4}, ReferenceBaseSequence: &recoveryBase, AssetIDs: []string{referenced.ID}, AssetManifestTrusted: true,
	}); err != nil || !inserted {
		t.Fatalf("advance past conflicted index: inserted=%v err=%v", inserted, err)
	}
	if state := repo.boardAssetReferenceStates[board.ID]; state.Conflicted || state.IndexedThroughSequence != 3 {
		t.Fatalf("advanced manifest did not clear conflict: %#v", state)
	}
	if err := repo.DeleteAsset(conflictAsset.ID); err != nil {
		t.Fatalf("delete after conflict resolution: %v", err)
	}
}

func TestMemoryLegacyUpdateMakesAssetIndexStale(t *testing.T) {
	repo := NewMemoryStore()
	user, err := repo.EnsureSystemAdmin("memory-legacy-refs@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	project, err := repo.CreateProject("Legacy", "", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	board, err := repo.CreateBoard(project.ID, "Board", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	asset := saveMemoryReferenceTestAsset(t, repo, project.ID, user.ID, "legacy.png")
	if _, _, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "legacy", ClientID: "old-client", UserID: user.ID, Update: []byte{1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteAsset(asset.ID); !errors.Is(err, ErrAssetReferenceIndexStale) {
		t.Fatalf("legacy client must make index stale, got %v", err)
	}
}

func TestMemoryStaleManifestDoesNotRejectUnrelatedOfflineUpdate(t *testing.T) {
	repo := NewMemoryStore()
	user, _ := repo.EnsureSystemAdmin("memory-offline-refs@example.com", "password")
	project, _ := repo.CreateProject("Offline", "", user.ID)
	board, _ := repo.CreateBoard(project.ID, "Board", user.ID)
	asset := saveMemoryReferenceTestAsset(t, repo, project.ID, user.ID, "offline.png")

	base := int64(0)
	if _, _, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "add-image", ClientID: "manager", UserID: user.ID,
		Update: []byte{1}, ReferenceBaseSequence: &base, AssetIDs: []string{asset.ID}, AssetManifestTrusted: true,
	}); err != nil {
		t.Fatal(err)
	}
	base = 1
	if _, _, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "remove-image", ClientID: "manager", UserID: user.ID,
		Update: []byte{2}, ReferenceBaseSequence: &base, AssetIDs: []string{}, AssetManifestTrusted: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteAsset(asset.ID); err != nil {
		t.Fatalf("delete reconciled image asset: %v", err)
	}

	offlineBase := int64(1)
	update, inserted, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "offline-text", ClientID: "offline-editor", UserID: user.ID,
		Update: []byte{3}, ReferenceBaseSequence: &offlineBase, AssetIDs: []string{asset.ID},
	})
	if err != nil || !inserted || update.ServerSequence != 3 {
		t.Fatalf("stale manifest rejected unrelated offline update: update=%#v inserted=%v err=%v", update, inserted, err)
	}
}

func TestMemoryUntrustedEditorManifestCannotAuthorizeAssetDeletion(t *testing.T) {
	repo := NewMemoryStore()
	user, _ := repo.EnsureSystemAdmin("memory-untrusted-refs@example.com", "password")
	project, _ := repo.CreateProject("Untrusted", "", user.ID)
	board, _ := repo.CreateBoard(project.ID, "Board", user.ID)
	asset := saveMemoryReferenceTestAsset(t, repo, project.ID, user.ID, "protected.png")
	base := int64(0)
	if _, _, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "lying-editor", ClientID: "editor", UserID: user.ID,
		Update: []byte{1}, ReferenceBaseSequence: &base, AssetIDs: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteAsset(asset.ID); !errors.Is(err, ErrAssetReferenceIndexStale) {
		t.Fatalf("untrusted manifest authorized deletion: %v", err)
	}
}

func TestMemoryAssetDeleteAndUpdateAreAtomic(t *testing.T) {
	repo := NewMemoryStore()
	user, err := repo.EnsureSystemAdmin("memory-asset-race@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	project, err := repo.CreateProject("Locks", "", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	board, err := repo.CreateBoard(project.ID, "Board", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	asset := saveMemoryReferenceTestAsset(t, repo, project.ID, user.ID, "race.png")
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
			Update: []byte{1}, ReferenceBaseSequence: &base, AssetIDs: []string{asset.ID}, AssetManifestTrusted: true,
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
	if updateErr == nil && !errors.Is(deleteErr, ErrAssetInUse) {
		t.Fatalf("update won but delete error = %v, want ErrAssetInUse", deleteErr)
	}
	if deleteErr == nil && !errors.Is(updateErr, ErrInvalidAssetReference) {
		t.Fatalf("delete won but update error = %v, want ErrInvalidAssetReference", updateErr)
	}
}

func TestMemoryAssetReferencesCascadeWithBoardAndProject(t *testing.T) {
	repo := NewMemoryStore()
	user, err := repo.EnsureSystemAdmin("memory-reference-cascade@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	project, err := repo.CreateProject("Cascade", "", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	board, err := repo.CreateBoard(project.ID, "Board", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	asset := saveMemoryReferenceTestAsset(t, repo, project.ID, user.ID, "cascade.png")
	base := int64(0)
	if _, _, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "reference", ClientID: "client", UserID: user.ID,
		Update: []byte{1}, ReferenceBaseSequence: &base, AssetIDs: []string{asset.ID}, AssetManifestTrusted: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteBoard(board.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := repo.boardAssetReferenceStates[board.ID]; ok {
		t.Fatal("board deletion retained reference state")
	}
	if _, ok := repo.boardAssetReferences[board.ID]; ok {
		t.Fatal("board deletion retained asset references")
	}

	second, err := repo.CreateBoard(project.ID, "Second", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteProject(project.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := repo.boardAssetReferenceStates[second.ID]; ok {
		t.Fatal("project deletion retained board reference state")
	}
	if _, ok := repo.boardAssetReferences[second.ID]; ok {
		t.Fatal("project deletion retained board asset references")
	}
}

func saveMemoryReferenceTestAsset(t *testing.T, repo *MemoryStore, projectID, userID, storageKey string) domain.Asset {
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
