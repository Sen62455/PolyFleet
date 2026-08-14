package server

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/store"
	"github.com/go-chi/chi/v5"
)

const publicSubscriptionTokenMarker = "hys_"

type subscriptionTokenRequest struct {
	Name           string     `json:"name"`
	AllowedFormats []string   `json:"allowed_formats"`
	ExpiresAt      *time.Time `json:"expires_at"`
}

type subscriptionTokenResponse struct {
	ID             string     `json:"id"`
	UserID         string     `json:"user_id"`
	Name           string     `json:"name"`
	TokenPrefix    string     `json:"token_prefix"`
	AllowedFormats []string   `json:"allowed_formats"`
	Status         string     `json:"status"`
	ExpiresAt      *time.Time `json:"expires_at"`
	LastUsedAt     *time.Time `json:"last_used_at"`
	RevokedAt      *time.Time `json:"revoked_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type subscriptionURLsResponse struct {
	URI     string `json:"uri,omitempty"`
	Base64  string `json:"base64,omitempty"`
	Clash   string `json:"clash,omitempty"`
	SingBox string `json:"sing_box,omitempty"`
}

type issuedSubscriptionTokenResponse struct {
	Subscription subscriptionTokenResponse `json:"subscription"`
	Token        string                    `json:"token"`
	URLs         subscriptionURLsResponse  `json:"urls"`
}

func (a *App) handleListSubscriptionTokens(response http.ResponseWriter, request *http.Request) {
	tokens, err := a.store.ListSubscriptionTokens(
		request.Context(), chi.URLParam(request, "userID"),
	)
	if a.writeSubscriptionStoreError(response, request, err) {
		return
	}
	now := time.Now().UTC()
	result := make([]subscriptionTokenResponse, 0, len(tokens))
	for _, token := range tokens {
		result = append(result, presentSubscriptionToken(token, now))
	}
	writeJSON(response, http.StatusOK, map[string]any{"tokens": result})
}

func (a *App) handleCreateSubscriptionToken(response http.ResponseWriter, request *http.Request) {
	var input subscriptionTokenRequest
	if err := decodeJSON(response, request, &input, 16*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid subscription token request")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	now := time.Now().UTC()
	if message := validateSubscriptionTokenRequest(input, now); message != "" {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", message)
		return
	}
	formats, _ := store.NormalizeSubscriptionFormats(input.AllowedFormats)
	issued, err := a.store.CreateSubscriptionToken(request.Context(), store.NewSubscriptionToken{
		ID: cryptoutil.NewID(), UserID: chi.URLParam(request, "userID"), Name: input.Name,
		AllowedFormats: formats, ExpiresAt: input.ExpiresAt, Now: now,
	})
	if a.writeSubscriptionStoreError(response, request, err) {
		return
	}
	setCredentialResponseHeaders(response)
	writeJSON(response, http.StatusCreated, a.presentIssuedSubscriptionToken(issued, now))
}

func (a *App) handleRotateSubscriptionToken(response http.ResponseWriter, request *http.Request) {
	now := time.Now().UTC()
	issued, err := a.store.RotateSubscriptionToken(
		request.Context(), chi.URLParam(request, "userID"),
		chi.URLParam(request, "tokenID"), now,
	)
	if a.writeSubscriptionStoreError(response, request, err) {
		return
	}
	setCredentialResponseHeaders(response)
	writeJSON(response, http.StatusOK, a.presentIssuedSubscriptionToken(issued, now))
}

func (a *App) handleRevokeSubscriptionToken(response http.ResponseWriter, request *http.Request) {
	err := a.store.RevokeSubscriptionToken(
		request.Context(), chi.URLParam(request, "userID"),
		chi.URLParam(request, "tokenID"), time.Now().UTC(),
	)
	if a.writeSubscriptionStoreError(response, request, err) {
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (a *App) handleRotateAssignmentCredential(response http.ResponseWriter, request *http.Request) {
	now := time.Now().UTC()
	user, credential, err := a.store.RotateAssignmentCredential(
		request.Context(), chi.URLParam(request, "userID"),
		chi.URLParam(request, "nodeID"), now, a.masterKey,
	)
	if a.writeUserStoreError(response, request, err) {
		return
	}
	setCredentialResponseHeaders(response)
	writeJSON(response, http.StatusAccepted, map[string]any{
		"user":       presentUser(user, now),
		"credential": presentCredential(credential),
	})
}

func (a *App) handleRotateUserCredentials(response http.ResponseWriter, request *http.Request) {
	now := time.Now().UTC()
	user, credentials, err := a.store.RotateUserCredentials(
		request.Context(), chi.URLParam(request, "userID"), now, a.masterKey,
	)
	if a.writeUserStoreError(response, request, err) {
		return
	}
	setCredentialResponseHeaders(response)
	writeJSON(response, http.StatusAccepted, map[string]any{
		"user":        presentUser(user, now),
		"credentials": presentCredentials(credentials),
	})
}

func (a *App) handleSubscriptionDefault(response http.ResponseWriter, request *http.Request) {
	a.handleSubscription(response, request, "base64")
}

func (a *App) handleSubscriptionURI(response http.ResponseWriter, request *http.Request) {
	a.handleSubscription(response, request, "uri")
}

func (a *App) handleSubscriptionBase64(response http.ResponseWriter, request *http.Request) {
	a.handleSubscription(response, request, "base64")
}

func (a *App) handleSubscriptionClash(response http.ResponseWriter, request *http.Request) {
	a.handleSubscription(response, request, "clash")
}

func (a *App) handleSubscriptionSingBox(response http.ResponseWriter, request *http.Request) {
	a.handleSubscription(response, request, "sing-box")
}

func (a *App) handleSubscription(
	response http.ResponseWriter,
	request *http.Request,
	format string,
) {
	token := chi.URLParam(request, "token")
	if len(token) < 32 || len(token) > 128 || !strings.HasPrefix(token, publicSubscriptionTokenMarker) {
		a.writeError(response, request, http.StatusNotFound, "subscription_not_found", "subscription not found")
		return
	}
	subscription, err := a.store.ResolveSubscription(
		request.Context(), cryptoutil.TokenHash(token), format, time.Now().UTC(), a.masterKey,
	)
	if errors.Is(err, store.ErrUnauthorized) || errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "subscription_not_found", "subscription not found")
		return
	}
	if err != nil {
		a.logger.Error("render subscription failed",
			"request_id", requestIDFromContext(request.Context()), "error", err,
		)
		a.writeError(response, request, http.StatusInternalServerError, "subscription_failed", "subscription could not be generated")
		return
	}
	rendered, err := renderSubscription(format, subscription)
	if err != nil {
		a.logger.Error("encode subscription failed",
			"request_id", requestIDFromContext(request.Context()), "error", err,
		)
		a.writeError(response, request, http.StatusInternalServerError, "subscription_failed", "subscription could not be generated")
		return
	}
	setSubscriptionResponseHeaders(response)
	response.Header().Set("Content-Type", rendered.ContentType)
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(rendered.Body)
}

func setSubscriptionResponseHeaders(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store, max-age=0")
	response.Header().Set("Pragma", "no-cache")
	response.Header().Set("Expires", "0")
}

func validateSubscriptionTokenRequest(input subscriptionTokenRequest, now time.Time) string {
	if len(input.Name) < 1 || len(input.Name) > 64 {
		return "name must be between 1 and 64 characters"
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.After(now) {
		return "expires_at must be in the future"
	}
	if _, err := store.NormalizeSubscriptionFormats(input.AllowedFormats); err != nil {
		return "allowed_formats must contain uri, base64, clash, or sing-box"
	}
	return ""
}

func presentSubscriptionToken(token store.SubscriptionToken, now time.Time) subscriptionTokenResponse {
	status := "active"
	if token.RevokedAt != nil {
		status = "revoked"
	} else if token.ExpiresAt != nil && !now.Before(*token.ExpiresAt) {
		status = "expired"
	}
	return subscriptionTokenResponse{
		ID: token.ID, UserID: token.UserID, Name: token.Name, TokenPrefix: token.TokenPrefix,
		AllowedFormats: token.AllowedFormats, Status: status, ExpiresAt: token.ExpiresAt,
		LastUsedAt: token.LastUsedAt, RevokedAt: token.RevokedAt,
		CreatedAt: token.CreatedAt, UpdatedAt: token.UpdatedAt,
	}
}

func (a *App) presentIssuedSubscriptionToken(
	issued store.IssuedSubscriptionToken,
	now time.Time,
) issuedSubscriptionTokenResponse {
	return issuedSubscriptionTokenResponse{
		Subscription: presentSubscriptionToken(issued.Token, now),
		Token:        issued.Secret,
		URLs:         a.subscriptionURLs(issued.Secret, issued.Token.AllowedFormats),
	}
}

func (a *App) subscriptionURLs(token string, formats []string) subscriptionURLsResponse {
	base := a.publicOrigin + "/sub/" + url.PathEscape(token)
	result := subscriptionURLsResponse{}
	for _, format := range formats {
		switch format {
		case "uri":
			result.URI = base + "/uri"
		case "base64":
			result.Base64 = base + "/base64"
		case "clash":
			result.Clash = base + "/clash"
		case "sing-box":
			result.SingBox = base + "/sing-box"
		}
	}
	return result
}

func (a *App) writeSubscriptionStoreError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		a.writeError(response, request, http.StatusNotFound, "subscription_resource_not_found", "user or subscription token not found")
	case errors.Is(err, store.ErrExpired):
		a.writeError(response, request, http.StatusConflict, "subscription_token_expired", "expired tokens cannot be rotated; create a new token")
	case errors.Is(err, store.ErrConflict):
		a.writeError(response, request, http.StatusConflict, "subscription_token_revoked", "revoked tokens cannot be rotated; create a new token")
	default:
		a.logger.Error("subscription token operation failed",
			"request_id", requestIDFromContext(request.Context()), "error", err,
		)
		a.writeError(response, request, http.StatusInternalServerError, "subscription_operation_failed", "subscription operation failed")
	}
	return true
}
