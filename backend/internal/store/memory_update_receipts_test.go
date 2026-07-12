package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"dreamwhiteboard/backend/internal/domain"
)

func TestMemoryPruneCompactedBoardUpdateReceipts(t *testing.T) {
	repo := NewMemoryStore()
	user, err := repo.EnsureSystemAdmin("memory-receipts@example.test", "password")
	if err != nil {
		t.Fatal(err)
	}
	project, err := repo.CreateProject("Receipt retention", "", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	board, err := repo.CreateBoard(project.ID, "Board", user.ID)
	if err != nil {
		t.Fatal(err)
	}

	inputs := make([]domain.BoardUpdate, 4)
	for index := range inputs {
		inputs[index] = domain.BoardUpdate{
			BoardID:            board.ID,
			UpdateID:           "receipt-" + string(rune('1'+index)),
			ClientID:           "receipt-client",
			UserID:             user.ID,
			Update:             []byte{byte(index + 1)},
			IntroducedAssetIDs: []string{},
		}
		if _, inserted, err := repo.AppendBoardUpdate(inputs[index]); err != nil || !inserted {
			t.Fatalf("append update %d: inserted=%v err=%v", index+1, inserted, err)
		}
	}
	if _, err := repo.SaveBoardCheckpoint(board.ID, []byte{9}, 3); err != nil {
		t.Fatalf("compact updates: %v", err)
	}
	originalCompactedAt := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	repo.mu.Lock()
	repo.boardUpdates[board.ID][0].CompactedAt = &originalCompactedAt
	repo.mu.Unlock()
	if _, err := repo.SaveBoardCheckpoint(board.ID, []byte{8}, 3); err != nil {
		t.Fatalf("repeat checkpoint: %v", err)
	}
	repo.mu.RLock()
	preservedCompactedAt := *repo.boardUpdates[board.ID][0].CompactedAt
	repo.mu.RUnlock()
	if !preservedCompactedAt.Equal(originalCompactedAt) {
		t.Fatalf("repeat checkpoint reset compacted_at from %v to %v", originalCompactedAt, preservedCompactedAt)
	}

	cutoff := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	compactedTimes := map[string]time.Time{
		"receipt-1": cutoff.Add(-24 * time.Hour),
		"receipt-2": cutoff,
		"receipt-3": cutoff.Add(-time.Hour),
	}
	repo.mu.Lock()
	updates := repo.boardUpdates[board.ID]
	for index := range updates {
		if compactedAt, ok := compactedTimes[updates[index].UpdateID]; ok {
			value := compactedAt
			updates[index].CompactedAt = &value
		}
		if updates[index].UpdateID == "receipt-4" {
			updates[index].CreatedAt = cutoff.Add(-365 * 24 * time.Hour)
		}
	}
	repo.boardUpdates[board.ID] = updates
	repo.mu.Unlock()

	if duplicate, inserted, err := repo.AppendBoardUpdate(inputs[0]); err != nil || inserted || duplicate.CompactedAt == nil || duplicate.Update != nil {
		t.Fatalf("duplicate inside retention window: update=%#v inserted=%v err=%v", duplicate, inserted, err)
	}
	if pruned, err := repo.PruneCompactedBoardUpdateReceipts(context.Background(), cutoff, 1); err != nil || pruned != 1 {
		t.Fatalf("prune oldest receipt: pruned=%d err=%v", pruned, err)
	}
	assertMemoryBoardUpdateIDs(t, repo, board.ID, "receipt-2", "receipt-3", "receipt-4")
	document, liveUpdates, err := repo.LoadBoardDocument(board.ID)
	if err != nil || document.CheckpointSequence != 3 || len(document.Checkpoint) != 1 || document.Checkpoint[0] != 8 ||
		len(liveUpdates) != 1 || liveUpdates[0].UpdateID != "receipt-4" {
		t.Fatalf("document after receipt prune: document=%#v updates=%#v err=%v", document, liveUpdates, err)
	}

	replayed, inserted, err := repo.AppendBoardUpdate(inputs[0])
	if err != nil || !inserted || replayed.ServerSequence != 5 {
		t.Fatalf("replay after receipt expiry: update=%#v inserted=%v err=%v", replayed, inserted, err)
	}
	if pruned, err := repo.PruneCompactedBoardUpdateReceipts(context.Background(), cutoff, 10); err != nil || pruned != 1 {
		t.Fatalf("prune remaining expired receipt: pruned=%d err=%v", pruned, err)
	}
	assertMemoryBoardUpdateIDs(t, repo, board.ID, "receipt-1", "receipt-2", "receipt-4")
	if duplicate, inserted, err := repo.AppendBoardUpdate(inputs[1]); err != nil || inserted || duplicate.ServerSequence != 2 {
		t.Fatalf("cutoff-equal receipt was not retained: update=%#v inserted=%v err=%v", duplicate, inserted, err)
	}

	if _, err := repo.PruneCompactedBoardUpdateReceipts(context.Background(), time.Time{}, 1); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero cutoff error = %v, want ErrInvalidInput", err)
	}
	if _, err := repo.PruneCompactedBoardUpdateReceipts(context.Background(), cutoff, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero limit error = %v, want ErrInvalidInput", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repo.PruneCompactedBoardUpdateReceipts(canceled, cutoff, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled prune error = %v, want context.Canceled", err)
	}
}

func assertMemoryBoardUpdateIDs(t *testing.T, repo *MemoryStore, boardID string, expected ...string) {
	t.Helper()
	repo.mu.RLock()
	defer repo.mu.RUnlock()
	actual := make(map[string]struct{}, len(repo.boardUpdates[boardID]))
	for _, update := range repo.boardUpdates[boardID] {
		actual[update.UpdateID] = struct{}{}
	}
	if len(actual) != len(expected) {
		t.Fatalf("board update IDs = %#v, want %v", actual, expected)
	}
	for _, updateID := range expected {
		if _, ok := actual[updateID]; !ok {
			t.Fatalf("board update IDs = %#v, missing %q", actual, updateID)
		}
	}
}
