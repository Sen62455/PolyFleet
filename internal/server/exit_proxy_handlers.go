package server

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/store"
	"github.com/go-chi/chi/v5"
)

type exitProxyRequest struct {
	ServerSide     *bool    `json:"server_side"`
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Protocol       string   `json:"protocol"`
	Host           string   `json:"host"`
	Port           int      `json:"port"`
	Username       string   `json:"username"`
	Password       *string  `json:"password"`
	TLS            bool     `json:"tls"`
	SkipCertVerify bool     `json:"skip_cert_verify"`
	UDP            *bool    `json:"udp"`
	Notes          string   `json:"notes"`
	NodeIDs        []string `json:"node_ids"`
}

type exitProxyAssignmentResponse struct {
	NodeID     string `json:"node_id"`
	NodeName   string `json:"node_name"`
	PublicHost string `json:"public_host"`
}

type exitProxyResponse struct {
	ServerSide     bool                          `json:"server_side"`
	ID             string                        `json:"id"`
	Name           string                        `json:"name"`
	Kind           string                        `json:"kind"`
	Protocol       string                        `json:"protocol"`
	Host           string                        `json:"host"`
	Port           int                           `json:"port"`
	Username       string                        `json:"username"`
	PasswordSet    bool                          `json:"password_set"`
	TLS            bool                          `json:"tls"`
	SkipCertVerify bool                          `json:"skip_cert_verify"`
	UDP            bool                          `json:"udp"`
	Notes          string                        `json:"notes"`
	Assignments    []exitProxyAssignmentResponse `json:"assignments"`
	CreatedAt      time.Time                     `json:"created_at"`
	UpdatedAt      time.Time                     `json:"updated_at"`
}

type exitProxyNodesRequest struct {
	NodeIDs []string `json:"node_ids"`
}

type landingGuideStep struct {
	Title    string `json:"title"`
	Where    string `json:"where"`
	Expected string `json:"expected"`
	Body     string `json:"body"`
	Filename string `json:"filename,omitempty"`
	Copy     string `json:"copy"`
}

type landingGuideResponse struct {
	ProxyID   string             `json:"proxy_id"`
	Name      string             `json:"name"`
	Kind      string             `json:"kind"`
	Protocol  string             `json:"protocol"`
	Listen    string             `json:"listen"`
	Username  string             `json:"username"`
	AllowFrom []string           `json:"allow_from"`
	Steps     []landingGuideStep `json:"steps"`
}

func presentExitProxy(proxy store.ExitProxy) exitProxyResponse {
	assignments := make([]exitProxyAssignmentResponse, 0, len(proxy.Assignments))
	for _, assignment := range proxy.Assignments {
		assignments = append(assignments, exitProxyAssignmentResponse{
			NodeID: assignment.NodeID, NodeName: assignment.NodeName, PublicHost: assignment.PublicHost,
		})
	}
	return exitProxyResponse{
		ServerSide: proxy.ServerSide,
		ID:         proxy.ID, Name: proxy.Name, Kind: proxy.Kind, Protocol: proxy.Protocol,
		Host: proxy.Host, Port: proxy.Port, Username: proxy.Username, PasswordSet: proxy.PasswordSet,
		TLS: proxy.TLS, SkipCertVerify: proxy.SkipCertVerify, UDP: proxy.UDP, Notes: proxy.Notes,
		Assignments: assignments, CreatedAt: proxy.CreatedAt, UpdatedAt: proxy.UpdatedAt,
	}
}

func (a *App) handleListExitProxies(response http.ResponseWriter, request *http.Request) {
	proxies, err := a.store.ListExitProxies(request.Context())
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "exit_proxies_read_failed", "could not read exit proxies")
		return
	}
	result := make([]exitProxyResponse, 0, len(proxies))
	for _, proxy := range proxies {
		result = append(result, presentExitProxy(proxy))
	}
	writeJSON(response, http.StatusOK, map[string]any{"exit_proxies": result})
}

func (a *App) handleGetExitProxy(response http.ResponseWriter, request *http.Request) {
	proxy, err := a.store.GetExitProxy(request.Context(), chi.URLParam(request, "proxyID"))
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "exit_proxy_not_found", "exit proxy not found")
		return
	}
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "exit_proxy_read_failed", "could not read exit proxy")
		return
	}
	writeJSON(response, http.StatusOK, presentExitProxy(proxy))
}

func (a *App) handleCreateExitProxy(response http.ResponseWriter, request *http.Request) {
	var input exitProxyRequest
	if err := decodeJSON(response, request, &input, 32*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid exit proxy request")
		return
	}
	input = normalizeExitProxyRequest(input)
	if message := validateExitProxyRequest(input, true); message != "" {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", message)
		return
	}
	now := time.Now().UTC()
	password := ""
	if input.Password != nil {
		password = *input.Password
	}
	if input.Kind == store.ExitProxyKindLandingVPS && password == "" {
		generated, err := cryptoutil.RandomToken(18)
		if err != nil {
			a.writeError(response, request, http.StatusInternalServerError, "exit_proxy_create_failed", "could not generate landing credentials")
			return
		}
		password = generated
		if input.Username == "" {
			input.Username = "polyfleet"
		}
	}
	udp := input.Protocol != store.ExitProxyProtocolHTTP
	if input.UDP != nil {
		udp = *input.UDP
	}
	proxy, err := a.store.CreateExitProxy(request.Context(), store.NewExitProxy{
		ServerSide: input.ServerSide != nil && *input.ServerSide,
		ID:         cryptoutil.NewID(), Name: input.Name, Kind: input.Kind, Protocol: input.Protocol,
		Host: input.Host, Port: input.Port, Username: input.Username, Password: password,
		TLS: input.TLS, SkipCertVerify: input.SkipCertVerify, UDP: udp, Notes: input.Notes,
		Now: now, MasterKey: a.masterKey,
	})
	if errors.Is(err, store.ErrConflict) {
		a.writeError(response, request, http.StatusConflict, "exit_proxy_conflict", "an exit proxy with that name already exists")
		return
	}
	if errors.Is(err, store.ErrUnsupported) {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", err.Error())
		return
	}
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "exit_proxy_create_failed", "could not create exit proxy")
		return
	}
	if len(input.NodeIDs) > 0 {
		proxy, err = a.store.ReplaceExitProxyNodes(request.Context(), proxy.ID, input.NodeIDs, now)
		if a.writeExitProxyStoreError(response, request, err) {
			return
		}
	}
	writeJSON(response, http.StatusCreated, presentExitProxy(proxy))
}

func (a *App) handleUpdateExitProxy(response http.ResponseWriter, request *http.Request) {
	var input exitProxyRequest
	if err := decodeJSON(response, request, &input, 32*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid exit proxy request")
		return
	}
	input = normalizeExitProxyRequest(input)
	if message := validateExitProxyRequest(input, false); message != "" {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", message)
		return
	}
	udp := input.Protocol != store.ExitProxyProtocolHTTP
	if input.UDP != nil {
		udp = *input.UDP
	}
	now := time.Now().UTC()
	proxy, err := a.store.UpdateExitProxy(request.Context(), chi.URLParam(request, "proxyID"), store.UpdateExitProxy{
		ServerSide: input.ServerSide,
		Name:       input.Name, Kind: input.Kind, Protocol: input.Protocol, Host: input.Host,
		Port: input.Port, Username: input.Username, Password: input.Password,
		TLS: input.TLS, SkipCertVerify: input.SkipCertVerify, UDP: udp, Notes: input.Notes,
		Now: now, MasterKey: a.masterKey,
	})
	if a.writeExitProxyStoreError(response, request, err) {
		return
	}
	if input.NodeIDs != nil {
		proxy, err = a.store.ReplaceExitProxyNodes(request.Context(), proxy.ID, input.NodeIDs, now)
		if a.writeExitProxyStoreError(response, request, err) {
			return
		}
	}
	writeJSON(response, http.StatusOK, presentExitProxy(proxy))
}

func (a *App) handleDeleteExitProxy(response http.ResponseWriter, request *http.Request) {
	err := a.store.DeleteExitProxy(request.Context(), chi.URLParam(request, "proxyID"))
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "exit_proxy_not_found", "exit proxy not found")
		return
	}
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "exit_proxy_delete_failed", "could not delete exit proxy")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (a *App) handleReplaceExitProxyNodes(response http.ResponseWriter, request *http.Request) {
	var input exitProxyNodesRequest
	if err := decodeJSON(response, request, &input, 16*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid exit proxy assignment request")
		return
	}
	if input.NodeIDs == nil {
		input.NodeIDs = []string{}
	}
	proxy, err := a.store.ReplaceExitProxyNodes(
		request.Context(), chi.URLParam(request, "proxyID"), input.NodeIDs, time.Now().UTC(),
	)
	if a.writeExitProxyStoreError(response, request, err) {
		return
	}
	writeJSON(response, http.StatusOK, presentExitProxy(proxy))
}

func (a *App) handleExitProxyLandingGuide(response http.ResponseWriter, request *http.Request) {
	proxy, err := a.store.GetExitProxy(request.Context(), chi.URLParam(request, "proxyID"))
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "exit_proxy_not_found", "exit proxy not found")
		return
	}
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "exit_proxy_read_failed", "could not read exit proxy")
		return
	}
	if proxy.Kind != store.ExitProxyKindLandingVPS {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "landing guide is only available for landing VPS exits")
		return
	}
	password, err := a.store.OpenExitProxyPassword(request.Context(), proxy.ID, a.masterKey)
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "exit_proxy_read_failed", "could not read landing credentials")
		return
	}
	writeJSON(response, http.StatusOK, buildLandingGuide(proxy, password))
}

func (a *App) writeExitProxyStoreError(response http.ResponseWriter, request *http.Request, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		a.writeError(response, request, http.StatusNotFound, "exit_proxy_not_found", "exit proxy or assigned node was not found")
	case errors.Is(err, store.ErrConflict):
		a.writeError(response, request, http.StatusConflict, "exit_proxy_conflict", "an exit proxy with that name already exists")
	case errors.Is(err, store.ErrUnsupported):
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", err.Error())
	default:
		a.writeError(response, request, http.StatusInternalServerError, "exit_proxy_write_failed", "could not save exit proxy")
	}
	return true
}

func normalizeExitProxyRequest(input exitProxyRequest) exitProxyRequest {
	input.Name = strings.TrimSpace(input.Name)
	input.Kind = strings.TrimSpace(input.Kind)
	input.Protocol = strings.TrimSpace(strings.ToLower(input.Protocol))
	input.Host = strings.TrimSpace(input.Host)
	input.Username = strings.TrimSpace(input.Username)
	input.Notes = strings.TrimSpace(input.Notes)
	if input.Password != nil {
		trimmed := strings.TrimSpace(*input.Password)
		input.Password = &trimmed
	}
	return input
}

func validateExitProxyRequest(input exitProxyRequest, creating bool) string {
	if len(input.Name) < 2 || len(input.Name) > 64 {
		return "name must be 2 to 64 characters"
	}
	if input.Kind != store.ExitProxyKindRentedIP && input.Kind != store.ExitProxyKindLandingVPS {
		return "kind must be rented_ip or landing_vps"
	}
	if input.Protocol != store.ExitProxyProtocolSOCKS5 && input.Protocol != store.ExitProxyProtocolHTTP {
		return "protocol must be socks5 or http"
	}
	if !validEndpointHost(input.Host) {
		return "host must be a domain name or IP address"
	}
	if input.Port < 1 || input.Port > 65535 {
		return "port must be between 1 and 65535"
	}
	if len(input.Username) > 128 {
		return "username must be at most 128 characters"
	}
	if input.Password != nil && len(*input.Password) > 256 {
		return "password must be at most 256 characters"
	}
	if len(input.Notes) > 512 {
		return "notes must be at most 512 characters"
	}
	if input.TLS && input.Protocol != store.ExitProxyProtocolHTTP {
		return "tls is only valid for http/https exits"
	}
	if input.SkipCertVerify && !input.TLS {
		return "skip_cert_verify requires tls"
	}
	if input.UDP != nil && *input.UDP && input.Protocol != store.ExitProxyProtocolSOCKS5 {
		return "udp is only valid for socks5 exits"
	}
	return ""
}

func nodeExitProxyID(input nodeRequest, current *store.Node) (string, string) {
	exitProxyID := ""
	if current != nil {
		exitProxyID = current.ExitProxyID
	}
	if input.ExitProxyID == nil {
		return exitProxyID, ""
	}
	return strings.TrimSpace(*input.ExitProxyID), ""
}

func (a *App) validateStoredExitProxy(request *http.Request, exitProxyID string) string {
	if exitProxyID == "" {
		return ""
	}
	if _, err := a.store.GetExitProxy(request.Context(), exitProxyID); errors.Is(err, store.ErrNotFound) {
		return "exit_proxy_id does not exist"
	} else if err != nil {
		return "could not validate exit_proxy_id"
	}
	return ""
}

func landingAllowFrom(proxy store.ExitProxy) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(proxy.Assignments))
	for _, assignment := range proxy.Assignments {
		host := strings.TrimSpace(assignment.PublicHost)
		if host == "" {
			continue
		}
		if ip := net.ParseIP(host); ip != nil {
			host = ip.String()
		}
		if _, exists := seen[host]; exists {
			continue
		}
		seen[host] = struct{}{}
		result = append(result, host)
	}
	return result
}
