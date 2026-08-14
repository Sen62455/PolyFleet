package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/store"
)

type contextKey string

const (
	requestIDKey      contextKey = "request_id"
	sessionKey        contextKey = "admin_session"
	agentKey          contextKey = "agent_identity"
	sessionCookieName            = "hyfleet_session"
)

func (a *App) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestID := strings.TrimSpace(request.Header.Get("X-Request-ID"))
		if len(requestID) < 8 || len(requestID) > 80 {
			requestID = cryptoutil.NewID()
		}
		response.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(request.Context(), requestIDKey, requestID)
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func requestIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func (a *App) recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				a.logger.Error("http panic recovered",
					"request_id", requestIDFromContext(request.Context()),
					"panic", recovered,
					"stack", string(debug.Stack()),
				)
				a.writeError(response, request, http.StatusInternalServerError,
					"internal_error", "an internal error occurred")
			}
		}()
		next.ServeHTTP(response, request)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (recorder *statusRecorder) WriteHeader(status int) {
	if recorder.status == 0 {
		recorder.status = status
	}
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *statusRecorder) Write(body []byte) (int, error) {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	count, err := recorder.ResponseWriter.Write(body)
	recorder.bytes += count
	return count, err
}

func (a *App) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: response}
		next.ServeHTTP(recorder, request)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		a.logger.LogAttrs(request.Context(), slog.LevelInfo, "http request",
			slog.String("request_id", requestIDFromContext(request.Context())),
			slog.String("method", request.Method),
			slog.String("path", redactedRequestPath(request.URL.Path)),
			slog.Int("status", status),
			slog.Int("bytes", recorder.bytes),
			slog.Int64("duration_ms", time.Since(started).Milliseconds()),
		)
	})
}

func (a *App) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		response.Header().Set("Content-Security-Policy",
			"default-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; "+
				"img-src 'self' data:; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'")
		if a.config.CookieSecure {
			response.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if strings.HasPrefix(request.URL.Path, "/api/") ||
			strings.HasPrefix(request.URL.Path, "/agent/") ||
			strings.HasPrefix(request.URL.Path, "/sub/") {
			response.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(response, request)
	})
}

func redactedRequestPath(path string) string {
	if !strings.HasPrefix(path, "/sub/") {
		return path
	}
	remainder := strings.TrimPrefix(path, "/sub/")
	_, suffix, found := strings.Cut(remainder, "/")
	if !found || suffix == "" {
		return "/sub/[redacted]"
	}
	return "/sub/[redacted]/" + suffix
}

func (a *App) subscriptionRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !a.subLimiter.Allow(remoteIP(request), time.Now().UTC()) {
			response.Header().Set("Retry-After", "60")
			a.writeError(response, request, http.StatusTooManyRequests,
				"subscription_rate_limited", "too many subscription requests")
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (a *App) sessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			a.writeError(response, request, http.StatusUnauthorized, "authentication_required", "sign in required")
			return
		}
		session, err := a.store.GetAdminSession(request.Context(), cryptoutil.TokenHash(cookie.Value))
		if err != nil {
			a.clearSessionCookie(response)
			a.writeError(response, request, http.StatusUnauthorized, "authentication_required", "sign in required")
			return
		}
		now := time.Now().UTC()
		if session.RevokedAt != nil || now.After(session.ExpiresAt) ||
			now.Sub(session.LastSeenAt) > a.config.SessionIdleTimeout {
			_ = a.store.RevokeAdminSession(request.Context(), session.ID, now)
			a.clearSessionCookie(response)
			a.writeError(response, request, http.StatusUnauthorized, "session_expired", "session expired")
			return
		}
		if now.Sub(session.LastSeenAt) >= time.Minute {
			if err := a.store.TouchAdminSession(request.Context(), session.ID, now); err != nil {
				a.logger.Warn("session touch failed", "request_id", requestIDFromContext(request.Context()))
			}
			session.LastSeenAt = now
		}
		ctx := context.WithValue(request.Context(), sessionKey, session)
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func sessionFromContext(ctx context.Context) store.AdminSession {
	value, _ := ctx.Value(sessionKey).(store.AdminSession)
	return value
}

func (a *App) csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet || request.Method == http.MethodHead || request.Method == http.MethodOptions {
			next.ServeHTTP(response, request)
			return
		}
		if !a.originAllowed(request) {
			a.writeError(response, request, http.StatusForbidden, "origin_rejected", "request origin rejected")
			return
		}
		session := sessionFromContext(request.Context())
		received := request.Header.Get("X-CSRF-Token")
		if len(received) != len(session.CSRFToken) ||
			subtle.ConstantTimeCompare([]byte(received), []byte(session.CSRFToken)) != 1 {
			a.writeError(response, request, http.StatusForbidden, "csrf_rejected", "CSRF validation failed")
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (a *App) originAllowed(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSuffix(parsed.Scheme+"://"+parsed.Host, "/"), a.publicOrigin)
}

func (a *App) agentMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-HyFleet-Protocol") != strconv.Itoa(1) {
			a.writeError(response, request, http.StatusUpgradeRequired,
				"protocol_incompatible", "unsupported Agent protocol")
			return
		}
		authorization := request.Header.Get("Authorization")
		credential, found := strings.CutPrefix(authorization, "Bearer ")
		if !found || credential == "" {
			a.writeError(response, request, http.StatusUnauthorized, "agent_authentication_failed", "Agent authentication failed")
			return
		}
		identity, err := a.store.AuthenticateAgent(request.Context(), credential)
		if err != nil {
			a.writeError(response, request, http.StatusUnauthorized, "agent_authentication_failed", "Agent authentication failed")
			return
		}
		ctx := context.WithValue(request.Context(), agentKey, identity)
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func agentFromContext(ctx context.Context) store.AgentIdentity {
	value, _ := ctx.Value(agentKey).(store.AgentIdentity)
	return value
}

func (a *App) setSessionCookie(response http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   a.config.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (a *App) clearSessionCookie(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.config.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func remoteIP(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil {
		return host
	}
	return request.RemoteAddr
}

func parseBearer(request *http.Request) (string, error) {
	value := request.Header.Get("Authorization")
	token, found := strings.CutPrefix(value, "Bearer ")
	if !found || token == "" {
		return "", errors.New("missing bearer token")
	}
	return token, nil
}
