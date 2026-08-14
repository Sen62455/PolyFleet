package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/protocol"
)

type suiReconcileError struct {
	code string
}

func (err suiReconcileError) Error() string { return err.code }

func (agent *Agent) runSUIUsageCycle(ctx context.Context) error {
	now := time.Now().UTC()
	adapterInfo, core := agent.suiClient.probe(ctx, now)
	agent.adapterInfo = adapterInfo
	agent.adapterCore = core
	agent.usage.Enabled = agent.config.SUIToken != ""
	if adapterInfo.Status != "compatible" {
		agent.usage.Available = false
		agent.usage.LastErrorCode = adapterInfo.ErrorCode
		reportErr := agent.reportSUIDiscovery(ctx, suiDiscovery{}, now)
		flushErr := agent.flushTrafficOutbox(ctx)
		return errors.Join(reportErr, flushErr)
	}
	discovery, err := agent.suiClient.discover(ctx)
	if err != nil {
		agent.adapterInfo.Status = "unavailable"
		agent.adapterInfo.ErrorCode = suiErrorCode(err)
		agent.usage.Available = false
		agent.usage.LastErrorCode = suiErrorCode(err)
		reportErr := agent.reportSUIDiscovery(ctx, suiDiscovery{}, now)
		flushErr := agent.flushTrafficOutbox(ctx)
		return errors.Join(err, reportErr, flushErr)
	}
	mappings, err := agent.localStore.listSUIMappings(ctx)
	if err != nil {
		return err
	}
	mappedByRemote := make(map[int64]suiClientMapping, len(mappings))
	for _, mapping := range mappings {
		mappedByRemote[mapping.RemoteClientID] = mapping
	}
	counters := make(map[string]trafficCounters, len(mappings))
	online := make([]protocol.OnlineUser, 0, len(mappings))
	for _, remote := range discovery.Clients {
		mapping, ok := mappedByRemote[remote.ID]
		if remote.Up < 0 || remote.Down < 0 {
			return suiReconcileError{code: "sui_traffic_invalid"}
		}
		userID := fmt.Sprintf("sui:%d", remote.ID)
		if ok {
			userID = mapping.UserID
		}
		counters[userID] = trafficCounters{TX: remote.Up, RX: remote.Down}
		if _, ok := discovery.Online[remote.Name]; ok {
			online = append(online, protocol.OnlineUser{UserID: userID, Connections: 1})
		}
	}
	if _, err := agent.localStore.recordTrafficSample(
		ctx, agent.state.InstallationID, counters, now,
	); err != nil {
		agent.usage.Available = false
		agent.usage.LastErrorCode = "traffic_store_failed"
		return err
	}
	agent.usage.Available = true
	agent.usage.LastErrorCode = ""
	agent.usage.LastSampledAt = &now
	var cycleErrors []error
	if err := agent.postOnlineSnapshot(ctx, online, now); err != nil {
		cycleErrors = append(cycleErrors, err)
	}
	if err := agent.reportSUIDiscovery(ctx, discovery, now); err != nil {
		cycleErrors = append(cycleErrors, err)
	}
	if err := agent.flushTrafficOutbox(ctx); err != nil {
		cycleErrors = append(cycleErrors, err)
	}
	return errors.Join(cycleErrors...)
}

func (agent *Agent) reportSUIDiscovery(ctx context.Context, discovery suiDiscovery, sampledAt time.Time) error {
	mappings, err := agent.localStore.listSUIMappings(ctx)
	if err != nil {
		return err
	}
	mappedByRemote := make(map[int64]suiClientMapping, len(mappings))
	for _, mapping := range mappings {
		mappedByRemote[mapping.RemoteClientID] = mapping
	}
	report := protocol.SUIReportRequest{
		InstallationID: agent.state.InstallationID,
		Adapter:        agent.adapterInfo,
		Core:           agent.adapterCore,
		Inbounds:       make([]protocol.SUIDiscoveredInbound, 0, len(discovery.Inbounds)),
		Clients:        make([]protocol.SUIDiscoveredClient, 0, len(discovery.Clients)),
		SampledAt:      sampledAt,
	}
	for _, inbound := range discovery.Inbounds {
		report.Inbounds = append(report.Inbounds, protocol.SUIDiscoveredInbound{
			RemoteID: inbound.ID, Tag: inbound.Tag, Type: inbound.Type,
			Listen: inbound.Listen, ListenPort: inbound.ListenPort,
		})
	}
	for _, remote := range discovery.Clients {
		item := protocol.SUIDiscoveredClient{
			RemoteID: remote.ID, Name: remote.Name, Enabled: remote.Enable,
			InboundIDs: remote.Inbounds, UploadBytes: remote.Up, DownloadBytes: remote.Down,
			ExpiresAt: remote.Expiry,
		}
		_, item.Online = discovery.Online[remote.Name]
		if mapping, ok := mappedByRemote[remote.ID]; ok {
			item.MappedUserID = mapping.UserID
			item.ManagementMode = mapping.ManagementMode
		}
		report.Clients = append(report.Clients, item)
	}
	var result protocol.SUIReportResponse
	status, err := agent.doJSON(ctx, http.MethodPost, "/agent/v1/s-ui-report", report,
		cryptoutil.NewID(), true, &result)
	if err != nil {
		return err
	}
	if status != http.StatusOK || !result.Accepted {
		return errors.New("S-UI report endpoint returned an invalid response")
	}
	return nil
}

func (agent *Agent) postOnlineSnapshot(
	ctx context.Context,
	users []protocol.OnlineUser,
	sampledAt time.Time,
) error {
	request := protocol.OnlineSnapshotRequest{
		SnapshotID: cryptoutil.NewID(), InstallationID: agent.state.InstallationID,
		SampledAt: sampledAt, Users: users,
	}
	var result protocol.OnlineSnapshotResponse
	status, err := agent.doJSON(ctx, http.MethodPost, "/agent/v1/online-snapshot",
		request, cryptoutil.NewID(), true, &result)
	if err != nil {
		return err
	}
	if status != http.StatusOK || !result.Accepted {
		return fmt.Errorf("online snapshot returned status %d", status)
	}
	return nil
}

func (agent *Agent) applySUIDesired(ctx context.Context, envelope protocol.DesiredEnvelope) error {
	adapterInfo, core := agent.suiClient.probe(ctx, time.Now().UTC())
	agent.adapterInfo = adapterInfo
	agent.adapterCore = core
	if adapterInfo.Status != "compatible" {
		return suiReconcileError{code: adapterInfo.ErrorCode}
	}
	discovery, err := agent.suiClient.discover(ctx)
	if err != nil {
		return suiReconcileError{code: suiErrorCode(err)}
	}
	targetInboundIDs := []int64{}
	if envelope.Snapshot.SUI != nil {
		targetInboundIDs = append(targetInboundIDs, envelope.Snapshot.SUI.TargetInboundIDs...)
	}
	if !validSUIInboundTargets(discovery.Inbounds, targetInboundIDs) {
		return suiReconcileError{code: "sui_target_inbound_invalid"}
	}
	mappings, err := agent.localStore.listSUIMappings(ctx)
	if err != nil {
		return err
	}
	mappingByUser := make(map[string]suiClientMapping, len(mappings))
	for _, mapping := range mappings {
		mappingByUser[mapping.UserID] = mapping
	}
	desiredUsers := make(map[string]protocol.DesiredUser, len(envelope.Snapshot.Users))
	for _, desired := range envelope.Snapshot.Users {
		if desired.ManagementMode != "read_only" && desired.ManagementMode != "managed" {
			return suiReconcileError{code: "sui_management_mode_invalid"}
		}
		desiredUsers[desired.ID] = desired
		mapping, hasMapping := mappingByUser[desired.ID]
		switch desired.ManagementMode {
		case "read_only":
			if err := agent.applySUIReadOnlyMapping(ctx, desired, mapping, hasMapping, discovery); err != nil {
				return err
			}
		case "managed":
			if len(targetInboundIDs) == 0 {
				return suiReconcileError{code: "sui_target_inbound_required"}
			}
			if err := agent.applySUIManagedClient(
				ctx, envelope, desired, mapping, hasMapping, discovery, targetInboundIDs,
			); err != nil {
				return err
			}
		}
	}
	for _, mapping := range mappings {
		if _, ok := desiredUsers[mapping.UserID]; ok {
			continue
		}
		if mapping.ManagementMode == "read_only" {
			if err := agent.localStore.deleteSUIMapping(ctx, mapping.UserID); err != nil {
				return err
			}
			continue
		}
		if err := agent.deleteOwnedSUIClient(ctx, mapping); err != nil {
			return err
		}
	}
	return nil
}

func validSUIInboundTargets(inbounds []suiInbound, targets []int64) bool {
	available := make(map[int64]struct{}, len(inbounds))
	for _, inbound := range inbounds {
		available[inbound.ID] = struct{}{}
	}
	seen := make(map[int64]struct{}, len(targets))
	for _, target := range targets {
		if _, ok := available[target]; !ok {
			return false
		}
		if _, exists := seen[target]; exists {
			return false
		}
		seen[target] = struct{}{}
	}
	return true
}

func (agent *Agent) applySUIReadOnlyMapping(
	ctx context.Context,
	desired protocol.DesiredUser,
	mapping suiClientMapping,
	hasMapping bool,
	discovery suiDiscovery,
) error {
	remoteID := desired.RemoteClientID
	if remoteID == 0 && hasMapping {
		remoteID = mapping.RemoteClientID
	}
	remote, ok := suiSummaryByID(discovery.Clients, remoteID)
	if !ok {
		return suiReconcileError{code: "sui_read_only_client_missing"}
	}
	now := time.Now().UTC()
	if !hasMapping || mapping.RemoteClientID != remote.ID {
		if err := agent.localStore.primeTrafficBaseline(ctx, desired.ID, trafficCounters{
			TX: remote.Up, RX: remote.Down,
		}, now); err != nil {
			return err
		}
	}
	return agent.localStore.upsertSUIMapping(ctx, suiClientMapping{
		UserID: desired.ID, RemoteClientID: remote.ID, ManagementMode: "read_only",
		RemoteName: remote.Name,
	}, now)
}

func (agent *Agent) applySUIManagedClient(
	ctx context.Context,
	envelope protocol.DesiredEnvelope,
	desired protocol.DesiredUser,
	mapping suiClientMapping,
	hasMapping bool,
	discovery suiDiscovery,
	targetInboundIDs []int64,
) error {
	remoteID := desired.RemoteClientID
	if hasMapping {
		remoteID = mapping.RemoteClientID
	}
	var detail suiClientDetail
	var err error
	if remoteID > 0 {
		detail, err = agent.suiClient.getClient(ctx, remoteID)
	}
	if errors.Is(err, errSUIClientNotFound) && hasMapping && mapping.ManagementMode == "managed" {
		detail, err = agent.recoverOwnedSUIClient(ctx, mapping, desired.Username, discovery)
		if err == nil {
			remoteID = detail.ID
		}
	}
	if err != nil && !errors.Is(err, errSUIClientNotFound) {
		return suiReconcileError{code: suiErrorCode(err)}
	}
	if remoteID == 0 || errors.Is(err, errSUIClientNotFound) {
		if hasMapping || desired.RemoteClientID > 0 {
			return suiReconcileError{code: "sui_owned_client_missing"}
		}
		for _, candidate := range discovery.Clients {
			if candidate.Name == desired.Username {
				return suiReconcileError{code: "sui_unmanaged_name_conflict"}
			}
		}
		secret, fetchErr := agent.fetchCredentialMaterial(ctx, envelope, desired.Credential.Ref)
		if fetchErr != nil {
			return fetchErr
		}
		detail = suiClientDetail{
			Enable: effectiveSUIEnabled(desired, time.Now().UTC()), Name: desired.Username,
			Inbounds: append([]int64(nil), targetInboundIDs...), Links: json.RawMessage("[]"),
			Config: json.RawMessage("{}"), Expiry: suiExpiry(desired.ExpiresAt),
		}
		if err := setSUIClientCredential(&detail, desired.Username, secret); err != nil {
			secret = ""
			return suiReconcileError{code: "sui_client_config_invalid"}
		}
		secret = ""
		if err := agent.suiClient.saveClient(ctx, "new", detail); err != nil {
			return suiReconcileError{code: suiErrorCode(err)}
		}
		refreshed, err := agent.suiClient.discover(ctx)
		if err != nil {
			return suiReconcileError{code: suiErrorCode(err)}
		}
		matches := make([]suiClientSummary, 0, 1)
		for _, candidate := range refreshed.Clients {
			if candidate.Name == desired.Username {
				matches = append(matches, candidate)
			}
		}
		if len(matches) != 1 {
			return suiReconcileError{code: "sui_created_client_ambiguous"}
		}
		detail, err = agent.suiClient.getClient(ctx, matches[0].ID)
		if err != nil {
			return suiReconcileError{code: suiErrorCode(err)}
		}
	}
	before, _ := json.Marshal(detail)
	currentSecret, err := suiClientCredential(detail)
	if err != nil {
		return suiReconcileError{code: "sui_client_credential_invalid"}
	}
	if suiCredentialFingerprint(currentSecret) != desired.Credential.Fingerprint {
		currentSecret, err = agent.fetchCredentialMaterial(ctx, envelope, desired.Credential.Ref)
		if err != nil {
			return err
		}
	}
	detail.Name = desired.Username
	detail.Enable = effectiveSUIEnabled(desired, time.Now().UTC())
	detail.Expiry = suiExpiry(desired.ExpiresAt)
	detail.Inbounds = reconcileSUIInboundMembership(
		detail.Inbounds, discovery.Inbounds, targetInboundIDs,
	)
	if err := setSUIClientCredential(&detail, desired.Username, currentSecret); err != nil {
		currentSecret = ""
		return suiReconcileError{code: "sui_client_config_invalid"}
	}
	currentSecret = ""
	after, _ := json.Marshal(detail)
	if !bytes.Equal(before, after) {
		if err := agent.suiClient.saveClient(ctx, "edit", detail); err != nil {
			return suiReconcileError{code: suiErrorCode(err)}
		}
	}
	verified, err := agent.suiClient.getClient(ctx, detail.ID)
	if err != nil {
		return suiReconcileError{code: suiErrorCode(err)}
	}
	verifiedSecret, err := suiClientCredential(verified)
	if err != nil || verified.Name != desired.Username ||
		suiCredentialFingerprint(verifiedSecret) != desired.Credential.Fingerprint {
		verifiedSecret = ""
		return suiReconcileError{code: "sui_client_verification_failed"}
	}
	verifiedSecret = ""
	now := time.Now().UTC()
	if !hasMapping || mapping.RemoteClientID != verified.ID {
		if err := agent.localStore.primeTrafficBaseline(ctx, desired.ID, trafficCounters{
			TX: verified.Up, RX: verified.Down,
		}, now); err != nil {
			return err
		}
	}
	return agent.localStore.upsertSUIMapping(ctx, suiClientMapping{
		UserID: desired.ID, RemoteClientID: verified.ID, ManagementMode: "managed",
		RemoteName: verified.Name, CredentialFingerprint: desired.Credential.Fingerprint,
	}, now)
}

func reconcileSUIInboundMembership(
	current []int64,
	discovered []suiInbound,
	targets []int64,
) []int64 {
	hysteriaIDs := make(map[int64]struct{}, len(discovered))
	for _, inbound := range discovered {
		hysteriaIDs[inbound.ID] = struct{}{}
	}
	result := make([]int64, 0, len(current)+len(targets))
	seen := make(map[int64]struct{}, len(current)+len(targets))
	for _, inboundID := range current {
		if _, isHysteria := hysteriaIDs[inboundID]; isHysteria {
			continue
		}
		if _, duplicate := seen[inboundID]; duplicate {
			continue
		}
		seen[inboundID] = struct{}{}
		result = append(result, inboundID)
	}
	for _, inboundID := range targets {
		if _, duplicate := seen[inboundID]; duplicate {
			continue
		}
		seen[inboundID] = struct{}{}
		result = append(result, inboundID)
	}
	return result
}

func (agent *Agent) recoverOwnedSUIClient(
	ctx context.Context,
	mapping suiClientMapping,
	desiredName string,
	discovery suiDiscovery,
) (suiClientDetail, error) {
	matches := make([]suiClientDetail, 0, 1)
	for _, candidate := range discovery.Clients {
		if candidate.Name != mapping.RemoteName && candidate.Name != desiredName {
			continue
		}
		detail, err := agent.suiClient.getClient(ctx, candidate.ID)
		if err != nil {
			return suiClientDetail{}, err
		}
		secret, err := suiClientCredential(detail)
		if err == nil && suiCredentialFingerprint(secret) == mapping.CredentialFingerprint {
			matches = append(matches, detail)
		}
		secret = ""
	}
	if len(matches) != 1 {
		return suiClientDetail{}, suiReconcileError{code: "sui_ownership_recovery_failed"}
	}
	return matches[0], nil
}

func (agent *Agent) deleteOwnedSUIClient(ctx context.Context, mapping suiClientMapping) error {
	detail, err := agent.suiClient.getClient(ctx, mapping.RemoteClientID)
	if errors.Is(err, errSUIClientNotFound) {
		return agent.localStore.deleteSUIMapping(ctx, mapping.UserID)
	}
	if err != nil {
		return suiReconcileError{code: suiErrorCode(err)}
	}
	secret, err := suiClientCredential(detail)
	if err != nil || detail.Name != mapping.RemoteName ||
		suiCredentialFingerprint(secret) != mapping.CredentialFingerprint {
		secret = ""
		return suiReconcileError{code: "sui_ownership_guard_failed"}
	}
	secret = ""
	if err := agent.suiClient.saveClient(ctx, "del", mapping.RemoteClientID); err != nil {
		return suiReconcileError{code: suiErrorCode(err)}
	}
	return agent.localStore.deleteSUIMapping(ctx, mapping.UserID)
}

func (agent *Agent) fetchCredentialMaterial(
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
		return "", suiReconcileError{code: "credential_material_unavailable"}
	}
	if status != http.StatusOK || result.CredentialRef != credentialRef || result.Secret == "" {
		result.Secret = ""
		return "", suiReconcileError{code: "credential_material_invalid"}
	}
	return result.Secret, nil
}

func effectiveSUIEnabled(desired protocol.DesiredUser, now time.Time) bool {
	if !desired.Enabled || desired.QuotaState == "limited" {
		return false
	}
	return desired.ExpiresAt == nil || now.Before(desired.ExpiresAt.UTC())
}

func suiExpiry(expiresAt *time.Time) int64 {
	if expiresAt == nil {
		return 0
	}
	return expiresAt.UTC().Unix()
}

func suiSummaryByID(clients []suiClientSummary, id int64) (suiClientSummary, bool) {
	index := sort.Search(len(clients), func(index int) bool { return clients[index].ID >= id })
	if index < len(clients) && clients[index].ID == id {
		return clients[index], true
	}
	return suiClientSummary{}, false
}
