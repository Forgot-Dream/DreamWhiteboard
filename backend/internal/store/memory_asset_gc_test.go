package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"dreamwhiteboard/backend/internal/domain"
)

func TestMemoryAssetGCCandidateGraceAndFinalizeOutbox(t *testing.T) {
	repo, user, project, _ := newMemoryAssetGCFixture(t, "lifecycle")
	asset := saveMemoryReferenceTestAsset(t, repo, project.ID, user.ID, "gc-lifecycle.png")
	now := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	grace := time.Hour

	if deleted, err := repo.SweepOrphanedAssets(context.Background(), now, grace, 100); err != nil || deleted != 0 {
		t.Fatalf("create candidate: deleted=%d err=%v", deleted, err)
	}
	candidate, ok := repo.assetGCCandidates[asset.ID]
	if !ok || !candidate.DetectedAt.Equal(now) || !candidate.EligibleAt.Equal(now.Add(grace)) {
		t.Fatalf("unexpected candidate: %#v exists=%v", candidate, ok)
	}
	if deleted, err := repo.SweepOrphanedAssets(context.Background(), now.Add(grace-time.Second), grace, 100); err != nil || deleted != 0 {
		t.Fatalf("sweep before grace: deleted=%d err=%v", deleted, err)
	}
	if _, err := repo.GetAsset(asset.ID); err != nil {
		t.Fatalf("asset was removed before grace elapsed: %v", err)
	}

	due := now.Add(grace)
	if deleted, err := repo.SweepOrphanedAssets(context.Background(), due, grace, 100); err != nil || deleted != 1 {
		t.Fatalf("finalize due candidate: deleted=%d err=%v", deleted, err)
	}
	if _, err := repo.GetAsset(asset.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("finalized asset metadata = %v, want ErrNotFound", err)
	}
	if _, ok := repo.assetGCCandidates[asset.ID]; ok {
		t.Fatal("finalized candidate remained tracked")
	}
	jobs, err := repo.ClaimStorageCleanupJobs(context.Background(), "gc-finalize", due, due.Add(time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Kind != StorageCleanupAssetFile || jobs[0].ProjectID != project.ID || jobs[0].TargetKey != asset.StorageKey {
		t.Fatalf("finalize did not atomically enqueue asset cleanup: %#v", jobs)
	}
}

func TestMemoryAssetGCCandidateCancelledByIntroducedReference(t *testing.T) {
	repo, user, project, board := newMemoryAssetGCFixture(t, "cancel")
	asset := saveMemoryReferenceTestAsset(t, repo, project.ID, user.ID, "gc-cancel.png")
	now := time.Date(2026, 7, 12, 2, 0, 0, 0, time.UTC)
	grace := time.Hour
	if _, err := repo.SweepOrphanedAssets(context.Background(), now, grace, 100); err != nil {
		t.Fatal(err)
	}
	if _, ok := repo.assetGCCandidates[asset.ID]; !ok {
		t.Fatal("orphan candidate was not created")
	}

	base := int64(0)
	if _, inserted, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "introduce-candidate", ClientID: "editor", UserID: user.ID,
		Update: []byte{1}, ReferenceBaseSequence: &base, AssetIDs: []string{asset.ID},
		IntroducedAssetIDs: []string{asset.ID},
	}); err != nil || !inserted {
		t.Fatalf("introduce candidate asset: inserted=%v err=%v", inserted, err)
	}
	if _, ok := repo.assetGCCandidates[asset.ID]; ok {
		t.Fatal("introduced reference did not cancel candidate")
	}
	if _, err := repo.SaveBoardAssetReferences(board.ID, user.ID, 1, []string{asset.ID}); err != nil {
		t.Fatalf("reconcile introduced reference: %v", err)
	}
	if deleted, err := repo.SweepOrphanedAssets(context.Background(), now.Add(2*grace), grace, 100); err != nil || deleted != 0 {
		t.Fatalf("referenced candidate sweep: deleted=%d err=%v", deleted, err)
	}
	if _, err := repo.GetAsset(asset.ID); err != nil {
		t.Fatalf("referenced asset was collected: %v", err)
	}
}

func TestMemoryAssetGCStaleIndexResetsGrace(t *testing.T) {
	repo, user, project, board := newMemoryAssetGCFixture(t, "stale-reset")
	asset := saveMemoryReferenceTestAsset(t, repo, project.ID, user.ID, "gc-stale-reset.png")
	now := time.Date(2026, 7, 12, 3, 0, 0, 0, time.UTC)
	grace := time.Hour
	if _, err := repo.SweepOrphanedAssets(context.Background(), now, grace, 100); err != nil {
		t.Fatal(err)
	}

	if _, _, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "make-stale", ClientID: "editor", UserID: user.ID,
		Update: []byte{1}, IntroducedAssetIDs: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := repo.assetGCCandidates[asset.ID]; ok {
		t.Fatal("stale update did not clear the project candidate")
	}
	oldDue := now.Add(grace)
	if deleted, err := repo.SweepOrphanedAssets(context.Background(), oldDue, grace, 100); err != nil || deleted != 0 {
		t.Fatalf("stale finalizer recheck: deleted=%d err=%v", deleted, err)
	}
	if _, err := repo.GetAsset(asset.ID); err != nil {
		t.Fatalf("stale reference state allowed collection: %v", err)
	}

	if _, err := repo.SaveBoardAssetReferences(board.ID, user.ID, 1, []string{}); err != nil {
		t.Fatalf("restore fresh empty index: %v", err)
	}
	restartedAt := oldDue.Add(time.Minute)
	if deleted, err := repo.SweepOrphanedAssets(context.Background(), restartedAt, grace, 100); err != nil || deleted != 0 {
		t.Fatalf("restart candidate: deleted=%d err=%v", deleted, err)
	}
	candidate, ok := repo.assetGCCandidates[asset.ID]
	if !ok || !candidate.DetectedAt.Equal(restartedAt) || !candidate.EligibleAt.Equal(restartedAt.Add(grace)) {
		t.Fatalf("grace was not restarted after stale reset: %#v exists=%v", candidate, ok)
	}
	if deleted, err := repo.SweepOrphanedAssets(context.Background(), restartedAt.Add(grace-time.Second), grace, 100); err != nil || deleted != 0 {
		t.Fatalf("asset deleted using old grace: deleted=%d err=%v", deleted, err)
	}
	if deleted, err := repo.SweepOrphanedAssets(context.Background(), restartedAt.Add(grace), grace, 100); err != nil || deleted != 1 {
		t.Fatalf("asset not deleted after restarted grace: deleted=%d err=%v", deleted, err)
	}
}

func TestMemoryAssetGCFinalizerRechecksStaleIndex(t *testing.T) {
	repo, user, project, board := newMemoryAssetGCFixture(t, "finalizer-stale")
	asset := saveMemoryReferenceTestAsset(t, repo, project.ID, user.ID, "gc-finalizer-stale.png")
	now := time.Date(2026, 7, 12, 3, 30, 0, 0, time.UTC)
	grace := time.Hour
	if _, err := repo.SweepOrphanedAssets(context.Background(), now, grace, 100); err != nil {
		t.Fatal(err)
	}
	// Model a candidate left behind across a crash while the opaque document
	// advanced. The finalizer must independently recheck freshness rather than
	// trusting candidate creation-time state.
	repo.boardUpdates[board.ID] = append(repo.boardUpdates[board.ID], domain.BoardUpdate{
		BoardID: board.ID, ServerSequence: 1, UpdateID: "out-of-band-stale",
		ClientID: "client", UserID: user.ID, Update: []byte{1}, CreatedAt: now.Add(time.Minute),
	})
	if _, ok := repo.assetGCCandidates[asset.ID]; !ok {
		t.Fatal("test setup lost candidate before finalizer recheck")
	}
	if deleted, err := repo.SweepOrphanedAssets(context.Background(), now.Add(grace), grace, 100); err != nil || deleted != 0 {
		t.Fatalf("stale finalizer recheck: deleted=%d err=%v", deleted, err)
	}
	if _, ok := repo.assetGCCandidates[asset.ID]; ok {
		t.Fatal("stale finalizer did not reset candidate")
	}
	if _, err := repo.GetAsset(asset.ID); err != nil {
		t.Fatalf("stale finalizer deleted asset metadata: %v", err)
	}
}

func TestMemoryAssetGCConflictClearsCandidate(t *testing.T) {
	repo, user, project, board := newMemoryAssetGCFixture(t, "conflict")
	asset := saveMemoryReferenceTestAsset(t, repo, project.ID, user.ID, "gc-conflict.png")
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	grace := time.Hour
	if _, err := repo.SweepOrphanedAssets(context.Background(), now, grace, 100); err != nil {
		t.Fatal(err)
	}
	state, err := repo.SaveBoardAssetReferences(board.ID, user.ID, 0, []string{asset.ID})
	if !errors.Is(err, ErrAssetReferenceConflict) || !state.Conflicted {
		t.Fatalf("create same-sequence conflict: state=%#v err=%v", state, err)
	}
	if _, ok := repo.assetGCCandidates[asset.ID]; ok {
		t.Fatal("reference conflict did not clear candidate")
	}
	if deleted, err := repo.SweepOrphanedAssets(context.Background(), now.Add(grace), grace, 100); err != nil || deleted != 0 {
		t.Fatalf("conflicted finalizer recheck: deleted=%d err=%v", deleted, err)
	}
	if _, err := repo.GetAsset(asset.ID); err != nil {
		t.Fatalf("conflicted reference state allowed collection: %v", err)
	}
}

func TestMemoryAssetGCStaleProjectDoesNotStarveFreshProject(t *testing.T) {
	repo := NewMemoryStore()
	user, err := repo.EnsureSystemAdmin("memory-gc-fairness@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	staleProject, _ := repo.CreateProject("Stale GC project", "", user.ID)
	staleBoard, _ := repo.CreateBoard(staleProject.ID, "Board", user.ID)
	staleAsset := saveMemoryReferenceTestAsset(t, repo, staleProject.ID, user.ID, "stale-old.png")
	if _, _, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: staleBoard.ID, UpdateID: "make-project-stale", ClientID: "editor", UserID: user.ID,
		Update: []byte{1}, IntroducedAssetIDs: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	freshProject, _ := repo.CreateProject("Fresh GC project", "", user.ID)
	if _, err := repo.CreateBoard(freshProject.ID, "Board", user.ID); err != nil {
		t.Fatal(err)
	}
	freshAsset := saveMemoryReferenceTestAsset(t, repo, freshProject.ID, user.ID, "fresh-new.png")
	stale := repo.assets[staleAsset.ID]
	stale.CreatedAt = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	repo.assets[staleAsset.ID] = stale
	fresh := repo.assets[freshAsset.ID]
	fresh.CreatedAt = time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	repo.assets[freshAsset.ID] = fresh

	now := time.Date(2026, 7, 12, 7, 0, 0, 0, time.UTC)
	if deleted, err := repo.SweepOrphanedAssets(context.Background(), now, time.Hour, 1); err != nil || deleted != 0 {
		t.Fatalf("fairness sweep: deleted=%d err=%v", deleted, err)
	}
	if _, ok := repo.assetGCCandidates[freshAsset.ID]; !ok {
		t.Fatal("stale project's older asset exhausted the batch before the fresh project")
	}
	if _, ok := repo.assetGCCandidates[staleAsset.ID]; ok {
		t.Fatal("stale project received a GC candidate")
	}
}

func newMemoryAssetGCFixture(t *testing.T, suffix string) (*MemoryStore, domain.User, domain.Project, domain.Board) {
	t.Helper()
	repo := NewMemoryStore()
	user, err := repo.EnsureSystemAdmin("memory-gc-"+suffix+"@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	project, err := repo.CreateProject("Asset GC "+suffix, "", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	board, err := repo.CreateBoard(project.ID, "Board", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return repo, user, project, board
}
