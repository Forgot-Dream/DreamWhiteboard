package store

import (
	"context"
	"testing"
	"time"

	"dreamwhiteboard/backend/internal/domain"
)

func TestMemoryStorageCleanupQueueLeaseAndRetry(t *testing.T) {
	repo := NewMemoryStore()
	user, err := repo.CreateUser("cleanup@example.test", "Cleanup", "cleanup-test-password", domain.SystemUser)
	if err != nil {
		t.Fatal(err)
	}
	project, err := repo.CreateProject("Cleanup", "", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	asset, err := repo.SaveAsset(domain.Asset{
		ProjectID:  project.ID,
		UploadedBy: user.ID,
		StorageKey: "asset.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteAsset(asset.ID); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Add(time.Second)
	firstLeaseUntil := now.Add(time.Minute)
	jobs, err := repo.ClaimStorageCleanupJobs(context.Background(), "lease-one", now, firstLeaseUntil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Kind != StorageCleanupAssetFile || jobs[0].ProjectID != project.ID || jobs[0].TargetKey != "asset.png" || jobs[0].AttemptCount != 1 {
		t.Fatalf("unexpected first claim: %#v", jobs)
	}
	if duplicate, err := repo.ClaimStorageCleanupJobs(context.Background(), "other-worker", now, now.Add(time.Minute), 10); err != nil || len(duplicate) != 0 {
		t.Fatalf("active lease was claimed twice: jobs=%#v err=%v", duplicate, err)
	}

	reclaimedAt := firstLeaseUntil.Add(time.Second)
	reclaimed, err := repo.ClaimStorageCleanupJobs(context.Background(), "lease-two", reclaimedAt, reclaimedAt.Add(time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed) != 1 || reclaimed[0].ID != jobs[0].ID || reclaimed[0].AttemptCount != 2 {
		t.Fatalf("expired lease was not reclaimed: %#v", reclaimed)
	}
	if completed, err := repo.CompleteStorageCleanupJob(context.Background(), jobs[0].ID, "lease-one"); err != nil || completed {
		t.Fatalf("stale lease completed reclaimed job: completed=%v err=%v", completed, err)
	}

	retryAt := reclaimedAt.Add(10 * time.Minute)
	if retried, err := repo.RetryStorageCleanupJob(context.Background(), jobs[0].ID, "lease-two", retryAt, "permission denied"); err != nil || !retried {
		t.Fatalf("retry cleanup job: retried=%v err=%v", retried, err)
	}
	if early, err := repo.ClaimStorageCleanupJobs(context.Background(), "lease-three", retryAt.Add(-time.Second), retryAt.Add(time.Minute), 10); err != nil || len(early) != 0 {
		t.Fatalf("job was available before retry time: jobs=%#v err=%v", early, err)
	}
	final, err := repo.ClaimStorageCleanupJobs(context.Background(), "lease-three", retryAt, retryAt.Add(time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(final) != 1 || final[0].AttemptCount != 3 || final[0].LastError != "permission denied" {
		t.Fatalf("unexpected retried job: %#v", final)
	}
	if completed, err := repo.CompleteStorageCleanupJob(context.Background(), final[0].ID, "lease-three"); err != nil || !completed {
		t.Fatalf("complete cleanup job: completed=%v err=%v", completed, err)
	}
}

func TestMemoryProjectDeletionQueuesDirectoryCleanup(t *testing.T) {
	repo := NewMemoryStore()
	user, err := repo.CreateUser("project-cleanup@example.test", "Cleanup", "cleanup-test-password", domain.SystemUser)
	if err != nil {
		t.Fatal(err)
	}
	project, err := repo.CreateProject("Cleanup", "", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveAsset(domain.Asset{ProjectID: project.ID, UploadedBy: user.ID, StorageKey: "one.png"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveAsset(domain.Asset{ProjectID: project.ID, UploadedBy: user.ID, StorageKey: "two.png"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteProject(project.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second)
	jobs, err := repo.ClaimStorageCleanupJobs(context.Background(), "project-lease", now, now.Add(time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Kind != StorageCleanupProjectDir || jobs[0].ProjectID != project.ID || jobs[0].TargetKey != "" {
		t.Fatalf("project deletion should enqueue one directory cleanup, got %#v", jobs)
	}
}
