package server

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/Sen62455/PolyFleet/internal/config"
	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/store"
	"github.com/Sen62455/PolyFleet/internal/webui"
)

type App struct {
	config        config.Server
	store         *store.Store
	masterKey     []byte
	logger        *slog.Logger
	publicOrigin  string
	dummyHash     string
	loginLimiter  *rateLimiter
	enrollLimiter *rateLimiter
	subLimiter    *rateLimiter
}

func New(cfg config.Server, database *store.Store, masterKey []byte, logger *slog.Logger) (*App, error) {
	publicURL, err := url.Parse(cfg.PublicURL)
	if err != nil {
		return nil, err
	}
	dummyHash, err := cryptoutil.HashPassword("not-a-real-password", cryptoutil.DefaultPasswordParams)
	if err != nil {
		return nil, err
	}
	return &App{
		config:        cfg,
		store:         database,
		masterKey:     masterKey,
		logger:        logger,
		publicOrigin:  strings.TrimSuffix(publicURL.Scheme+"://"+publicURL.Host, "/"),
		dummyHash:     dummyHash,
		loginLimiter:  newRateLimiter(8, 5*time.Minute),
		enrollLimiter: newRateLimiter(20, 5*time.Minute),
		subLimiter:    newRateLimiter(120, time.Minute),
	}, nil
}

func (a *App) Handler() (http.Handler, error) {
	frontend, err := webui.Handler()
	if err != nil {
		return nil, err
	}
	router := chi.NewRouter()
	router.Use(a.requestIDMiddleware)
	router.Use(a.recoveryMiddleware)
	router.Use(a.loggingMiddleware)
	router.Use(a.securityHeadersMiddleware)

	router.Get("/healthz", a.handleHealth)
	router.Route("/api/v1", func(api chi.Router) {
		api.Get("/setup/status", a.handleSetupStatus)
		api.Post("/setup/bootstrap", a.handleBootstrap)
		api.Post("/auth/login", a.handleLogin)
		api.Group(func(authenticated chi.Router) {
			authenticated.Use(a.sessionMiddleware)
			authenticated.Use(a.csrfMiddleware)
			authenticated.Get("/auth/session", a.handleSession)
			authenticated.Post("/auth/logout", a.handleLogout)
			authenticated.Get("/nodes", a.handleListNodes)
			authenticated.Get("/operations", a.handleListOperations)
			authenticated.Post("/nodes", a.handleCreateNode)
			authenticated.Get("/nodes/{nodeID}", a.handleGetNode)
			authenticated.Get("/nodes/{nodeID}/metrics", a.handleListNodeMetrics)
			authenticated.Get("/nodes/{nodeID}/telemetry", a.handleGetNodeTelemetry)
			authenticated.Put("/nodes/{nodeID}", a.handleUpdateNode)
			authenticated.Post("/nodes/{nodeID}/traffic-calibration", a.handleCalibrateNodeTraffic)
			authenticated.Delete("/nodes/{nodeID}", a.handleArchiveNode)
			authenticated.Post("/nodes/{nodeID}/enrollment-token", a.handleEnrollmentToken)
			authenticated.Post(
				"/nodes/{nodeID}/reality/rotate-identity", a.handleRotateRealityIdentity,
			)
			authenticated.Get("/nodes/{nodeID}/operations", a.handleListNodeOperations)
			authenticated.Post("/nodes/{nodeID}/operations", a.handleCreateNodeOperation)
			authenticated.Post(
				"/nodes/{nodeID}/operations/{operationID}/retry", a.handleRetryNodeOperation,
			)
			authenticated.Post("/nodes/{nodeID}/retry-sync", a.handleRetryNodeSync)
			authenticated.Get("/nodes/{nodeID}/backups", a.handleListConfigBackups)
			authenticated.Get("/nodes/{nodeID}/s-ui", a.handleGetSUIState)
			authenticated.Put("/nodes/{nodeID}/s-ui/targets", a.handleSetSUITargets)
			authenticated.Post("/nodes/{nodeID}/s-ui/clients/{clientID}/import", a.handleImportSUIClient)
			authenticated.Post("/nodes/{nodeID}/s-ui/clients/{clientID}/adopt", a.handleAdoptSUIClient)
			authenticated.Get("/users", a.handleListUsers)
			authenticated.Post("/users", a.handleCreateUser)
			authenticated.Get("/users/{userID}", a.handleGetUser)
			authenticated.Put("/users/{userID}", a.handleUpdateUser)
			authenticated.Delete("/users/{userID}", a.handleArchiveUser)
			authenticated.Post("/users/{userID}/assignments", a.handleAssignUser)
			authenticated.Put("/users/{userID}/assignments/{nodeID}", a.handleUpdateAssignment)
			authenticated.Delete("/users/{userID}/assignments/{nodeID}", a.handleUnassignUser)
			authenticated.Post(
				"/users/{userID}/assignments/{nodeID}/credential",
				a.handleRevealAssignmentCredential,
			)
			authenticated.Post("/users/{userID}/kick", a.handleKickUser)
			authenticated.Post("/users/{userID}/rotate-credentials", a.handleRotateUserCredentials)
			authenticated.Post(
				"/users/{userID}/assignments/{nodeID}/rotate-credential",
				a.handleRotateAssignmentCredential,
			)
			authenticated.Get(
				"/users/{userID}/subscription-tokens", a.handleListSubscriptionTokens,
			)
			authenticated.Post(
				"/users/{userID}/subscription-tokens", a.handleCreateSubscriptionToken,
			)
			authenticated.Post(
				"/users/{userID}/subscription-tokens/{tokenID}/rotate",
				a.handleRotateSubscriptionToken,
			)
			authenticated.Delete(
				"/users/{userID}/subscription-tokens/{tokenID}",
				a.handleRevokeSubscriptionToken,
			)
			authenticated.Get("/alerts", a.handleListAlerts)
			authenticated.Post("/alerts/{alertID}/acknowledge", a.handleAcknowledgeAlert)
		})
	})
	router.Route("/agent/v1", func(agent chi.Router) {
		agent.Post("/enroll", a.handleAgentEnroll)
		agent.Group(func(secured chi.Router) {
			secured.Use(a.agentMiddleware)
			secured.Post("/heartbeat", a.handleAgentHeartbeat)
			secured.Post("/telemetry", a.handleAgentTelemetry)
			secured.Get("/desired", a.handleAgentDesired)
			secured.Post("/desired/{version}/ack", a.handleAgentDesiredAck)
			secured.Post("/traffic-batches", a.handleAgentTrafficBatches)
			secured.Post("/online-snapshot", a.handleAgentOnlineSnapshot)
			secured.Post("/s-ui-report", a.handleAgentSUIReport)
			secured.Post("/credential-material", a.handleCredentialMaterial)
			secured.Get("/operations", a.handleAgentOperations)
			secured.Post("/operations/{operationID}/result", a.handleAgentOperationResult)
		})
	})
	router.Group(func(subscription chi.Router) {
		subscription.Use(a.subscriptionRateLimitMiddleware)
		subscription.Get("/sub/{token}", a.handleSubscriptionDefault)
		subscription.Get("/sub/{token}/uri", a.handleSubscriptionURI)
		subscription.Get("/sub/{token}/base64", a.handleSubscriptionBase64)
		subscription.Get("/sub/{token}/clash", a.handleSubscriptionClash)
		subscription.Get("/sub/{token}/sing-box", a.handleSubscriptionSingBox)
	})
	router.NotFound(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/") ||
			strings.HasPrefix(request.URL.Path, "/agent/") ||
			strings.HasPrefix(request.URL.Path, "/sub/") {
			a.writeError(response, request, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		frontend.ServeHTTP(response, request)
	})
	return router, nil
}
