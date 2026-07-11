package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"dreamwhiteboard/backend/internal/domain"
)

type MemoryStore struct {
	mu                        sync.RWMutex
	users                     map[string]domain.User
	userByMail                map[string]string
	sessions                  map[string]domain.Session
	projects                  map[string]domain.Project
	members                   map[string]map[string]domain.ProjectMember
	boards                    map[string]domain.Board
	boardDocuments            map[string]domain.BoardDocument
	boardUpdates              map[string][]domain.BoardUpdate
	boardAssetReferenceStates map[string]domain.BoardAssetReferenceState
	boardAssetReferences      map[string]map[string]struct{}
	assets                    map[string]domain.Asset
	cleanupJobs               map[int64]StorageCleanupJob
	nextCleanupID             int64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		users:                     map[string]domain.User{},
		userByMail:                map[string]string{},
		sessions:                  map[string]domain.Session{},
		projects:                  map[string]domain.Project{},
		members:                   map[string]map[string]domain.ProjectMember{},
		boards:                    map[string]domain.Board{},
		boardDocuments:            map[string]domain.BoardDocument{},
		boardUpdates:              map[string][]domain.BoardUpdate{},
		boardAssetReferenceStates: map[string]domain.BoardAssetReferenceState{},
		boardAssetReferences:      map[string]map[string]struct{}{},
		assets:                    map[string]domain.Asset{},
		cleanupJobs:               map[int64]StorageCleanupJob{},
	}
}

func (s *MemoryStore) Ready(context.Context) error { return nil }

func (s *MemoryStore) Close() error { return nil }

func (s *MemoryStore) EnsureSystemAdmin(email, password string) (domain.User, error) {
	email = normalizeEmail(email)
	if email == "" || password == "" {
		return domain.User{}, ErrInvalidInput
	}
	s.mu.RLock()
	existingID, exists := s.userByMail[email]
	existing := s.users[existingID]
	s.mu.RUnlock()
	if exists {
		if existing.SystemRole != domain.SystemAdmin {
			return domain.User{}, ErrConflict
		}
		return publicStoreUser(existing), nil
	}
	hash := HashPassword(password)

	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.userByMail[email]; ok {
		existing := s.users[id]
		if existing.SystemRole != domain.SystemAdmin {
			return domain.User{}, ErrConflict
		}
		return publicStoreUser(existing), nil
	}
	user := domain.User{
		ID:                 newID("usr"),
		Email:              email,
		Name:               "System Admin",
		SystemRole:         domain.SystemAdmin,
		PasswordHash:       hash,
		MustChangePassword: true,
		CreatedAt:          time.Now().UTC(),
	}
	s.users[user.ID] = user
	s.userByMail[email] = user.ID
	return publicStoreUser(user), nil
}

func (s *MemoryStore) Authenticate(email, password string) (domain.User, error) {
	s.mu.RLock()
	id, ok := s.userByMail[normalizeEmail(email)]
	user := s.users[id]
	s.mu.RUnlock()
	if !ok {
		_ = VerifyPassword(password, dummyPasswordHash)
		return domain.User{}, ErrInvalidCredentials
	}
	if !VerifyPassword(password, user.PasswordHash) {
		return domain.User{}, ErrInvalidCredentials
	}
	return publicStoreUser(user), nil
}

func (s *MemoryStore) CreateUser(email, name, password, systemRole string) (domain.User, error) {
	email = normalizeEmail(email)
	if systemRole == "" {
		systemRole = domain.SystemUser
	}
	if email == "" || password == "" || !domain.IsSystemRole(systemRole) {
		return domain.User{}, ErrInvalidInput
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = email
	}
	hash := HashPassword(password)

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.userByMail[email]; ok {
		return domain.User{}, ErrConflict
	}
	user := domain.User{
		ID:                 newID("usr"),
		Email:              email,
		Name:               name,
		SystemRole:         systemRole,
		PasswordHash:       hash,
		MustChangePassword: true,
		CreatedAt:          time.Now().UTC(),
	}
	s.users[user.ID] = user
	s.userByMail[email] = user.ID
	return publicStoreUser(user), nil
}

func (s *MemoryStore) UpdateUser(id, name, systemRole string) (domain.User, error) {
	if systemRole != "" && !domain.IsSystemRole(systemRole) {
		return domain.User{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[id]
	if !ok {
		return domain.User{}, ErrNotFound
	}
	if name = strings.TrimSpace(name); name != "" {
		user.Name = name
	}
	if systemRole != "" {
		if user.SystemRole == domain.SystemAdmin && systemRole != domain.SystemAdmin && systemAdminCount(s.users) <= 1 {
			return domain.User{}, ErrLastSystemAdmin
		}
		user.SystemRole = systemRole
	}
	s.users[id] = user
	return publicStoreUser(user), nil
}

func (s *MemoryStore) UpdatePassword(userID, newPassword string, mustChangePassword bool) error {
	if newPassword == "" {
		return ErrInvalidInput
	}
	hash := HashPassword(newPassword)
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok {
		return ErrNotFound
	}
	user.PasswordHash = hash
	user.MustChangePassword = mustChangePassword
	user.PasswordChangedAt = &now
	s.users[userID] = user
	for tokenHash, session := range s.sessions {
		if session.UserID == userID {
			delete(s.sessions, tokenHash)
		}
	}
	return nil
}

func (s *MemoryStore) GetUser(id string) (domain.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.users[id]
	if !ok {
		return domain.User{}, ErrNotFound
	}
	return publicStoreUser(user), nil
}

func (s *MemoryStore) ListUsers() ([]domain.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	users := make([]domain.User, 0, len(s.users))
	for _, user := range s.users {
		users = append(users, publicStoreUser(user))
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Email < users[j].Email })
	return users, nil
}

func (s *MemoryStore) CreateSession(tokenHash, userID string, expiresAt time.Time) (domain.Session, error) {
	if tokenHash == "" || expiresAt.IsZero() {
		return domain.Session{}, ErrInvalidInput
	}
	now := time.Now().UTC()
	if !expiresAt.After(now) {
		return domain.Session{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for existingHash, existing := range s.sessions {
		if !existing.ExpiresAt.After(now) {
			delete(s.sessions, existingHash)
		}
	}
	if _, ok := s.users[userID]; !ok {
		return domain.Session{}, ErrNotFound
	}
	if _, ok := s.sessions[tokenHash]; ok {
		return domain.Session{}, ErrConflict
	}
	session := domain.Session{
		TokenHash:      tokenHash,
		UserID:         userID,
		ExpiresAt:      expiresAt.UTC(),
		LastAccessedAt: now,
		CreatedAt:      now,
	}
	s.sessions[tokenHash] = session
	return session, nil
}

func (s *MemoryStore) GetSession(tokenHash string, now time.Time) (domain.Session, error) {
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[tokenHash]
	if !ok {
		return domain.Session{}, ErrNotFound
	}
	if !session.ExpiresAt.After(now) {
		delete(s.sessions, tokenHash)
		return domain.Session{}, ErrNotFound
	}
	session.LastAccessedAt = now
	s.sessions[tokenHash] = session
	return session, nil
}

func (s *MemoryStore) DeleteSession(tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, tokenHash)
	return nil
}

func (s *MemoryStore) DeleteUserSessions(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tokenHash, session := range s.sessions {
		if session.UserID == userID {
			delete(s.sessions, tokenHash)
		}
	}
	return nil
}

func (s *MemoryStore) CreateProject(name, description, createdBy string) (domain.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[createdBy]; !ok {
		return domain.Project{}, ErrNotFound
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Untitled project"
	}
	now := time.Now().UTC()
	project := domain.Project{
		ID:          newID("prj"),
		Name:        name,
		Description: strings.TrimSpace(description),
		CreatedBy:   createdBy,
		CreatedAt:   now,
	}
	s.projects[project.ID] = project
	s.members[project.ID] = map[string]domain.ProjectMember{
		createdBy: {
			ProjectID: project.ID,
			UserID:    createdBy,
			Role:      domain.RoleOwner,
			CreatedAt: now,
		},
	}
	return project, nil
}

func (s *MemoryStore) ListProjects(user domain.User) ([]domain.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	projects := []domain.Project{}
	for _, project := range s.projects {
		_, member := s.members[project.ID][user.ID]
		if user.SystemRole == domain.SystemAdmin || member {
			projects = append(projects, project)
		}
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].CreatedAt.After(projects[j].CreatedAt) })
	return projects, nil
}

func (s *MemoryStore) GetProject(id string) (domain.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	project, ok := s.projects[id]
	if !ok {
		return domain.Project{}, ErrNotFound
	}
	return project, nil
}

func (s *MemoryStore) UpdateProject(id, name, description string) (domain.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	project, ok := s.projects[id]
	if !ok {
		return domain.Project{}, ErrNotFound
	}
	if name = strings.TrimSpace(name); name != "" {
		project.Name = name
	}
	project.Description = strings.TrimSpace(description)
	s.projects[id] = project
	return project, nil
}

func (s *MemoryStore) DeleteProject(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.projects[id]; !ok {
		return ErrNotFound
	}
	s.enqueueStorageCleanupLocked(StorageCleanupProjectDir, id, "", time.Now().UTC())
	delete(s.projects, id)
	delete(s.members, id)
	for boardID, board := range s.boards {
		if board.ProjectID == id {
			delete(s.boards, boardID)
			delete(s.boardDocuments, boardID)
			delete(s.boardUpdates, boardID)
			delete(s.boardAssetReferenceStates, boardID)
			delete(s.boardAssetReferences, boardID)
		}
	}
	for assetID, asset := range s.assets {
		if asset.ProjectID == id {
			delete(s.assets, assetID)
		}
	}
	return nil
}

func (s *MemoryStore) MemberRole(projectID, userID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	member, ok := s.members[projectID][userID]
	if !ok {
		return "", ErrNotFound
	}
	return member.Role, nil
}

func (s *MemoryStore) UpsertMember(projectID, userID, role string) (domain.ProjectMember, error) {
	if !domain.IsProjectRole(role) {
		return domain.ProjectMember{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.projects[projectID]; !ok {
		return domain.ProjectMember{}, ErrNotFound
	}
	if _, ok := s.users[userID]; !ok {
		return domain.ProjectMember{}, ErrNotFound
	}
	if s.members[projectID] == nil {
		s.members[projectID] = map[string]domain.ProjectMember{}
	}
	existing, exists := s.members[projectID][userID]
	if exists && domain.RemovesLastOwner(existing.Role, role, ownerCount(s.members[projectID])) {
		return domain.ProjectMember{}, ErrLastOwner
	}
	createdAt := time.Now().UTC()
	if exists {
		createdAt = existing.CreatedAt
	}
	member := domain.ProjectMember{
		ProjectID: projectID,
		UserID:    userID,
		Role:      role,
		CreatedAt: createdAt,
	}
	s.members[projectID][userID] = member
	return member, nil
}

func (s *MemoryStore) DeleteMember(projectID, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	members, ok := s.members[projectID]
	if !ok {
		return ErrNotFound
	}
	member, ok := members[userID]
	if !ok {
		return ErrNotFound
	}
	if domain.RemovesLastOwner(member.Role, "", ownerCount(members)) {
		return ErrLastOwner
	}
	delete(members, userID)
	return nil
}

func (s *MemoryStore) ListMembers(projectID string) ([]domain.ProjectMember, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	members, ok := s.members[projectID]
	if !ok {
		return nil, ErrNotFound
	}
	result := make([]domain.ProjectMember, 0, len(members))
	for _, member := range members {
		user := publicStoreUser(s.users[member.UserID])
		member.User = &user
		result = append(result, member)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].User.Email < result[j].User.Email })
	return result, nil
}

func (s *MemoryStore) CreateBoard(projectID, name, createdBy string) (domain.Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.projects[projectID]; !ok {
		return domain.Board{}, ErrNotFound
	}
	if _, ok := s.users[createdBy]; !ok {
		return domain.Board{}, ErrNotFound
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Untitled board"
	}
	now := time.Now().UTC()
	board := domain.Board{
		ID:        newID("brd"),
		ProjectID: projectID,
		Name:      name,
		CreatedBy: createdBy,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.boards[board.ID] = board
	s.boardDocuments[board.ID] = domain.BoardDocument{BoardID: board.ID, UpdatedAt: now}
	s.boardAssetReferenceStates[board.ID] = domain.BoardAssetReferenceState{
		BoardID:                board.ID,
		IndexedThroughSequence: 0,
		RefsHash:               hashAssetReferences([]string{}),
		IndexedBy:              createdBy,
		IndexedAt:              &now,
	}
	s.boardAssetReferences[board.ID] = map[string]struct{}{}
	return board, nil
}

func (s *MemoryStore) ListBoards(projectID string) ([]domain.Board, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.projects[projectID]; !ok {
		return nil, ErrNotFound
	}
	boards := []domain.Board{}
	for _, board := range s.boards {
		if board.ProjectID == projectID {
			boards = append(boards, board)
		}
	}
	sort.Slice(boards, func(i, j int) bool { return boards[i].UpdatedAt.After(boards[j].UpdatedAt) })
	return boards, nil
}

func (s *MemoryStore) GetBoard(id string) (domain.Board, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	board, ok := s.boards[id]
	if !ok {
		return domain.Board{}, ErrNotFound
	}
	return board, nil
}

func (s *MemoryStore) UpdateBoard(id, name string) (domain.Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	board, ok := s.boards[id]
	if !ok {
		return domain.Board{}, ErrNotFound
	}
	if name = strings.TrimSpace(name); name != "" {
		board.Name = name
	}
	board.UpdatedAt = time.Now().UTC()
	s.boards[id] = board
	return board, nil
}

func (s *MemoryStore) DeleteBoard(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.boards[id]; !ok {
		return ErrNotFound
	}
	delete(s.boards, id)
	delete(s.boardDocuments, id)
	delete(s.boardUpdates, id)
	delete(s.boardAssetReferenceStates, id)
	delete(s.boardAssetReferences, id)
	return nil
}

func (s *MemoryStore) LoadBoardDocument(boardID string) (domain.BoardDocument, []domain.BoardUpdate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.boards[boardID]; !ok {
		return domain.BoardDocument{}, nil, ErrNotFound
	}
	document, ok := s.boardDocuments[boardID]
	if !ok {
		return domain.BoardDocument{}, nil, ErrNotFound
	}
	document.Checkpoint = cloneBytes(document.Checkpoint)
	updates := make([]domain.BoardUpdate, 0, len(s.boardUpdates[boardID]))
	for _, update := range s.boardUpdates[boardID] {
		if update.ServerSequence > document.CheckpointSequence {
			updates = append(updates, cloneBoardUpdate(update))
		}
	}
	sort.Slice(updates, func(i, j int) bool { return updates[i].ServerSequence < updates[j].ServerSequence })
	return document, updates, nil
}

func (s *MemoryStore) AppendBoardUpdate(update domain.BoardUpdate) (domain.BoardUpdate, bool, error) {
	if update.BoardID == "" || update.UpdateID == "" || update.ClientID == "" || update.UserID == "" || len(update.Update) == 0 {
		return domain.BoardUpdate{}, false, ErrInvalidInput
	}
	hasReferenceBase := update.ReferenceBaseSequence != nil
	hasAssetManifest := update.AssetIDs != nil
	if hasReferenceBase != hasAssetManifest || hasReferenceBase && *update.ReferenceBaseSequence < 0 {
		return domain.BoardUpdate{}, false, ErrInvalidInput
	}
	assetIDs, err := canonicalAssetIDs(update.AssetIDs)
	if err != nil {
		return domain.BoardUpdate{}, false, err
	}
	update.AssetIDs = assetIDs
	update.ReferenceBaseSequence = cloneInt64(update.ReferenceBaseSequence)
	update.UpdateHash = hashBoardUpdate(update.Update, update.ReferenceBaseSequence, update.AssetIDs)

	s.mu.Lock()
	defer s.mu.Unlock()
	board, ok := s.boards[update.BoardID]
	if !ok {
		return domain.BoardUpdate{}, false, ErrNotFound
	}
	if _, ok := s.users[update.UserID]; !ok {
		return domain.BoardUpdate{}, false, ErrNotFound
	}
	for _, existing := range s.boardUpdates[update.BoardID] {
		if existing.UpdateID == update.UpdateID {
			if existing.UpdateHash != update.UpdateHash {
				return domain.BoardUpdate{}, false, ErrUpdateIDConflict
			}
			return cloneBoardUpdate(existing), false, nil
		}
	}
	document, ok := s.boardDocuments[update.BoardID]
	if !ok {
		return domain.BoardUpdate{}, false, ErrNotFound
	}
	latestSequence := s.latestBoardSequenceLocked(update.BoardID, document)
	if hasAssetManifest && update.AssetManifestTrusted && *update.ReferenceBaseSequence == latestSequence {
		if err := s.validateBoardAssetReferencesLocked(board.ProjectID, update.AssetIDs); err != nil {
			return domain.BoardUpdate{}, false, err
		}
	}
	update.ServerSequence = latestSequence + 1
	update.Update = cloneBytes(update.Update)
	update.AssetIDs = cloneStrings(update.AssetIDs)
	update.CreatedAt = time.Now().UTC()
	s.boardUpdates[update.BoardID] = append(s.boardUpdates[update.BoardID], update)
	board.UpdatedAt = update.CreatedAt
	s.boards[board.ID] = board
	if hasAssetManifest && update.AssetManifestTrusted && *update.ReferenceBaseSequence == latestSequence {
		s.replaceBoardAssetReferencesLocked(
			update.BoardID,
			update.ServerSequence,
			update.UserID,
			update.CreatedAt,
			update.AssetIDs,
		)
	}
	return cloneBoardUpdate(update), true, nil
}

func (s *MemoryStore) SaveBoardCheckpoint(boardID string, checkpoint []byte, throughSequence int64) (domain.BoardDocument, error) {
	if boardID == "" || throughSequence < 0 {
		return domain.BoardDocument{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.boards[boardID]; !ok {
		return domain.BoardDocument{}, ErrNotFound
	}
	document, ok := s.boardDocuments[boardID]
	if !ok {
		return domain.BoardDocument{}, ErrNotFound
	}
	latest := document.CheckpointSequence
	for _, update := range s.boardUpdates[boardID] {
		if update.ServerSequence > latest {
			latest = update.ServerSequence
		}
	}
	if throughSequence < document.CheckpointSequence || throughSequence > latest {
		return domain.BoardDocument{}, ErrInvalidCheckpointSequence
	}
	document.Checkpoint = cloneBytes(checkpoint)
	document.CheckpointSequence = throughSequence
	document.UpdatedAt = time.Now().UTC()
	s.boardDocuments[boardID] = document
	updates := s.boardUpdates[boardID]
	for i := range updates {
		if updates[i].ServerSequence <= throughSequence {
			updates[i].Update = nil
			compactedAt := document.UpdatedAt
			updates[i].CompactedAt = &compactedAt
		}
	}
	s.boardUpdates[boardID] = updates
	document.Checkpoint = cloneBytes(document.Checkpoint)
	return document, nil
}

func (s *MemoryStore) SaveBoardAssetReferences(boardID, indexedBy string, throughSequence int64, assetIDs []string) (domain.BoardAssetReferenceState, error) {
	if boardID == "" || indexedBy == "" || throughSequence < 0 || assetIDs == nil {
		return domain.BoardAssetReferenceState{}, ErrInvalidInput
	}
	canonicalIDs, err := canonicalAssetIDs(assetIDs)
	if err != nil {
		return domain.BoardAssetReferenceState{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	board, ok := s.boards[boardID]
	if !ok {
		return domain.BoardAssetReferenceState{}, ErrNotFound
	}
	if _, ok := s.users[indexedBy]; !ok {
		return domain.BoardAssetReferenceState{}, ErrNotFound
	}
	document, ok := s.boardDocuments[boardID]
	if !ok {
		return domain.BoardAssetReferenceState{}, ErrNotFound
	}
	latestSequence := s.latestBoardSequenceLocked(boardID, document)
	if throughSequence != latestSequence {
		return domain.BoardAssetReferenceState{}, ErrAssetReferenceIndexStale
	}
	if err := s.validateBoardAssetReferencesLocked(board.ProjectID, canonicalIDs); err != nil {
		return domain.BoardAssetReferenceState{}, err
	}

	refsHash := hashAssetReferences(canonicalIDs)
	current, ok := s.boardAssetReferenceStates[boardID]
	if !ok {
		return domain.BoardAssetReferenceState{}, ErrAssetReferenceIndexStale
	}
	if current.IndexedThroughSequence == throughSequence {
		if current.RefsHash != "" && current.RefsHash != refsHash {
			current.Conflicted = true
			s.boardAssetReferenceStates[boardID] = current
			return cloneBoardAssetReferenceState(current), ErrAssetReferenceConflict
		}
		if current.Conflicted {
			return cloneBoardAssetReferenceState(current), ErrAssetReferenceConflict
		}
		return cloneBoardAssetReferenceState(current), nil
	}

	now := time.Now().UTC()
	state := s.replaceBoardAssetReferencesLocked(boardID, throughSequence, indexedBy, now, canonicalIDs)
	return cloneBoardAssetReferenceState(state), nil
}

func (s *MemoryStore) SaveAsset(asset domain.Asset) (domain.Asset, error) {
	if asset.Size < 0 || asset.Width < 0 || asset.Height < 0 {
		return domain.Asset{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.projects[asset.ProjectID]; !ok {
		return domain.Asset{}, ErrNotFound
	}
	if _, ok := s.users[asset.UploadedBy]; !ok {
		return domain.Asset{}, ErrNotFound
	}
	if asset.ID == "" {
		asset.ID = newID("ast")
	}
	if asset.StorageKey == "" {
		asset.StorageKey = asset.ID
	}
	if _, ok := s.assets[asset.ID]; ok {
		return domain.Asset{}, ErrConflict
	}
	for _, existing := range s.assets {
		if existing.StorageKey == asset.StorageKey {
			return domain.Asset{}, ErrConflict
		}
	}
	asset.CreatedAt = time.Now().UTC()
	s.assets[asset.ID] = asset
	return asset, nil
}

func (s *MemoryStore) GetAsset(id string) (domain.Asset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	asset, ok := s.assets[id]
	if !ok {
		return domain.Asset{}, ErrNotFound
	}
	return asset, nil
}

func (s *MemoryStore) ListAssetsByProject(projectID string) ([]domain.Asset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.projects[projectID]; !ok {
		return nil, ErrNotFound
	}
	assets := []domain.Asset{}
	for _, asset := range s.assets {
		if asset.ProjectID == projectID {
			assets = append(assets, asset)
		}
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].CreatedAt.Before(assets[j].CreatedAt) })
	return assets, nil
}

func (s *MemoryStore) DeleteAsset(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	asset, ok := s.assets[id]
	if !ok {
		return ErrNotFound
	}
	for boardID, board := range s.boards {
		if board.ProjectID != asset.ProjectID {
			continue
		}
		state, ok := s.boardAssetReferenceStates[boardID]
		if ok && state.Conflicted {
			return ErrAssetReferenceIndexStale
		}
	}
	for boardID, board := range s.boards {
		if board.ProjectID != asset.ProjectID {
			continue
		}
		document, documentOK := s.boardDocuments[boardID]
		state, stateOK := s.boardAssetReferenceStates[boardID]
		if !documentOK || !stateOK || state.IndexedThroughSequence != s.latestBoardSequenceLocked(boardID, document) {
			return ErrAssetReferenceIndexStale
		}
	}
	for boardID, board := range s.boards {
		if board.ProjectID != asset.ProjectID {
			continue
		}
		if _, referenced := s.boardAssetReferences[boardID][id]; referenced {
			return ErrAssetInUse
		}
	}
	s.enqueueStorageCleanupLocked(StorageCleanupAssetFile, asset.ProjectID, asset.StorageKey, time.Now().UTC())
	delete(s.assets, id)
	return nil
}

func (s *MemoryStore) latestBoardSequenceLocked(boardID string, document domain.BoardDocument) int64 {
	latest := document.CheckpointSequence
	for _, update := range s.boardUpdates[boardID] {
		if update.ServerSequence > latest {
			latest = update.ServerSequence
		}
	}
	return latest
}

func (s *MemoryStore) validateBoardAssetReferencesLocked(projectID string, assetIDs []string) error {
	for _, assetID := range assetIDs {
		asset, ok := s.assets[assetID]
		if !ok || asset.ProjectID != projectID {
			return ErrInvalidAssetReference
		}
	}
	return nil
}

func (s *MemoryStore) replaceBoardAssetReferencesLocked(boardID string, throughSequence int64, indexedBy string, indexedAt time.Time, assetIDs []string) domain.BoardAssetReferenceState {
	refs := make(map[string]struct{}, len(assetIDs))
	for _, assetID := range assetIDs {
		refs[assetID] = struct{}{}
	}
	s.boardAssetReferences[boardID] = refs
	state := domain.BoardAssetReferenceState{
		BoardID:                boardID,
		IndexedThroughSequence: throughSequence,
		RefsHash:               hashAssetReferences(assetIDs),
		IndexedBy:              indexedBy,
		IndexedAt:              &indexedAt,
		Conflicted:             false,
	}
	s.boardAssetReferenceStates[boardID] = state
	return state
}

func ownerCount(members map[string]domain.ProjectMember) int {
	count := 0
	for _, member := range members {
		if member.Role == domain.RoleOwner {
			count++
		}
	}
	return count
}

func systemAdminCount(users map[string]domain.User) int {
	count := 0
	for _, user := range users {
		if user.SystemRole == domain.SystemAdmin {
			count++
		}
	}
	return count
}

func publicStoreUser(user domain.User) domain.User {
	user.PasswordHash = ""
	return user
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value...)
}

func cloneStrings(value []string) []string {
	if value == nil {
		return nil
	}
	return append([]string(nil), value...)
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneBoardUpdate(update domain.BoardUpdate) domain.BoardUpdate {
	update.Update = cloneBytes(update.Update)
	update.AssetIDs = cloneStrings(update.AssetIDs)
	update.ReferenceBaseSequence = cloneInt64(update.ReferenceBaseSequence)
	return update
}

func cloneBoardAssetReferenceState(state domain.BoardAssetReferenceState) domain.BoardAssetReferenceState {
	if state.IndexedAt != nil {
		indexedAt := *state.IndexedAt
		state.IndexedAt = &indexedAt
	}
	return state
}
