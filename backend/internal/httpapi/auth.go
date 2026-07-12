package httpapi

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"dreamwhiteboard/backend/internal/domain"
	"dreamwhiteboard/backend/internal/store"
)

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r, http.MethodPost)
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	email, validEmail := validateEmail(req.Email)
	if !validEmail || req.Password == "" {
		writeAPIError(w, r, http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect", nil)
		return
	}
	now := s.config.Now().UTC()
	accountLimitKey := "account:" + email
	ipLimitKey := "ip:" + s.clientIP(r)
	accountReservation, allowed, retryAfter := s.loginAccount.allow(accountLimitKey, now)
	if !allowed {
		setRetryAfter(w, retryAfter)
		writeAPIError(w, r, http.StatusTooManyRequests, "login_rate_limited", "too many login attempts; try again later", nil)
		return
	}
	ipReservation, allowed, retryAfter := s.loginIP.allow(ipLimitKey, now)
	if !allowed {
		s.loginAccount.release(accountReservation)
		setRetryAfter(w, retryAfter)
		writeAPIError(w, r, http.StatusTooManyRequests, "login_rate_limited", "too many login attempts; try again later", nil)
		return
	}
	user, err := s.repo.Authenticate(email, req.Password)
	if errors.Is(err, store.ErrInvalidCredentials) {
		writeAPIError(w, r, http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect", nil)
		return
	}
	if err != nil {
		s.loginAccount.release(accountReservation)
		s.loginIP.release(ipReservation)
		s.writeRepositoryUnavailable(w, r, "authenticate credentials", err)
		return
	}
	s.loginAccount.reset(accountLimitKey)
	s.loginIP.release(ipReservation)
	if err := s.issueSession(w, user.ID, now); err != nil {
		s.logger.Error("create login session", "request_id", requestID(r.Context()), "user_id", user.ID, "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, "session_create_failed", "could not create session", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": publicUser(user)})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r, http.MethodPost)
		return
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		if err := s.repo.DeleteSession(store.HashSessionToken(cookie.Value)); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.logger.Error("delete logout session", "request_id", requestID(r.Context()), "error", err)
		}
	}
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, user domain.User) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, publicUser(user))
}

func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request, user domain.User) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r, http.MethodPost)
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	fields := validateNewPassword(req.NewPassword)
	if req.CurrentPassword == "" {
		fields["current_password"] = "current password is required"
	}
	if len(fields) > 0 {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", fields)
		return
	}
	if _, err := s.repo.Authenticate(user.Email, req.CurrentPassword); errors.Is(err, store.ErrInvalidCredentials) {
		writeAPIError(w, r, http.StatusUnauthorized, "invalid_current_password", "current password is incorrect", nil)
		return
	} else if err != nil {
		s.writeRepositoryUnavailable(w, r, "verify current password", err)
		return
	}
	if err := s.repo.UpdatePassword(user.ID, req.NewPassword, false); err != nil {
		writeResult(w, r, nil, err)
		return
	}
	s.loginAccount.reset("account:" + user.Email)
	if err := s.issueSession(w, user.ID, s.config.Now().UTC()); err != nil {
		s.logger.Error("renew session after password change", "request_id", requestID(r.Context()), "user_id", user.ID, "error", err)
		s.clearSessionCookie(w)
		writeAPIError(w, r, http.StatusInternalServerError, "session_create_failed", "password changed; sign in again", nil)
		return
	}
	updated, err := s.repo.GetUser(user.ID)
	if err != nil {
		s.writeRepositoryUnavailable(w, r, "reload user after password change", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": publicUser(updated)})
}

func (s *Server) issueSession(w http.ResponseWriter, userID string, now time.Time) error {
	token := randomHex(32)
	expiresAt := now.Add(s.config.SessionTTL)
	if _, err := s.repo.CreateSession(store.HashSessionToken(token), userID, expiresAt); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/api",
		Expires:  expiresAt,
		MaxAge:   int(s.config.SessionTTL / time.Second),
		HttpOnly: true,
		Secure:   s.config.CookieSecure,
		SameSite: s.config.CookieSameSite,
	})
	return nil
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/api",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.config.CookieSecure,
		SameSite: s.config.CookieSameSite,
	})
}

func validateEmail(value string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(value))
	if email == "" || len(email) > 254 {
		return "", false
	}
	parsed, err := mail.ParseAddress(email)
	return email, err == nil && parsed.Address == email
}

func validateNewPassword(password string) map[string]string {
	fields := map[string]string{}
	if len(password) < 12 {
		fields["new_password"] = "password must contain at least 12 characters"
	} else if len(password) > 128 {
		fields["new_password"] = "password must contain at most 128 characters"
	}
	return fields
}
