package store

import (
	"database/sql"
	"errors"
	"time"

	"dreamwhiteboard/backend/internal/domain"
	"github.com/lib/pq"
)

func (s *PostgresStore) SaveBoardAssetReferences(boardID, indexedBy string, throughSequence int64, assetIDs []string) (domain.BoardAssetReferenceState, error) {
	if boardID == "" || indexedBy == "" || throughSequence < 0 || assetIDs == nil {
		return domain.BoardAssetReferenceState{}, ErrInvalidInput
	}
	canonical, err := canonicalAssetIDs(assetIDs)
	if err != nil {
		return domain.BoardAssetReferenceState{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.BoardAssetReferenceState{}, err
	}
	defer tx.Rollback()

	projectID, checkpointSequence, err := lockBoardDocumentTx(tx, boardID)
	if err != nil {
		return domain.BoardAssetReferenceState{}, err
	}
	latestSequence, err := latestBoardSequenceTx(tx, boardID, checkpointSequence)
	if err != nil {
		return domain.BoardAssetReferenceState{}, err
	}
	if throughSequence != latestSequence {
		return domain.BoardAssetReferenceState{}, ErrAssetReferenceIndexStale
	}
	if err := validateProjectAssetReferencesTx(tx, projectID, canonical); err != nil {
		return domain.BoardAssetReferenceState{}, err
	}

	state, err := lockBoardAssetReferenceStateTx(tx, boardID)
	if err != nil {
		return domain.BoardAssetReferenceState{}, err
	}
	refsHash := hashAssetReferences(canonical)
	if state.IndexedThroughSequence == throughSequence {
		if state.Conflicted || (state.RefsHash != "" && state.RefsHash != refsHash) {
			if !state.Conflicted {
				if _, err := tx.Exec(`UPDATE board_asset_reference_state SET conflicted=TRUE WHERE board_id=$1`, boardID); err != nil {
					return domain.BoardAssetReferenceState{}, err
				}
				state.Conflicted = true
			}
			if err := clearAssetGCCandidatesTx(tx, projectID); err != nil {
				return domain.BoardAssetReferenceState{}, err
			}
			if err := tx.Commit(); err != nil {
				return domain.BoardAssetReferenceState{}, err
			}
			return state, ErrAssetReferenceConflict
		}
		if err := tx.Commit(); err != nil {
			return domain.BoardAssetReferenceState{}, err
		}
		return state, nil
	}

	state, err = replaceBoardAssetReferencesTx(tx, boardID, projectID, indexedBy, throughSequence, canonical)
	if err != nil {
		return domain.BoardAssetReferenceState{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.BoardAssetReferenceState{}, err
	}
	return state, nil
}

func lockBoardDocumentTx(tx *sql.Tx, boardID string) (string, int64, error) {
	var projectID string
	var checkpointSequence int64
	err := tx.QueryRow(`SELECT b.project_id, d.checkpoint_sequence
		FROM board_documents d
		JOIN boards b ON b.id=d.board_id
		WHERE d.board_id=$1
		FOR UPDATE OF d`, boardID).Scan(&projectID, &checkpointSequence)
	if err != nil {
		return "", 0, mapSQLError(err)
	}
	return projectID, checkpointSequence, nil
}

func latestBoardSequenceTx(tx *sql.Tx, boardID string, checkpointSequence int64) (int64, error) {
	var latestSequence int64
	err := tx.QueryRow(`SELECT GREATEST($2, COALESCE(MAX(server_sequence), 0))
		FROM board_updates WHERE board_id=$1`, boardID, checkpointSequence).Scan(&latestSequence)
	return latestSequence, err
}

func validateProjectAssetReferencesTx(tx *sql.Tx, projectID string, canonical []string) error {
	if len(canonical) == 0 {
		return nil
	}
	rows, err := tx.Query(`SELECT id FROM assets
		WHERE project_id=$1 AND id=ANY($2)
		ORDER BY id
		FOR KEY SHARE`, projectID, pq.Array(canonical))
	if err != nil {
		return err
	}
	found := make([]string, 0, len(canonical))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		found = append(found, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(found) != len(canonical) {
		return ErrInvalidAssetReference
	}
	for index := range canonical {
		if found[index] != canonical[index] {
			return ErrInvalidAssetReference
		}
	}
	return nil
}

func lockBoardAssetReferenceStateTx(tx *sql.Tx, boardID string) (domain.BoardAssetReferenceState, error) {
	var state domain.BoardAssetReferenceState
	var indexedBy sql.NullString
	var indexedAt sql.NullTime
	err := tx.QueryRow(`SELECT board_id, indexed_through_sequence, refs_hash, indexed_by, indexed_at, conflicted
		FROM board_asset_reference_state WHERE board_id=$1 FOR UPDATE`, boardID).Scan(
		&state.BoardID,
		&state.IndexedThroughSequence,
		&state.RefsHash,
		&indexedBy,
		&indexedAt,
		&state.Conflicted,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BoardAssetReferenceState{}, ErrAssetReferenceIndexStale
	}
	if err != nil {
		return domain.BoardAssetReferenceState{}, err
	}
	if indexedBy.Valid {
		state.IndexedBy = indexedBy.String
	}
	if indexedAt.Valid {
		value := indexedAt.Time
		state.IndexedAt = &value
	}
	return state, nil
}

func replaceBoardAssetReferencesTx(tx *sql.Tx, boardID, projectID, indexedBy string, throughSequence int64, canonical []string) (domain.BoardAssetReferenceState, error) {
	if _, err := tx.Exec(`DELETE FROM board_asset_references WHERE board_id=$1`, boardID); err != nil {
		return domain.BoardAssetReferenceState{}, err
	}
	if len(canonical) > 0 {
		if _, err := tx.Exec(`INSERT INTO board_asset_references (board_id, project_id, asset_id)
			SELECT $1, $2, unnest($3::text[])`, boardID, projectID, pq.Array(canonical)); err != nil {
			return domain.BoardAssetReferenceState{}, mapAssetReferenceSQLError(err)
		}
	}
	now := time.Now().UTC()
	refsHash := hashAssetReferences(canonical)
	if _, err := tx.Exec(`UPDATE board_asset_reference_state
		SET indexed_through_sequence=$2, refs_hash=$3, indexed_by=$4, indexed_at=$5, conflicted=FALSE
		WHERE board_id=$1`, boardID, throughSequence, refsHash, indexedBy, now); err != nil {
		return domain.BoardAssetReferenceState{}, mapSQLError(err)
	}
	if err := clearReferencedAssetGCCandidatesTx(tx, projectID, canonical); err != nil {
		return domain.BoardAssetReferenceState{}, err
	}
	return domain.BoardAssetReferenceState{
		BoardID:                boardID,
		IndexedThroughSequence: throughSequence,
		RefsHash:               refsHash,
		IndexedBy:              indexedBy,
		IndexedAt:              &now,
	}, nil
}

func mapAssetReferenceSQLError(err error) error {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23503" {
		return ErrInvalidAssetReference
	}
	return mapSQLError(err)
}

type projectBoardReferenceSequence struct {
	boardID        string
	latestSequence int64
}

// lockProjectBoardDocumentsTx freezes every board sequence in a project. The
// caller must lock its target asset rows before validating reference states so
// all update, delete, and GC transactions use document -> asset -> state order.
func lockProjectBoardDocumentsTx(tx *sql.Tx, projectID string) ([]projectBoardReferenceSequence, error) {
	rows, err := tx.Query(`SELECT b.id, d.checkpoint_sequence
		FROM boards b
		JOIN board_documents d ON d.board_id=b.id
		WHERE b.project_id=$1
		ORDER BY b.id
		FOR UPDATE OF d`, projectID)
	if err != nil {
		return nil, err
	}
	type boardSequence struct {
		id         string
		checkpoint int64
	}
	boards := []boardSequence{}
	for rows.Next() {
		var board boardSequence
		if err := rows.Scan(&board.id, &board.checkpoint); err != nil {
			rows.Close()
			return nil, err
		}
		boards = append(boards, board)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	locked := make([]projectBoardReferenceSequence, 0, len(boards))
	for _, board := range boards {
		latest, err := latestBoardSequenceTx(tx, board.id, board.checkpoint)
		if err != nil {
			return nil, err
		}
		locked = append(locked, projectBoardReferenceSequence{boardID: board.id, latestSequence: latest})
	}
	return locked, nil
}

func validateFreshProjectBoardReferencesTx(tx *sql.Tx, boards []projectBoardReferenceSequence) error {
	for _, board := range boards {
		state, err := lockBoardAssetReferenceStateTx(tx, board.boardID)
		if err != nil {
			if errors.Is(err, ErrAssetReferenceIndexStale) {
				return ErrAssetReferenceIndexStale
			}
			return err
		}
		if state.Conflicted || state.IndexedThroughSequence != board.latestSequence {
			return ErrAssetReferenceIndexStale
		}
	}
	return nil
}
