package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
)

func (s *PostgresStore) SweepOrphanedAssets(ctx context.Context, now time.Time, grace time.Duration, limit int) (int, error) {
	if now.IsZero() || grace <= 0 || limit <= 0 {
		return 0, ErrInvalidInput
	}
	if limit > 1000 {
		limit = 1000
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM asset_gc_candidates c
		WHERE EXISTS (
			SELECT 1
			FROM boards b
			LEFT JOIN board_documents d ON d.board_id=b.id
			LEFT JOIN board_asset_reference_state refs_state ON refs_state.board_id=b.id
			WHERE b.project_id=c.project_id
			  AND (
				d.board_id IS NULL
				OR refs_state.board_id IS NULL
				OR refs_state.conflicted
				OR refs_state.indexed_through_sequence <> GREATEST(
					d.checkpoint_sequence,
					COALESCE((SELECT MAX(u.server_sequence) FROM board_updates u WHERE u.board_id=b.id), 0)
				)
			  )
		)`); err != nil {
		return 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.id, a.project_id
		FROM assets a
		LEFT JOIN asset_gc_candidates c ON c.asset_id=a.id
		WHERE (c.asset_id IS NOT NULL
		   OR NOT EXISTS (
			SELECT 1 FROM board_asset_references r
			WHERE r.project_id=a.project_id AND r.asset_id=a.id
		   ))
		  AND NOT EXISTS (
			SELECT 1
			FROM boards b
			LEFT JOIN board_documents d ON d.board_id=b.id
			LEFT JOIN board_asset_reference_state refs_state ON refs_state.board_id=b.id
			WHERE b.project_id=a.project_id
			  AND (
				d.board_id IS NULL
				OR refs_state.board_id IS NULL
				OR refs_state.conflicted
				OR refs_state.indexed_through_sequence <> GREATEST(
					d.checkpoint_sequence,
					COALESCE((SELECT MAX(u.server_sequence) FROM board_updates u WHERE u.board_id=b.id), 0)
				)
			  )
		  )
		ORDER BY
			CASE
				WHEN c.eligible_at <= $1 THEN 0
				WHEN c.asset_id IS NULL THEN 1
				ELSE 2
			END,
			COALESCE(c.eligible_at, a.created_at),
			a.id
		LIMIT $2`, now.UTC(), limit)
	if err != nil {
		return 0, err
	}
	type candidate struct {
		assetID   string
		projectID string
	}
	candidates := []candidate{}
	for rows.Next() {
		var value candidate
		if err := rows.Scan(&value.assetID, &value.projectID); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	deleted := 0
	for _, candidate := range candidates {
		removed, err := s.sweepOrphanedAsset(ctx, candidate.assetID, candidate.projectID, now.UTC(), grace)
		if err != nil {
			return deleted, err
		}
		if removed {
			deleted++
		}
	}
	return deleted, nil
}

func (s *PostgresStore) sweepOrphanedAsset(ctx context.Context, assetID, projectID string, now time.Time, grace time.Duration) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var lockedProject string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM projects WHERE id=$1 FOR UPDATE`, projectID).Scan(&lockedProject); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	boards, err := lockProjectBoardDocumentsTx(tx, projectID)
	if err != nil {
		return false, err
	}

	var storageKey string
	if err := tx.QueryRowContext(ctx, `SELECT storage_key FROM assets
		WHERE id=$1 AND project_id=$2 FOR UPDATE`, assetID, projectID).Scan(&storageKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, tx.Commit()
		}
		return false, err
	}
	if err := validateFreshProjectBoardReferencesTx(tx, boards); err != nil {
		if errors.Is(err, ErrAssetReferenceIndexStale) {
			if err := clearAssetGCCandidatesTx(tx, projectID); err != nil {
				return false, err
			}
			return false, tx.Commit()
		}
		return false, err
	}
	var referenced bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM board_asset_references WHERE project_id=$1 AND asset_id=$2
	)`, projectID, assetID).Scan(&referenced); err != nil {
		return false, err
	}
	if referenced {
		if _, err := tx.ExecContext(ctx, `DELETE FROM asset_gc_candidates WHERE asset_id=$1`, assetID); err != nil {
			return false, err
		}
		return false, tx.Commit()
	}

	var eligibleAt time.Time
	err = tx.QueryRowContext(ctx, `SELECT eligible_at FROM asset_gc_candidates WHERE asset_id=$1 FOR UPDATE`, assetID).Scan(&eligibleAt)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO asset_gc_candidates
			(asset_id, project_id, detected_at, eligible_at)
			VALUES ($1,$2,$3,$4)`, assetID, projectID, now, now.Add(grace)); err != nil {
			return false, mapSQLError(err)
		}
		return false, tx.Commit()
	}
	if err != nil {
		return false, err
	}
	if eligibleAt.After(now) {
		return false, tx.Commit()
	}
	if err := enqueueStorageCleanupTx(tx, StorageCleanupAssetFile, projectID, storageKey); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM assets WHERE id=$1 AND project_id=$2`, assetID, projectID)
	if err := resultError(result, err); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func clearAssetGCCandidatesTx(tx *sql.Tx, projectID string) error {
	_, err := tx.Exec(`DELETE FROM asset_gc_candidates WHERE project_id=$1`, projectID)
	return err
}

func clearReferencedAssetGCCandidatesTx(tx *sql.Tx, projectID string, assetIDs []string) error {
	if len(assetIDs) == 0 {
		return nil
	}
	_, err := tx.Exec(`DELETE FROM asset_gc_candidates
		WHERE project_id=$1 AND asset_id=ANY($2)`, projectID, pq.Array(assetIDs))
	return err
}
