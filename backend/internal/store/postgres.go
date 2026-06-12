package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"dreamwhiteboard/backend/internal/domain"
	_ "github.com/lib/pq"
)

type PostgresStore struct {
	db *sql.DB
}

func OpenPostgres(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) EnsureSystemAdmin(email, password string) (domain.User, error) {
	email = normalizeEmail(email)
	if user, ok := s.userByEmail(email); ok {
		return user, nil
	}
	user := domain.User{ID: newID("usr"), Email: email, Name: "System Admin", SystemRole: domain.SystemAdmin, PasswordHash: HashPassword(password), CreatedAt: time.Now().UTC()}
	_, err := s.db.Exec(`INSERT INTO users (id, email, name, system_role, password_hash, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		user.ID, user.Email, user.Name, user.SystemRole, user.PasswordHash, user.CreatedAt)
	if isUnique(err) {
		if existing, ok := s.userByEmail(email); ok {
			return existing, nil
		}
	}
	return user, err
}

func (s *PostgresStore) Authenticate(email, password string) (domain.User, bool) {
	user, ok := s.userByEmail(normalizeEmail(email))
	if !ok {
		return domain.User{}, false
	}
	return user, VerifyPassword(password, user.PasswordHash)
}

func (s *PostgresStore) CreateUser(email, name, password, systemRole string) (domain.User, error) {
	email = normalizeEmail(email)
	if systemRole == "" {
		systemRole = domain.SystemUser
	}
	if strings.TrimSpace(name) == "" {
		name = email
	}
	user := domain.User{ID: newID("usr"), Email: email, Name: strings.TrimSpace(name), SystemRole: systemRole, PasswordHash: HashPassword(password), CreatedAt: time.Now().UTC()}
	_, err := s.db.Exec(`INSERT INTO users (id, email, name, system_role, password_hash, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		user.ID, user.Email, user.Name, user.SystemRole, user.PasswordHash, user.CreatedAt)
	if isUnique(err) {
		return domain.User{}, ErrConflict
	}
	return publicStoreUser(user), err
}

func (s *PostgresStore) UpdateUser(id, name, systemRole string) (domain.User, error) {
	user, ok := s.GetUser(id)
	if !ok {
		return domain.User{}, ErrNotFound
	}
	if strings.TrimSpace(name) != "" {
		user.Name = strings.TrimSpace(name)
	}
	if systemRole == domain.SystemAdmin || systemRole == domain.SystemUser {
		user.SystemRole = systemRole
	}
	_, err := s.db.Exec(`UPDATE users SET name=$2, system_role=$3 WHERE id=$1`, user.ID, user.Name, user.SystemRole)
	return user, err
}

func (s *PostgresStore) GetUser(id string) (domain.User, bool) {
	user, err := s.scanUser(s.db.QueryRow(`SELECT id, email, name, system_role, password_hash, created_at FROM users WHERE id=$1`, id))
	return user, err == nil
}

func (s *PostgresStore) ListUsers() []domain.User {
	rows, err := s.db.Query(`SELECT id, email, name, system_role, password_hash, created_at FROM users ORDER BY email`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	users := []domain.User{}
	for rows.Next() {
		var user domain.User
		if err := rows.Scan(&user.ID, &user.Email, &user.Name, &user.SystemRole, &user.PasswordHash, &user.CreatedAt); err == nil {
			users = append(users, publicStoreUser(user))
		}
	}
	return users
}

func (s *PostgresStore) CreateProject(name, description, createdBy string) (domain.Project, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return domain.Project{}, err
	}
	defer tx.Rollback()
	project := domain.Project{ID: newID("prj"), Name: strings.TrimSpace(name), Description: strings.TrimSpace(description), CreatedBy: createdBy, CreatedAt: time.Now().UTC()}
	if project.Name == "" {
		project.Name = "Untitled project"
	}
	if _, err := tx.Exec(`INSERT INTO projects (id, name, description, created_by, created_at) VALUES ($1,$2,$3,$4,$5)`,
		project.ID, project.Name, project.Description, project.CreatedBy, project.CreatedAt); err != nil {
		return domain.Project{}, err
	}
	if _, err := tx.Exec(`INSERT INTO project_members (project_id, user_id, role, created_at) VALUES ($1,$2,$3,$4)`,
		project.ID, createdBy, domain.RoleOwner, time.Now().UTC()); err != nil {
		return domain.Project{}, err
	}
	return project, tx.Commit()
}

func (s *PostgresStore) ListProjects(user domain.User) []domain.Project {
	query := `SELECT id, name, description, created_by, created_at FROM projects ORDER BY created_at DESC`
	args := []any{}
	if user.SystemRole != domain.SystemAdmin {
		query = `SELECT p.id, p.name, p.description, p.created_by, p.created_at
			FROM projects p JOIN project_members m ON m.project_id = p.id
			WHERE m.user_id=$1 ORDER BY p.created_at DESC`
		args = append(args, user.ID)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	projects := []domain.Project{}
	for rows.Next() {
		var project domain.Project
		if err := rows.Scan(&project.ID, &project.Name, &project.Description, &project.CreatedBy, &project.CreatedAt); err == nil {
			projects = append(projects, project)
		}
	}
	return projects
}

func (s *PostgresStore) GetProject(id string) (domain.Project, error) {
	var project domain.Project
	err := s.db.QueryRow(`SELECT id, name, description, created_by, created_at FROM projects WHERE id=$1`, id).
		Scan(&project.ID, &project.Name, &project.Description, &project.CreatedBy, &project.CreatedAt)
	return project, mapSQLError(err)
}

func (s *PostgresStore) UpdateProject(id, name, description string) (domain.Project, error) {
	project, err := s.GetProject(id)
	if err != nil {
		return domain.Project{}, err
	}
	if strings.TrimSpace(name) != "" {
		project.Name = strings.TrimSpace(name)
	}
	project.Description = strings.TrimSpace(description)
	_, err = s.db.Exec(`UPDATE projects SET name=$2, description=$3 WHERE id=$1`, id, project.Name, project.Description)
	return project, err
}

func (s *PostgresStore) DeleteProject(id string) error {
	result, err := s.db.Exec(`DELETE FROM projects WHERE id=$1`, id)
	return resultError(result, err)
}

func (s *PostgresStore) MemberRole(projectID, userID string) (string, bool) {
	var role string
	err := s.db.QueryRow(`SELECT role FROM project_members WHERE project_id=$1 AND user_id=$2`, projectID, userID).Scan(&role)
	return role, err == nil
}

func (s *PostgresStore) UpsertMember(projectID, userID, role string) (domain.ProjectMember, error) {
	if !domain.IsProjectRole(role) {
		return domain.ProjectMember{}, fmt.Errorf("invalid role")
	}
	member := domain.ProjectMember{ProjectID: projectID, UserID: userID, Role: role, CreatedAt: time.Now().UTC()}
	_, err := s.db.Exec(`INSERT INTO project_members (project_id, user_id, role, created_at)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (project_id, user_id) DO UPDATE SET role=EXCLUDED.role`,
		projectID, userID, role, member.CreatedAt)
	return member, mapSQLError(err)
}

func (s *PostgresStore) DeleteMember(projectID, userID string) error {
	result, err := s.db.Exec(`DELETE FROM project_members WHERE project_id=$1 AND user_id=$2`, projectID, userID)
	return resultError(result, err)
}

func (s *PostgresStore) ListMembers(projectID string) ([]domain.ProjectMember, error) {
	rows, err := s.db.Query(`SELECT m.project_id, m.user_id, m.role, m.created_at, u.id, u.email, u.name, u.system_role, u.created_at
		FROM project_members m JOIN users u ON u.id = m.user_id WHERE m.project_id=$1 ORDER BY u.email`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []domain.ProjectMember{}
	for rows.Next() {
		var member domain.ProjectMember
		var user domain.User
		if err := rows.Scan(&member.ProjectID, &member.UserID, &member.Role, &member.CreatedAt, &user.ID, &user.Email, &user.Name, &user.SystemRole, &user.CreatedAt); err != nil {
			return nil, err
		}
		member.User = &user
		members = append(members, member)
	}
	return members, nil
}

func (s *PostgresStore) CreateBoard(projectID, name, createdBy string) (domain.Board, error) {
	now := time.Now().UTC()
	board := domain.Board{ID: newID("brd"), ProjectID: projectID, Name: strings.TrimSpace(name), CreatedBy: createdBy, CreatedAt: now, UpdatedAt: now}
	if board.Name == "" {
		board.Name = "Untitled board"
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.Board{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO boards (id, project_id, name, version, created_by, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		board.ID, board.ProjectID, board.Name, board.Version, board.CreatedBy, board.CreatedAt, board.UpdatedAt); err != nil {
		return domain.Board{}, mapSQLError(err)
	}
	if _, err := tx.Exec(`INSERT INTO board_snapshots (board_id, version, blocks, updated_at) VALUES ($1,$2,$3,$4)`, board.ID, 0, `[]`, now); err != nil {
		return domain.Board{}, err
	}
	return board, tx.Commit()
}

func (s *PostgresStore) ListBoards(projectID string) ([]domain.Board, error) {
	rows, err := s.db.Query(`SELECT id, project_id, name, version, created_by, created_at, updated_at FROM boards WHERE project_id=$1 ORDER BY updated_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	boards := []domain.Board{}
	for rows.Next() {
		var board domain.Board
		if err := rows.Scan(&board.ID, &board.ProjectID, &board.Name, &board.Version, &board.CreatedBy, &board.CreatedAt, &board.UpdatedAt); err != nil {
			return nil, err
		}
		boards = append(boards, board)
	}
	return boards, nil
}

func (s *PostgresStore) GetBoard(id string) (domain.Board, error) {
	var board domain.Board
	err := s.db.QueryRow(`SELECT id, project_id, name, version, created_by, created_at, updated_at FROM boards WHERE id=$1`, id).
		Scan(&board.ID, &board.ProjectID, &board.Name, &board.Version, &board.CreatedBy, &board.CreatedAt, &board.UpdatedAt)
	return board, mapSQLError(err)
}

func (s *PostgresStore) UpdateBoard(id, name string) (domain.Board, error) {
	board, err := s.GetBoard(id)
	if err != nil {
		return domain.Board{}, err
	}
	if strings.TrimSpace(name) != "" {
		board.Name = strings.TrimSpace(name)
	}
	board.UpdatedAt = time.Now().UTC()
	_, err = s.db.Exec(`UPDATE boards SET name=$2, updated_at=$3 WHERE id=$1`, id, board.Name, board.UpdatedAt)
	return board, err
}

func (s *PostgresStore) DeleteBoard(id string) error {
	result, err := s.db.Exec(`DELETE FROM boards WHERE id=$1`, id)
	return resultError(result, err)
}

func (s *PostgresStore) Snapshot(boardID string) (domain.BoardSnapshot, error) {
	var snapshot domain.BoardSnapshot
	var raw []byte
	err := s.db.QueryRow(`SELECT b.id, b.version, COALESCE(s.blocks, '[]'::jsonb), b.updated_at
		FROM boards b LEFT JOIN board_snapshots s ON s.board_id=b.id WHERE b.id=$1`, boardID).
		Scan(&snapshot.BoardID, &snapshot.Version, &raw, &snapshot.UpdatedAt)
	if err != nil {
		return domain.BoardSnapshot{}, mapSQLError(err)
	}
	if err := json.Unmarshal(raw, &snapshot.Blocks); err != nil {
		return domain.BoardSnapshot{}, err
	}
	sort.Slice(snapshot.Blocks, func(i, j int) bool { return snapshot.Blocks[i].Z < snapshot.Blocks[j].Z })
	return snapshot, nil
}

func (s *PostgresStore) ApplyOperation(op domain.Operation) (domain.Operation, domain.BoardSnapshot, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return domain.Operation{}, domain.BoardSnapshot{}, err
	}
	defer tx.Rollback()
	var board domain.Board
	err = tx.QueryRow(`SELECT id, project_id, name, version, created_by, created_at, updated_at FROM boards WHERE id=$1 FOR UPDATE`, op.BoardID).
		Scan(&board.ID, &board.ProjectID, &board.Name, &board.Version, &board.CreatedBy, &board.CreatedAt, &board.UpdatedAt)
	if err != nil {
		return domain.Operation{}, domain.BoardSnapshot{}, mapSQLError(err)
	}
	var raw []byte
	if err := tx.QueryRow(`SELECT blocks FROM board_snapshots WHERE board_id=$1 FOR UPDATE`, op.BoardID).Scan(&raw); err != nil {
		return domain.Operation{}, domain.BoardSnapshot{}, mapSQLError(err)
	}
	var list []domain.Block
	if err := json.Unmarshal(raw, &list); err != nil {
		return domain.Operation{}, domain.BoardSnapshot{}, err
	}
	blocks := map[string]domain.Block{}
	for _, block := range list {
		blocks[block.ID] = block
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
	snapshot := snapshotLocked(op.BoardID, board, blocks)
	blocksJSON, _ := json.Marshal(snapshot.Blocks)
	payloadJSON, _ := json.Marshal(op.Payload)
	if _, err := tx.Exec(`UPDATE boards SET version=$2, updated_at=$3 WHERE id=$1`, board.ID, board.Version, board.UpdatedAt); err != nil {
		return domain.Operation{}, domain.BoardSnapshot{}, err
	}
	if _, err := tx.Exec(`INSERT INTO board_snapshots (board_id, version, blocks, updated_at) VALUES ($1,$2,$3,$4)
		ON CONFLICT (board_id) DO UPDATE SET version=EXCLUDED.version, blocks=EXCLUDED.blocks, updated_at=EXCLUDED.updated_at`,
		board.ID, snapshot.Version, string(blocksJSON), snapshot.UpdatedAt); err != nil {
		return domain.Operation{}, domain.BoardSnapshot{}, err
	}
	if _, err := tx.Exec(`INSERT INTO board_operations (id, board_id, client_id, user_id, op_type, base_version, version, payload, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		op.ID, op.BoardID, op.ClientID, op.UserID, op.Type, op.BaseVersion, op.Version, string(payloadJSON), op.CreatedAt); err != nil {
		return domain.Operation{}, domain.BoardSnapshot{}, err
	}
	return op, snapshot, tx.Commit()
}

func (s *PostgresStore) SaveAsset(asset domain.Asset) domain.Asset {
	if asset.ID == "" {
		asset.ID = newID("ast")
	}
	asset.CreatedAt = time.Now().UTC()
	_, _ = s.db.Exec(`INSERT INTO assets (id, project_id, uploaded_by, file_name, content_type, size, path, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		asset.ID, asset.ProjectID, asset.UploadedBy, asset.FileName, asset.ContentType, asset.Size, asset.Path, asset.CreatedAt)
	return asset
}

func (s *PostgresStore) GetAsset(id string) (domain.Asset, error) {
	var asset domain.Asset
	err := s.db.QueryRow(`SELECT id, project_id, uploaded_by, file_name, content_type, size, path, created_at FROM assets WHERE id=$1`, id).
		Scan(&asset.ID, &asset.ProjectID, &asset.UploadedBy, &asset.FileName, &asset.ContentType, &asset.Size, &asset.Path, &asset.CreatedAt)
	return asset, mapSQLError(err)
}

func (s *PostgresStore) userByEmail(email string) (domain.User, bool) {
	user, err := s.scanUser(s.db.QueryRow(`SELECT id, email, name, system_role, password_hash, created_at FROM users WHERE email=$1`, email))
	return user, err == nil
}

func (s *PostgresStore) scanUser(row interface{ Scan(...any) error }) (domain.User, error) {
	var user domain.User
	err := row.Scan(&user.ID, &user.Email, &user.Name, &user.SystemRole, &user.PasswordHash, &user.CreatedAt)
	return user, err
}

func publicStoreUser(user domain.User) domain.User {
	user.PasswordHash = ""
	return user
}

func mapSQLError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func resultError(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate key")
}
