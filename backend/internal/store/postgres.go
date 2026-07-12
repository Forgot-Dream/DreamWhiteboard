package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"dreamwhiteboard/backend/internal/domain"
	"github.com/lib/pq"
)

const LatestSchemaVersion = 5

type PostgresStore struct {
	db *sql.DB
}

func OpenPostgres(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) Ready(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return err
	}
	var count, minimum, maximum, verified int
	if err := s.db.QueryRowContext(ctx, `SELECT
		COUNT(*),
		COALESCE(MIN(version), 0),
		COALESCE(MAX(version), 0),
		COUNT(*) FILTER (WHERE name <> '' AND checksum ~ '^[0-9a-f]{64}$')
		FROM schema_migrations`).Scan(&count, &minimum, &maximum, &verified); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if count != LatestSchemaVersion || minimum != 1 || maximum != LatestSchemaVersion || verified != LatestSchemaVersion {
		return fmt.Errorf("database migration history is incomplete or incompatible: count=%d verified=%d min=%d max=%d required=1..%d", count, verified, minimum, maximum, LatestSchemaVersion)
	}
	var sessions, documents, updates, cleanupJobs, referenceState, references, gcCandidates sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT
		to_regclass('sessions'),
		to_regclass('board_documents'),
		to_regclass('board_updates'),
		to_regclass('storage_cleanup_jobs'),
		to_regclass('board_asset_reference_state'),
		to_regclass('board_asset_references'),
		to_regclass('asset_gc_candidates')`).Scan(&sessions, &documents, &updates, &cleanupJobs, &referenceState, &references, &gcCandidates); err != nil {
		return fmt.Errorf("inspect required schema: %w", err)
	}
	if !sessions.Valid || !documents.Valid || !updates.Valid || !cleanupJobs.Valid || !referenceState.Valid || !references.Valid || !gcCandidates.Valid {
		return fmt.Errorf("database schema is missing required relations")
	}
	return nil
}

func (s *PostgresStore) Close() error { return s.db.Close() }

func (s *PostgresStore) UserCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *PostgresStore) EnsureSystemAdmin(email, password string) (domain.User, error) {
	email = normalizeEmail(email)
	if email == "" || password == "" {
		return domain.User{}, ErrInvalidInput
	}
	if user, err := s.userByEmailResult(email); err == nil {
		if user.SystemRole != domain.SystemAdmin {
			return domain.User{}, ErrConflict
		}
		return publicStoreUser(user), nil
	} else if !errors.Is(err, ErrNotFound) {
		return domain.User{}, err
	}
	user := domain.User{
		ID:                 newID("usr"),
		Email:              email,
		Name:               "System Admin",
		SystemRole:         domain.SystemAdmin,
		PasswordHash:       HashPassword(password),
		MustChangePassword: true,
		CreatedAt:          time.Now().UTC(),
	}
	_, err := s.db.Exec(`INSERT INTO users
		(id, email, name, system_role, password_hash, must_change_password, password_changed_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		user.ID,
		user.Email,
		user.Name,
		user.SystemRole,
		user.PasswordHash,
		user.MustChangePassword,
		user.PasswordChangedAt,
		user.CreatedAt,
	)
	if isUnique(err) {
		if existing, lookupErr := s.userByEmailResult(email); lookupErr == nil {
			if existing.SystemRole != domain.SystemAdmin {
				return domain.User{}, ErrConflict
			}
			return publicStoreUser(existing), nil
		} else if !errors.Is(lookupErr, ErrNotFound) {
			return domain.User{}, lookupErr
		}
	}
	return publicStoreUser(user), mapSQLError(err)
}

func (s *PostgresStore) Authenticate(email, password string) (domain.User, error) {
	user, err := s.userByEmailResult(normalizeEmail(email))
	if errors.Is(err, ErrNotFound) {
		_ = VerifyPassword(password, dummyPasswordHash)
		return domain.User{}, ErrInvalidCredentials
	}
	if err != nil {
		return domain.User{}, err
	}
	if !VerifyPassword(password, user.PasswordHash) {
		return domain.User{}, ErrInvalidCredentials
	}
	return publicStoreUser(user), nil
}

func (s *PostgresStore) CreateUser(email, name, password, systemRole string) (domain.User, error) {
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
	user := domain.User{
		ID:                 newID("usr"),
		Email:              email,
		Name:               name,
		SystemRole:         systemRole,
		PasswordHash:       HashPassword(password),
		MustChangePassword: true,
		CreatedAt:          time.Now().UTC(),
	}
	_, err := s.db.Exec(`INSERT INTO users
		(id, email, name, system_role, password_hash, must_change_password, password_changed_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		user.ID,
		user.Email,
		user.Name,
		user.SystemRole,
		user.PasswordHash,
		user.MustChangePassword,
		user.PasswordChangedAt,
		user.CreatedAt,
	)
	return publicStoreUser(user), mapSQLError(err)
}

func (s *PostgresStore) UpdateUser(id, name, systemRole string) (domain.User, error) {
	if systemRole != "" && !domain.IsSystemRole(systemRole) {
		return domain.User{}, ErrInvalidInput
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.User{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('dreamwhiteboard.system_admins'))`); err != nil {
		return domain.User{}, err
	}
	user, err := scanUser(tx.QueryRow(userSelect+` WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return domain.User{}, mapSQLError(err)
	}
	if name = strings.TrimSpace(name); name != "" {
		user.Name = name
	}
	if systemRole != "" {
		if user.SystemRole == domain.SystemAdmin && systemRole != domain.SystemAdmin {
			var count int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE system_role=$1`, domain.SystemAdmin).Scan(&count); err != nil {
				return domain.User{}, err
			}
			if count <= 1 {
				return domain.User{}, ErrLastSystemAdmin
			}
		}
		user.SystemRole = systemRole
	}
	if _, err := tx.Exec(`UPDATE users SET name=$2, system_role=$3 WHERE id=$1`, user.ID, user.Name, user.SystemRole); err != nil {
		return domain.User{}, mapSQLError(err)
	}
	return publicStoreUser(user), tx.Commit()
}

func (s *PostgresStore) UpdatePassword(userID, newPassword string, mustChangePassword bool) error {
	if newPassword == "" {
		return ErrInvalidInput
	}
	passwordHash := HashPassword(newPassword)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	result, err := tx.Exec(`UPDATE users
		SET password_hash=$2, must_change_password=$3, password_changed_at=$4
		WHERE id=$1`, userID, passwordHash, mustChangePassword, now)
	if err := resultError(result, err); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id=$1`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) GetUser(id string) (domain.User, error) {
	user, err := s.getUser(id)
	return publicStoreUser(user), err
}

func (s *PostgresStore) ListUsers() ([]domain.User, error) {
	rows, err := s.db.Query(userSelect + ` ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []domain.User{}
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, publicStoreUser(user))
	}
	return users, rows.Err()
}

func (s *PostgresStore) CreateSession(tokenHash, userID string, expiresAt time.Time) (domain.Session, error) {
	if tokenHash == "" || expiresAt.IsZero() {
		return domain.Session{}, ErrInvalidInput
	}
	now := time.Now().UTC()
	if !expiresAt.After(now) {
		return domain.Session{}, ErrInvalidInput
	}
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at<=$1`, now); err != nil {
		return domain.Session{}, err
	}
	session := domain.Session{
		TokenHash:      tokenHash,
		UserID:         userID,
		ExpiresAt:      expiresAt.UTC(),
		LastAccessedAt: now,
		CreatedAt:      now,
	}
	err := s.db.QueryRow(`INSERT INTO sessions
		(token_hash, user_id, expires_at, last_accessed_at, created_at)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING token_hash, user_id, expires_at, last_accessed_at, created_at`,
		session.TokenHash,
		session.UserID,
		session.ExpiresAt,
		session.LastAccessedAt,
		session.CreatedAt,
	).Scan(
		&session.TokenHash,
		&session.UserID,
		&session.ExpiresAt,
		&session.LastAccessedAt,
		&session.CreatedAt,
	)
	return session, mapSQLError(err)
}

func (s *PostgresStore) GetSession(tokenHash string, now time.Time) (domain.Session, error) {
	if tokenHash == "" {
		return domain.Session{}, ErrNotFound
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	var session domain.Session
	err := s.db.QueryRow(`UPDATE sessions
		SET last_accessed_at=$2
		WHERE token_hash=$1 AND expires_at>$2
		RETURNING token_hash, user_id, expires_at, last_accessed_at, created_at`, tokenHash, now).Scan(
		&session.TokenHash,
		&session.UserID,
		&session.ExpiresAt,
		&session.LastAccessedAt,
		&session.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE token_hash=$1 AND expires_at<=$2`, tokenHash, now)
		return domain.Session{}, ErrNotFound
	}
	return session, mapSQLError(err)
}

func (s *PostgresStore) DeleteSession(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash=$1`, tokenHash)
	return err
}

func (s *PostgresStore) DeleteUserSessions(userID string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id=$1`, userID)
	return err
}

func (s *PostgresStore) CreateProject(name, description, createdBy string) (domain.Project, error) {
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
	tx, err := s.db.Begin()
	if err != nil {
		return domain.Project{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO projects (id, name, description, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5)`, project.ID, project.Name, project.Description, project.CreatedBy, project.CreatedAt); err != nil {
		return domain.Project{}, mapSQLError(err)
	}
	if _, err := tx.Exec(`INSERT INTO project_members (project_id, user_id, role, created_at)
		VALUES ($1,$2,$3,$4)`, project.ID, createdBy, domain.RoleOwner, now); err != nil {
		return domain.Project{}, mapSQLError(err)
	}
	return project, tx.Commit()
}

func (s *PostgresStore) ListProjects(user domain.User) ([]domain.Project, error) {
	query := `SELECT id, name, description, created_by, created_at FROM projects ORDER BY created_at DESC`
	args := []any{}
	if user.SystemRole != domain.SystemAdmin {
		query = `SELECT p.id, p.name, p.description, p.created_by, p.created_at
			FROM projects p
			JOIN project_members m ON m.project_id=p.id
			WHERE m.user_id=$1
			ORDER BY p.created_at DESC`
		args = append(args, user.ID)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []domain.Project{}
	for rows.Next() {
		var project domain.Project
		if err := rows.Scan(&project.ID, &project.Name, &project.Description, &project.CreatedBy, &project.CreatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func (s *PostgresStore) GetProject(id string) (domain.Project, error) {
	var project domain.Project
	err := s.db.QueryRow(`SELECT id, name, description, created_by, created_at FROM projects WHERE id=$1`, id).Scan(
		&project.ID,
		&project.Name,
		&project.Description,
		&project.CreatedBy,
		&project.CreatedAt,
	)
	return project, mapSQLError(err)
}

func (s *PostgresStore) UpdateProject(id, name, description string) (domain.Project, error) {
	project, err := s.GetProject(id)
	if err != nil {
		return domain.Project{}, err
	}
	if name = strings.TrimSpace(name); name != "" {
		project.Name = name
	}
	project.Description = strings.TrimSpace(description)
	_, err = s.db.Exec(`UPDATE projects SET name=$2, description=$3 WHERE id=$1`, id, project.Name, project.Description)
	return project, mapSQLError(err)
}

func (s *PostgresStore) DeleteProject(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var projectID string
	if err := tx.QueryRow(`SELECT id FROM projects WHERE id=$1 FOR UPDATE`, id).Scan(&projectID); err != nil {
		return mapSQLError(err)
	}
	if err := enqueueStorageCleanupTx(tx, StorageCleanupProjectDir, projectID, ""); err != nil {
		return err
	}
	result, err := tx.Exec(`DELETE FROM projects WHERE id=$1`, projectID)
	if err := resultError(result, err); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) MemberRole(projectID, userID string) (string, error) {
	var role string
	err := s.db.QueryRow(`SELECT role FROM project_members WHERE project_id=$1 AND user_id=$2`, projectID, userID).Scan(&role)
	return role, mapSQLError(err)
}

func (s *PostgresStore) UpsertMember(projectID, userID, role string) (domain.ProjectMember, error) {
	if !domain.IsProjectRole(role) {
		return domain.ProjectMember{}, ErrInvalidInput
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.ProjectMember{}, err
	}
	defer tx.Rollback()
	if err := lockProject(tx, projectID); err != nil {
		return domain.ProjectMember{}, err
	}
	var userExists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, userID).Scan(&userExists); err != nil {
		return domain.ProjectMember{}, err
	}
	if !userExists {
		return domain.ProjectMember{}, ErrNotFound
	}

	var currentRole string
	err = tx.QueryRow(`SELECT role FROM project_members WHERE project_id=$1 AND user_id=$2`, projectID, userID).Scan(&currentRole)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.ProjectMember{}, err
	}
	if err == nil && currentRole == domain.RoleOwner && role != domain.RoleOwner {
		count, err := countOwners(tx, projectID)
		if err != nil {
			return domain.ProjectMember{}, err
		}
		if domain.RemovesLastOwner(currentRole, role, count) {
			return domain.ProjectMember{}, ErrLastOwner
		}
	}

	member := domain.ProjectMember{ProjectID: projectID, UserID: userID, Role: role}
	err = tx.QueryRow(`INSERT INTO project_members (project_id, user_id, role, created_at)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (project_id, user_id) DO UPDATE SET role=EXCLUDED.role
		RETURNING created_at`, projectID, userID, role, time.Now().UTC()).Scan(&member.CreatedAt)
	if err != nil {
		return domain.ProjectMember{}, mapSQLError(err)
	}
	return member, tx.Commit()
}

func (s *PostgresStore) DeleteMember(projectID, userID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockProject(tx, projectID); err != nil {
		return err
	}
	var role string
	if err := tx.QueryRow(`SELECT role FROM project_members WHERE project_id=$1 AND user_id=$2`, projectID, userID).Scan(&role); err != nil {
		return mapSQLError(err)
	}
	if role == domain.RoleOwner {
		count, err := countOwners(tx, projectID)
		if err != nil {
			return err
		}
		if domain.RemovesLastOwner(role, "", count) {
			return ErrLastOwner
		}
	}
	result, err := tx.Exec(`DELETE FROM project_members WHERE project_id=$1 AND user_id=$2`, projectID, userID)
	if err := resultError(result, err); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) ListMembers(projectID string) ([]domain.ProjectMember, error) {
	if _, err := s.GetProject(projectID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT
		m.project_id, m.user_id, m.role, m.created_at,
		u.id, u.email, u.name, u.system_role, u.must_change_password, u.password_changed_at, u.created_at
		FROM project_members m
		JOIN users u ON u.id=m.user_id
		WHERE m.project_id=$1
		ORDER BY u.email`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []domain.ProjectMember{}
	for rows.Next() {
		var member domain.ProjectMember
		var user domain.User
		if err := rows.Scan(
			&member.ProjectID,
			&member.UserID,
			&member.Role,
			&member.CreatedAt,
			&user.ID,
			&user.Email,
			&user.Name,
			&user.SystemRole,
			&user.MustChangePassword,
			&user.PasswordChangedAt,
			&user.CreatedAt,
		); err != nil {
			return nil, err
		}
		member.User = &user
		members = append(members, member)
	}
	return members, rows.Err()
}

func (s *PostgresStore) CreateBoard(projectID, name, createdBy string) (domain.Board, error) {
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
	tx, err := s.db.Begin()
	if err != nil {
		return domain.Board{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO boards
		(id, project_id, name, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		board.ID,
		board.ProjectID,
		board.Name,
		board.CreatedBy,
		board.CreatedAt,
		board.UpdatedAt,
	); err != nil {
		return domain.Board{}, mapSQLError(err)
	}
	if _, err := tx.Exec(`INSERT INTO board_documents
		(board_id, checkpoint, checkpoint_sequence, updated_at)
		VALUES ($1,$2,$3,$4)`, board.ID, []byte{}, 0, now); err != nil {
		return domain.Board{}, mapSQLError(err)
	}
	if _, err := tx.Exec(`INSERT INTO board_asset_reference_state
		(board_id, indexed_through_sequence, refs_hash, indexed_by, indexed_at, conflicted)
		VALUES ($1,0,$2,$3,$4,FALSE)`, board.ID, hashAssetReferences([]string{}), createdBy, now); err != nil {
		return domain.Board{}, mapSQLError(err)
	}
	return board, tx.Commit()
}

func (s *PostgresStore) ListBoards(projectID string) ([]domain.Board, error) {
	if _, err := s.GetProject(projectID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id, project_id, name, created_by, created_at, updated_at
		FROM boards WHERE project_id=$1 ORDER BY updated_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	boards := []domain.Board{}
	for rows.Next() {
		var board domain.Board
		if err := rows.Scan(
			&board.ID,
			&board.ProjectID,
			&board.Name,
			&board.CreatedBy,
			&board.CreatedAt,
			&board.UpdatedAt,
		); err != nil {
			return nil, err
		}
		boards = append(boards, board)
	}
	return boards, rows.Err()
}

func (s *PostgresStore) GetBoard(id string) (domain.Board, error) {
	var board domain.Board
	err := s.db.QueryRow(`SELECT id, project_id, name, created_by, created_at, updated_at
		FROM boards WHERE id=$1`, id).Scan(
		&board.ID,
		&board.ProjectID,
		&board.Name,
		&board.CreatedBy,
		&board.CreatedAt,
		&board.UpdatedAt,
	)
	return board, mapSQLError(err)
}

func (s *PostgresStore) UpdateBoard(id, name string) (domain.Board, error) {
	board, err := s.GetBoard(id)
	if err != nil {
		return domain.Board{}, err
	}
	if name = strings.TrimSpace(name); name != "" {
		board.Name = name
	}
	board.UpdatedAt = time.Now().UTC()
	_, err = s.db.Exec(`UPDATE boards SET name=$2, updated_at=$3 WHERE id=$1`, id, board.Name, board.UpdatedAt)
	return board, mapSQLError(err)
}

func (s *PostgresStore) DeleteBoard(id string) error {
	result, err := s.db.Exec(`DELETE FROM boards WHERE id=$1`, id)
	return resultError(result, err)
}

func (s *PostgresStore) LoadBoardDocument(boardID string) (domain.BoardDocument, []domain.BoardUpdate, error) {
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return domain.BoardDocument{}, nil, err
	}
	defer tx.Rollback()
	var document domain.BoardDocument
	err = tx.QueryRow(`SELECT board_id, checkpoint, checkpoint_sequence, updated_at
		FROM board_documents WHERE board_id=$1`, boardID).Scan(
		&document.BoardID,
		&document.Checkpoint,
		&document.CheckpointSequence,
		&document.UpdatedAt,
	)
	if err != nil {
		return domain.BoardDocument{}, nil, mapSQLError(err)
	}
	rows, err := tx.Query(`SELECT board_id, server_sequence, update_id, client_id, user_id, update_data, update_hash, introduced_asset_ids, compacted_at, created_at
		FROM board_updates
		WHERE board_id=$1 AND server_sequence>$2
		ORDER BY server_sequence`, boardID, document.CheckpointSequence)
	if err != nil {
		return domain.BoardDocument{}, nil, err
	}
	defer rows.Close()
	updates := []domain.BoardUpdate{}
	for rows.Next() {
		var update domain.BoardUpdate
		if err := scanBoardUpdate(rows, &update); err != nil {
			return domain.BoardDocument{}, nil, err
		}
		updates = append(updates, update)
	}
	if err := rows.Err(); err != nil {
		return domain.BoardDocument{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return domain.BoardDocument{}, nil, err
	}
	return document, updates, nil
}

func (s *PostgresStore) AppendBoardUpdate(update domain.BoardUpdate) (domain.BoardUpdate, bool, error) {
	if update.BoardID == "" || update.UpdateID == "" || update.ClientID == "" || update.UserID == "" || len(update.Update) == 0 {
		return domain.BoardUpdate{}, false, ErrInvalidInput
	}
	manifestPresent := update.AssetIDs != nil
	introducedAssetsPresent := update.IntroducedAssetIDs != nil
	if manifestPresent != (update.ReferenceBaseSequence != nil) || (update.ReferenceBaseSequence != nil && *update.ReferenceBaseSequence < 0) {
		return domain.BoardUpdate{}, false, ErrInvalidInput
	}
	canonical, err := canonicalAssetIDs(update.AssetIDs)
	if err != nil {
		return domain.BoardUpdate{}, false, err
	}
	introduced, err := canonicalAssetIDs(update.IntroducedAssetIDs)
	if err != nil {
		return domain.BoardUpdate{}, false, err
	}
	if !assetIDsContainAll(canonical, introduced) {
		return domain.BoardUpdate{}, false, ErrInvalidAssetReference
	}
	update.AssetIDs = canonical
	update.IntroducedAssetIDs = introduced
	update.UpdateHash = hashBoardUpdate(update.Update, update.ReferenceBaseSequence, canonical, introduced)
	tx, err := s.db.Begin()
	if err != nil {
		return domain.BoardUpdate{}, false, err
	}
	defer tx.Rollback()
	projectID, checkpointSequence, err := lockBoardDocumentTx(tx, update.BoardID)
	if err != nil {
		return domain.BoardUpdate{}, false, err
	}
	var existing domain.BoardUpdate
	err = tx.QueryRow(`SELECT board_id, server_sequence, update_id, client_id, user_id, update_data, update_hash, introduced_asset_ids, compacted_at, created_at
		FROM board_updates WHERE board_id=$1 AND update_id=$2`, update.BoardID, update.UpdateID).Scan(
		&existing.BoardID,
		&existing.ServerSequence,
		&existing.UpdateID,
		&existing.ClientID,
		&existing.UserID,
		&existing.Update,
		&existing.UpdateHash,
		pq.Array(&existing.IntroducedAssetIDs),
		&existing.CompactedAt,
		&existing.CreatedAt,
	)
	if err == nil {
		if existing.UpdateHash != update.UpdateHash {
			return domain.BoardUpdate{}, false, ErrUpdateIDConflict
		}
		if err := tx.Commit(); err != nil {
			return domain.BoardUpdate{}, false, err
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.BoardUpdate{}, false, err
	}
	if !introducedAssetsPresent {
		return domain.BoardUpdate{}, false, ErrAssetClaimsRequired
	}
	latestSequence, err := latestBoardSequenceTx(tx, update.BoardID, checkpointSequence)
	if err != nil {
		return domain.BoardUpdate{}, false, err
	}
	if introducedAssetsPresent {
		if err := validateProjectAssetReferencesTx(tx, projectID, introduced); err != nil {
			return domain.BoardUpdate{}, false, err
		}
	}
	if manifestPresent && update.AssetManifestTrusted && *update.ReferenceBaseSequence == latestSequence {
		if err := validateProjectAssetReferencesTx(tx, projectID, canonical); err != nil {
			return domain.BoardUpdate{}, false, err
		}
	}
	update.ServerSequence = latestSequence + 1
	update.CreatedAt = time.Now().UTC()
	_, err = tx.Exec(`INSERT INTO board_updates
		(board_id, server_sequence, update_id, client_id, user_id, update_data, update_hash, introduced_asset_ids, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		update.BoardID,
		update.ServerSequence,
		update.UpdateID,
		update.ClientID,
		update.UserID,
		update.Update,
		update.UpdateHash,
		pq.Array(update.IntroducedAssetIDs),
		update.CreatedAt,
	)
	if err != nil {
		return domain.BoardUpdate{}, false, mapSQLError(err)
	}
	if _, err := tx.Exec(`UPDATE boards SET updated_at=$2 WHERE id=$1`, update.BoardID, update.CreatedAt); err != nil {
		return domain.BoardUpdate{}, false, mapSQLError(err)
	}
	if manifestPresent && update.AssetManifestTrusted && *update.ReferenceBaseSequence == latestSequence {
		if _, err := replaceBoardAssetReferencesTx(tx, update.BoardID, projectID, update.UserID, update.ServerSequence, canonical); err != nil {
			return domain.BoardUpdate{}, false, err
		}
	} else if err := clearAssetGCCandidatesTx(tx, projectID); err != nil {
		return domain.BoardUpdate{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.BoardUpdate{}, false, err
	}
	return update, true, nil
}

func (s *PostgresStore) SaveBoardCheckpoint(boardID string, checkpoint []byte, throughSequence int64) (domain.BoardDocument, error) {
	if boardID == "" || throughSequence < 0 {
		return domain.BoardDocument{}, ErrInvalidInput
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.BoardDocument{}, err
	}
	defer tx.Rollback()
	var document domain.BoardDocument
	if err := tx.QueryRow(`SELECT board_id, checkpoint, checkpoint_sequence, updated_at
		FROM board_documents WHERE board_id=$1 FOR UPDATE`, boardID).Scan(
		&document.BoardID,
		&document.Checkpoint,
		&document.CheckpointSequence,
		&document.UpdatedAt,
	); err != nil {
		return domain.BoardDocument{}, mapSQLError(err)
	}
	var latestSequence int64
	if err := tx.QueryRow(`SELECT GREATEST($2, COALESCE(MAX(server_sequence), 0))
		FROM board_updates WHERE board_id=$1`, boardID, document.CheckpointSequence).Scan(&latestSequence); err != nil {
		return domain.BoardDocument{}, err
	}
	if throughSequence < document.CheckpointSequence || throughSequence > latestSequence {
		return domain.BoardDocument{}, ErrInvalidCheckpointSequence
	}
	if checkpoint == nil {
		checkpoint = []byte{}
	}
	document.Checkpoint = cloneBytes(checkpoint)
	document.CheckpointSequence = throughSequence
	document.UpdatedAt = time.Now().UTC()
	if _, err := tx.Exec(`UPDATE board_documents
		SET checkpoint=$2, checkpoint_sequence=$3, updated_at=$4
		WHERE board_id=$1`,
		boardID,
		document.Checkpoint,
		document.CheckpointSequence,
		document.UpdatedAt,
	); err != nil {
		return domain.BoardDocument{}, err
	}
	if _, err := tx.Exec(`UPDATE board_updates
		SET update_data=NULL, compacted_at=$3
		WHERE board_id=$1 AND server_sequence<=$2 AND update_data IS NOT NULL`, boardID, throughSequence, document.UpdatedAt); err != nil {
		return domain.BoardDocument{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.BoardDocument{}, err
	}
	return document, nil
}

func (s *PostgresStore) SaveAsset(asset domain.Asset) (domain.Asset, error) {
	if asset.Size < 0 || asset.Width < 0 || asset.Height < 0 {
		return domain.Asset{}, ErrInvalidInput
	}
	if asset.ID == "" {
		asset.ID = newID("ast")
	}
	if asset.StorageKey == "" {
		asset.StorageKey = asset.ID
	}
	asset.CreatedAt = time.Now().UTC()
	_, err := s.db.Exec(`INSERT INTO assets
		(id, project_id, uploaded_by, file_name, content_type, size, path, storage_key, sha256, width, height, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		asset.ID,
		asset.ProjectID,
		asset.UploadedBy,
		asset.FileName,
		asset.ContentType,
		asset.Size,
		asset.Path,
		asset.StorageKey,
		asset.SHA256,
		asset.Width,
		asset.Height,
		asset.CreatedAt,
	)
	return asset, mapSQLError(err)
}

func (s *PostgresStore) GetAsset(id string) (domain.Asset, error) {
	var asset domain.Asset
	err := s.db.QueryRow(assetSelect+` WHERE id=$1`, id).Scan(assetScanTargets(&asset)...)
	return asset, mapSQLError(err)
}

func (s *PostgresStore) ListAssetsByProject(projectID string) ([]domain.Asset, error) {
	if _, err := s.GetProject(projectID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(assetSelect+` WHERE project_id=$1 ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assets := []domain.Asset{}
	for rows.Next() {
		var asset domain.Asset
		if err := rows.Scan(assetScanTargets(&asset)...); err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	return assets, rows.Err()
}

func (s *PostgresStore) DeleteAsset(id string) error {
	var projectID string
	if err := s.db.QueryRow(`SELECT project_id FROM assets WHERE id=$1`, id).Scan(&projectID); err != nil {
		return mapSQLError(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var lockedProject string
	if err := tx.QueryRow(`SELECT id FROM projects WHERE id=$1 FOR UPDATE`, projectID).Scan(&lockedProject); err != nil {
		return mapSQLError(err)
	}
	boards, err := lockProjectBoardDocumentsTx(tx, projectID)
	if err != nil {
		return err
	}
	var lockedProjectID, storageKey string
	if err := tx.QueryRow(`SELECT project_id, storage_key FROM assets WHERE id=$1 FOR UPDATE`, id).Scan(&lockedProjectID, &storageKey); err != nil {
		return mapSQLError(err)
	}
	if lockedProjectID != projectID {
		return ErrInvalidAssetReference
	}
	if err := validateFreshProjectBoardReferencesTx(tx, boards); err != nil {
		return err
	}
	var referenced bool
	if err := tx.QueryRow(`SELECT EXISTS (
		SELECT 1 FROM board_asset_references WHERE project_id=$1 AND asset_id=$2
	)`, projectID, id).Scan(&referenced); err != nil {
		return err
	}
	if referenced {
		return ErrAssetInUse
	}
	if err := enqueueStorageCleanupTx(tx, StorageCleanupAssetFile, projectID, storageKey); err != nil {
		return err
	}
	result, err := tx.Exec(`DELETE FROM assets WHERE id=$1`, id)
	if err := resultError(result, err); err != nil {
		return err
	}
	return tx.Commit()
}

const userSelect = `SELECT id, email, name, system_role, password_hash, must_change_password, password_changed_at, created_at FROM users`

func (s *PostgresStore) userByEmailResult(email string) (domain.User, error) {
	user, err := scanUser(s.db.QueryRow(userSelect+` WHERE email=$1`, email))
	return user, mapSQLError(err)
}

func (s *PostgresStore) getUser(id string) (domain.User, error) {
	user, err := scanUser(s.db.QueryRow(userSelect+` WHERE id=$1`, id))
	return user, mapSQLError(err)
}

func scanUser(row interface{ Scan(...any) error }) (domain.User, error) {
	var user domain.User
	err := row.Scan(
		&user.ID,
		&user.Email,
		&user.Name,
		&user.SystemRole,
		&user.PasswordHash,
		&user.MustChangePassword,
		&user.PasswordChangedAt,
		&user.CreatedAt,
	)
	return user, err
}

func lockProject(tx *sql.Tx, projectID string) error {
	var id string
	if err := tx.QueryRow(`SELECT id FROM projects WHERE id=$1 FOR UPDATE`, projectID).Scan(&id); err != nil {
		return mapSQLError(err)
	}
	return nil
}

func countOwners(tx *sql.Tx, projectID string) (int, error) {
	var count int
	err := tx.QueryRow(`SELECT COUNT(*) FROM project_members WHERE project_id=$1 AND role=$2`, projectID, domain.RoleOwner).Scan(&count)
	return count, err
}

func scanBoardUpdate(row interface{ Scan(...any) error }, update *domain.BoardUpdate) error {
	return row.Scan(
		&update.BoardID,
		&update.ServerSequence,
		&update.UpdateID,
		&update.ClientID,
		&update.UserID,
		&update.Update,
		&update.UpdateHash,
		pq.Array(&update.IntroducedAssetIDs),
		&update.CompactedAt,
		&update.CreatedAt,
	)
}

const assetSelect = `SELECT id, project_id, uploaded_by, file_name, content_type, size, path, storage_key, sha256, width, height, created_at FROM assets`

func assetScanTargets(asset *domain.Asset) []any {
	return []any{
		&asset.ID,
		&asset.ProjectID,
		&asset.UploadedBy,
		&asset.FileName,
		&asset.ContentType,
		&asset.Size,
		&asset.Path,
		&asset.StorageKey,
		&asset.SHA256,
		&asset.Width,
		&asset.Height,
		&asset.CreatedAt,
	}
}

func mapSQLError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		switch pqErr.Code {
		case "23505":
			return ErrConflict
		case "23503":
			return ErrNotFound
		case "23502", "23514":
			return ErrInvalidInput
		}
	}
	return err
}

func resultError(result sql.Result, err error) error {
	if err != nil {
		return mapSQLError(err)
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
	if err == nil {
		return false
	}
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}
