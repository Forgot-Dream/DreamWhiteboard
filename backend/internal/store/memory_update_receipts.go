package store

import (
	"context"
	"sort"
	"time"

	"dreamwhiteboard/backend/internal/domain"
)

type compactedBoardUpdateReceipt struct {
	boardID        string
	serverSequence int64
	compactedAt    time.Time
}

func (s *MemoryStore) PruneCompactedBoardUpdateReceipts(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	if cutoff.IsZero() || limit <= 0 {
		return 0, ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if limit > MaxBoardUpdateReceiptPruneBatchSize {
		limit = MaxBoardUpdateReceiptPruneBatchSize
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	candidates := make([]compactedBoardUpdateReceipt, 0)
	for boardID, updates := range s.boardUpdates {
		for _, update := range updates {
			if update.CompactedAt == nil || !update.CompactedAt.Before(cutoff) {
				continue
			}
			candidates = append(candidates, compactedBoardUpdateReceipt{
				boardID:        boardID,
				serverSequence: update.ServerSequence,
				compactedAt:    update.CompactedAt.UTC(),
			})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].compactedAt.Equal(candidates[j].compactedAt) {
			return candidates[i].compactedAt.Before(candidates[j].compactedAt)
		}
		if candidates[i].boardID != candidates[j].boardID {
			return candidates[i].boardID < candidates[j].boardID
		}
		return candidates[i].serverSequence < candidates[j].serverSequence
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	if len(candidates) == 0 {
		return 0, nil
	}

	type receiptKey struct {
		boardID        string
		serverSequence int64
	}
	selected := make(map[receiptKey]struct{}, len(candidates))
	for _, candidate := range candidates {
		selected[receiptKey{boardID: candidate.boardID, serverSequence: candidate.serverSequence}] = struct{}{}
	}
	for boardID, updates := range s.boardUpdates {
		retained := make([]domain.BoardUpdate, 0, len(updates))
		for _, update := range updates {
			if _, remove := selected[receiptKey{boardID: boardID, serverSequence: update.ServerSequence}]; remove {
				continue
			}
			retained = append(retained, update)
		}
		s.boardUpdates[boardID] = retained
	}
	return len(candidates), nil
}
