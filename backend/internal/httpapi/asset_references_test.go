package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dreamwhiteboard/backend/internal/domain"
	"dreamwhiteboard/backend/internal/store"
)

type assetReferenceHTTPFixture struct {
	repo         *store.MemoryStore
	handler      http.Handler
	project      domain.Project
	board        domain.Board
	owner        domain.User
	editor       domain.User
	viewer       domain.User
	ownerCookie  *http.Cookie
	editorCookie *http.Cookie
	viewerCookie *http.Cookie
}

func newAssetReferenceHTTPFixture(t *testing.T) *assetReferenceHTTPFixture {
	t.Helper()
	repo := store.NewMemoryStore()
	owner := createAssetReferenceUser(t, repo, "asset-owner@example.com")
	editor := createAssetReferenceUser(t, repo, "asset-editor@example.com")
	viewer := createAssetReferenceUser(t, repo, "asset-viewer@example.com")
	project, err := repo.CreateProject("Asset references", "", owner.ID)
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
	handler := NewServer(repo, t.TempDir())
	return &assetReferenceHTTPFixture{
		repo:         repo,
		handler:      handler,
		project:      project,
		board:        board,
		owner:        owner,
		editor:       editor,
		viewer:       viewer,
		ownerCookie:  createAssetReferenceSession(t, repo, owner),
		editorCookie: createAssetReferenceSession(t, repo, editor),
		viewerCookie: createAssetReferenceSession(t, repo, viewer),
	}
}

func TestPutBoardAssetReferencesAllowsProjectManager(t *testing.T) {
	fixture := newAssetReferenceHTTPFixture(t)
	asset := fixture.saveAsset(t, fixture.project, "referenced")
	fixture.appendLegacyUpdate(t, "before-reconciliation")

	rec := fixture.putReferences(t, fixture.ownerCookie, 1, []string{asset.ID})
	assertStatus(t, rec, http.StatusOK)
	var state domain.BoardAssetReferenceState
	decodeResponse(t, rec, &state)
	if state.BoardID != fixture.board.ID || state.IndexedThroughSequence != 1 || state.IndexedBy != fixture.owner.ID || state.IndexedAt == nil || state.Conflicted || len(state.RefsHash) != 64 {
		t.Fatalf("unexpected reference state: %#v", state)
	}
}

func TestPutBoardAssetReferencesRejectsViewer(t *testing.T) {
	fixture := newAssetReferenceHTTPFixture(t)
	rec := fixture.putReferences(t, fixture.viewerCookie, 0, nil)
	assertStatus(t, rec, http.StatusForbidden)
	assertErrorCode(t, rec, "project_admin_required")

	rec = fixture.putReferences(t, fixture.editorCookie, 0, nil)
	assertStatus(t, rec, http.StatusForbidden)
	assertErrorCode(t, rec, "project_admin_required")
}

func TestPutBoardAssetReferencesRejectsStaleSequence(t *testing.T) {
	fixture := newAssetReferenceHTTPFixture(t)
	fixture.appendLegacyUpdate(t, "legacy-update")

	rec := fixture.putReferences(t, fixture.ownerCookie, 0, nil)
	assertStatus(t, rec, http.StatusConflict)
	assertErrorCode(t, rec, "asset_reference_index_stale")
}

func TestPutBoardAssetReferencesRejectsInvalidAssets(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		fixture := newAssetReferenceHTTPFixture(t)
		rec := fixture.putReferences(t, fixture.ownerCookie, 0, []string{"ast_missing"})
		assertStatus(t, rec, http.StatusUnprocessableEntity)
		assertErrorCode(t, rec, "invalid_asset_reference")
	})

	t.Run("different project", func(t *testing.T) {
		fixture := newAssetReferenceHTTPFixture(t)
		otherProject, err := fixture.repo.CreateProject("Other project", "", fixture.owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		foreignAsset := fixture.saveAsset(t, otherProject, "foreign")
		rec := fixture.putReferences(t, fixture.ownerCookie, 0, []string{foreignAsset.ID})
		assertStatus(t, rec, http.StatusUnprocessableEntity)
		assertErrorCode(t, rec, "invalid_asset_reference")
	})
}

func TestPutBoardAssetReferencesPersistsSameSequenceConflict(t *testing.T) {
	fixture := newAssetReferenceHTTPFixture(t)
	first := fixture.saveAsset(t, fixture.project, "first")
	second := fixture.saveAsset(t, fixture.project, "second")
	fixture.appendLegacyUpdate(t, "before-conflict")

	rec := fixture.putReferences(t, fixture.ownerCookie, 1, []string{first.ID})
	assertStatus(t, rec, http.StatusOK)

	rec = fixture.putReferences(t, fixture.ownerCookie, 1, []string{second.ID})
	assertStatus(t, rec, http.StatusConflict)
	assertErrorCode(t, rec, "asset_reference_conflict")

	// A later writer cannot clear the conflict by submitting the original set.
	// Once two clients disagree about the same sequence, deletion must remain
	// conservatively disabled until a newer document sequence is reconciled.
	rec = fixture.putReferences(t, fixture.ownerCookie, 1, []string{first.ID})
	assertStatus(t, rec, http.StatusConflict)
	assertErrorCode(t, rec, "asset_reference_conflict")
}

func createAssetReferenceUser(t *testing.T, repo *store.MemoryStore, email string) domain.User {
	t.Helper()
	user, err := repo.CreateUser(email, email, "AssetReferencePass123!", domain.SystemUser)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdatePassword(user.ID, "AssetReferencePass123!", false); err != nil {
		t.Fatal(err)
	}
	user, err = repo.GetUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func createAssetReferenceSession(t *testing.T, repo *store.MemoryStore, user domain.User) *http.Cookie {
	t.Helper()
	token := randomHex(32)
	if _, err := repo.CreateSession(store.HashSessionToken(token), user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookieName, Value: token, Path: "/api"}
}

func (f *assetReferenceHTTPFixture) saveAsset(t *testing.T, project domain.Project, suffix string) domain.Asset {
	t.Helper()
	asset, err := f.repo.SaveAsset(domain.Asset{
		ID:          "ast_" + suffix,
		ProjectID:   project.ID,
		UploadedBy:  f.owner.ID,
		FileName:    suffix + ".png",
		ContentType: "image/png",
		Size:        1,
		StorageKey:  suffix + ".png",
		SHA256:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Width:       1,
		Height:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return asset
}

func (f *assetReferenceHTTPFixture) putReferences(t *testing.T, cookie *http.Cookie, throughSequence int64, assetIDs []string) *httptest.ResponseRecorder {
	t.Helper()
	if assetIDs == nil {
		assetIDs = []string{}
	}
	return requestJSON(t, f.handler, http.MethodPut, "/api/boards/"+f.board.ID+"/asset-references", cookie, map[string]any{
		"through_sequence": throughSequence,
		"asset_ids":        assetIDs,
	})
}

func (f *assetReferenceHTTPFixture) appendLegacyUpdate(t *testing.T, updateID string) {
	t.Helper()
	if _, inserted, err := f.repo.AppendBoardUpdate(domain.BoardUpdate{
		BoardID: f.board.ID, UpdateID: updateID, ClientID: "legacy-client",
		UserID: f.editor.ID, Update: []byte{1}, IntroducedAssetIDs: []string{},
	}); err != nil || !inserted {
		t.Fatalf("append update: inserted=%v err=%v", inserted, err)
	}
}
