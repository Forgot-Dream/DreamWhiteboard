package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"dreamwhiteboard/backend/internal/domain"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrForbidden = errors.New("forbidden")
)

type MemoryStore struct {
	mu          sync.RWMutex
	users       map[string]domain.User
	userByMail  map[string]string
	projects    map[string]domain.Project
	members     map[string]map[string]domain.ProjectMember
	boards      map[string]domain.Board
	boardBlocks map[string]map[string]domain.Block
	operations  map[string][]domain.Operation
	assets      map[string]domain.Asset
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		users:       map[string]domain.User{},
		userByMail:  map[string]string{},
		projects:    map[string]domain.Project{},
		members:     map[string]map[string]domain.ProjectMember{},
		boards:      map[string]domain.Board{},
		boardBlocks: map[string]map[string]domain.Block{},
		operations:  map[string][]domain.Operation{},
		assets:      map[string]domain.Asset{},
	}
}

func (s *MemoryStore) EnsureSystemAdmin(email, password string) (domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	email = normalizeEmail(email)
	if id, ok := s.userByMail[email]; ok {
		return s.users[id], nil
	}
	user := domain.User{
		ID:           newID("usr"),
		Email:        email,
		Name:         "System Admin",
		SystemRole:   domain.SystemAdmin,
		PasswordHash: HashPassword(password),
		CreatedAt:    time.Now().UTC(),
	}
	s.users[user.ID] = user
	s.userByMail[email] = user.ID
	return user, nil
}

func (s *MemoryStore) Authenticate(email, password string) (domain.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.userByMail[normalizeEmail(email)]
	if !ok {
		return domain.User{}, false
	}
	user := s.users[id]
	return user, VerifyPassword(password, user.PasswordHash)
}

func (s *MemoryStore) CreateUser(email, name, password, systemRole string) (domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	email = normalizeEmail(email)
	if _, ok := s.userByMail[email]; ok {
		return domain.User{}, ErrConflict
	}
	if systemRole == "" {
		systemRole = domain.SystemUser
	}
	user := domain.User{
		ID:           newID("usr"),
		Email:        email,
		Name:         strings.TrimSpace(name),
		SystemRole:   systemRole,
		PasswordHash: HashPassword(password),
		CreatedAt:    time.Now().UTC(),
	}
	if user.Name == "" {
		user.Name = email
	}
	s.users[user.ID] = user
	s.userByMail[email] = user.ID
	return user, nil
}

func (s *MemoryStore) UpdateUser(id, name, systemRole string) (domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[id]
	if !ok {
		return domain.User{}, ErrNotFound
	}
	if strings.TrimSpace(name) != "" {
		user.Name = strings.TrimSpace(name)
	}
	if systemRole == domain.SystemAdmin || systemRole == domain.SystemUser {
		user.SystemRole = systemRole
	}
	s.users[id] = user
	return user, nil
}

func (s *MemoryStore) GetUser(id string) (domain.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.users[id]
	return user, ok
}

func (s *MemoryStore) ListUsers() []domain.User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	users := make([]domain.User, 0, len(s.users))
	for _, user := range s.users {
		users = append(users, user)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Email < users[j].Email })
	return users
}

func (s *MemoryStore) CreateProject(name, description, createdBy string) (domain.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	project := domain.Project{
		ID:          newID("prj"),
		Name:        strings.TrimSpace(name),
		Description: strings.TrimSpace(description),
		CreatedBy:   createdBy,
		CreatedAt:   time.Now().UTC(),
	}
	if project.Name == "" {
		project.Name = "Untitled project"
	}
	s.projects[project.ID] = project
	s.members[project.ID] = map[string]domain.ProjectMember{
		createdBy: {
			ProjectID: project.ID,
			UserID:    createdBy,
			Role:      domain.RoleOwner,
			CreatedAt: time.Now().UTC(),
		},
	}
	return project, nil
}

func (s *MemoryStore) ListProjects(user domain.User) []domain.Project {
	s.mu.RLock()
	defer s.mu.RUnlock()
	projects := []domain.Project{}
	for _, project := range s.projects {
		if user.SystemRole == domain.SystemAdmin || s.members[project.ID][user.ID].UserID != "" {
			projects = append(projects, project)
		}
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].CreatedAt.After(projects[j].CreatedAt) })
	return projects
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
	if strings.TrimSpace(name) != "" {
		project.Name = strings.TrimSpace(name)
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
	delete(s.projects, id)
	delete(s.members, id)
	for boardID, board := range s.boards {
		if board.ProjectID == id {
			delete(s.boards, boardID)
			delete(s.boardBlocks, boardID)
			delete(s.operations, boardID)
		}
	}
	return nil
}

func (s *MemoryStore) MemberRole(projectID, userID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	member, ok := s.members[projectID][userID]
	return member.Role, ok
}

func (s *MemoryStore) UpsertMember(projectID, userID, role string) (domain.ProjectMember, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.projects[projectID]; !ok {
		return domain.ProjectMember{}, ErrNotFound
	}
	if _, ok := s.users[userID]; !ok {
		return domain.ProjectMember{}, ErrNotFound
	}
	if !domain.IsProjectRole(role) {
		return domain.ProjectMember{}, fmt.Errorf("invalid role")
	}
	if s.members[projectID] == nil {
		s.members[projectID] = map[string]domain.ProjectMember{}
	}
	member := domain.ProjectMember{
		ProjectID: projectID,
		UserID:    userID,
		Role:      role,
		CreatedAt: time.Now().UTC(),
	}
	s.members[projectID][userID] = member
	return member, nil
}

func (s *MemoryStore) DeleteMember(projectID, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.members[projectID][userID]; !ok {
		return ErrNotFound
	}
	delete(s.members[projectID], userID)
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
		u := s.users[member.UserID]
		u.PasswordHash = ""
		member.User = &u
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
	now := time.Now().UTC()
	board := domain.Board{
		ID:        newID("brd"),
		ProjectID: projectID,
		Name:      strings.TrimSpace(name),
		Version:   0,
		CreatedBy: createdBy,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if board.Name == "" {
		board.Name = "Untitled board"
	}
	s.boards[board.ID] = board
	s.boardBlocks[board.ID] = map[string]domain.Block{}
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
	if strings.TrimSpace(name) != "" {
		board.Name = strings.TrimSpace(name)
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
	delete(s.boardBlocks, id)
	delete(s.operations, id)
	return nil
}

func (s *MemoryStore) Snapshot(boardID string) (domain.BoardSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	board, ok := s.boards[boardID]
	if !ok {
		return domain.BoardSnapshot{}, ErrNotFound
	}
	blocks := make([]domain.Block, 0, len(s.boardBlocks[boardID]))
	for _, block := range s.boardBlocks[boardID] {
		blocks = append(blocks, cloneBlock(block))
	}
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].Z == blocks[j].Z {
			return blocks[i].ID < blocks[j].ID
		}
		return blocks[i].Z < blocks[j].Z
	})
	return domain.BoardSnapshot{BoardID: boardID, Version: board.Version, Blocks: blocks, UpdatedAt: board.UpdatedAt}, nil
}

func (s *MemoryStore) ApplyOperation(op domain.Operation) (domain.Operation, domain.BoardSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	board, ok := s.boards[op.BoardID]
	if !ok {
		return domain.Operation{}, domain.BoardSnapshot{}, ErrNotFound
	}
	blocks := s.boardBlocks[op.BoardID]
	if blocks == nil {
		blocks = map[string]domain.Block{}
		s.boardBlocks[op.BoardID] = blocks
	}
	board.Version++
	board.UpdatedAt = time.Now().UTC()
	op.Version = board.Version
	op.CreatedAt = board.UpdatedAt
	if op.ID == "" {
		op.ID = newID("op")
	}
	if err := applyBlockOperation(blocks, op); err != nil {
		return domain.Operation{}, domain.BoardSnapshot{}, err
	}
	s.boards[op.BoardID] = board
	s.operations[op.BoardID] = append(s.operations[op.BoardID], op)
	snapshot := snapshotLocked(op.BoardID, board, blocks)
	return op, snapshot, nil
}

func (s *MemoryStore) SaveAsset(asset domain.Asset) domain.Asset {
	s.mu.Lock()
	defer s.mu.Unlock()
	if asset.ID == "" {
		asset.ID = newID("ast")
	}
	asset.CreatedAt = time.Now().UTC()
	s.assets[asset.ID] = asset
	return asset
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

func applyBlockOperation(blocks map[string]domain.Block, op domain.Operation) error {
	block := blockFromPayload(op.Payload)
	switch op.Type {
	case "create_block":
		if block.ID == "" {
			block.ID = newID("blk")
		}
		if block.Type == "" {
			block.Type = domain.BlockNote
		}
		if block.W == 0 {
			block.W = 240
		}
		if block.H == 0 {
			block.H = 160
		}
		blocks[block.ID] = cloneBlock(block)
	case "update_block", "move_block", "resize_block", "reorder_block":
		current, ok := blocks[block.ID]
		if !ok {
			return ErrNotFound
		}
		merged := mergeBlock(current, block, op.Type)
		blocks[merged.ID] = merged
	case "delete_block":
		id, _ := op.Payload["id"].(string)
		if id == "" {
			id = block.ID
		}
		delete(blocks, id)
	default:
		return fmt.Errorf("unsupported operation %q", op.Type)
	}
	return nil
}

func blockFromPayload(payload map[string]any) domain.Block {
	if nested, ok := payload["block"].(map[string]any); ok {
		payload = nested
	}
	return domain.Block{
		ID:   stringField(payload, "id"),
		Type: stringField(payload, "type"),
		X:    floatField(payload, "x"),
		Y:    floatField(payload, "y"),
		W:    floatField(payload, "w"),
		H:    floatField(payload, "h"),
		Z:    int(floatField(payload, "z")),
		Data: mapField(payload, "data"),
	}
}

func mergeBlock(current, update domain.Block, opType string) domain.Block {
	if update.Type != "" {
		current.Type = update.Type
	}
	if opType == "move_block" || opType == "update_block" || opType == "create_block" {
		current.X = update.X
		current.Y = update.Y
	}
	if opType == "resize_block" || opType == "update_block" {
		if update.W > 0 {
			current.W = update.W
		}
		if update.H > 0 {
			current.H = update.H
		}
	}
	if opType == "reorder_block" || opType == "update_block" {
		current.Z = update.Z
	}
	if update.Data != nil {
		current.Data = cloneMap(update.Data)
	}
	return current
}

func snapshotLocked(boardID string, board domain.Board, blocks map[string]domain.Block) domain.BoardSnapshot {
	out := make([]domain.Block, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, cloneBlock(block))
	}
	return domain.BoardSnapshot{BoardID: boardID, Version: board.Version, Blocks: out, UpdatedAt: board.UpdatedAt}
}

func stringField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func floatField(m map[string]any, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}

func mapField(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return cloneMap(v)
}

func cloneBlock(block domain.Block) domain.Block {
	block.Data = cloneMap(block.Data)
	return block
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func HashPassword(password string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	sum := sha256.Sum256(append(salt, []byte(password)...))
	return base64.RawURLEncoding.EncodeToString(salt) + "." + hex.EncodeToString(sum[:])
}

func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, ".")
	if len(parts) != 2 {
		return false
	}
	salt, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	sum := sha256.Sum256(append(salt, []byte(password)...))
	return hex.EncodeToString(sum[:]) == parts[1]
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
