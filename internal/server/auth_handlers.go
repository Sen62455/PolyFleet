package server

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/store"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{3,32}$`)

type bootstrapRequest struct {
	BootstrapToken string `json:"bootstrap_token"`
	Username       string `json:"username"`
	Password       string `json:"password"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type sessionResponse struct {
	Admin     adminResponse `json:"admin"`
	CSRFToken string        `json:"csrf_token"`
	ExpiresAt time.Time     `json:"expires_at"`
}

type adminResponse struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

func (a *App) handleHealth(response http.ResponseWriter, request *http.Request) {
	if err := a.store.DB().PingContext(request.Context()); err != nil {
		a.writeError(response, request, http.StatusServiceUnavailable, "database_unavailable", "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   time.Now().UTC(),
	})
}

func (a *App) handleSetupStatus(response http.ResponseWriter, request *http.Request) {
	count, err := a.store.CountAdmins(request.Context())
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "setup_status_failed", "could not read setup status")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"setup_required":             count == 0,
		"bootstrap_token_configured": a.config.BootstrapToken != "",
	})
}

func (a *App) handleBootstrap(response http.ResponseWriter, request *http.Request) {
	if !a.originAllowed(request) {
		a.writeError(response, request, http.StatusForbidden, "origin_rejected", "request origin rejected")
		return
	}
	var input bootstrapRequest
	if err := decodeJSON(response, request, &input, 16*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid bootstrap request")
		return
	}
	if a.config.BootstrapToken == "" {
		a.writeError(response, request, http.StatusServiceUnavailable,
			"bootstrap_not_configured", "server bootstrap token is not configured")
		return
	}
	if len(input.BootstrapToken) != len(a.config.BootstrapToken) ||
		subtle.ConstantTimeCompare([]byte(input.BootstrapToken), []byte(a.config.BootstrapToken)) != 1 {
		a.writeError(response, request, http.StatusForbidden, "bootstrap_rejected", "bootstrap authorization failed")
		return
	}
	input.Username = strings.TrimSpace(input.Username)
	if !usernamePattern.MatchString(input.Username) || len(input.Password) < 12 || len(input.Password) > 128 {
		a.writeError(response, request, http.StatusUnprocessableEntity,
			"validation_failed", "username or password does not meet the required policy")
		return
	}
	count, err := a.store.CountAdmins(request.Context())
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "bootstrap_failed", "administrator setup failed")
		return
	}
	if count != 0 {
		a.writeError(response, request, http.StatusConflict, "already_initialized", "administrator already initialized")
		return
	}
	hash, err := cryptoutil.HashPassword(input.Password, cryptoutil.DefaultPasswordParams)
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "bootstrap_failed", "administrator setup failed")
		return
	}
	now := time.Now().UTC()
	admin := store.Admin{
		ID:           cryptoutil.NewID(),
		Username:     input.Username,
		PasswordHash: hash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := a.store.CreateAdmin(request.Context(), admin); err != nil {
		a.writeError(response, request, http.StatusConflict, "already_initialized", "administrator already initialized")
		return
	}
	a.respondWithNewSession(response, request, admin)
}

func (a *App) handleLogin(response http.ResponseWriter, request *http.Request) {
	if !a.originAllowed(request) {
		a.writeError(response, request, http.StatusForbidden, "origin_rejected", "request origin rejected")
		return
	}
	now := time.Now().UTC()
	if !a.loginLimiter.Allow(remoteIP(request), now) {
		response.Header().Set("Retry-After", "300")
		a.writeError(response, request, http.StatusTooManyRequests, "rate_limited", "too many sign-in attempts")
		return
	}
	var input loginRequest
	if err := decodeJSON(response, request, &input, 16*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid sign-in request")
		return
	}
	admin, err := a.store.GetAdminByUsername(request.Context(), strings.TrimSpace(input.Username))
	encoded := a.dummyHash
	if err == nil {
		encoded = admin.PasswordHash
	}
	valid, verifyErr := cryptoutil.VerifyPassword(encoded, input.Password)
	if verifyErr != nil || err != nil || !valid || admin.DisabledAt != nil {
		a.writeError(response, request, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}
	a.respondWithNewSession(response, request, admin)
}

func (a *App) respondWithNewSession(response http.ResponseWriter, request *http.Request, admin store.Admin) {
	now := time.Now().UTC()
	token, err := cryptoutil.RandomToken(32)
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "session_failed", "could not create session")
		return
	}
	csrf, err := cryptoutil.RandomToken(24)
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "session_failed", "could not create session")
		return
	}
	session := store.AdminSession{
		ID:         cryptoutil.NewID(),
		AdminID:    admin.ID,
		Username:   admin.Username,
		TokenHash:  cryptoutil.TokenHash(token),
		CSRFToken:  csrf,
		ExpiresAt:  now.Add(a.config.SessionLifetime),
		LastSeenAt: now,
		CreatedAt:  now,
	}
	if err := a.store.CreateAdminSession(request.Context(), session); err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "session_failed", "could not create session")
		return
	}
	a.setSessionCookie(response, token, session.ExpiresAt)
	writeJSON(response, http.StatusOK, sessionResponse{
		Admin:     adminResponse{ID: admin.ID, Username: admin.Username},
		CSRFToken: session.CSRFToken,
		ExpiresAt: session.ExpiresAt,
	})
}

func (a *App) handleSession(response http.ResponseWriter, request *http.Request) {
	session := sessionFromContext(request.Context())
	writeJSON(response, http.StatusOK, sessionResponse{
		Admin:     adminResponse{ID: session.AdminID, Username: session.Username},
		CSRFToken: session.CSRFToken,
		ExpiresAt: session.ExpiresAt,
	})
}

func (a *App) handleLogout(response http.ResponseWriter, request *http.Request) {
	session := sessionFromContext(request.Context())
	if err := a.store.RevokeAdminSession(request.Context(), session.ID, time.Now().UTC()); err != nil && !errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusInternalServerError, "logout_failed", "could not end session")
		return
	}
	a.clearSessionCookie(response)
	response.WriteHeader(http.StatusNoContent)
}
