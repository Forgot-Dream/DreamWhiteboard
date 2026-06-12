package domain

import "time"

const (
	SystemAdmin = "system_admin"
	SystemUser  = "user"

	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleEditor = "editor"
	RoleViewer = "viewer"

	BlockRichText = "rich_text"
	BlockNote     = "note"
	BlockImage    = "image"
	BlockShape    = "shape"
)

type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	SystemRole   string    `json:"system_role"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

type ProjectMember struct {
	ProjectID string    `json:"project_id"`
	UserID    string    `json:"user_id"`
	Role      string    `json:"role"`
	User      *User     `json:"user,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Board struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	Version   int64     `json:"version"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Block struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	X        float64        `json:"x"`
	Y        float64        `json:"y"`
	W        float64        `json:"w"`
	H        float64        `json:"h"`
	Z        int            `json:"z"`
	Data     map[string]any `json:"data"`
	LockedBy string         `json:"locked_by,omitempty"`
}

type BoardSnapshot struct {
	BoardID   string    `json:"board_id"`
	Version   int64     `json:"version"`
	Blocks    []Block   `json:"blocks"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Operation struct {
	ID          string         `json:"id"`
	BoardID     string         `json:"board_id"`
	ClientID    string         `json:"client_id"`
	UserID      string         `json:"user_id"`
	Type        string         `json:"type"`
	BaseVersion int64          `json:"base_version"`
	Version     int64          `json:"version"`
	Payload     map[string]any `json:"payload"`
	CreatedAt   time.Time      `json:"created_at"`
}

type Asset struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	UploadedBy  string    `json:"uploaded_by"`
	FileName    string    `json:"file_name"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	Path        string    `json:"path"`
	CreatedAt   time.Time `json:"created_at"`
}

func CanEdit(role string) bool {
	return role == RoleOwner || role == RoleAdmin || role == RoleEditor
}

func CanManageMembers(role string) bool {
	return role == RoleOwner || role == RoleAdmin
}

func IsProjectRole(role string) bool {
	return role == RoleOwner || role == RoleAdmin || role == RoleEditor || role == RoleViewer
}
