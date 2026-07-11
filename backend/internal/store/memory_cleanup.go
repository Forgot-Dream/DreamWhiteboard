package store

import (
	"context"
	"sort"
	"strings"
	"time"
)

func (s *MemoryStore) enqueueStorageCleanupLocked(kind StorageCleanupKind, projectID, targetKey string, now time.Time) StorageCleanupJob {
	for _, existing := range s.cleanupJobs {
		if existing.Kind == kind && existing.ProjectID == projectID && existing.TargetKey == targetKey {
			return existing
		}
	}
	s.nextCleanupID++
	job := StorageCleanupJob{
		ID:          s.nextCleanupID,
		Kind:        kind,
		ProjectID:   projectID,
		TargetKey:   targetKey,
		AvailableAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.cleanupJobs[job.ID] = job
	return job
}

func (s *MemoryStore) ClaimStorageCleanupJobs(ctx context.Context, leaseToken string, now, leaseUntil time.Time, limit int) ([]StorageCleanupJob, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	leaseToken = strings.TrimSpace(leaseToken)
	if leaseToken == "" || now.IsZero() || !leaseUntil.After(now) || limit <= 0 {
		return nil, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	due := make([]StorageCleanupJob, 0, len(s.cleanupJobs))
	for _, job := range s.cleanupJobs {
		leaseExpired := job.LeaseUntil == nil || !job.LeaseUntil.After(now)
		if !job.AvailableAt.After(now) && leaseExpired {
			due = append(due, job)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].AvailableAt.Equal(due[j].AvailableAt) {
			return due[i].ID < due[j].ID
		}
		return due[i].AvailableAt.Before(due[j].AvailableAt)
	})
	if len(due) > limit {
		due = due[:limit]
	}
	claimed := make([]StorageCleanupJob, 0, len(due))
	for _, job := range due {
		lease := leaseUntil
		job.LeaseToken = leaseToken
		job.LeaseUntil = &lease
		job.AttemptCount++
		job.UpdatedAt = now
		s.cleanupJobs[job.ID] = job
		claimed = append(claimed, job)
	}
	return claimed, nil
}

func (s *MemoryStore) CompleteStorageCleanupJob(ctx context.Context, id int64, leaseToken string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if id <= 0 || strings.TrimSpace(leaseToken) == "" {
		return false, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.cleanupJobs[id]
	if !ok || job.LeaseToken != leaseToken {
		return false, nil
	}
	delete(s.cleanupJobs, id)
	return true, nil
}

func (s *MemoryStore) RetryStorageCleanupJob(ctx context.Context, id int64, leaseToken string, availableAt time.Time, lastError string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if id <= 0 || strings.TrimSpace(leaseToken) == "" || availableAt.IsZero() {
		return false, ErrInvalidInput
	}
	if len(lastError) > 4096 {
		lastError = lastError[:4096]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.cleanupJobs[id]
	if !ok || job.LeaseToken != leaseToken {
		return false, nil
	}
	job.AvailableAt = availableAt
	job.LeaseToken = ""
	job.LeaseUntil = nil
	job.LastError = lastError
	job.UpdatedAt = time.Now().UTC()
	s.cleanupJobs[id] = job
	return true, nil
}
