package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"dreamwhiteboard/backend/internal/domain"
)

func TestPostgresPruneCompactedBoardUpdateReceiptsIntegration(t *testing.T) {
	repo, _ := newMigratedPostgresTestStore(t)
	user := createPostgresTestUser(t, repo, "receipt-retention")
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
			UpdateID:           "postgres-receipt-" + string(rune('1'+index)),
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

	cutoff := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	compactedTimes := map[string]time.Time{
		"postgres-receipt-1": cutoff.Add(-24 * time.Hour),
		"postgres-receipt-2": cutoff,
		"postgres-receipt-3": cutoff.Add(-time.Hour),
	}
	for updateID, compactedAt := range compactedTimes {
		if _, err := repo.db.Exec(`UPDATE board_updates SET compacted_at=$3
			WHERE board_id=$1 AND update_id=$2`, board.ID, updateID, compactedAt); err != nil {
			t.Fatalf("set compacted_at for %s: %v", updateID, err)
		}
	}
	if _, err := repo.db.Exec(`UPDATE board_updates SET created_at=$3
		WHERE board_id=$1 AND update_id=$2`, board.ID, "postgres-receipt-4", cutoff.Add(-365*24*time.Hour)); err != nil {
		t.Fatalf("age uncompacted update: %v", err)
	}

	if duplicate, inserted, err := repo.AppendBoardUpdate(inputs[0]); err != nil || inserted || duplicate.CompactedAt == nil || duplicate.Update != nil {
		t.Fatalf("duplicate inside retention window: update=%#v inserted=%v err=%v", duplicate, inserted, err)
	}
	if pruned, err := repo.PruneCompactedBoardUpdateReceipts(context.Background(), cutoff, 1); err != nil || pruned != 1 {
		t.Fatalf("prune oldest receipt: pruned=%d err=%v", pruned, err)
	}
	assertPostgresBoardUpdateIDs(t, repo, board.ID,
		"postgres-receipt-2", "postgres-receipt-3", "postgres-receipt-4")
	document, liveUpdates, err := repo.LoadBoardDocument(board.ID)
	if err != nil || document.CheckpointSequence != 3 || len(document.Checkpoint) != 1 || document.Checkpoint[0] != 9 ||
		len(liveUpdates) != 1 || liveUpdates[0].UpdateID != "postgres-receipt-4" {
		t.Fatalf("document after receipt prune: document=%#v updates=%#v err=%v", document, liveUpdates, err)
	}

	replayed, inserted, err := repo.AppendBoardUpdate(inputs[0])
	if err != nil || !inserted || replayed.ServerSequence != 5 {
		t.Fatalf("replay after receipt expiry: update=%#v inserted=%v err=%v", replayed, inserted, err)
	}
	if pruned, err := repo.PruneCompactedBoardUpdateReceipts(context.Background(), cutoff, 10); err != nil || pruned != 1 {
		t.Fatalf("prune remaining expired receipt: pruned=%d err=%v", pruned, err)
	}
	assertPostgresBoardUpdateIDs(t, repo, board.ID,
		"postgres-receipt-1", "postgres-receipt-2", "postgres-receipt-4")
	if duplicate, inserted, err := repo.AppendBoardUpdate(inputs[1]); err != nil || inserted || duplicate.ServerSequence != 2 {
		t.Fatalf("cutoff-equal receipt was not retained: update=%#v inserted=%v err=%v", duplicate, inserted, err)
	}
}

func assertPostgresBoardUpdateIDs(t *testing.T, repo *PostgresStore, boardID string, expected ...string) {
	t.Helper()
	rows, err := repo.db.Query(`SELECT update_id FROM board_updates WHERE board_id=$1 ORDER BY update_id`, boardID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	actual := []string{}
	for rows.Next() {
		var updateID string
		if err := rows.Scan(&updateID); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, updateID)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("board update IDs = %v, want %v", actual, expected)
	}
}
