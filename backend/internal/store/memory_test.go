package store

import (
	"testing"

	"dreamwhiteboard/backend/internal/domain"
)

func TestPermissionsAndSnapshots(t *testing.T) {
	repo := NewMemoryStore()
	admin, err := repo.EnsureSystemAdmin("admin@example.com", "admin123")
	if err != nil {
		t.Fatal(err)
	}
	editor, err := repo.CreateUser("editor@example.com", "Editor", "pw", domain.SystemUser)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := repo.CreateUser("viewer@example.com", "Viewer", "pw", domain.SystemUser)
	if err != nil {
		t.Fatal(err)
	}
	project, err := repo.CreateProject("Project", "", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertMember(project.ID, editor.ID, domain.RoleEditor); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertMember(project.ID, viewer.ID, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	board, err := repo.CreateBoard(project.ID, "Board", editor.ID)
	if err != nil {
		t.Fatal(err)
	}
	op, snapshot, err := repo.ApplyOperation(domain.Operation{
		BoardID:  board.ID,
		ClientID: "client-1",
		UserID:   editor.ID,
		Type:     "create_block",
		Payload: map[string]any{"block": map[string]any{
			"id": "block-1", "type": domain.BlockNote, "x": 10.0, "y": 20.0, "w": 240.0, "h": 120.0,
			"data": map[string]any{"text": "hello"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if op.Version != 1 || snapshot.Version != 1 {
		t.Fatalf("expected version 1, got op=%d snapshot=%d", op.Version, snapshot.Version)
	}
	if len(snapshot.Blocks) != 1 || snapshot.Blocks[0].ID != "block-1" {
		t.Fatalf("unexpected snapshot: %#v", snapshot.Blocks)
	}
	role, ok := repo.MemberRole(project.ID, viewer.ID)
	if !ok || domain.CanEdit(role) {
		t.Fatalf("viewer role should not be editable: %s", role)
	}
}

func TestAuthenticate(t *testing.T) {
	repo := NewMemoryStore()
	if _, err := repo.EnsureSystemAdmin("admin@example.com", "admin123"); err != nil {
		t.Fatal(err)
	}
	if _, ok := repo.Authenticate("admin@example.com", "bad"); ok {
		t.Fatal("bad password authenticated")
	}
	user, ok := repo.Authenticate("ADMIN@example.com", "admin123")
	if !ok || user.SystemRole != domain.SystemAdmin {
		t.Fatalf("admin did not authenticate: %#v", user)
	}
}
