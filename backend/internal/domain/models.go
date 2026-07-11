package domain

import "time"

const (
	SystemAdmin = "system_admin"
	SystemUser  = "user"

	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleEditor = "editor"
	RoleViewer = "viewer"

	BlockText  = "text"
	BlockImage = "image"
)

type User struct {
	ID                 string     `json:"id"`
	Email              string     `json:"email"`
	Name               string     `json:"name"`
	SystemRole         string     `json:"system_role"`
	PasswordHash       string     `json:"-"`
	MustChangePassword bool       `json:"must_change_password"`
	PasswordChangedAt  *time.Time `json:"password_changed_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

type Session struct {
	TokenHash      string    `json:"-"`
	UserID         string    `json:"user_id"`
	ExpiresAt      time.Time `json:"expires_at"`
	LastAccessedAt time.Time `json:"last_accessed_at"`
	CreatedAt      time.Time `json:"created_at"`
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
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type BoardDocument struct {
	BoardID            string    `json:"board_id"`
	Checkpoint         []byte    `json:"checkpoint,omitempty"`
	CheckpointSequence int64     `json:"checkpoint_sequence"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type BoardUpdate struct {
	BoardID               string     `json:"board_id"`
	ServerSequence        int64      `json:"server_sequence"`
	UpdateID              string     `json:"update_id"`
	ClientID              string     `json:"client_id"`
	UserID                string     `json:"user_id"`
	Update                []byte     `json:"update"`
	ReferenceBaseSequence *int64     `json:"reference_base_sequence,omitempty"`
	AssetIDs              []string   `json:"asset_ids,omitempty"`
	AssetManifestTrusted  bool       `json:"-"`
	UpdateHash            string     `json:"-"`
	CompactedAt           *time.Time `json:"-"`
	CreatedAt             time.Time  `json:"created_at"`
}

type BoardAssetReferenceState struct {
	BoardID                string     `json:"board_id"`
	IndexedThroughSequence int64      `json:"indexed_through_sequence"`
	RefsHash               string     `json:"refs_hash"`
	IndexedBy              string     `json:"indexed_by,omitempty"`
	IndexedAt              *time.Time `json:"indexed_at,omitempty"`
	Conflicted             bool       `json:"conflicted"`
}

type Asset struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	UploadedBy  string    `json:"uploaded_by"`
	FileName    string    `json:"file_name"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	Path        string    `json:"-"`
	StorageKey  string    `json:"-"`
	SHA256      string    `json:"sha256"`
	Width       int       `json:"width,omitempty"`
	Height      int       `json:"height,omitempty"`
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

func IsSystemRole(role string) bool {
	return role == SystemAdmin || role == SystemUser
}

func RemovesLastOwner(currentRole, nextRole string, ownerCount int) bool {
	return currentRole == RoleOwner && nextRole != RoleOwner && ownerCount <= 1
}
