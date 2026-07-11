package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"dreamwhiteboard/backend/internal/domain"
	"dreamwhiteboard/backend/internal/realtime"
	"dreamwhiteboard/backend/internal/store"
	"github.com/go-chi/chi/v5"
)

const sessionCookieName = "dw_session"

type Config struct {
	UploadDir      string
	AllowedOrigins []string
	SessionTTL     time.Duration
	CookieSecure   bool
	CookieSameSite http.SameSite
	MaxBodyBytes   int64
	MaxUploadBytes int64
	MaxImagePixels int64
	LoginLimit     int
	LoginWindow    time.Duration
	WSAuthInterval time.Duration
	TrustProxy     bool
	Logger         *slog.Logger
	Now            func() time.Time
}

func DefaultConfig(uploadDir string) Config {
	return Config{
		UploadDir:      uploadDir,
		AllowedOrigins: []string{"http://localhost:5173", "http://127.0.0.1:5173"},
		SessionTTL:     7 * 24 * time.Hour,
		CookieSecure:   true,
		CookieSameSite: http.SameSiteLaxMode,
		MaxBodyBytes:   1 << 20,
		MaxUploadBytes: 25 << 20,
		MaxImagePixels: 40_000_000,
		LoginLimit:     5,
		LoginWindow:    5 * time.Minute,
		WSAuthInterval: 10 * time.Second,
		Logger:         slog.Default(),
		Now:            time.Now,
	}
}

type Server struct {
	repo        store.Repository
	uploadDir   string
	hub         *realtime.Hub
	handler     http.Handler
	config      Config
	logger      *slog.Logger
	login       *loginLimiter
	startupErr  error
	wsHandlers  sync.WaitGroup
	wsLifecycle sync.Mutex
	wsClosing   bool
}

// NewServer preserves the original constructor for tests and embedders. Production
// callers should use NewServerWithConfig so security-sensitive values are explicit.
func NewServer(repo store.Repository, uploadDir string) http.Handler {
	return NewServerWithConfig(repo, DefaultConfig(uploadDir))
}

func NewServerWithConfig(repo store.Repository, cfg Config) *Server {
	cfg = normalizeConfig(cfg)
	s := &Server{
		repo:      repo,
		uploadDir: cfg.UploadDir,
		hub:       realtime.NewHub(),
		config:    cfg,
		logger:    cfg.Logger,
		login:     newLoginLimiter(cfg.LoginLimit, cfg.LoginWindow),
	}
	if err := os.MkdirAll(cfg.UploadDir, 0o750); err != nil {
		s.startupErr = err
	} else if err := probeUploadDirectory(cfg.UploadDir); err != nil {
		s.startupErr = err
	}

	r := chi.NewRouter()
	r.Use(s.requestIDMiddleware)
	r.Use(s.accessLogMiddleware)
	r.Use(s.securityHeadersMiddleware)
	r.Use(s.corsMiddleware)
	r.Use(s.bodyLimitMiddleware)

	r.Get("/healthz", s.handleHealth)
	r.Get("/readyz", s.handleReady)
	r.Handle("/api/auth/login", http.HandlerFunc(s.handleLogin))
	r.Handle("/api/auth/logout", http.HandlerFunc(s.handleLogout))
	r.Handle("/api/me", s.withAuth(s.handleMe))
	r.Handle("/api/me/password", s.withAuth(s.handlePasswordChange))
	r.Handle("/api/projects", s.withAuth(s.handleProjects))
	r.Handle("/api/projects/*", s.withAuth(s.handleProjectSubroutes))
	r.Handle("/api/boards/*", s.withAuth(s.handleBoardSubroutes))
	r.Handle("/api/assets/*", s.withAuth(s.handleAsset))
	r.Handle("/api/admin/users", s.withAuth(s.requireSystemAdmin(s.handleAdminUsers)))
	r.Handle("/api/admin/users/*", s.withAuth(s.requireSystemAdmin(s.handleAdminUser)))
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	})
	s.handler = r
	return s
}

func normalizeConfig(cfg Config) Config {
	defaults := DefaultConfig(cfg.UploadDir)
	if strings.TrimSpace(cfg.UploadDir) == "" {
		cfg.UploadDir = "./uploads"
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = defaults.SessionTTL
	}
	if cfg.CookieSameSite == 0 {
		cfg.CookieSameSite = defaults.CookieSameSite
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = defaults.MaxBodyBytes
	}
	if cfg.MaxUploadBytes <= 0 {
		cfg.MaxUploadBytes = defaults.MaxUploadBytes
	}
	if cfg.MaxImagePixels <= 0 {
		cfg.MaxImagePixels = defaults.MaxImagePixels
	}
	if cfg.LoginLimit <= 0 {
		cfg.LoginLimit = defaults.LoginLimit
	}
	if cfg.LoginWindow <= 0 {
		cfg.LoginWindow = defaults.LoginWindow
	}
	if cfg.WSAuthInterval <= 0 {
		cfg.WSAuthInterval = defaults.WSAuthInterval
	}
	if cfg.Logger == nil {
		cfg.Logger = defaults.Logger
	}
	if cfg.Now == nil {
		cfg.Now = defaults.Now
	}
	return cfg
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) Close() error {
	s.wsLifecycle.Lock()
	s.wsClosing = true
	s.wsLifecycle.Unlock()
	if closer, ok := any(s.hub).(interface{ Close() }); ok {
		closer.Close()
	}
	done := make(chan struct{})
	go func() {
		s.wsHandlers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("timed out waiting for websocket handlers to stop")
	}
}

func (s *Server) beginWebSocket() bool {
	s.wsLifecycle.Lock()
	defer s.wsLifecycle.Unlock()
	if s.wsClosing {
		return false
	}
	s.wsHandlers.Add(1)
	return true
}

type authedHandler func(http.ResponseWriter, *http.Request, domain.User)

func (s *Server) withAuth(next authedHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := s.currentUser(r)
		if errors.Is(err, store.ErrNotFound) {
			writeAPIError(w, r, http.StatusUnauthorized, "authentication_required", "authentication required", nil)
			return
		}
		if err != nil {
			s.writeRepositoryUnavailable(w, r, "authenticate session", err)
			return
		}
		if user.MustChangePassword && r.URL.Path != "/api/me" && r.URL.Path != "/api/me/password" {
			writeAPIError(w, r, http.StatusForbidden, "password_change_required", "password must be changed before continuing", nil)
			return
		}
		next(w, r, user)
	})
}

func (s *Server) requireSystemAdmin(next authedHandler) authedHandler {
	return func(w http.ResponseWriter, r *http.Request, user domain.User) {
		if user.SystemRole != domain.SystemAdmin {
			writeAPIError(w, r, http.StatusForbidden, "system_admin_required", "system administrator access required", nil)
			return
		}
		next(w, r, user)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.startupErr != nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "not_ready", "service is not ready", nil)
		return
	}
	if ready, ok := s.repo.(interface{ Ready(context.Context) error }); ok {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := ready.Ready(ctx); err != nil {
			s.logger.Error("readiness check failed", "request_id", requestID(r.Context()), "error", err)
			writeAPIError(w, r, http.StatusServiceUnavailable, "not_ready", "service is not ready", nil)
			return
		}
	}
	if err := probeUploadDirectory(s.uploadDir); err != nil {
		s.logger.Error("upload directory readiness check failed", "request_id", requestID(r.Context()), "error", err)
		writeAPIError(w, r, http.StatusServiceUnavailable, "not_ready", "service is not ready", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func probeUploadDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("upload path is not a directory")
	}
	probe, err := os.CreateTemp(directory, ".ready-*")
	if err != nil {
		return err
	}
	path := probe.Name()
	closeErr := probe.Close()
	removeErr := os.Remove(path)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}

func (s *Server) canViewProject(user domain.User, projectID string) (bool, error) {
	if user.SystemRole == domain.SystemAdmin {
		return true, nil
	}
	_, err := s.repo.MemberRole(projectID, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Server) canEditProject(user domain.User, projectID string) (bool, error) {
	if user.SystemRole == domain.SystemAdmin {
		return true, nil
	}
	role, err := s.repo.MemberRole(projectID, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return domain.CanEdit(role), nil
}

func (s *Server) canManageProject(user domain.User, projectID string) (bool, error) {
	if user.SystemRole == domain.SystemAdmin {
		return true, nil
	}
	role, err := s.repo.MemberRole(projectID, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return domain.CanManageMembers(role), nil
}

func (s *Server) currentUser(r *http.Request) (domain.User, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return domain.User{}, store.ErrNotFound
	}
	session, err := s.repo.GetSession(store.HashSessionToken(cookie.Value), s.config.Now().UTC())
	if err != nil {
		return domain.User{}, err
	}
	return s.repo.GetUser(session.UserID)
}

func (s *Server) writeRepositoryUnavailable(w http.ResponseWriter, r *http.Request, operation string, err error) {
	s.logger.Error("repository unavailable", "request_id", requestID(r.Context()), "operation", operation, "error", err)
	writeAPIError(w, r, http.StatusServiceUnavailable, "service_unavailable", "service is temporarily unavailable", nil)
}

func (s *Server) requirePermission(w http.ResponseWriter, r *http.Request, allowed bool, err error, code, message string) bool {
	if err != nil {
		s.writeRepositoryUnavailable(w, r, "authorize project access", err)
		return false
	}
	if !allowed {
		writeAPIError(w, r, http.StatusForbidden, code, message, nil)
		return false
	}
	return true
}

func writeResult(w http.ResponseWriter, r *http.Request, value any, err error) {
	writeResultStatus(w, r, http.StatusOK, value, err)
}

func writeResultStatus(w http.ResponseWriter, r *http.Request, status int, value any, err error) {
	if err == nil {
		writeJSON(w, status, value)
		return
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeAPIError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
	case errors.Is(err, store.ErrConflict):
		writeAPIError(w, r, http.StatusConflict, "conflict", "resource conflicts with existing state", nil)
	case errors.Is(err, store.ErrForbidden):
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "operation is not allowed", nil)
	case errors.Is(err, store.ErrLastOwner):
		writeAPIError(w, r, http.StatusConflict, "last_owner_required", "a project must retain at least one owner", nil)
	case errors.Is(err, store.ErrLastSystemAdmin):
		writeAPIError(w, r, http.StatusConflict, "last_system_admin_required", "at least one system administrator is required", nil)
	case errors.Is(err, store.ErrAssetReferenceIndexStale):
		writeAPIError(w, r, http.StatusConflict, "asset_reference_index_stale", "asset references are not indexed through the latest board update", nil)
	case errors.Is(err, store.ErrAssetReferenceConflict):
		writeAPIError(w, r, http.StatusConflict, "asset_reference_conflict", "conflicting asset references were reported for the same board sequence", nil)
	case errors.Is(err, store.ErrAssetInUse):
		writeAPIError(w, r, http.StatusConflict, "asset_in_use", "asset is referenced by a board", nil)
	case errors.Is(err, store.ErrInvalidAssetReference):
		writeAPIError(w, r, http.StatusUnprocessableEntity, "invalid_asset_reference", "asset references must exist in the board project", nil)
	case errors.Is(err, store.ErrInvalidInput):
		writeAPIError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", nil)
	default:
		slog.Default().Error("repository request failed", "request_id", requestID(r.Context()), "error", err)
		writeAPIError(w, r, http.StatusServiceUnavailable, "service_unavailable", "service is temporarily unavailable", nil)
	}
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
