package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dreamwhiteboard/backend/internal/domain"
	"dreamwhiteboard/backend/internal/store"
)

func TestProjectRolePermissions(t *testing.T) {
	repo := store.NewMemoryStore()
	if _, err := repo.EnsureSystemAdmin("admin@example.com", "admin123"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(repo, t.TempDir())
	adminToken := login(t, handler, "admin@example.com", "admin123")

	viewer := postJSON[domain.User](t, handler, adminToken, "/api/admin/users", map[string]any{
		"email": "viewer@example.com", "name": "Viewer", "password": "viewer123", "system_role": domain.SystemUser,
	}, http.StatusOK)
	editor := postJSON[domain.User](t, handler, adminToken, "/api/admin/users", map[string]any{
		"email": "editor@example.com", "name": "Editor", "password": "editor123", "system_role": domain.SystemUser,
	}, http.StatusOK)
	project := postJSON[domain.Project](t, handler, adminToken, "/api/projects", map[string]any{"name": "Role test"}, http.StatusOK)
	postJSON[domain.ProjectMember](t, handler, adminToken, "/api/projects/"+project.ID+"/members", map[string]any{
		"user_id": viewer.ID, "role": domain.RoleViewer,
	}, http.StatusOK)
	postJSON[domain.ProjectMember](t, handler, adminToken, "/api/projects/"+project.ID+"/members", map[string]any{
		"user_id": editor.ID, "role": domain.RoleEditor,
	}, http.StatusOK)

	viewerToken := login(t, handler, "viewer@example.com", "viewer123")
	postJSON[map[string]string](t, handler, viewerToken, "/api/projects/"+project.ID+"/boards", map[string]any{"name": "Denied"}, http.StatusForbidden)

	editorToken := login(t, handler, "editor@example.com", "editor123")
	board := postJSON[domain.Board](t, handler, editorToken, "/api/projects/"+project.ID+"/boards", map[string]any{"name": "Allowed"}, http.StatusOK)
	if board.Name != "Allowed" {
		t.Fatalf("unexpected board: %#v", board)
	}
}

func login(t *testing.T, handler http.Handler, email, password string) string {
	t.Helper()
	result := postJSON[struct {
		Token string `json:"token"`
	}](t, handler, "", "/api/auth/login", map[string]any{"email": email, "password": password}, http.StatusOK)
	if result.Token == "" {
		t.Fatal("missing token")
	}
	return result.Token
}

func postJSON[T any](t *testing.T, handler http.Handler, token, path string, body any, status int) T {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != status {
		t.Fatalf("POST %s: got status %d, want %d, body %s", path, rec.Code, status, rec.Body.String())
	}
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v, body=%s", err, rec.Body.String())
	}
	return out
}
