package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func enqueueStorageCleanupTx(tx *sql.Tx, kind StorageCleanupKind, projectID, targetKey string) error {
	if projectID == "" || (kind == StorageCleanupAssetFile && targetKey == "") ||
		(kind != StorageCleanupAssetFile && kind != StorageCleanupProjectDir) {
		return ErrInvalidInput
	}
	_, err := tx.Exec(`INSERT INTO storage_cleanup_jobs
		(kind, project_id, target_key, available_at, created_at, updated_at)
		VALUES ($1,$2,$3,now(),now(),now())
		ON CONFLICT (kind, project_id, target_key) DO NOTHING`, kind, projectID, targetKey)
	return mapSQLError(err)
}

func (s *PostgresStore) ClaimStorageCleanupJobs(ctx context.Context, leaseToken string, now, leaseUntil time.Time, limit int) ([]StorageCleanupJob, error) {
	leaseToken = strings.TrimSpace(leaseToken)
	if leaseToken == "" || now.IsZero() || !leaseUntil.After(now) || limit <= 0 {
		return nil, ErrInvalidInput
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `WITH candidates AS (
		SELECT id
		FROM storage_cleanup_jobs
		WHERE available_at <= $2
		  AND (lease_until IS NULL OR lease_until <= $2)
		ORDER BY available_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT $4
	)
	UPDATE storage_cleanup_jobs AS jobs
	SET lease_token=$1,
		lease_until=$3,
		attempt_count=jobs.attempt_count+1,
		updated_at=$2
	FROM candidates
	WHERE jobs.id=candidates.id
	RETURNING jobs.id, jobs.kind, jobs.project_id, jobs.target_key,
		jobs.attempt_count, jobs.available_at, jobs.lease_token,
		jobs.lease_until, jobs.last_error, jobs.created_at, jobs.updated_at`,
		leaseToken, now.UTC(), leaseUntil.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []StorageCleanupJob{}
	for rows.Next() {
		var job StorageCleanupJob
		if err := rows.Scan(
			&job.ID,
			&job.Kind,
			&job.ProjectID,
			&job.TargetKey,
			&job.AttemptCount,
			&job.AvailableAt,
			&job.LeaseToken,
			&job.LeaseUntil,
			&job.LastError,
			&job.CreatedAt,
			&job.UpdatedAt,
		); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *PostgresStore) CompleteStorageCleanupJob(ctx context.Context, id int64, leaseToken string) (bool, error) {
	if id <= 0 || strings.TrimSpace(leaseToken) == "" {
		return false, ErrInvalidInput
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM storage_cleanup_jobs WHERE id=$1 AND lease_token=$2`, id, leaseToken)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

func (s *PostgresStore) RetryStorageCleanupJob(ctx context.Context, id int64, leaseToken string, availableAt time.Time, lastError string) (bool, error) {
	if id <= 0 || strings.TrimSpace(leaseToken) == "" || availableAt.IsZero() {
		return false, ErrInvalidInput
	}
	if len(lastError) > 4096 {
		lastError = lastError[:4096]
	}
	result, err := s.db.ExecContext(ctx, `UPDATE storage_cleanup_jobs
		SET available_at=$3,
			lease_token=NULL,
			lease_until=NULL,
			last_error=$4,
			updated_at=now()
		WHERE id=$1 AND lease_token=$2`, id, leaseToken, availableAt.UTC(), lastError)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}
