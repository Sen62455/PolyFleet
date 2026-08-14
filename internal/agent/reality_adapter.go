package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/nodeops"
	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/google/uuid"
)

type realityApplyError struct {
	code string
}

var errRealityDataPlaneChanged = errors.New("Reality data plane changed after health verification")

func (err realityApplyError) Error() string { return err.code }

func realityApplyErrorCode(err error) string {
	var typed realityApplyError
	if errors.As(err, &typed) && typed.code != "" {
		return typed.code
	}
	return "reality_apply_failed"
}

func (agent *Agent) applyAndPersistVLESSRealityDesired(
	ctx context.Context,
	envelope protocol.DesiredEnvelope,
) (uint64, error) {
	agent.dataPlaneMu.Lock()
	defer agent.dataPlaneMu.Unlock()
	agent.dataPlaneRevision++
	revision := agent.dataPlaneRevision
	if err := agent.localStore.queueKicks(ctx, envelope.Snapshot.Kicks, time.Now().UTC()); err != nil {
		return revision, realityApplyError{code: "kick_queue_failed"}
	}

	material, err := agent.applyVLESSRealityDesired(ctx, envelope)
	if err != nil {
		return revision, err
	}
	if err := agent.executeRealityPendingKicks(ctx); err != nil {
		return revision, realityApplyError{code: "kick_apply_failed"}
	}
	previousState := agent.state
	agent.state.AppliedVersion = envelope.Snapshot.Version
	agent.state.AppliedSnapshotHash = envelope.SHA256
	agent.state.PendingAckVersion = envelope.Snapshot.Version
	agent.state.PendingAckHash = envelope.SHA256
	agent.state.PendingAckReality = material
	if err := SaveState(agent.config.StatePath, agent.state); err != nil {
		agent.state = previousState
		return revision, err
	}
	return revision, nil
}

func (agent *Agent) applyVLESSRealityDesired(
	ctx context.Context,
	envelope protocol.DesiredEnvelope,
) (*protocol.AppliedRealityMaterial, error) {
	desired := envelope.Snapshot.VLESSReality
	if desired == nil || desired.KeyGeneration < 1 || desired.ListenPort < 1 ||
		desired.ListenPort > 65535 || desired.HandshakeServerPort != 443 ||
		desired.Flow != "xtls-rprx-vision" || desired.Network != "tcp" {
		return nil, realityApplyError{code: "reality_desired_invalid"}
	}
	if len(envelope.Snapshot.Users) > 4096 {
		return nil, realityApplyError{code: "reality_user_limit_exceeded"}
	}
	request := nodeops.RealityApplyRequest{
		RequestID: cryptoutil.NewID(), NodeID: envelope.Snapshot.NodeID,
		Version: envelope.Snapshot.Version, SnapshotSHA256: envelope.SHA256,
		Settings: nodeops.RealityApplySettings{
			ListenPort: desired.ListenPort, ServerName: desired.ServerName,
			HandshakeServer:     desired.HandshakeServer,
			HandshakeServerPort: desired.HandshakeServerPort,
			Flow:                desired.Flow, Network: desired.Network,
			KeyGeneration: desired.KeyGeneration, APISecret: agent.config.RealityAPISecret,
		},
		Users: make([]nodeops.RealityApplyUser, 0, len(envelope.Snapshot.Users)),
	}
	seenUsers := make(map[string]struct{}, len(envelope.Snapshot.Users))
	for _, user := range envelope.Snapshot.Users {
		if _, err := uuid.Parse(user.ID); err != nil || user.Credential.Protocol != "vless" ||
			user.Credential.Ref == "" || user.Credential.Fingerprint == "" ||
			user.Credential.VerifierSHA256 != "" || user.ManagementMode != "" ||
			user.RemoteClientID != 0 || !validRealityQuotaState(user.QuotaState) {
			return nil, realityApplyError{code: "reality_user_invalid"}
		}
		if _, duplicate := seenUsers[user.ID]; duplicate {
			return nil, realityApplyError{code: "reality_user_duplicate"}
		}
		seenUsers[user.ID] = struct{}{}
		if !effectiveRealityUser(user) {
			continue
		}
		secret, err := agent.fetchVLESSCredentialMaterial(ctx, envelope, user.Credential.Ref)
		if err != nil {
			return nil, err
		}
		if !validVLESSCredential(secret, user.Credential.Fingerprint) {
			secret = ""
			return nil, realityApplyError{code: "reality_credential_invalid"}
		}
		request.Users = append(request.Users, nodeops.RealityApplyUser{UserID: user.ID, UUID: secret})
		secret = ""
	}
	response, err := agent.executeRealityApplyWithHelper(ctx, request)
	for index := range request.Users {
		request.Users[index].UUID = ""
	}
	if err != nil {
		return nil, err
	}
	if response.Status != "succeeded" || response.AppliedVersion != envelope.Snapshot.Version ||
		response.SnapshotSHA256 != envelope.SHA256 || response.Reality == nil ||
		response.Reality.KeyGeneration != desired.KeyGeneration ||
		!validRealityPublicMaterial(*response.Reality) {
		return nil, realityApplyError{code: "reality_helper_invalid_response"}
	}
	material := *response.Reality
	return &material, nil
}

func validRealityQuotaState(state string) bool {
	return state == "unlimited" || state == "active" || state == "limited"
}

func (agent *Agent) fetchVLESSCredentialMaterial(
	ctx context.Context,
	envelope protocol.DesiredEnvelope,
	credentialRef string,
) (string, error) {
	request := protocol.CredentialMaterialRequest{
		CredentialRef: credentialRef, DesiredVersion: envelope.Snapshot.Version,
		SnapshotSHA256: envelope.SHA256,
	}
	var result protocol.CredentialMaterialResponse
	status, err := agent.doJSON(ctx, http.MethodPost, "/agent/v1/credential-material",
		request, cryptoutil.NewID(), true, &result)
	if err != nil {
		return "", realityApplyError{code: "credential_material_unavailable"}
	}
	if status != http.StatusOK || result.CredentialRef != credentialRef || result.Secret == "" {
		result.Secret = ""
		return "", realityApplyError{code: "credential_material_invalid"}
	}
	return result.Secret, nil
}

func (agent *Agent) executeRealityApplyWithHelper(
	ctx context.Context,
	request nodeops.RealityApplyRequest,
) (nodeops.HelperResponse, error) {
	if agent.realityApplyExecutor != nil {
		return agent.realityApplyExecutor(ctx, request)
	}
	response, err := exchangeHelper(
		ctx,
		agent.config.OperationsSocketPath,
		55*time.Second,
		nodeops.HelperRequest{RealityApply: &request},
	)
	if err != nil {
		code := "reality_helper_unavailable"
		switch helperExchangeErrorStage(err) {
		case helperExchangeWrite:
			code = "reality_helper_write_failed"
		case helperExchangeRead:
			code = "reality_helper_read_failed"
		}
		return nodeops.HelperResponse{}, realityApplyError{code: code}
	}
	if response.Status == "failed" {
		code := response.ErrorCode
		if !strings.HasPrefix(code, "reality_") {
			code = "reality_helper_failed"
		}
		return nodeops.HelperResponse{}, realityApplyError{code: code}
	}
	return response, nil
}

func (agent *Agent) probeVLESSReality(
	ctx context.Context,
	now time.Time,
) (protocol.AdapterInfo, protocol.CoreInfo, uint64) {
	probedAt := now.UTC()
	adapter := protocol.AdapterInfo{
		Name: "sing_box_vless_reality", Status: "unavailable",
		ErrorCode: "reality_probe_unavailable", LastProbedAt: &probedAt,
	}
	core := protocol.CoreInfo{Name: "sing-box"}

	// Serialize only the local helper exchange with other data-plane actions.
	// The lock is released before the heartbeat HTTP request is sent.
	agent.dataPlaneMu.Lock()
	response, err := agent.executeRealityProbeWithHelper(ctx, nodeops.RealityProbeRequest{})
	revision := agent.dataPlaneRevision
	agent.dataPlaneMu.Unlock()
	if err != nil {
		return adapter, core, revision
	}
	result := response.RealityProbe
	if response.Status != "succeeded" || result == nil || result.ProbedAt.IsZero() ||
		(result.AdapterStatus != "compatible" && result.AdapterStatus != "incompatible") ||
		(result.AdapterStatus == "compatible" &&
			(result.AdapterVersion == "" || result.CoreVersion == "" || result.AdapterErrorCode != "")) ||
		(result.AdapterStatus == "incompatible" && result.AdapterErrorCode != "reality_binary_incompatible") {
		adapter.ErrorCode = "reality_probe_invalid_response"
		return adapter, core, revision
	}
	adapter.Status = result.AdapterStatus
	adapter.Version = result.AdapterVersion
	adapter.ErrorCode = result.AdapterErrorCode
	core.Version = result.CoreVersion
	core.Running = result.CoreRunning && result.AdapterStatus == "compatible"
	return adapter, core, revision
}

func (agent *Agent) sendPendingRealityAck(ctx context.Context, expectedRevision uint64) error {
	agent.dataPlaneMu.Lock()
	defer agent.dataPlaneMu.Unlock()
	if agent.state.PendingAckVersion == 0 {
		return nil
	}
	if agent.dataPlaneRevision != expectedRevision {
		return errRealityDataPlaneChanged
	}
	return agent.sendPendingAck(ctx)
}

func (agent *Agent) executeRealityProbeWithHelper(
	ctx context.Context,
	request nodeops.RealityProbeRequest,
) (nodeops.HelperResponse, error) {
	if agent.realityProbeExecutor != nil {
		return agent.realityProbeExecutor(ctx, request)
	}
	return exchangeHelper(
		ctx,
		agent.config.OperationsSocketPath,
		10*time.Second,
		nodeops.HelperRequest{RealityProbe: &request},
	)
}

func effectiveRealityUser(user protocol.DesiredUser) bool {
	return user.Enabled && user.QuotaState != "limited"
}

func validVLESSCredential(secret, fingerprint string) bool {
	parsed, err := uuid.Parse(secret)
	if err != nil || parsed.String() != strings.ToLower(secret) {
		return false
	}
	digest := sha256.Sum256([]byte(secret))
	expected := "fp_" + base64.RawURLEncoding.EncodeToString(digest[:6])
	return expected == fingerprint
}

func validRealityPublicMaterial(material protocol.AppliedRealityMaterial) bool {
	if material.KeyGeneration < 1 || len(material.ShortID) != 16 {
		return false
	}
	for _, character := range material.ShortID {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	decoded, err := base64.RawURLEncoding.DecodeString(material.PublicKey)
	return err == nil && len(decoded) == 32
}

func (agent *Agent) validatePendingRealityAck() error {
	if agent.config.AdapterType != "sing_box_vless_reality" {
		if agent.state.PendingAckReality != nil {
			return fmt.Errorf("non-Reality Agent state contains Reality acknowledgement material")
		}
		return nil
	}
	if agent.state.PendingAckVersion == 0 && agent.state.PendingAckReality != nil {
		return fmt.Errorf("Reality acknowledgement material has no pending version")
	}
	if agent.state.PendingAckVersion > 0 &&
		(agent.state.PendingAckHash == "" || agent.state.PendingAckReality == nil) {
		return fmt.Errorf("pending Reality acknowledgement has no public material")
	}
	if agent.state.PendingAckReality != nil && !validRealityPublicMaterial(*agent.state.PendingAckReality) {
		return fmt.Errorf("pending Reality acknowledgement public material is invalid")
	}
	return nil
}
