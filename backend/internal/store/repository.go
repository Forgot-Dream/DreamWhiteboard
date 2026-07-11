package store

import (
	"time"

	"dreamwhiteboard/backend/internal/domain"
)

type Repository interface {
	EnsureSystemAdmin(email, password string) (domain.User, error)
	Authenticate(email, password string) (domain.User, error)
	CreateUser(email, name, password, systemRole string) (domain.User, error)
	UpdateUser(id, name, systemRole string) (domain.User, error)
	UpdatePassword(userID, newPassword string, mustChangePassword bool) error
	GetUser(id string) (domain.User, error)
	ListUsers() ([]domain.User, error)
	CreateSession(tokenHash, userID string, expiresAt time.Time) (domain.Session, error)
	GetSession(tokenHash string, now time.Time) (domain.Session, error)
	DeleteSession(tokenHash string) error
	DeleteUserSessions(userID string) error
	CreateProject(name, description, createdBy string) (domain.Project, error)
	ListProjects(user domain.User) ([]domain.Project, error)
	GetProject(id string) (domain.Project, error)
	UpdateProject(id, name, description string) (domain.Project, error)
	DeleteProject(id string) error
	MemberRole(projectID, userID string) (string, error)
	UpsertMember(projectID, userID, role string) (domain.ProjectMember, error)
	DeleteMember(projectID, userID string) error
	ListMembers(projectID string) ([]domain.ProjectMember, error)
	CreateBoard(projectID, name, createdBy string) (domain.Board, error)
	ListBoards(projectID string) ([]domain.Board, error)
	GetBoard(id string) (domain.Board, error)
	UpdateBoard(id, name string) (domain.Board, error)
	DeleteBoard(id string) error
	LoadBoardDocument(boardID string) (domain.BoardDocument, []domain.BoardUpdate, error)
	AppendBoardUpdate(update domain.BoardUpdate) (domain.BoardUpdate, bool, error)
	SaveBoardCheckpoint(boardID string, checkpoint []byte, throughSequence int64) (domain.BoardDocument, error)
	SaveAsset(asset domain.Asset) (domain.Asset, error)
	GetAsset(id string) (domain.Asset, error)
	ListAssetsByProject(projectID string) ([]domain.Asset, error)
	DeleteAsset(id string) error
}

var (
	_ Repository = (*MemoryStore)(nil)
	_ Repository = (*PostgresStore)(nil)
)
