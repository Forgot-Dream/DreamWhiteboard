package store

import (
	"context"
	"time"
)

func (s *PostgresStore) PruneCompactedBoardUpdateReceipts(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	if cutoff.IsZero() || limit <= 0 {
		return 0, ErrInvalidInput
	}
	if limit > MaxBoardUpdateReceiptPruneBatchSize {
		limit = MaxBoardUpdateReceiptPruneBatchSize
	}
	result, err := s.db.ExecContext(ctx, `WITH expired AS (
		SELECT board_id, server_sequence
		FROM board_updates
		WHERE compacted_at IS NOT NULL AND compacted_at < $1
		ORDER BY compacted_at, board_id, server_sequence
		FOR UPDATE SKIP LOCKED
		LIMIT $2
	)
	DELETE FROM board_updates AS receipts
	USING expired
	WHERE receipts.board_id=expired.board_id
	  AND receipts.server_sequence=expired.server_sequence`, cutoff.UTC(), limit)
	if err != nil {
		return 0, err
	}
	deleted, err := result.RowsAffected()
	return int(deleted), err
}
