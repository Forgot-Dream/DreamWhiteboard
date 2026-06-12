package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"dreamwhiteboard/backend/internal/domain"
	"dreamwhiteboard/backend/internal/realtime"
	"dreamwhiteboard/backend/internal/store"
)

type Server struct {
	repo      store.Repository
	uploadDir string
	hub       *realtime.Hub
	sessions  map[string]string
	mu        sync.RWMutex
}

func NewServer(repo store.Repository, uploadDir string) http.Handler {
	s := &Server{
		repo:      repo,
		uploadDir: uploadDir,
		hub:       realtime.NewHub(),
		sessions:  map[string]string{},
	}
	_ = os.MkdirAll(uploadDir, 0o755)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", s.handleLogin)
	mux.HandleFunc("/api/auth/logout", s.withAuth(s.handleLogout))
	mux.HandleFunc("/api/me", s.withAuth(s.handleMe))
	mux.HandleFunc("/api/projects", s.withAuth(s.handleProjects))
	mux.HandleFunc("/api/projects/", s.withAuth(s.handleProjectSubroutes))
	mux.HandleFunc("/api/boards/", s.withAuth(s.handleBoardSubroutes))
	mux.HandleFunc("/api/assets/", s.withAuth(s.handleAsset))
	mux.HandleFunc("/api/admin/users", s.withAuth(s.requireSystemAdmin(s.handleAdminUsers)))
	mux.HandleFunc("/api/admin/users/", s.withAuth(s.requireSystemAdmin(s.handleAdminUser)))
	return cors(mux)
}

type authedHandler func(http.ResponseWriter, *http.Request, domain.User)

func (s *Server) withAuth(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.currentUser(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next(w, r, user)
	}
}

func (s *Server) requireSystemAdmin(next authedHandler) authedHandler {
	return func(w http.ResponseWriter, r *http.Request, user domain.User) {
		if user.SystemRole != domain.SystemAdmin {
			writeError(w, http.StatusForbidden, "system admin required")
			return
		}
		next(w, r, user)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	user, ok := s.repo.Authenticate(req.Email, req.Password)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	token := randomToken()
	s.mu.Lock()
	s.sessions[token] = user.ID
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "dw_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": publicUser(user)})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, user domain.User) {
	token := bearerToken(r)
	if token == "" {
		if cookie, err := r.Cookie("dw_session"); err == nil {
			token = cookie.Value
		}
	}
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "dw_session", Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, user domain.User) {
	writeJSON(w, http.StatusOK, publicUser(user))
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request, user domain.User) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.repo.ListProjects(user))
	case http.MethodPost:
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if decodeJSON(w, r, &req) {
			project, err := s.repo.CreateProject(req.Name, req.Description, user.ID)
			writeResult(w, project, err)
		}
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleProjectSubroutes(w http.ResponseWriter, r *http.Request, user domain.User) {
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/projects/"))
	if len(parts) == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	projectID := parts[0]
	if len(parts) == 1 {
		s.handleProject(w, r, user, projectID)
		return
	}
	switch parts[1] {
	case "members":
		s.handleMembers(w, r, user, projectID)
	case "boards":
		s.handleProjectBoards(w, r, user, projectID)
	case "assets":
		s.handleAssetUpload(w, r, user, projectID)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (s *Server) handleProject(w http.ResponseWriter, r *http.Request, user domain.User, projectID string) {
	if !s.canViewProject(user, projectID) {
		writeError(w, http.StatusForbidden, "project access denied")
		return
	}
	switch r.Method {
	case http.MethodGet:
		project, err := s.repo.GetProject(projectID)
		writeResult(w, project, err)
	case http.MethodPatch:
		if !s.canManageProject(user, projectID) {
			writeError(w, http.StatusForbidden, "project admin required")
			return
		}
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if decodeJSON(w, r, &req) {
			project, err := s.repo.UpdateProject(projectID, req.Name, req.Description)
			writeResult(w, project, err)
		}
	case http.MethodDelete:
		if !s.canManageProject(user, projectID) {
			writeError(w, http.StatusForbidden, "project admin required")
			return
		}
		writeResult(w, map[string]bool{"ok": true}, s.repo.DeleteProject(projectID))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleMembers(w http.ResponseWriter, r *http.Request, user domain.User, projectID string) {
	if !s.canViewProject(user, projectID) {
		writeError(w, http.StatusForbidden, "project access denied")
		return
	}
	switch r.Method {
	case http.MethodGet:
		members, err := s.repo.ListMembers(projectID)
		writeResult(w, members, err)
	case http.MethodPost:
		if !s.canManageProject(user, projectID) {
			writeError(w, http.StatusForbidden, "project admin required")
			return
		}
		var req struct {
			UserID string `json:"user_id"`
			Role   string `json:"role"`
		}
		if decodeJSON(w, r, &req) {
			member, err := s.repo.UpsertMember(projectID, req.UserID, req.Role)
			writeResult(w, member, err)
		}
	case http.MethodDelete:
		if !s.canManageProject(user, projectID) {
			writeError(w, http.StatusForbidden, "project admin required")
			return
		}
		userID := r.URL.Query().Get("user_id")
		writeResult(w, map[string]bool{"ok": true}, s.repo.DeleteMember(projectID, userID))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleProjectBoards(w http.ResponseWriter, r *http.Request, user domain.User, projectID string) {
	if !s.canViewProject(user, projectID) {
		writeError(w, http.StatusForbidden, "project access denied")
		return
	}
	switch r.Method {
	case http.MethodGet:
		boards, err := s.repo.ListBoards(projectID)
		writeResult(w, boards, err)
	case http.MethodPost:
		if !s.canEditProject(user, projectID) {
			writeError(w, http.StatusForbidden, "editor required")
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		if decodeJSON(w, r, &req) {
			board, err := s.repo.CreateBoard(projectID, req.Name, user.ID)
			writeResult(w, board, err)
		}
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleBoardSubroutes(w http.ResponseWriter, r *http.Request, user domain.User) {
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/boards/"))
	if len(parts) == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	boardID := parts[0]
	board, err := s.repo.GetBoard(boardID)
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	if !s.canViewProject(user, board.ProjectID) {
		writeError(w, http.StatusForbidden, "board access denied")
		return
	}
	if len(parts) == 2 && parts[1] == "ws" {
		s.handleBoardWS(w, r, user, board)
		return
	}
	switch r.Method {
	case http.MethodGet:
		snapshot, err := s.repo.Snapshot(boardID)
		writeResult(w, map[string]any{"board": board, "snapshot": snapshot}, err)
	case http.MethodPatch:
		if !s.canEditProject(user, board.ProjectID) {
			writeError(w, http.StatusForbidden, "editor required")
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		if decodeJSON(w, r, &req) {
			board, err := s.repo.UpdateBoard(boardID, req.Name)
			writeResult(w, board, err)
		}
	case http.MethodDelete:
		if !s.canManageProject(user, board.ProjectID) {
			writeError(w, http.StatusForbidden, "project admin required")
			return
		}
		writeResult(w, map[string]bool{"ok": true}, s.repo.DeleteBoard(boardID))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleAssetUpload(w http.ResponseWriter, r *http.Request, user domain.User, projectID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.canEditProject(user, projectID) {
		writeError(w, http.StatusForbidden, "editor required")
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart upload")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	assetID := randomID("ast")
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == "" {
		exts, _ := mime.ExtensionsByType(header.Header.Get("Content-Type"))
		if len(exts) > 0 {
			ext = exts[0]
		}
	}
	projectDir := filepath.Join(s.uploadDir, projectID)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "create upload directory failed")
		return
	}
	path := filepath.Join(projectDir, assetID+ext)
	out, err := os.Create(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create upload failed")
		return
	}
	size, copyErr := io.Copy(out, file)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		writeError(w, http.StatusInternalServerError, "save upload failed")
		return
	}
	asset := s.repo.SaveAsset(domain.Asset{
		ID:          assetID,
		ProjectID:   projectID,
		UploadedBy:  user.ID,
		FileName:    header.Filename,
		ContentType: header.Header.Get("Content-Type"),
		Size:        size,
		Path:        path,
	})
	writeJSON(w, http.StatusCreated, asset)
}

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request, user domain.User) {
	assetID := strings.TrimPrefix(r.URL.Path, "/api/assets/")
	asset, err := s.repo.GetAsset(assetID)
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	if !s.canViewProject(user, asset.ProjectID) {
		writeError(w, http.StatusForbidden, "asset access denied")
		return
	}
	w.Header().Set("Content-Type", asset.ContentType)
	http.ServeFile(w, r, asset.Path)
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request, user domain.User) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, publicUsers(s.repo.ListUsers()))
	case http.MethodPost:
		var req struct {
			Email      string `json:"email"`
			Name       string `json:"name"`
			Password   string `json:"password"`
			SystemRole string `json:"system_role"`
		}
		if decodeJSON(w, r, &req) {
			if req.Password == "" {
				req.Password = "changeme123"
			}
			user, err := s.repo.CreateUser(req.Email, req.Name, req.Password, req.SystemRole)
			writeResult(w, publicUser(user), err)
		}
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleAdminUser(w http.ResponseWriter, r *http.Request, user domain.User) {
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/admin/users/")
	var req struct {
		Name       string `json:"name"`
		SystemRole string `json:"system_role"`
	}
	if decodeJSON(w, r, &req) {
		updated, err := s.repo.UpdateUser(id, req.Name, req.SystemRole)
		writeResult(w, publicUser(updated), err)
	}
}

func (s *Server) canViewProject(user domain.User, projectID string) bool {
	if user.SystemRole == domain.SystemAdmin {
		return true
	}
	_, ok := s.repo.MemberRole(projectID, user.ID)
	return ok
}

func (s *Server) canEditProject(user domain.User, projectID string) bool {
	if user.SystemRole == domain.SystemAdmin {
		return true
	}
	role, ok := s.repo.MemberRole(projectID, user.ID)
	return ok && domain.CanEdit(role)
}

func (s *Server) canManageProject(user domain.User, projectID string) bool {
	if user.SystemRole == domain.SystemAdmin {
		return true
	}
	role, ok := s.repo.MemberRole(projectID, user.ID)
	return ok && domain.CanManageMembers(role)
}

func (s *Server) currentUser(r *http.Request) (domain.User, bool) {
	token := bearerToken(r)
	if token == "" {
		if cookie, err := r.Cookie("dw_session"); err == nil {
			token = cookie.Value
		}
	}
	s.mu.RLock()
	userID := s.sessions[token]
	s.mu.RUnlock()
	if userID == "" {
		return domain.User{}, false
	}
	return s.repo.GetUser(userID)
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

func publicUser(user domain.User) domain.User {
	user.PasswordHash = ""
	return user
}

func publicUsers(users []domain.User) []domain.User {
	out := make([]domain.User, len(users))
	for i := range users {
		out[i] = publicUser(users[i])
	}
	return out
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return false
	}
	return true
}

func writeResult(w http.ResponseWriter, value any, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, value)
		return
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict")
	case errors.Is(err, store.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func splitPath(path string) []string {
	raw := strings.Split(strings.Trim(path, "/"), "/")
	parts := []string{}
	for _, part := range raw {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func randomToken() string { return randomID("tok") }

func randomID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
