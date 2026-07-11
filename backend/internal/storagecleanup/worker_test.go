package storagecleanup

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dreamwhiteboard/backend/internal/domain"
	"dreamwhiteboard/backend/internal/store"
)

func TestWorkerRetriesAssetCleanupThenSucceeds(t *testing.T) {
	repo, project, asset := cleanupFixture(t, "retry.png")
	root := t.TempDir()
	projectDir := filepath.Join(root, project.ID)
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, asset.StorageKey)
	if err := os.WriteFile(path, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteAsset(asset.ID); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Add(time.Second)
	removeAttempts := 0
	leaseNumber := 0
	cfg := testWorkerConfig(root, &now)
	cfg.RemoveFile = func(path string) error {
		removeAttempts++
		if removeAttempts == 1 {
			return os.ErrPermission
		}
		return os.Remove(path)
	}
	cfg.NewLeaseToken = func() (string, error) {
		leaseNumber++
		return "lease-" + string(rune('0'+leaseNumber)), nil
	}
	worker, err := New(repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := worker.RunOnce(context.Background()); err != nil || processed != 1 {
		t.Fatalf("first cleanup attempt: processed=%d err=%v", processed, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("failed cleanup should leave file for retry: %v", err)
	}
	now = now.Add(cfg.MinBackoff - time.Second)
	if processed, err := worker.RunOnce(context.Background()); err != nil || processed != 0 {
		t.Fatalf("cleanup ran before backoff elapsed: processed=%d err=%v", processed, err)
	}
	now = now.Add(time.Second)
	if processed, err := worker.RunOnce(context.Background()); err != nil || processed != 1 {
		t.Fatalf("second cleanup attempt: processed=%d err=%v", processed, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup did not remove file: %v", err)
	}
	if removeAttempts != 2 {
		t.Fatalf("remove attempts = %d, want 2", removeAttempts)
	}
}

func TestWorkerRecoversAfterDeleteBeforeAcknowledgement(t *testing.T) {
	repo, project, asset := cleanupFixture(t, "crash.png")
	root := t.TempDir()
	projectDir := filepath.Join(root, project.ID)
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, asset.StorageKey)
	if err := os.WriteFile(path, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteAsset(asset.ID); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Add(time.Second)
	leaseUntil := now.Add(time.Minute)
	claimed, err := repo.ClaimStorageCleanupJobs(context.Background(), "crashed-worker", now, leaseUntil, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim cleanup job: jobs=%#v err=%v", claimed, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// Simulate a process crash by deliberately not acknowledging the leased job.
	now = leaseUntil.Add(time.Second)
	cfg := testWorkerConfig(root, &now)
	worker, err := New(repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := worker.RunOnce(context.Background()); err != nil || processed != 1 {
		t.Fatalf("recover cleanup receipt: processed=%d err=%v", processed, err)
	}
	if jobs, err := repo.ClaimStorageCleanupJobs(context.Background(), "verification", now.Add(time.Hour), now.Add(2*time.Hour), 1); err != nil || len(jobs) != 0 {
		t.Fatalf("completed receipt remained queued: jobs=%#v err=%v", jobs, err)
	}
}

func TestWorkerRemovesProjectDirectoryRecursively(t *testing.T) {
	repo, project, _ := cleanupFixture(t, "nested.png")
	root := t.TempDir()
	nested := filepath.Join(root, project.ID, "nested", "orphan.bin")
	if err := os.MkdirAll(filepath.Dir(nested), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteProject(project.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second)
	worker, err := New(repo, testWorkerConfig(root, &now))
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := worker.RunOnce(context.Background()); err != nil || processed != 1 {
		t.Fatalf("project cleanup: processed=%d err=%v", processed, err)
	}
	if _, err := os.Stat(filepath.Join(root, project.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("project directory survived cleanup: %v", err)
	}
}

func cleanupFixture(t *testing.T, storageKey string) (*store.MemoryStore, domain.Project, domain.Asset) {
	t.Helper()
	repo := store.NewMemoryStore()
	user, err := repo.CreateUser("worker@example.test", "Worker", "worker-test-password", domain.SystemUser)
	if err != nil {
		t.Fatal(err)
	}
	project, err := repo.CreateProject("Worker", "", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	asset, err := repo.SaveAsset(domain.Asset{ProjectID: project.ID, UploadedBy: user.ID, StorageKey: storageKey})
	if err != nil {
		t.Fatal(err)
	}
	return repo, project, asset
}

func testWorkerConfig(root string, now *time.Time) Config {
	cfg := DefaultConfig(root)
	cfg.Now = func() time.Time { return *now }
	cfg.MinBackoff = 10 * time.Second
	cfg.MaxBackoff = time.Minute
	cfg.Lease = time.Minute
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg.NewLeaseToken = func() (string, error) { return "worker-lease", nil }
	return cfg
}
