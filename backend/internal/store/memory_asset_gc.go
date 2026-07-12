package store

import (
	"context"
	"sort"
	"time"
)

type assetGCCandidate struct {
	ProjectID  string
	DetectedAt time.Time
	EligibleAt time.Time
}

func (s *MemoryStore) SweepOrphanedAssets(ctx context.Context, now time.Time, grace time.Duration, limit int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if now.IsZero() || grace <= 0 || limit <= 0 {
		return 0, ErrInvalidInput
	}
	if limit > 1000 {
		limit = 1000
	}
	now = now.UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	type pendingAsset struct {
		id        string
		projectID string
		category  int
		orderAt   time.Time
	}
	projectFresh := make(map[string]bool)
	for _, asset := range s.assets {
		fresh, checked := projectFresh[asset.ProjectID]
		if !checked {
			fresh = s.projectAssetReferencesFreshLocked(asset.ProjectID)
			projectFresh[asset.ProjectID] = fresh
			if !fresh {
				s.clearProjectAssetGCCandidatesLocked(asset.ProjectID)
			}
		}
	}
	assets := make([]pendingAsset, 0, len(s.assets))
	for _, asset := range s.assets {
		if !projectFresh[asset.ProjectID] {
			continue
		}
		candidate, tracked := s.assetGCCandidates[asset.ID]
		referenced := s.isAssetReferencedLocked(asset.ProjectID, asset.ID)
		if !tracked && referenced {
			continue
		}
		category := 1
		orderAt := asset.CreatedAt
		if tracked {
			category = 2
			orderAt = candidate.EligibleAt
			if !candidate.EligibleAt.After(now) {
				category = 0
			}
		}
		assets = append(assets, pendingAsset{
			id: asset.ID, projectID: asset.ProjectID,
			category: category, orderAt: orderAt,
		})
	}
	sort.Slice(assets, func(i, j int) bool {
		if assets[i].category != assets[j].category {
			return assets[i].category < assets[j].category
		}
		if !assets[i].orderAt.Equal(assets[j].orderAt) {
			return assets[i].orderAt.Before(assets[j].orderAt)
		}
		return assets[i].id < assets[j].id
	})
	if len(assets) > limit {
		assets = assets[:limit]
	}

	deleted := 0
	for _, pending := range assets {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		if s.isAssetReferencedLocked(pending.projectID, pending.id) {
			delete(s.assetGCCandidates, pending.id)
			continue
		}
		asset, ok := s.assets[pending.id]
		if !ok {
			delete(s.assetGCCandidates, pending.id)
			continue
		}
		candidate, tracked := s.assetGCCandidates[pending.id]
		if !tracked {
			s.assetGCCandidates[pending.id] = assetGCCandidate{
				ProjectID:  asset.ProjectID,
				DetectedAt: now,
				EligibleAt: now.Add(grace),
			}
			continue
		}
		if candidate.EligibleAt.After(now) {
			continue
		}
		s.enqueueStorageCleanupLocked(StorageCleanupAssetFile, asset.ProjectID, asset.StorageKey, now)
		delete(s.assets, asset.ID)
		delete(s.assetGCCandidates, asset.ID)
		deleted++
	}
	return deleted, nil
}

func (s *MemoryStore) projectAssetReferencesFreshLocked(projectID string) bool {
	for boardID, board := range s.boards {
		if board.ProjectID != projectID {
			continue
		}
		document, documentOK := s.boardDocuments[boardID]
		state, stateOK := s.boardAssetReferenceStates[boardID]
		if !documentOK || !stateOK || state.Conflicted || state.IndexedThroughSequence != s.latestBoardSequenceLocked(boardID, document) {
			return false
		}
	}
	return true
}

func (s *MemoryStore) isAssetReferencedLocked(projectID, assetID string) bool {
	for boardID, board := range s.boards {
		if board.ProjectID != projectID {
			continue
		}
		if _, referenced := s.boardAssetReferences[boardID][assetID]; referenced {
			return true
		}
	}
	return false
}
