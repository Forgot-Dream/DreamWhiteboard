package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"dreamwhiteboard/backend/internal/domain"
	"dreamwhiteboard/backend/internal/store"
)

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request, user domain.User) {
	switch r.Method {
	case http.MethodGet:
		projects, err := s.repo.ListProjects(user)
		writeResult(w, r, projects, err)
	case http.MethodPost:
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		fields := map[string]string{}
		if err := validateRequiredName(req.Name, 200); err != "" {
			fields["name"] = err
		}
		if len(req.Description) > 10_000 {
			fields["description"] = "description must contain at most 10000 characters"
		}
		if len(fields) > 0 {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", fields)
			return
		}
		project, err := s.repo.CreateProject(req.Name, req.Description, user.ID)
		writeResultStatus(w, r, http.StatusCreated, project, err)
	default:
		methodNotAllowed(w, r, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleProjectSubroutes(w http.ResponseWriter, r *http.Request, user domain.User) {
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/projects/"))
	if len(parts) == 0 {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
		return
	}
	projectID := parts[0]
	if len(parts) == 1 {
		s.handleProject(w, r, user, projectID)
		return
	}
	switch parts[1] {
	case "members":
		s.handleMembers(w, r, user, projectID, parts[2:]...)
	case "boards":
		if len(parts) != 2 {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
			return
		}
		s.handleProjectBoards(w, r, user, projectID)
	case "assets":
		if len(parts) != 2 {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
			return
		}
		s.handleAssetUpload(w, r, user, projectID)
	default:
		writeAPIError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
	}
}

func (s *Server) handleProject(w http.ResponseWriter, r *http.Request, user domain.User, projectID string) {
	allowed, permissionErr := s.canViewProject(user, projectID)
	if !s.requirePermission(w, r, allowed, permissionErr, "project_access_denied", "project access denied") {
		return
	}
	switch r.Method {
	case http.MethodGet:
		project, err := s.repo.GetProject(projectID)
		writeResult(w, r, project, err)
	case http.MethodPatch:
		allowed, permissionErr := s.canManageProject(user, projectID)
		if !s.requirePermission(w, r, allowed, permissionErr, "project_admin_required", "project administrator access required") {
			return
		}
		var req struct {
			Name        *string `json:"name"`
			Description *string `json:"description"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Name == nil && req.Description == nil {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "validation_failed", "at least one field must be provided", nil)
			return
		}
		project, err := s.repo.GetProject(projectID)
		if err != nil {
			writeResult(w, r, nil, err)
			return
		}
		fields := map[string]string{}
		if req.Name != nil {
			if msg := validateRequiredName(*req.Name, 200); msg != "" {
				fields["name"] = msg
			} else {
				project.Name = strings.TrimSpace(*req.Name)
			}
		}
		if req.Description != nil {
			if len(*req.Description) > 10_000 {
				fields["description"] = "description must contain at most 10000 characters"
			} else {
				project.Description = strings.TrimSpace(*req.Description)
			}
		}
		if len(fields) > 0 {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", fields)
			return
		}
		updated, err := s.repo.UpdateProject(projectID, project.Name, project.Description)
		writeResult(w, r, updated, err)
	case http.MethodDelete:
		allowed, permissionErr := s.canManageProject(user, projectID)
		if !s.requirePermission(w, r, allowed, permissionErr, "project_admin_required", "project administrator access required") {
			return
		}
		assets, err := s.repo.ListAssetsByProject(projectID)
		if err != nil && !isNotFound(err) {
			writeResult(w, r, nil, err)
			return
		}
		if err := s.repo.DeleteProject(projectID); err != nil {
			writeResult(w, r, nil, err)
			return
		}
		s.cleanupProjectFiles(projectID, assets, r)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		methodNotAllowed(w, r, http.MethodGet, http.MethodPatch, http.MethodDelete)
	}
}

func (s *Server) handleMembers(w http.ResponseWriter, r *http.Request, user domain.User, projectID string, memberPath ...string) {
	allowed, permissionErr := s.canViewProject(user, projectID)
	if !s.requirePermission(w, r, allowed, permissionErr, "project_access_denied", "project access denied") {
		return
	}
	targetID := ""
	if len(memberPath) > 1 {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
		return
	}
	if len(memberPath) == 1 {
		targetID = strings.TrimSpace(memberPath[0])
	}
	switch r.Method {
	case http.MethodGet:
		if targetID != "" {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
			return
		}
		members, err := s.repo.ListMembers(projectID)
		writeResult(w, r, members, err)
	case http.MethodPost, http.MethodPatch:
		allowed, permissionErr := s.canManageProject(user, projectID)
		if !s.requirePermission(w, r, allowed, permissionErr, "project_admin_required", "project administrator access required") {
			return
		}
		var req struct {
			UserID string `json:"user_id"`
			Role   string `json:"role"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if targetID == "" {
			targetID = strings.TrimSpace(req.UserID)
		}
		fields := map[string]string{}
		if targetID == "" {
			fields["user_id"] = "user_id is required"
		}
		if !domain.IsProjectRole(req.Role) {
			fields["role"] = "role must be owner, admin, editor, or viewer"
		}
		if len(fields) > 0 {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", fields)
			return
		}
		canModify, err := s.canModifyMember(user, projectID, targetID, req.Role, false)
		if err != nil {
			s.writeRepositoryUnavailable(w, r, "authorize member change", err)
			return
		}
		if !canModify {
			writeAPIError(w, r, http.StatusForbidden, "owner_required", "only an owner may modify owner membership", nil)
			return
		}
		member, err := s.repo.UpsertMember(projectID, targetID, req.Role)
		writeResult(w, r, member, err)
	case http.MethodDelete:
		allowed, permissionErr := s.canManageProject(user, projectID)
		if !s.requirePermission(w, r, allowed, permissionErr, "project_admin_required", "project administrator access required") {
			return
		}
		if targetID == "" {
			targetID = strings.TrimSpace(r.URL.Query().Get("user_id"))
		}
		if targetID == "" {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "validation_failed", "user_id is required", map[string]string{"user_id": "user_id is required"})
			return
		}
		canModify, err := s.canModifyMember(user, projectID, targetID, "", true)
		if err != nil {
			s.writeRepositoryUnavailable(w, r, "authorize member removal", err)
			return
		}
		if !canModify {
			writeAPIError(w, r, http.StatusForbidden, "owner_required", "only an owner may modify owner membership", nil)
			return
		}
		if err := s.repo.DeleteMember(projectID, targetID); err != nil {
			writeResult(w, r, nil, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		methodNotAllowed(w, r, http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete)
	}
}

func (s *Server) canModifyMember(actor domain.User, projectID, targetID, nextRole string, deleting bool) (bool, error) {
	if actor.SystemRole == domain.SystemAdmin {
		return true, nil
	}
	actorRole, err := s.repo.MemberRole(projectID, actor.ID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !domain.CanManageMembers(actorRole) {
		return false, nil
	}
	if actorRole == domain.RoleOwner {
		return true, nil
	}
	targetRole, err := s.repo.MemberRole(projectID, targetID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	if err == nil && targetRole == domain.RoleOwner {
		return false, nil
	}
	return deleting || nextRole != domain.RoleOwner, nil
}

func (s *Server) handleProjectBoards(w http.ResponseWriter, r *http.Request, user domain.User, projectID string) {
	allowed, permissionErr := s.canViewProject(user, projectID)
	if !s.requirePermission(w, r, allowed, permissionErr, "project_access_denied", "project access denied") {
		return
	}
	switch r.Method {
	case http.MethodGet:
		boards, err := s.repo.ListBoards(projectID)
		writeResult(w, r, boards, err)
	case http.MethodPost:
		allowed, permissionErr := s.canEditProject(user, projectID)
		if !s.requirePermission(w, r, allowed, permissionErr, "editor_required", "project editor access required") {
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if msg := validateRequiredName(req.Name, 200); msg != "" {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", map[string]string{"name": msg})
			return
		}
		board, err := s.repo.CreateBoard(projectID, req.Name, user.ID)
		writeResultStatus(w, r, http.StatusCreated, board, err)
	default:
		methodNotAllowed(w, r, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleBoardSubroutes(w http.ResponseWriter, r *http.Request, user domain.User) {
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/boards/"))
	if len(parts) == 0 || len(parts) > 2 {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
		return
	}
	boardID := parts[0]
	board, err := s.repo.GetBoard(boardID)
	if err != nil {
		writeResult(w, r, nil, err)
		return
	}
	allowed, permissionErr := s.canViewProject(user, board.ProjectID)
	if !s.requirePermission(w, r, allowed, permissionErr, "board_access_denied", "board access denied") {
		return
	}
	if len(parts) == 2 {
		if parts[1] != "ws" {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
			return
		}
		if !s.beginWebSocket() {
			writeAPIError(w, r, http.StatusServiceUnavailable, "shutting_down", "service is shutting down", nil)
			return
		}
		defer s.wsHandlers.Done()
		s.handleBoardWS(w, r, user, board)
		return
	}
	switch r.Method {
	case http.MethodGet:
		role := domain.RoleOwner
		if user.SystemRole != domain.SystemAdmin {
			role, err = s.repo.MemberRole(board.ProjectID, user.ID)
			if err != nil {
				s.writeRepositoryUnavailable(w, r, "load board role", err)
				return
			}
		}
		canEdit, err := s.canEditProject(user, board.ProjectID)
		if err != nil {
			s.writeRepositoryUnavailable(w, r, "load board edit permission", err)
			return
		}
		canManage, err := s.canManageProject(user, board.ProjectID)
		if err != nil {
			s.writeRepositoryUnavailable(w, r, "load board manage permission", err)
			return
		}
		if user.SystemRole == domain.SystemAdmin {
			role = domain.RoleOwner
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"board": board,
			"permission": map[string]any{
				"role": role, "can_edit": canEdit, "can_manage": canManage,
			},
			"collaboration_endpoint": "/api/boards/" + board.ID + "/ws",
		})
	case http.MethodPatch:
		allowed, permissionErr := s.canEditProject(user, board.ProjectID)
		if !s.requirePermission(w, r, allowed, permissionErr, "editor_required", "project editor access required") {
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if msg := validateRequiredName(req.Name, 200); msg != "" {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", map[string]string{"name": msg})
			return
		}
		updated, err := s.repo.UpdateBoard(boardID, req.Name)
		writeResult(w, r, updated, err)
	case http.MethodDelete:
		allowed, permissionErr := s.canManageProject(user, board.ProjectID)
		if !s.requirePermission(w, r, allowed, permissionErr, "project_admin_required", "project administrator access required") {
			return
		}
		if err := s.repo.DeleteBoard(boardID); err != nil {
			writeResult(w, r, nil, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		methodNotAllowed(w, r, http.MethodGet, http.MethodPatch, http.MethodDelete)
	}
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request, _ domain.User) {
	switch r.Method {
	case http.MethodGet:
		users, err := s.repo.ListUsers()
		writeResult(w, r, publicUsers(users), err)
	case http.MethodPost:
		var req struct {
			Email      string `json:"email"`
			Name       string `json:"name"`
			Password   string `json:"password"`
			SystemRole string `json:"system_role"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		fields := map[string]string{}
		email, ok := validateEmail(req.Email)
		if !ok {
			fields["email"] = "email must be a valid address"
		}
		if req.Name != "" && len(strings.TrimSpace(req.Name)) > 200 {
			fields["name"] = "name must contain at most 200 characters"
		}
		for key, message := range validateNewPassword(req.Password) {
			if key == "new_password" {
				fields["password"] = message
			}
		}
		if req.SystemRole == "" {
			req.SystemRole = domain.SystemUser
		}
		if !domain.IsSystemRole(req.SystemRole) {
			fields["system_role"] = "system_role must be system_admin or user"
		}
		if len(fields) > 0 {
			writeAPIError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", fields)
			return
		}
		created, err := s.repo.CreateUser(email, req.Name, req.Password, req.SystemRole)
		writeResultStatus(w, r, http.StatusCreated, publicUser(created), err)
	default:
		methodNotAllowed(w, r, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleAdminUser(w http.ResponseWriter, r *http.Request, actor domain.User) {
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/admin/users/"))
	if len(parts) == 0 || len(parts) > 2 {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
		return
	}
	userID := parts[0]
	if len(parts) == 2 {
		if parts[1] != "password" {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
			return
		}
		s.handleAdminPasswordReset(w, r, actor, userID)
		return
	}
	if r.Method != http.MethodPatch {
		methodNotAllowed(w, r, http.MethodPatch)
		return
	}
	var req struct {
		Name       string `json:"name"`
		SystemRole string `json:"system_role"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	fields := map[string]string{}
	if req.Name != "" && len(strings.TrimSpace(req.Name)) > 200 {
		fields["name"] = "name must contain at most 200 characters"
	}
	if req.SystemRole != "" && !domain.IsSystemRole(req.SystemRole) {
		fields["system_role"] = "system_role must be system_admin or user"
	}
	if len(fields) > 0 {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", fields)
		return
	}
	existing, err := s.repo.GetUser(userID)
	if err != nil {
		writeResult(w, r, nil, err)
		return
	}
	if req.SystemRole == "" {
		req.SystemRole = existing.SystemRole
	}
	updated, err := s.repo.UpdateUser(userID, req.Name, req.SystemRole)
	writeResult(w, r, publicUser(updated), err)
}

func (s *Server) handleAdminPasswordReset(w http.ResponseWriter, r *http.Request, actor domain.User, userID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r, http.MethodPost)
		return
	}
	if _, err := s.repo.GetUser(userID); err != nil {
		writeResult(w, r, nil, err)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	fields := validateNewPassword(req.Password)
	if message, ok := fields["new_password"]; ok {
		fields = map[string]string{"password": message}
	}
	if len(fields) > 0 {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", fields)
		return
	}
	if err := s.repo.UpdatePassword(userID, req.Password, true); err != nil {
		writeResult(w, r, nil, err)
		return
	}
	if actor.ID == userID {
		s.clearSessionCookie(w)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func validateRequiredName(value string, max int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "name is required"
	}
	if len(value) > max {
		return "name must contain at most 200 characters"
	}
	return ""
}

func isNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }
