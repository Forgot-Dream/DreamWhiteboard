package store

import "dreamwhiteboard/backend/internal/domain"

type Repository interface {
	EnsureSystemAdmin(email, password string) (domain.User, error)
	Authenticate(email, password string) (domain.User, bool)
	CreateUser(email, name, password, systemRole string) (domain.User, error)
	UpdateUser(id, name, systemRole string) (domain.User, error)
	GetUser(id string) (domain.User, bool)
	ListUsers() []domain.User
	CreateProject(name, description, createdBy string) (domain.Project, error)
	ListProjects(user domain.User) []domain.Project
	GetProject(id string) (domain.Project, error)
	UpdateProject(id, name, description string) (domain.Project, error)
	DeleteProject(id string) error
	MemberRole(projectID, userID string) (string, bool)
	UpsertMember(projectID, userID, role string) (domain.ProjectMember, error)
	DeleteMember(projectID, userID string) error
	ListMembers(projectID string) ([]domain.ProjectMember, error)
	CreateBoard(projectID, name, createdBy string) (domain.Board, error)
	ListBoards(projectID string) ([]domain.Board, error)
	GetBoard(id string) (domain.Board, error)
	UpdateBoard(id, name string) (domain.Board, error)
	DeleteBoard(id string) error
	Snapshot(boardID string) (domain.BoardSnapshot, error)
	ApplyOperation(op domain.Operation) (domain.Operation, domain.BoardSnapshot, error)
	SaveAsset(asset domain.Asset) domain.Asset
	GetAsset(id string) (domain.Asset, error)
}
