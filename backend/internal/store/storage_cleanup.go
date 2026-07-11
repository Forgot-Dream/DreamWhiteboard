package store

import (
	"context"
	"time"
)

type StorageCleanupKind string

const (
	StorageCleanupAssetFile  StorageCleanupKind = "asset_file"
	StorageCleanupProjectDir StorageCleanupKind = "project_dir"
)

type StorageCleanupJob struct {
	ID           int64
	Kind         StorageCleanupKind
	ProjectID    string
	TargetKey    string
	AttemptCount int
	AvailableAt  time.Time
	LeaseToken   string
	LeaseUntil   *time.Time
	LastError    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// StorageCleanupQueue provides an at-least-once queue for deleting files after
// the corresponding metadata deletion has committed. Lease tokens prevent a
// stale worker from acknowledging a job that has already been reclaimed.
type StorageCleanupQueue interface {
	ClaimStorageCleanupJobs(ctx context.Context, leaseToken string, now, leaseUntil time.Time, limit int) ([]StorageCleanupJob, error)
	CompleteStorageCleanupJob(ctx context.Context, id int64, leaseToken string) (bool, error)
	RetryStorageCleanupJob(ctx context.Context, id int64, leaseToken string, availableAt time.Time, lastError string) (bool, error)
}

var (
	_ StorageCleanupQueue = (*MemoryStore)(nil)
	_ StorageCleanupQueue = (*PostgresStore)(nil)
)
