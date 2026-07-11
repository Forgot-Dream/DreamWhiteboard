package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dreamwhiteboard/backend/internal/domain"
	"dreamwhiteboard/backend/internal/store"
)

const (
	initialAdminPassword = "InitialAdminPass123!"
	changedAdminPassword = "ChangedAdminPass123!"
)

type failingAssetRepository struct{ *store.MemoryStore }

func (r *failingAssetRepository) SaveAsset(domain.Asset) (domain.Asset, error) {
	return domain.Asset{}, errors.New("injected asset persistence failure")
}

type unavailableRepository struct {
	*store.MemoryStore
	authErr    error
	sessionErr error
	memberErr  error
}

func (r *unavailableRepository) Authenticate(email, password string) (domain.User, error) {
	if r.authErr != nil {
		return domain.User{}, r.authErr
	}
	return r.MemoryStore.Authenticate(email, password)
}

func (r *unavailableRepository) GetSession(tokenHash string, now time.Time) (domain.Session, error) {
	if r.sessionErr != nil {
		return domain.Session{}, r.sessionErr
	}
	return r.MemoryStore.GetSession(tokenHash, now)
}

func (r *unavailableRepository) MemberRole(projectID, userID string) (string, error) {
	if r.memberErr != nil {
		return "", r.memberErr
	}
	return r.MemoryStore.MemberRole(projectID, userID)
}

func TestPersistentCookieAuthenticationAndForcedPasswordChange(t *testing.T) {
	repo := store.NewMemoryStore()
	admin, err := repo.EnsureSystemAdmin("admin@example.com", initialAdminPassword)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(repo, t.TempDir())
	cookie, body := loginCookie(t, handler, "admin@example.com", initialAdminPassword, http.StatusOK)
	if strings.Contains(body, "token") {
		t.Fatalf("login response must not expose a token: %s", body)
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/api" {
		t.Fatalf("insecure session cookie: %#v", cookie)
	}

	rec := requestJSON(t, handler, http.MethodGet, "/api/admin/users", cookie, nil)
	assertStatus(t, rec, http.StatusForbidden)
	assertErrorCode(t, rec, "password_change_required")

	cookie = changePassword(t, handler, cookie, initialAdminPassword, changedAdminPassword)
	rec = requestJSON(t, handler, http.MethodGet, "/api/admin/users", cookie, nil)
	assertStatus(t, rec, http.StatusOK)

	// The raw token remains useful after rebuilding the HTTP server because only
	// its hash is persisted in the repository.
	rebuilt := NewServer(repo, t.TempDir())
	rec = requestJSON(t, rebuilt, http.MethodGet, "/api/me", cookie, nil)
	assertStatus(t, rec, http.StatusOK)
	var current domain.User
	decodeResponse(t, rec, &current)
	if current.ID != admin.ID || current.PasswordHash != "" || current.MustChangePassword {
		t.Fatalf("unexpected current user: %#v", current)
	}

	// Header and URL credentials are deliberately no longer accepted.
	req := httptest.NewRequest(http.MethodGet, "/api/me?token="+cookie.Value, nil)
	req.Header.Set("Authorization", "Bearer "+cookie.Value)
	rec = httptest.NewRecorder()
	rebuilt.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusUnauthorized)

	rec = requestJSON(t, rebuilt, http.MethodPost, "/api/auth/logout", cookie, map[string]any{})
	assertStatus(t, rec, http.StatusOK)
	rec = requestJSON(t, rebuilt, http.MethodGet, "/api/me", cookie, nil)
	assertStatus(t, rec, http.StatusUnauthorized)
}

func TestSessionExpirationAndLoginRateLimit(t *testing.T) {
	repo := store.NewMemoryStore()
	if _, err := repo.EnsureSystemAdmin("admin@example.com", initialAdminPassword); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cfg := DefaultConfig(t.TempDir())
	cfg.Now = func() time.Time { return now }
	cfg.SessionTTL = time.Hour
	cfg.LoginLimit = 2
	cfg.LoginWindow = time.Minute
	handler := NewServerWithConfig(repo, cfg)

	for attempt := 0; attempt < 2; attempt++ {
		_, _ = loginCookie(t, handler, "admin@example.com", "WrongPassword123!", http.StatusUnauthorized)
	}
	rec := requestJSON(t, handler, http.MethodPost, "/api/auth/login", nil, map[string]any{
		"email": "admin@example.com", "password": initialAdminPassword,
	})
	assertStatus(t, rec, http.StatusTooManyRequests)
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("rate limited response is missing Retry-After")
	}

	now = now.Add(2 * time.Minute)
	cookie, _ := loginCookie(t, handler, "admin@example.com", initialAdminPassword, http.StatusOK)
	now = now.Add(2 * time.Hour)
	rec = requestJSON(t, handler, http.MethodGet, "/api/me", cookie, nil)
	assertStatus(t, rec, http.StatusUnauthorized)
}

func TestRepositoryOutagesAreNotReportedAsAuthOrPermissionFailures(t *testing.T) {
	t.Run("login", func(t *testing.T) {
		base := store.NewMemoryStore()
		if _, err := base.EnsureSystemAdmin("admin@example.com", initialAdminPassword); err != nil {
			t.Fatal(err)
		}
		injected := errors.New("database offline")
		cfg := DefaultConfig(t.TempDir())
		cfg.LoginLimit = 1
		handler := NewServerWithConfig(&unavailableRepository{MemoryStore: base, authErr: injected}, cfg)
		for attempt := 0; attempt < 2; attempt++ {
			rec := requestJSON(t, handler, http.MethodPost, "/api/auth/login", nil, map[string]any{
				"email": "admin@example.com", "password": initialAdminPassword,
			})
			assertStatus(t, rec, http.StatusServiceUnavailable)
			assertErrorCode(t, rec, "service_unavailable")
		}
	})

	t.Run("session", func(t *testing.T) {
		repo := &unavailableRepository{MemoryStore: store.NewMemoryStore(), sessionErr: errors.New("database offline")}
		handler := NewServer(repo, t.TempDir())
		rec := requestJSON(t, handler, http.MethodGet, "/api/me", &http.Cookie{Name: sessionCookieName, Value: "raw-token"}, nil)
		assertStatus(t, rec, http.StatusServiceUnavailable)
		assertErrorCode(t, rec, "service_unavailable")
	})

	t.Run("membership", func(t *testing.T) {
		base := store.NewMemoryStore()
		user, err := base.CreateUser("member@example.com", "Member", "InitialMemberPass123!", domain.SystemUser)
		if err != nil {
			t.Fatal(err)
		}
		if err := base.UpdatePassword(user.ID, "ChangedMemberPass123!", false); err != nil {
			t.Fatal(err)
		}
		project, err := base.CreateProject("Project", "", user.ID)
		if err != nil {
			t.Fatal(err)
		}
		token := "membership-session-token"
		if _, err := base.CreateSession(store.HashSessionToken(token), user.ID, time.Now().UTC().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		repo := &unavailableRepository{MemoryStore: base, memberErr: errors.New("database offline")}
		handler := NewServer(repo, t.TempDir())
		rec := requestJSON(t, handler, http.MethodGet, "/api/projects/"+project.ID, &http.Cookie{Name: sessionCookieName, Value: token}, nil)
		assertStatus(t, rec, http.StatusServiceUnavailable)
		assertErrorCode(t, rec, "service_unavailable")
	})
}

func TestProjectRolePermissionsAndLastOwnerProtection(t *testing.T) {
	repo, handler, admin, adminCookie := setupAdmin(t)
	viewer := createUser(t, handler, adminCookie, "viewer@example.com", "Viewer", "InitialViewerPass123!", domain.SystemUser)
	editor := createUser(t, handler, adminCookie, "editor@example.com", "Editor", "InitialEditorPass123!", domain.SystemUser)
	project := jsonRequest[domain.Project](t, handler, http.MethodPost, "/api/projects", adminCookie, map[string]any{"name": "Role test"}, http.StatusCreated)
	jsonRequest[domain.ProjectMember](t, handler, http.MethodPost, "/api/projects/"+project.ID+"/members", adminCookie, map[string]any{
		"user_id": viewer.ID, "role": domain.RoleViewer,
	}, http.StatusOK)
	jsonRequest[domain.ProjectMember](t, handler, http.MethodPost, "/api/projects/"+project.ID+"/members", adminCookie, map[string]any{
		"user_id": editor.ID, "role": domain.RoleEditor,
	}, http.StatusOK)

	viewerCookie, _ := loginCookie(t, handler, viewer.Email, "InitialViewerPass123!", http.StatusOK)
	viewerCookie = changePassword(t, handler, viewerCookie, "InitialViewerPass123!", "ChangedViewerPass123!")
	rec := requestJSON(t, handler, http.MethodPost, "/api/projects/"+project.ID+"/boards", viewerCookie, map[string]any{"name": "Denied"})
	assertStatus(t, rec, http.StatusForbidden)

	editorCookie, _ := loginCookie(t, handler, editor.Email, "InitialEditorPass123!", http.StatusOK)
	editorCookie = changePassword(t, handler, editorCookie, "InitialEditorPass123!", "ChangedEditorPass123!")
	board := jsonRequest[domain.Board](t, handler, http.MethodPost, "/api/projects/"+project.ID+"/boards", editorCookie, map[string]any{"name": "Allowed"}, http.StatusCreated)
	if board.Name != "Allowed" {
		t.Fatalf("unexpected board: %#v", board)
	}

	rec = requestJSON(t, handler, http.MethodPatch, "/api/projects/"+project.ID+"/members/"+admin.ID, adminCookie, map[string]any{"role": domain.RoleViewer})
	assertStatus(t, rec, http.StatusConflict)
	assertErrorCode(t, rec, "last_owner_required")
	if role, err := repo.MemberRole(project.ID, admin.ID); err != nil || role != domain.RoleOwner {
		t.Fatalf("last owner was modified: %q, %v", role, err)
	}
}

func TestAdminPasswordResetRevokesSessionsAndForcesChange(t *testing.T) {
	_, handler, _, adminCookie := setupAdmin(t)
	user := createUser(t, handler, adminCookie, "reset@example.com", "Reset User", "InitialResetPass123!", domain.SystemUser)
	userCookie, _ := loginCookie(t, handler, user.Email, "InitialResetPass123!", http.StatusOK)

	rec := requestJSON(t, handler, http.MethodPost, "/api/admin/users/"+user.ID+"/password", adminCookie, map[string]any{
		"password": "AdministratorReset123!",
	})
	assertStatus(t, rec, http.StatusOK)
	rec = requestJSON(t, handler, http.MethodGet, "/api/me", userCookie, nil)
	assertStatus(t, rec, http.StatusUnauthorized)
	_, _ = loginCookie(t, handler, user.Email, "InitialResetPass123!", http.StatusUnauthorized)
	replacement, _ := loginCookie(t, handler, user.Email, "AdministratorReset123!", http.StatusOK)
	rec = requestJSON(t, handler, http.MethodGet, "/api/projects", replacement, nil)
	assertStatus(t, rec, http.StatusForbidden)
	assertErrorCode(t, rec, "password_change_required")
}

func TestLastSystemAdministratorCannotBeDemoted(t *testing.T) {
	_, handler, admin, cookie := setupAdmin(t)
	rec := requestJSON(t, handler, http.MethodPatch, "/api/admin/users/"+admin.ID, cookie, map[string]any{
		"system_role": domain.SystemUser,
	})
	assertStatus(t, rec, http.StatusConflict)
	assertErrorCode(t, rec, "last_system_admin_required")
}

func TestUploadValidationMetadataAndProjectCleanup(t *testing.T) {
	repo, handler, _, cookie := setupAdmin(t)
	project := jsonRequest[domain.Project](t, handler, http.MethodPost, "/api/projects", cookie, map[string]any{"name": "Assets"}, http.StatusCreated)

	var imageBytes bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&imageBytes, img); err != nil {
		t.Fatal(err)
	}
	asset := uploadFile(t, handler, cookie, "/api/projects/"+project.ID+"/assets", "pixel.png", imageBytes.Bytes(), http.StatusCreated)
	if asset.ContentType != "image/png" || asset.Width != 2 || asset.Height != 3 || len(asset.SHA256) != 64 || asset.Size != int64(imageBytes.Len()) {
		t.Fatalf("unexpected asset metadata: %#v", asset)
	}
	stored, err := repo.GetAsset(asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.StorageKey == "" || stored.Path == "" {
		t.Fatalf("asset storage metadata not persisted: %#v", stored)
	}
	if _, err := os.Stat(stored.Path); err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/assets/"+asset.ID, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	if rec.Header().Get("Content-Type") != "image/png" || !bytes.Equal(rec.Body.Bytes(), imageBytes.Bytes()) {
		t.Fatal("served asset differs from uploaded image")
	}

	uploadFile(t, handler, cookie, "/api/projects/"+project.ID+"/assets", "fake.png", []byte("not an image"), http.StatusUnsupportedMediaType)
	rec = requestJSON(t, handler, http.MethodDelete, "/api/projects/"+project.ID, cookie, nil)
	assertStatus(t, rec, http.StatusOK)
	if _, err := os.Stat(stored.Path); !os.IsNotExist(err) {
		t.Fatalf("project asset was not cleaned up: %v", err)
	}
}

func TestUploadRollsBackFileWhenMetadataPersistenceFails(t *testing.T) {
	base := store.NewMemoryStore()
	admin, err := base.EnsureSystemAdmin("admin@example.com", initialAdminPassword)
	if err != nil {
		t.Fatal(err)
	}
	uploadDir := t.TempDir()
	handler := NewServer(&failingAssetRepository{MemoryStore: base}, uploadDir)
	cookie, _ := loginCookie(t, handler, admin.Email, initialAdminPassword, http.StatusOK)
	cookie = changePassword(t, handler, cookie, initialAdminPassword, changedAdminPassword)
	project := jsonRequest[domain.Project](t, handler, http.MethodPost, "/api/projects", cookie, map[string]any{"name": "Rollback"}, http.StatusCreated)

	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	uploadFile(t, handler, cookie, "/api/projects/"+project.ID+"/assets", "rollback.png", imageBytes.Bytes(), http.StatusServiceUnavailable)
	projectDir := filepath.Join(uploadDir, project.ID)
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed upload left files behind: %#v", entries)
	}
}

func TestSecurityHeadersCORSRequestIDAndBodyLimit(t *testing.T) {
	repo := store.NewMemoryStore()
	cfg := DefaultConfig(t.TempDir())
	cfg.MaxBodyBytes = 32
	handler := NewServerWithConfig(repo, cfg)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusForbidden)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("disallowed origin was reflected")
	}

	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" || rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Request-ID") == "" {
		t.Fatalf("security headers missing: %#v", rec.Header())
	}

	rec = requestJSON(t, handler, http.MethodPost, "/api/auth/login", nil, map[string]any{
		"email": "admin@example.com", "password": strings.Repeat("x", 100),
	})
	assertStatus(t, rec, http.StatusRequestEntityTooLarge)
}

func TestLoginLimiterReservesConcurrentAttempts(t *testing.T) {
	limiter := newLoginLimiter(3, time.Minute)
	now := time.Now().UTC()
	start := make(chan struct{})
	var allowed atomic.Int32
	var group sync.WaitGroup
	for index := 0; index < 32; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			if ok, _ := limiter.allow("account:target@example.com", now); ok {
				allowed.Add(1)
			}
		}()
	}
	close(start)
	group.Wait()
	if got := allowed.Load(); got != 3 {
		t.Fatalf("concurrent limiter admitted %d attempts, want 3", got)
	}
}

func TestTrustedProxyUsesLastValidForwardedAddress(t *testing.T) {
	repo := store.NewMemoryStore()
	cfg := DefaultConfig(t.TempDir())
	cfg.TrustProxy = true
	server := NewServerWithConfig(repo, cfg)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.10, garbage, 198.51.100.20")
	if got := server.clientIP(req); got != "198.51.100.20" {
		t.Fatalf("trusted proxy address = %q, want last valid hop", got)
	}
}

func TestReadinessRejectsUnwritableUploadPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStore(), path)
	rec := requestJSON(t, handler, http.MethodGet, "/readyz", nil, nil)
	assertStatus(t, rec, http.StatusServiceUnavailable)
	assertErrorCode(t, rec, "not_ready")
}

func setupAdmin(t *testing.T) (*store.MemoryStore, http.Handler, domain.User, *http.Cookie) {
	t.Helper()
	repo := store.NewMemoryStore()
	admin, err := repo.EnsureSystemAdmin("admin@example.com", initialAdminPassword)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(repo, t.TempDir())
	cookie, _ := loginCookie(t, handler, admin.Email, initialAdminPassword, http.StatusOK)
	cookie = changePassword(t, handler, cookie, initialAdminPassword, changedAdminPassword)
	admin, _ = repo.GetUser(admin.ID)
	return repo, handler, admin, cookie
}

func createUser(t *testing.T, handler http.Handler, cookie *http.Cookie, email, name, password, role string) domain.User {
	t.Helper()
	return jsonRequest[domain.User](t, handler, http.MethodPost, "/api/admin/users", cookie, map[string]any{
		"email": email, "name": name, "password": password, "system_role": role,
	}, http.StatusCreated)
}

func loginCookie(t *testing.T, handler http.Handler, email, password string, status int) (*http.Cookie, string) {
	t.Helper()
	rec := requestJSON(t, handler, http.MethodPost, "/api/auth/login", nil, map[string]any{"email": email, "password": password})
	assertStatus(t, rec, status)
	if status != http.StatusOK {
		return nil, rec.Body.String()
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			return cookie, rec.Body.String()
		}
	}
	t.Fatal("login response is missing session cookie")
	return nil, ""
}

func changePassword(t *testing.T, handler http.Handler, cookie *http.Cookie, current, next string) *http.Cookie {
	t.Helper()
	rec := requestJSON(t, handler, http.MethodPost, "/api/me/password", cookie, map[string]any{"current_password": current, "new_password": next})
	assertStatus(t, rec, http.StatusOK)
	for _, replacement := range rec.Result().Cookies() {
		if replacement.Name == sessionCookieName && replacement.Value != "" {
			return replacement
		}
	}
	t.Fatal("password change response is missing renewed session cookie")
	return nil
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, cookie *http.Cookie, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func jsonRequest[T any](t *testing.T, handler http.Handler, method, path string, cookie *http.Cookie, body any, status int) T {
	t.Helper()
	rec := requestJSON(t, handler, method, path, cookie, body)
	assertStatus(t, rec, status)
	var result T
	decodeResponse(t, rec, &result)
	return result
}

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v, body=%s", err, rec.Body.String())
	}
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, expected int) {
	t.Helper()
	if rec.Code != expected {
		t.Fatalf("got status %d, want %d, body=%s", rec.Code, expected, rec.Body.String())
	}
}

func assertErrorCode(t *testing.T, rec *httptest.ResponseRecorder, expected string) {
	t.Helper()
	var envelope errorEnvelope
	decodeResponse(t, rec, &envelope)
	if envelope.Error.Code != expected {
		t.Fatalf("got error code %q, want %q, body=%s", envelope.Error.Code, expected, rec.Body.String())
	}
}

func uploadFile(t *testing.T, handler http.Handler, cookie *http.Cookie, path, name string, contents []byte, status int) domain.Asset {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, status)
	if status != http.StatusCreated {
		return domain.Asset{}
	}
	var asset domain.Asset
	decodeResponse(t, rec, &asset)
	return asset
}
