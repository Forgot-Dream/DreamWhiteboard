package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"dreamwhiteboard/backend/internal/domain"
)

func TestPostgresAssetGCCandidateGraceAndFinalizeOutboxIntegration(t *testing.T) {
	repo, user, project, _ := newPostgresAssetGCFixture(t, "lifecycle")
	asset := savePostgresReferenceTestAsset(t, repo, project.ID, user.ID, "gc-lifecycle.png")
	now := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	grace := time.Hour

	if deleted, err := repo.SweepOrphanedAssets(context.Background(), now, grace, 100); err != nil || deleted != 0 {
		t.Fatalf("create candidate: deleted=%d err=%v", deleted, err)
	}
	var detectedAt, eligibleAt time.Time
	if err := repo.db.QueryRow(`SELECT detected_at, eligible_at FROM asset_gc_candidates WHERE asset_id=$1`, asset.ID).Scan(&detectedAt, &eligibleAt); err != nil {
		t.Fatalf("load candidate: %v", err)
	}
	if !detectedAt.Equal(now) || !eligibleAt.Equal(now.Add(grace)) {
		t.Fatalf("candidate times = (%v, %v), want (%v, %v)", detectedAt, eligibleAt, now, now.Add(grace))
	}
	if deleted, err := repo.SweepOrphanedAssets(context.Background(), now.Add(grace-time.Second), grace, 100); err != nil || deleted != 0 {
		t.Fatalf("sweep before grace: deleted=%d err=%v", deleted, err)
	}
	if _, err := repo.GetAsset(asset.ID); err != nil {
		t.Fatalf("asset was removed before grace elapsed: %v", err)
	}

	if deleted, err := repo.SweepOrphanedAssets(context.Background(), now.Add(grace), grace, 100); err != nil || deleted != 1 {
		t.Fatalf("finalize due candidate: deleted=%d err=%v", deleted, err)
	}
	if _, err := repo.GetAsset(asset.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("finalized asset metadata = %v, want ErrNotFound", err)
	}
	var candidateCount, cleanupCount int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM asset_gc_candidates WHERE asset_id=$1`, asset.ID).Scan(&candidateCount); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM storage_cleanup_jobs
		WHERE kind=$1 AND project_id=$2 AND target_key=$3`, StorageCleanupAssetFile, project.ID, asset.StorageKey).Scan(&cleanupCount); err != nil {
		t.Fatal(err)
	}
	if candidateCount != 0 || cleanupCount != 1 {
		t.Fatalf("finalize transaction state: candidate_count=%d cleanup_count=%d", candidateCount, cleanupCount)
	}
}

func TestPostgresAssetGCCandidateCancelledByIntroducedReferenceIntegration(t *testing.T) {
	repo, user, project, board := newPostgresAssetGCFixture(t, "cancel")
	asset := savePostgresReferenceTestAsset(t, repo, project.ID, user.ID, "gc-cancel.png")
	now := time.Date(2026, 7, 12, 2, 0, 0, 0, time.UTC)
	grace := time.Hour
	if _, err := repo.SweepOrphanedAssets(context.Background(), now, grace, 100); err != nil {
		t.Fatal(err)
	}
	if count := postgresAssetGCCandidateCount(t, repo, project.ID); count != 1 {
		t.Fatalf("candidate count=%d, want 1", count)
	}

	base := int64(0)
	if _, inserted, err := repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: board.ID, UpdateID: "introduce-candidate", ClientID: "editor", UserID: user.ID,
		Update: []byte{1}, ReferenceBaseSequence: &base, AssetIDs: []string{asset.ID},
		IntroducedAssetIDs: []string{asset.ID},
	}); err != nil || !inserted {
		t.Fatalf("introduce candidate asset: inserted=%v err=%v", inserted, err)
	}
	if count := postgresAssetGCCandidateCount(t, repo, project.ID); count != 0 {
		t.Fatalf("introduced reference left %d candidates", count)
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

func TestPostgresAssetGCStaleIndexResetsGraceIntegration(t *testing.T) {
	repo, user, project, board := newPostgresAssetGCFixture(t, "stale-reset")
	asset := savePostgresReferenceTestAsset(t, repo, project.ID, user.ID, "gc-stale-reset.png")
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
	if count := postgresAssetGCCandidateCount(t, repo, project.ID); count != 0 {
		t.Fatalf("stale update left %d candidates", count)
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
	var detectedAt, eligibleAt time.Time
	if err := repo.db.QueryRow(`SELECT detected_at, eligible_at FROM asset_gc_candidates WHERE asset_id=$1`, asset.ID).Scan(&detectedAt, &eligibleAt); err != nil {
		t.Fatal(err)
	}
	if !detectedAt.Equal(restartedAt) || !eligibleAt.Equal(restartedAt.Add(grace)) {
		t.Fatalf("grace was not restarted: detected=%v eligible=%v", detectedAt, eligibleAt)
	}
	if deleted, err := repo.SweepOrphanedAssets(context.Background(), restartedAt.Add(grace-time.Second), grace, 100); err != nil || deleted != 0 {
		t.Fatalf("asset deleted using old grace: deleted=%d err=%v", deleted, err)
	}
	if deleted, err := repo.SweepOrphanedAssets(context.Background(), restartedAt.Add(grace), grace, 100); err != nil || deleted != 1 {
		t.Fatalf("asset not deleted after restarted grace: deleted=%d err=%v", deleted, err)
	}
}

func TestPostgresAssetGCFinalizerRechecksStaleIndexIntegration(t *testing.T) {
	repo, user, project, board := newPostgresAssetGCFixture(t, "finalizer-stale")
	asset := savePostgresReferenceTestAsset(t, repo, project.ID, user.ID, "gc-finalizer-stale.png")
	now := time.Date(2026, 7, 12, 3, 30, 0, 0, time.UTC)
	grace := time.Hour
	if _, err := repo.SweepOrphanedAssets(context.Background(), now, grace, 100); err != nil {
		t.Fatal(err)
	}
	update := []byte{1}
	if _, err := repo.db.Exec(`INSERT INTO board_updates
		(board_id, server_sequence, update_id, client_id, user_id, update_data, update_hash, introduced_asset_ids, created_at)
		VALUES ($1,1,'out-of-band-stale','client',$2,$3,$4,'{}'::text[],$5)`,
		board.ID, user.ID, update, hashBoardUpdate(update, nil, nil, []string{}), now.Add(time.Minute)); err != nil {
		t.Fatalf("advance document without candidate reset: %v", err)
	}
	if count := postgresAssetGCCandidateCount(t, repo, project.ID); count != 1 {
		t.Fatalf("test setup candidate count=%d, want 1", count)
	}
	if deleted, err := repo.SweepOrphanedAssets(context.Background(), now.Add(grace), grace, 100); err != nil || deleted != 0 {
		t.Fatalf("stale finalizer recheck: deleted=%d err=%v", deleted, err)
	}
	if count := postgresAssetGCCandidateCount(t, repo, project.ID); count != 0 {
		t.Fatalf("stale finalizer left %d candidates", count)
	}
	if _, err := repo.GetAsset(asset.ID); err != nil {
		t.Fatalf("stale finalizer deleted asset metadata: %v", err)
	}
}

func TestPostgresAssetGCConflictClearsCandidateIntegration(t *testing.T) {
	repo, user, project, board := newPostgresAssetGCFixture(t, "conflict")
	asset := savePostgresReferenceTestAsset(t, repo, project.ID, user.ID, "gc-conflict.png")
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	grace := time.Hour
	if _, err := repo.SweepOrphanedAssets(context.Background(), now, grace, 100); err != nil {
		t.Fatal(err)
	}
	state, err := repo.SaveBoardAssetReferences(board.ID, user.ID, 0, []string{asset.ID})
	if !errors.Is(err, ErrAssetReferenceConflict) || !state.Conflicted {
		t.Fatalf("create same-sequence conflict: state=%#v err=%v", state, err)
	}
	if count := postgresAssetGCCandidateCount(t, repo, project.ID); count != 0 {
		t.Fatalf("reference conflict left %d candidates", count)
	}
	if deleted, err := repo.SweepOrphanedAssets(context.Background(), now.Add(grace), grace, 100); err != nil || deleted != 0 {
		t.Fatalf("conflicted finalizer recheck: deleted=%d err=%v", deleted, err)
	}
	if _, err := repo.GetAsset(asset.ID); err != nil {
		t.Fatalf("conflicted reference state allowed collection: %v", err)
	}
}

func TestPostgresAssetGCStaleProjectDoesNotStarveFreshProjectIntegration(t *testing.T) {
	repo, _ := newMigratedPostgresTestStore(t)
	user := createPostgresTestUser(t, repo, "asset-gc-fairness")
	staleProject, _ := repo.CreateProject("Stale GC project", "", user.ID)
	staleBoard, _ := repo.CreateBoard(staleProject.ID, "Board", user.ID)
	staleAsset := savePostgresReferenceTestAsset(t, repo, staleProject.ID, user.ID, "gc-stale-old.png")
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
	freshAsset := savePostgresReferenceTestAsset(t, repo, freshProject.ID, user.ID, "gc-fresh-new.png")
	if _, err := repo.db.Exec(`UPDATE assets SET created_at=$2 WHERE id=$1`, staleAsset.ID,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`UPDATE assets SET created_at=$2 WHERE id=$1`, freshAsset.ID,
		time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 12, 7, 0, 0, 0, time.UTC)
	if deleted, err := repo.SweepOrphanedAssets(context.Background(), now, time.Hour, 1); err != nil || deleted != 0 {
		t.Fatalf("fairness sweep: deleted=%d err=%v", deleted, err)
	}
	var freshCandidate, staleCandidate int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FILTER (WHERE asset_id=$1), COUNT(*) FILTER (WHERE asset_id=$2)
		FROM asset_gc_candidates`, freshAsset.ID, staleAsset.ID).Scan(&freshCandidate, &staleCandidate); err != nil {
		t.Fatal(err)
	}
	if freshCandidate != 1 || staleCandidate != 0 {
		t.Fatalf("candidate fairness: fresh=%d stale=%d", freshCandidate, staleCandidate)
	}
}

func TestPostgresAssetGCAndIntroducedUpdateLockOrderingIntegration(t *testing.T) {
	repo, user, project, board := newPostgresAssetGCFixture(t, "locks")
	asset := savePostgresReferenceTestAsset(t, repo, project.ID, user.ID, "gc-locks.png")
	now := time.Date(2026, 7, 12, 5, 0, 0, 0, time.UTC)
	grace := time.Hour
	if _, err := repo.SweepOrphanedAssets(context.Background(), now, grace, 100); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	var updateErr, sweepErr error
	var deleted int
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		base := int64(0)
		_, _, updateErr = repo.AppendBoardUpdate(domain.BoardUpdate{
			BoardID: board.ID, UpdateID: "gc-race", ClientID: "editor", UserID: user.ID,
			Update: []byte{1}, ReferenceBaseSequence: &base, AssetIDs: []string{asset.ID},
			IntroducedAssetIDs: []string{asset.ID},
		})
	}()
	go func() {
		defer wait.Done()
		<-start
		deleted, sweepErr = repo.SweepOrphanedAssets(context.Background(), now.Add(grace), grace, 100)
	}()
	close(start)
	done := make(chan struct{})
	go func() {
		wait.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("asset GC and update did not complete; possible lock-order deadlock")
	}
	if sweepErr != nil {
		t.Fatalf("concurrent GC sweep: %v", sweepErr)
	}
	switch {
	case updateErr == nil:
		if deleted != 0 {
			t.Fatalf("introduced update committed but GC deleted %d assets", deleted)
		}
		if _, err := repo.GetAsset(asset.ID); err != nil {
			t.Fatalf("introduced update won but asset is missing: %v", err)
		}
	case errors.Is(updateErr, ErrInvalidAssetReference):
		if deleted != 1 {
			t.Fatalf("GC won but deleted=%d, want 1", deleted)
		}
		if _, err := repo.GetAsset(asset.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GC won but asset lookup = %v, want ErrNotFound", err)
		}
	default:
		t.Fatalf("concurrent introduced update returned %v", updateErr)
	}
}

func newPostgresAssetGCFixture(t *testing.T, suffix string) (*PostgresStore, domain.User, domain.Project, domain.Board) {
	t.Helper()
	repo, _ := newMigratedPostgresTestStore(t)
	user := createPostgresTestUser(t, repo, "asset-gc-"+suffix)
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

func postgresAssetGCCandidateCount(t *testing.T, repo *PostgresStore, projectID string) int {
	t.Helper()
	var count int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM asset_gc_candidates WHERE project_id=$1`, projectID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
