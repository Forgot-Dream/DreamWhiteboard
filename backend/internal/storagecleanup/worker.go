package storagecleanup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"dreamwhiteboard/backend/internal/filestore"
	"dreamwhiteboard/backend/internal/store"
)

type Config struct {
	UploadDir     string
	Interval      time.Duration
	Lease         time.Duration
	MinBackoff    time.Duration
	MaxBackoff    time.Duration
	BatchSize     int
	Logger        *slog.Logger
	Now           func() time.Time
	RemoveFile    func(string) error
	RemoveAll     func(string) error
	NewLeaseToken func() (string, error)
}

func DefaultConfig(uploadDir string) Config {
	return Config{
		UploadDir:     uploadDir,
		Interval:      5 * time.Second,
		Lease:         2 * time.Minute,
		MinBackoff:    5 * time.Second,
		MaxBackoff:    time.Hour,
		BatchSize:     32,
		Logger:        slog.Default(),
		Now:           time.Now,
		RemoveFile:    os.Remove,
		RemoveAll:     os.RemoveAll,
		NewLeaseToken: randomToken,
	}
}

type Worker struct {
	queue  store.StorageCleanupQueue
	config Config
}

func New(queue store.StorageCleanupQueue, cfg Config) (*Worker, error) {
	if queue == nil {
		return nil, errors.New("storage cleanup queue is required")
	}
	defaults := DefaultConfig(cfg.UploadDir)
	if strings.TrimSpace(cfg.UploadDir) == "" {
		return nil, errors.New("upload directory is required")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaults.Interval
	}
	if cfg.Lease <= 0 {
		cfg.Lease = defaults.Lease
	}
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = defaults.MinBackoff
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = defaults.MaxBackoff
	}
	if cfg.MaxBackoff < cfg.MinBackoff {
		cfg.MaxBackoff = cfg.MinBackoff
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaults.BatchSize
	}
	if cfg.Logger == nil {
		cfg.Logger = defaults.Logger
	}
	if cfg.Now == nil {
		cfg.Now = defaults.Now
	}
	if cfg.RemoveFile == nil {
		cfg.RemoveFile = defaults.RemoveFile
	}
	if cfg.RemoveAll == nil {
		cfg.RemoveAll = defaults.RemoveAll
	}
	if cfg.NewLeaseToken == nil {
		cfg.NewLeaseToken = defaults.NewLeaseToken
	}
	return &Worker{queue: queue, config: cfg}, nil
}

func (w *Worker) Run(ctx context.Context) {
	w.processAndLog(ctx)
	ticker := time.NewTicker(w.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.processAndLog(ctx)
		}
	}
}

func (w *Worker) processAndLog(ctx context.Context) {
	processed, err := w.RunOnce(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		w.config.Logger.Error("process storage cleanup queue", "error", err)
		return
	}
	if processed > 0 {
		w.config.Logger.Debug("processed storage cleanup jobs", "count", processed)
	}
}

func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	token, err := w.config.NewLeaseToken()
	if err != nil {
		return 0, fmt.Errorf("create cleanup lease token: %w", err)
	}
	now := w.config.Now().UTC()
	jobs, err := w.queue.ClaimStorageCleanupJobs(ctx, token, now, now.Add(w.config.Lease), w.config.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("claim cleanup jobs: %w", err)
	}
	processed := 0
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		removeErr := w.remove(job)
		if removeErr == nil || errors.Is(removeErr, os.ErrNotExist) {
			completed, err := w.queue.CompleteStorageCleanupJob(ctx, job.ID, token)
			if err != nil {
				return processed, fmt.Errorf("complete cleanup job %d: %w", job.ID, err)
			}
			if completed {
				processed++
			} else {
				w.config.Logger.Warn("storage cleanup lease was lost before completion", "job_id", job.ID)
			}
			continue
		}

		nextAttempt := w.config.Now().UTC().Add(w.backoff(job.AttemptCount))
		retried, err := w.queue.RetryStorageCleanupJob(ctx, job.ID, token, nextAttempt, removeErr.Error())
		if err != nil {
			return processed, fmt.Errorf("retry cleanup job %d: %w", job.ID, err)
		}
		if retried {
			processed++
			w.config.Logger.Warn("storage cleanup failed and will be retried",
				"job_id", job.ID,
				"kind", job.Kind,
				"project_id", job.ProjectID,
				"attempt", job.AttemptCount,
				"next_attempt", nextAttempt,
				"error", removeErr,
			)
		} else {
			w.config.Logger.Warn("storage cleanup lease was lost before retry", "job_id", job.ID)
		}
	}
	return processed, nil
}

func (w *Worker) remove(job store.StorageCleanupJob) error {
	switch job.Kind {
	case store.StorageCleanupAssetFile:
		path, err := filestore.AssetPath(w.config.UploadDir, job.ProjectID, job.TargetKey)
		if err != nil {
			return err
		}
		return w.config.RemoveFile(path)
	case store.StorageCleanupProjectDir:
		path, err := filestore.ProjectPath(w.config.UploadDir, job.ProjectID)
		if err != nil {
			return err
		}
		return w.config.RemoveAll(path)
	default:
		return fmt.Errorf("unsupported cleanup kind %q", job.Kind)
	}
}

func (w *Worker) backoff(attempt int) time.Duration {
	delay := w.config.MinBackoff
	for step := 1; step < attempt && delay < w.config.MaxBackoff; step++ {
		if delay > w.config.MaxBackoff/2 {
			return w.config.MaxBackoff
		}
		delay *= 2
	}
	if delay > w.config.MaxBackoff {
		return w.config.MaxBackoff
	}
	return delay
}

func randomToken() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
