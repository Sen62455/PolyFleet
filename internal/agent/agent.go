package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sen62455/PolyFleet/internal/buildinfo"
	"github.com/Sen62455/PolyFleet/internal/config"
	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/nodeops"
	"github.com/Sen62455/PolyFleet/internal/protocol"
)

type Agent struct {
	config               config.Agent
	logger               *slog.Logger
	client               *http.Client
	collector            Collector
	telemetryCollector   TelemetryCollector
	state                State
	authCache            *AuthCache
	localStore           *localStore
	statsClient          *hysteriaStatsClient
	realityStatsClient   *realityStatsClient
	suiClient            *suiClient
	usage                protocol.UsageInfo
	adapterInfo          protocol.AdapterInfo
	adapterCore          protocol.CoreInfo
	operationExecutor    func(context.Context, protocol.NodeOperation) protocol.OperationResultRequest
	realityApplyExecutor func(context.Context, nodeops.RealityApplyRequest) (nodeops.HelperResponse, error)
	realityProbeExecutor func(context.Context, nodeops.RealityProbeRequest) (nodeops.HelperResponse, error)
	dataPlaneMu          sync.Mutex
	// Protected by dataPlaneMu; invalidates an earlier Reality health observation.
	dataPlaneRevision     uint64
	operationCycleRunning atomic.Bool
	operationWG           sync.WaitGroup
}

type serverRejectionError struct {
	status int
	code   string
}

func (err serverRejectionError) Error() string {
	if err.code != "" {
		return "server rejected request: " + err.code
	}
	return fmt.Sprintf("server rejected request with status %d", err.status)
}

func New(cfg config.Agent, logger *slog.Logger) (*Agent, error) {
	if cfg.TelemetryEvery <= 0 {
		cfg.TelemetryEvery = time.Minute
	}
	if cfg.LocalDatabasePath == "" {
		cfg.LocalDatabasePath = filepath.Join(filepath.Dir(cfg.StatePath), "agent.db")
	}
	if cfg.AdapterType == "native_hysteria2" {
		if cfg.AuthListen == "" {
			cfg.AuthListen = "127.0.0.1:18081"
		}
		if cfg.AuthPath == "" {
			cfg.AuthPath = "/hysteria/auth"
		}
		if cfg.AuthCachePath == "" {
			cfg.AuthCachePath = filepath.Join(filepath.Dir(cfg.StatePath), "auth-cache.json")
		}
		if cfg.TrafficStatsURL == "" {
			cfg.TrafficStatsURL = "http://127.0.0.1:18082"
		}
		if cfg.TrafficDatabasePath == "" {
			cfg.TrafficDatabasePath = filepath.Join(filepath.Dir(cfg.StatePath), "agent.db")
		}
		if cfg.TrafficEvery <= 0 {
			cfg.TrafficEvery = 30 * time.Second
		}
	}
	if cfg.AdapterType == "s_ui" {
		if cfg.SUIAPIURL == "" {
			cfg.SUIAPIURL = "http://127.0.0.1:2095/app/apiv2"
		}
		if cfg.TrafficEvery <= 0 {
			cfg.TrafficEvery = 30 * time.Second
		}
	}
	if cfg.AdapterType == "sing_box_vless_reality" && cfg.TrafficEvery <= 0 {
		cfg.TrafficEvery = 30 * time.Second
	}
	state, err := LoadState(cfg.StatePath)
	if err != nil {
		return nil, err
	}
	if err := SaveState(cfg.StatePath, state); err != nil {
		return nil, err
	}
	collector := NewCollector()
	result := &Agent{
		config:    cfg,
		logger:    logger,
		client:    &http.Client{Timeout: 20 * time.Second},
		collector: collector,
		state:     state,
		adapterInfo: protocol.AdapterInfo{
			Name: cfg.AdapterType, Status: "unknown",
		},
		adapterCore: protocol.CoreInfo{Name: cfg.CoreName},
	}
	result.telemetryCollector, _ = collector.(TelemetryCollector)
	local, err := openLocalStore(context.Background(), cfg.LocalDatabasePath)
	if err != nil {
		return nil, err
	}
	result.localStore = local
	if err := result.validatePendingRealityAck(); err != nil {
		_ = local.Close()
		return nil, err
	}
	if cfg.AdapterType == "native_hysteria2" {
		cache, err := LoadAuthCache(cfg.AuthCachePath)
		if err != nil {
			_ = local.Close()
			return nil, err
		}
		cacheNodeID, _, _ := cache.Metadata()
		if state.NodeID != "" && cacheNodeID != "" && state.NodeID != cacheNodeID {
			_ = local.Close()
			return nil, errors.New("native auth cache belongs to another node")
		}
		result.authCache = cache
		result.usage.Enabled = cfg.TrafficStatsSecret != ""
		if cfg.TrafficStatsSecret == "" {
			result.usage.LastErrorCode = "stats_not_configured"
		} else {
			result.statsClient = newHysteriaStatsClient(cfg.TrafficStatsURL, cfg.TrafficStatsSecret)
		}
	}
	if cfg.AdapterType == "s_ui" {
		if state.NodeID != "" {
			if err := local.bindSUIStore(context.Background(), state.NodeID); err != nil {
				_ = local.Close()
				return nil, err
			}
		}
		result.suiClient = newSUIClient(cfg.SUIAPIURL, cfg.SUIToken)
		result.usage.Enabled = cfg.SUIToken != ""
		if cfg.SUIToken == "" {
			result.usage.LastErrorCode = "sui_token_not_configured"
		}
	}
	if cfg.AdapterType == "sing_box_vless_reality" {
		result.usage.Enabled = cfg.RealityAPISecret != ""
		if cfg.RealityAPISecret == "" {
			result.usage.LastErrorCode = "reality_api_secret_not_configured"
		} else {
			result.realityStatsClient = newRealityStatsClient(cfg.RealityAPIURL, cfg.RealityAPISecret)
		}
	}
	return result, nil
}

func (agent *Agent) Close() error {
	if agent.localStore == nil {
		return nil
	}
	store := agent.localStore
	agent.localStore = nil
	return store.Close()
}

func (agent *Agent) Run(ctx context.Context) error {
	defer agent.Close()
	defer agent.operationWG.Wait()
	var authServerErrors <-chan error
	stopAuthServer := func() {}
	if agent.config.AdapterType == "native_hysteria2" {
		var err error
		authServerErrors, stopAuthServer, err = agent.startNativeAuthServer(ctx)
		if err != nil {
			return err
		}
		defer stopAuthServer()
	}
	if agent.state.NodeCredential == "" {
		if agent.config.EnrollmentToken == "" {
			return errors.New("Agent is not enrolled and HYFLEET_ENROLLMENT_TOKEN is empty")
		}
		if err := agent.enrollWithBackoff(ctx); err != nil {
			return err
		}
	}
	agent.logger.Info("Agent started",
		"node_id", agent.state.NodeID,
		"installation_id", agent.state.InstallationID,
		"adapter", agent.config.AdapterType,
	)
	realityAdapter := agent.config.AdapterType == "sing_box_vless_reality"
	if !realityAdapter {
		if err := agent.sendPendingAck(ctx); err != nil {
			agent.logger.Warn("pending desired acknowledgement failed", "error", err)
		}
	}
	if err := agent.runUsageCycle(ctx); err != nil {
		agent.logger.Warn("initial usage cycle failed", "error", err)
	}
	if _, revision, err := agent.heartbeat(ctx); err != nil {
		agent.logger.Warn("initial heartbeat failed", "error", err)
	} else if realityAdapter {
		// Refresh the controller's core state before a persisted Reality ACK can
		// make the endpoint subscription-eligible again after an Agent restart.
		if err := agent.sendPendingRealityAck(ctx, revision); err != nil {
			agent.logger.Warn("pending desired acknowledgement failed", "error", err)
		}
	}
	if err := agent.reportTelemetry(ctx); err != nil {
		agent.logger.Warn("initial telemetry report failed", "error", err)
	}
	if err := agent.pollDesired(ctx); err != nil {
		agent.logger.Warn("initial desired-state poll failed", "error", err)
	}
	agent.startOperationCycle(ctx, "initial")
	heartbeatTimer := time.NewTimer(jitter(agent.config.HeartbeatEvery))
	telemetryTimer := time.NewTimer(jitter(agent.config.TelemetryEvery))
	desiredTimer := time.NewTimer(jitter(agent.config.DesiredEvery))
	trafficTimer := time.NewTimer(jitter(agent.config.TrafficEvery))
	defer heartbeatTimer.Stop()
	defer telemetryTimer.Stop()
	defer desiredTimer.Stop()
	defer trafficTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			if realityAdapter {
				shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				agent.dataPlaneMu.Lock()
				_, _, err := agent.sampleRealityUsage(shutdownCtx, false)
				agent.dataPlaneMu.Unlock()
				cancel()
				if err != nil {
					agent.logger.Warn("final Reality usage sample failed", "error", err)
				}
			}
			return nil
		case err, open := <-authServerErrors:
			if open && err != nil {
				return fmt.Errorf("native Hysteria2 auth server stopped: %w", err)
			}
			authServerErrors = nil
		case <-heartbeatTimer.C:
			if _, revision, err := agent.heartbeat(ctx); err != nil {
				agent.logger.Warn("heartbeat failed", "error", err)
			} else if realityAdapter {
				if err := agent.sendPendingRealityAck(ctx, revision); err != nil {
					agent.logger.Warn("desired acknowledgement retry failed", "error", err)
				}
			}
			heartbeatTimer.Reset(jitter(agent.config.HeartbeatEvery))
		case <-telemetryTimer.C:
			if err := agent.reportTelemetry(ctx); err != nil {
				agent.logger.Warn("telemetry report failed", "error", err)
			}
			telemetryTimer.Reset(jitter(agent.config.TelemetryEvery))
		case <-desiredTimer.C:
			if realityAdapter {
				if err := agent.pollDesired(ctx); err != nil {
					agent.logger.Warn("desired-state poll failed", "error", err)
				}
			} else {
				if err := agent.sendPendingAck(ctx); err != nil {
					agent.logger.Warn("desired acknowledgement retry failed", "error", err)
				} else if err := agent.pollDesired(ctx); err != nil {
					agent.logger.Warn("desired-state poll failed", "error", err)
				}
			}
			agent.startOperationCycle(ctx, "scheduled")
			desiredTimer.Reset(jitter(agent.config.DesiredEvery))
		case <-trafficTimer.C:
			if err := agent.runUsageCycle(ctx); err != nil {
				agent.logger.Warn("usage cycle failed", "error", err)
			}
			trafficTimer.Reset(jitter(agent.config.TrafficEvery))
		}
	}
}

func (agent *Agent) startOperationCycle(ctx context.Context, source string) {
	if !agent.operationCycleRunning.CompareAndSwap(false, true) {
		return
	}
	agent.operationWG.Add(1)
	go func() {
		defer agent.operationWG.Done()
		defer agent.operationCycleRunning.Store(false)
		if err := agent.runOperationCycle(ctx); err != nil && ctx.Err() == nil {
			agent.logger.Warn("operation cycle failed", "source", source, "error", err)
		}
	}()
}

func (agent *Agent) enrollWithBackoff(ctx context.Context) error {
	delay := time.Second
	for {
		if err := agent.enroll(ctx); err == nil {
			return nil
		} else {
			agent.logger.Warn("enrollment attempt failed", "error", err, "retry_in", delay)
		}
		timer := time.NewTimer(jitter(delay))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if delay < 2*time.Minute {
			delay *= 2
			if delay > 2*time.Minute {
				delay = 2 * time.Minute
			}
		}
	}
}

func (agent *Agent) enroll(ctx context.Context) error {
	if agent.state.PendingEnrollmentRequestID == "" {
		agent.state.PendingEnrollmentRequestID = cryptoutil.NewID()
		if err := SaveState(agent.config.StatePath, agent.state); err != nil {
			return err
		}
	}
	facts := agent.collector.Facts()
	request := protocol.EnrollRequest{
		EnrollmentToken: agent.config.EnrollmentToken,
		InstallationID:  agent.state.InstallationID,
		RequestID:       agent.state.PendingEnrollmentRequestID,
		AgentVersion:    buildinfo.Version,
		OS:              facts.OS,
		OSVersion:       facts.OSVersion,
		Architecture:    facts.Architecture,
		Capabilities:    agent.capabilities(),
		Adapter: protocol.EnrollmentAdapter{
			Type:     agent.config.AdapterType,
			CoreName: agent.config.CoreName,
		},
	}
	var result protocol.EnrollResponse
	status, err := agent.doJSON(ctx, http.MethodPost, "/agent/v1/enroll", request,
		agent.state.PendingEnrollmentRequestID, false, &result)
	if err != nil {
		return err
	}
	if status != http.StatusOK || result.Protocol != protocol.MajorVersion ||
		result.NodeID == "" || result.NodeCredential == "" {
		return fmt.Errorf("server returned invalid enrollment response (status %d)", status)
	}
	agent.state.NodeID = result.NodeID
	agent.state.NodeCredential = result.NodeCredential
	agent.state.PendingEnrollmentRequestID = ""
	if agent.config.AdapterType == "s_ui" {
		if err := agent.localStore.bindSUIStore(ctx, agent.state.NodeID); err != nil {
			return err
		}
	}
	return SaveState(agent.config.StatePath, agent.state)
}

func (agent *Agent) heartbeat(ctx context.Context) (int64, uint64, error) {
	metrics, sampleErr := agent.collector.Sample(ctx)
	if sampleErr != nil {
		facts := agent.collector.Facts()
		metrics = protocol.HostMetrics{
			Hostname:      facts.Hostname,
			KernelVersion: facts.KernelVersion,
			CPUCores:      facts.CPUCores,
		}
		agent.logger.Warn("host metrics collection failed; sending heartbeat without fresh metrics", "error", sampleErr)
	}
	usage := agent.usage
	if agent.localStore != nil {
		count, countErr := agent.localStore.trafficOutboxCount(ctx)
		if countErr != nil {
			return 0, 0, countErr
		}
		usage.OutboxBatches = count
	}
	core := protocol.CoreInfo{
		Name:    agent.config.CoreName,
		Running: agent.collector.ServiceRunning(ctx, agent.config.ServiceUnit),
	}
	adapterInfo := agent.adapterInfo
	var dataPlaneRevision uint64
	if agent.config.AdapterType == "native_hysteria2" {
		adapterInfo.Status = "compatible"
		adapterInfo.ErrorCode = ""
	} else if agent.config.AdapterType == "s_ui" {
		core = agent.adapterCore
	} else if agent.config.AdapterType == "sing_box_vless_reality" {
		adapterInfo, core, dataPlaneRevision = agent.probeVLESSReality(ctx, time.Now().UTC())
		agent.adapterInfo = adapterInfo
		agent.adapterCore = core
	}
	request := protocol.HeartbeatRequest{
		InstallationID: agent.state.InstallationID,
		AppliedVersion: agent.state.AppliedVersion,
		Capabilities:   agent.capabilities(),
		Agent: protocol.AgentInfo{
			Version:  buildinfo.Version,
			Protocol: protocol.MajorVersion,
		},
		Core:      core,
		Adapter:   adapterInfo,
		Host:      metrics,
		Usage:     usage,
		SampledAt: time.Now().UTC(),
	}
	var result protocol.HeartbeatResponse
	status, err := agent.doJSON(ctx, http.MethodPost, "/agent/v1/heartbeat", request,
		cryptoutil.NewID(), true, &result)
	if err != nil {
		return 0, dataPlaneRevision, err
	}
	if status != http.StatusOK {
		return 0, dataPlaneRevision, fmt.Errorf("heartbeat returned status %d", status)
	}
	return result.DesiredVersion, dataPlaneRevision, nil
}

func (agent *Agent) reportTelemetry(ctx context.Context) error {
	if agent.telemetryCollector == nil {
		return nil
	}
	request := agent.telemetryCollector.SampleTelemetry(ctx)
	request.InstallationID = agent.state.InstallationID
	if request.SampledAt.IsZero() {
		request.SampledAt = time.Now().UTC()
	}
	if request.Processes == nil {
		request.Processes = []protocol.ProcessTelemetry{}
	}
	if request.Services == nil {
		request.Services = []protocol.ServiceTelemetry{}
	}
	var result protocol.TelemetrySnapshotResponse
	status, err := agent.doJSON(
		ctx, http.MethodPost, "/agent/v1/telemetry", request,
		cryptoutil.NewID(), true, &result,
	)
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		return nil
	}
	if err != nil {
		return err
	}
	if status != http.StatusOK || !result.Accepted {
		return fmt.Errorf("telemetry report returned invalid response (status %d)", status)
	}
	return nil
}

func (agent *Agent) pollDesired(ctx context.Context) error {
	endpoint := "/agent/v1/desired?after=" + strconv.FormatInt(agent.state.AppliedVersion, 10)
	var result protocol.DesiredEnvelope
	status, err := agent.doJSON(ctx, http.MethodGet, endpoint, nil, cryptoutil.NewID(), true, &result)
	if err != nil {
		return err
	}
	if status == http.StatusNoContent {
		return nil
	}
	if status != http.StatusOK {
		return fmt.Errorf("desired-state poll returned status %d", status)
	}
	expectedSchema := 1
	if agent.config.AdapterType == "sing_box_vless_reality" {
		expectedSchema = 2
	}
	if result.Snapshot.NodeID != agent.state.NodeID || result.Snapshot.Adapter != agent.config.AdapterType ||
		result.Snapshot.Version <= agent.state.AppliedVersion ||
		result.Snapshot.SchemaVersion != expectedSchema {
		return errors.New("desired snapshot identity or version is invalid")
	}
	canonical, err := json.Marshal(result.Snapshot)
	if err != nil {
		return fmt.Errorf("encode desired snapshot for verification: %w", err)
	}
	hash := sha256.Sum256(canonical)
	encodedHash := base64.RawURLEncoding.EncodeToString(hash[:])
	if encodedHash != result.SHA256 {
		return errors.New("desired snapshot hash mismatch")
	}
	if agent.config.AdapterType == "native_hysteria2" {
		if err := agent.localStore.queueKicks(ctx, result.Snapshot.Kicks, time.Now().UTC()); err != nil {
			return agent.ackFailed(
				ctx, result, "kick_queue_failed", "native kick queue rejected desired state",
			)
		}
		if err := agent.authCache.Apply(result.Snapshot, result.SHA256, time.Now().UTC()); err != nil {
			agent.logger.Error("apply native auth cache failed", "version", result.Snapshot.Version, "error", err)
			return agent.ackFailed(
				ctx, result, "auth_cache_apply_failed", "native auth cache rejected desired state",
			)
		}
		if err := agent.executePendingKicks(ctx); err != nil {
			agent.logger.Error("apply native kick requests failed", "version", result.Snapshot.Version, "error", err)
			return agent.ackFailed(
				ctx, result, "kick_apply_failed", "native kick requests could not be applied",
			)
		}
	} else if agent.config.AdapterType == "s_ui" {
		if err := agent.applySUIDesired(ctx, result); err != nil {
			code := suiErrorCode(err)
			agent.logger.Error("apply S-UI desired state failed", "version", result.Snapshot.Version, "error_code", code)
			return agent.ackFailed(ctx, result, code, "S-UI reconciliation rejected desired state")
		}
	} else if agent.config.AdapterType == "sing_box_vless_reality" {
		revision, err := agent.applyAndPersistVLESSRealityDesired(ctx, result)
		if err != nil {
			code := realityApplyErrorCode(err)
			agent.logger.Error("apply VLESS Reality desired state failed",
				"version", result.Snapshot.Version, "error_code", code)
			return agent.ackFailed(ctx, result, code, "VLESS Reality reconciliation rejected desired state")
		}
		return agent.sendPendingRealityAck(ctx, revision)
	} else if len(result.Snapshot.Users) != 0 {
		return agent.ackFailed(
			ctx, result, "adapter_users_unsupported", "this adapter cannot apply users in Phase 2",
		)
	}
	agent.state.AppliedVersion = result.Snapshot.Version
	agent.state.AppliedSnapshotHash = result.SHA256
	agent.state.PendingAckVersion = result.Snapshot.Version
	agent.state.PendingAckHash = result.SHA256
	if err := SaveState(agent.config.StatePath, agent.state); err != nil {
		return err
	}
	return agent.sendPendingAck(ctx)
}

func (agent *Agent) capabilities() []string {
	capabilities := []string{
		"host_metrics", "desired_state_v1", "node_operations_v1",
		"operation_result_outbox_v1", "runtime_telemetry_v1",
	}
	if agent.config.AdapterType == "native_hysteria2" {
		return append(capabilities, "native_http_auth", "persistent_auth_cache",
			"traffic_stats_v1", "traffic_outbox_v1", "online_snapshot_v1", "kick_generation_v1")
	}
	if agent.config.AdapterType == "s_ui" {
		return append(capabilities, "sui_apiv2_v1", "sui_discovery_v1",
			"sui_ownership_v1", "credential_material_v1", "traffic_stats_v1",
			"traffic_outbox_v1", "online_snapshot_v1")
	}
	if agent.config.AdapterType == "sing_box_vless_reality" {
		capabilities = append(capabilities, "desired_state_v2", "credential_material_v1",
			"sing_box_vless_reality")
		if agent.realityStatsClient != nil {
			capabilities = append(capabilities, "traffic_stats_v1", "traffic_outbox_v1",
				"online_snapshot_v1", "kick_generation_v1", "reality_user_control_v1")
		}
		return capabilities
	}
	return append(capabilities, "read_only_adapter")
}

func (agent *Agent) runUsageCycle(ctx context.Context) error {
	if agent.localStore == nil {
		return nil
	}
	if agent.config.AdapterType == "sing_box_vless_reality" {
		return agent.runRealityUsageCycle(ctx)
	}
	if agent.config.AdapterType == "s_ui" {
		return agent.runSUIUsageCycle(ctx)
	}
	var cycleErrors []error
	if agent.statsClient != nil {
		counters, err := agent.statsClient.traffic(ctx)
		if err != nil {
			agent.usage.Available = false
			agent.usage.LastErrorCode = "traffic_api_unavailable"
			cycleErrors = append(cycleErrors, err)
		} else {
			sampledAt := time.Now().UTC()
			if _, err := agent.localStore.recordTrafficSample(
				ctx, agent.state.InstallationID, counters, sampledAt,
			); err != nil {
				agent.usage.Available = false
				agent.usage.LastErrorCode = "traffic_store_failed"
				cycleErrors = append(cycleErrors, err)
			} else {
				agent.usage.Available = true
				agent.usage.LastErrorCode = ""
				agent.usage.LastSampledAt = &sampledAt
			}
		}
		if err := agent.reportOnline(ctx); err != nil {
			cycleErrors = append(cycleErrors, err)
		}
	}
	if err := agent.flushTrafficOutbox(ctx); err != nil {
		cycleErrors = append(cycleErrors, err)
	}
	if err := agent.executePendingKicks(ctx); err != nil {
		cycleErrors = append(cycleErrors, err)
	}
	return errors.Join(cycleErrors...)
}

func (agent *Agent) flushTrafficOutbox(ctx context.Context) error {
	batches, err := agent.localStore.listTrafficOutbox(ctx, 20)
	if err != nil {
		return err
	}
	for _, batch := range batches {
		var result protocol.TrafficBatchesResponse
		status, err := agent.doJSON(ctx, http.MethodPost, "/agent/v1/traffic-batches",
			protocol.TrafficBatchesRequest{Batches: []protocol.TrafficBatch{batch}},
			cryptoutil.NewID(), true, &result)
		if err != nil {
			_ = agent.localStore.recordTrafficFailure(ctx, batch.ID, "transport_error", time.Now().UTC())
			return err
		}
		if status != http.StatusOK || len(result.Results) != 1 || result.Results[0].ID != batch.ID {
			_ = agent.localStore.recordTrafficFailure(ctx, batch.ID, "invalid_response", time.Now().UTC())
			return errors.New("traffic batch endpoint returned an invalid response")
		}
		batchResult := result.Results[0]
		switch batchResult.Status {
		case "accepted", "duplicate":
			if err := agent.localStore.deleteTrafficOutbox(ctx, batch.ID); err != nil {
				return err
			}
		case "rejected":
			code := batchResult.ErrorCode
			if code == "" {
				code = "rejected"
			}
			_ = agent.localStore.recordTrafficFailure(ctx, batch.ID, code, time.Now().UTC())
			return fmt.Errorf("traffic batch rejected: %s", code)
		default:
			_ = agent.localStore.recordTrafficFailure(ctx, batch.ID, "invalid_status", time.Now().UTC())
			return errors.New("traffic batch endpoint returned an unknown status")
		}
	}
	return nil
}

func (agent *Agent) reportOnline(ctx context.Context) error {
	users, err := agent.statsClient.online(ctx)
	if err != nil {
		return err
	}
	return agent.postOnlineSnapshot(ctx, users, time.Now().UTC())
}

func (agent *Agent) executePendingKicks(ctx context.Context) error {
	kicks, err := agent.localStore.listPendingKicks(ctx, 256)
	if err != nil || len(kicks) == 0 {
		return err
	}
	if agent.statsClient == nil {
		return errors.New("Hysteria Traffic Stats secret is not configured")
	}
	userIDs := make([]string, 0, len(kicks))
	for _, kick := range kicks {
		userIDs = append(userIDs, kick.UserID)
	}
	if err := agent.statsClient.kick(ctx, userIDs); err != nil {
		_ = agent.localStore.recordKickFailure(ctx, kicks, "kick_api_unavailable", time.Now().UTC())
		return err
	}
	return agent.localStore.markKicksApplied(ctx, kicks, time.Now().UTC())
}

func (agent *Agent) ackFailed(ctx context.Context, desired protocol.DesiredEnvelope, code, message string) error {
	request := protocol.DesiredAckRequest{
		Status:       "failed",
		SnapshotHash: desired.SHA256,
		Adapter:      agent.config.AdapterType,
		ErrorCode:    code,
		Message:      message,
	}
	status, err := agent.doJSON(ctx, http.MethodPost,
		"/agent/v1/desired/"+strconv.FormatInt(desired.Snapshot.Version, 10)+"/ack",
		request, cryptoutil.NewID(), true, nil)
	if err != nil {
		return err
	}
	if status != http.StatusNoContent {
		return fmt.Errorf("failed acknowledgement returned status %d", status)
	}
	return nil
}

func (agent *Agent) sendPendingAck(ctx context.Context) error {
	if agent.state.PendingAckVersion == 0 {
		return nil
	}
	pendingVersion := agent.state.PendingAckVersion
	request := protocol.DesiredAckRequest{
		Status:       "applied",
		SnapshotHash: agent.state.PendingAckHash,
		Adapter:      agent.config.AdapterType,
		Reality:      agent.state.PendingAckReality,
	}
	status, err := agent.doJSON(ctx, http.MethodPost,
		"/agent/v1/desired/"+strconv.FormatInt(pendingVersion, 10)+"/ack",
		request, cryptoutil.NewID(), true, nil)
	var rejection serverRejectionError
	if status == http.StatusConflict && errors.As(err, &rejection) &&
		rejection.code == "desired_version_conflict" {
		superseded, confirmationErr := agent.pendingAckIsSuperseded(ctx, pendingVersion)
		if confirmationErr != nil {
			return fmt.Errorf("confirm pending acknowledgement is stale: %w", confirmationErr)
		}
		if !superseded {
			return err
		}
		return agent.clearPendingAck()
	}
	if err != nil {
		return err
	}
	if status != http.StatusNoContent {
		return fmt.Errorf("desired acknowledgement returned status %d", status)
	}
	return agent.clearPendingAck()
}

func (agent *Agent) pendingAckIsSuperseded(ctx context.Context, pendingVersion int64) (bool, error) {
	endpoint := "/agent/v1/desired?after=" + strconv.FormatInt(pendingVersion, 10)
	var result protocol.DesiredEnvelope
	status, err := agent.doJSON(ctx, http.MethodGet, endpoint, nil, cryptoutil.NewID(), true, &result)
	if err != nil {
		return false, err
	}
	if status == http.StatusNoContent {
		return false, nil
	}
	if status != http.StatusOK {
		return false, fmt.Errorf("desired-state confirmation returned status %d", status)
	}
	if result.Snapshot.NodeID != agent.state.NodeID ||
		result.Snapshot.Adapter != agent.config.AdapterType {
		return false, errors.New("desired-state confirmation identity is invalid")
	}
	return result.Snapshot.Version > pendingVersion, nil
}

func (agent *Agent) clearPendingAck() error {
	previousState := agent.state
	agent.state.PendingAckVersion = 0
	agent.state.PendingAckHash = ""
	agent.state.PendingAckReality = nil
	if err := SaveState(agent.config.StatePath, agent.state); err != nil {
		agent.state = previousState
		return err
	}
	return nil
}

func (agent *Agent) doJSON(
	ctx context.Context,
	method, endpoint string,
	body any,
	requestID string,
	authenticated bool,
	destination any,
) (int, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	base, err := url.Parse(agent.config.ServerURL)
	if err != nil {
		return 0, err
	}
	relative, err := url.Parse(endpoint)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, method, base.ResolveReference(relative).String(), reader)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-HyFleet-Protocol", strconv.Itoa(protocol.MajorVersion))
	request.Header.Set("X-HyFleet-Agent", buildinfo.Version)
	request.Header.Set("X-Request-ID", requestID)
	request.Header.Set("User-Agent", "hyfleet-agent/"+buildinfo.Version)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		request.Header.Set("Authorization", "Bearer "+agent.state.NodeCredential)
	}
	response, err := agent.client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("send request: %w", err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, 2*1024*1024)
	if response.StatusCode >= 400 {
		var apiError protocol.ErrorResponse
		if err := json.NewDecoder(limited).Decode(&apiError); err == nil && apiError.Error.Code != "" {
			return response.StatusCode, serverRejectionError{
				status: response.StatusCode,
				code:   apiError.Error.Code,
			}
		}
		return response.StatusCode, serverRejectionError{status: response.StatusCode}
	}
	if destination != nil && response.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(limited).Decode(destination); err != nil {
			return response.StatusCode, fmt.Errorf("decode response: %w", err)
		}
	}
	return response.StatusCode, nil
}

func jitter(base time.Duration) time.Duration {
	if base <= 0 {
		return time.Second
	}
	spread := base / 10
	if spread <= 0 {
		return base
	}
	return base - spread + time.Duration(rand.Int64N(int64(spread*2)+1))
}
