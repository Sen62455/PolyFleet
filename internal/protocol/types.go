package protocol

import "time"

const (
	MajorVersion            = 1
	MaxTrafficItemsPerBatch = 1000
	MaxTelemetryProcesses   = 16
	MaxTelemetryServices    = 128
)

type ErrorResponse struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

type EnrollRequest struct {
	EnrollmentToken string            `json:"enrollment_token"`
	InstallationID  string            `json:"installation_id"`
	RequestID       string            `json:"request_id"`
	AgentVersion    string            `json:"agent_version"`
	OS              string            `json:"os"`
	OSVersion       string            `json:"os_version"`
	Architecture    string            `json:"architecture"`
	Capabilities    []string          `json:"capabilities"`
	Adapter         EnrollmentAdapter `json:"adapter"`
}

type EnrollmentAdapter struct {
	Type     string `json:"type"`
	CoreName string `json:"core_name,omitempty"`
}

type EnrollResponse struct {
	NodeID         string        `json:"node_id"`
	NodeCredential string        `json:"node_credential"`
	Protocol       int           `json:"protocol"`
	Polling        PollingPolicy `json:"polling"`
	ServerTime     time.Time     `json:"server_time"`
}

type PollingPolicy struct {
	HeartbeatSeconds int `json:"heartbeat_seconds"`
	DesiredSeconds   int `json:"desired_seconds"`
}

type HeartbeatRequest struct {
	InstallationID string      `json:"installation_id"`
	AppliedVersion int64       `json:"applied_version"`
	Capabilities   []string    `json:"capabilities"`
	Agent          AgentInfo   `json:"agent"`
	Core           CoreInfo    `json:"core"`
	Adapter        AdapterInfo `json:"adapter"`
	Host           HostMetrics `json:"host"`
	Usage          UsageInfo   `json:"usage"`
	SampledAt      time.Time   `json:"sampled_at"`
}

type AgentInfo struct {
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
}

type CoreInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Running bool   `json:"running"`
}

type AdapterInfo struct {
	Name         string     `json:"name"`
	Version      string     `json:"version,omitempty"`
	Status       string     `json:"status"`
	ErrorCode    string     `json:"error_code,omitempty"`
	LastProbedAt *time.Time `json:"last_probed_at,omitempty"`
}

type UsageInfo struct {
	Enabled       bool       `json:"enabled"`
	Available     bool       `json:"available"`
	OutboxBatches int        `json:"outbox_batches"`
	LastSampledAt *time.Time `json:"last_sampled_at,omitempty"`
	LastErrorCode string     `json:"last_error_code,omitempty"`
}

type HostMetrics struct {
	Hostname                string  `json:"hostname,omitempty"`
	KernelVersion           string  `json:"kernel_version,omitempty"`
	UptimeSeconds           int64   `json:"uptime_seconds"`
	CPUCores                int     `json:"cpu_cores"`
	CPUPercent              float64 `json:"cpu_percent"`
	MemoryUsedBytes         int64   `json:"memory_used_bytes"`
	MemoryTotalBytes        int64   `json:"memory_total_bytes"`
	SwapUsedBytes           int64   `json:"swap_used_bytes"`
	SwapTotalBytes          int64   `json:"swap_total_bytes"`
	DiskUsedBytes           int64   `json:"disk_used_bytes"`
	DiskTotalBytes          int64   `json:"disk_total_bytes"`
	DiskReadBytesPerSecond  int64   `json:"disk_read_bytes_per_second"`
	DiskWriteBytesPerSecond int64   `json:"disk_write_bytes_per_second"`
	NetworkRXBPS            int64   `json:"network_rx_bps"`
	NetworkTXBPS            int64   `json:"network_tx_bps"`
	NetworkRXBytesTotal     int64   `json:"network_rx_bytes_total"`
	NetworkTXBytesTotal     int64   `json:"network_tx_bytes_total"`
	Load1                   float64 `json:"load_1"`
	Load5                   float64 `json:"load_5"`
	Load15                  float64 `json:"load_15"`
}

type HeartbeatResponse struct {
	ServerTime     time.Time `json:"server_time"`
	DesiredVersion int64     `json:"desired_version"`
}

type TelemetrySnapshotRequest struct {
	InstallationID     string             `json:"installation_id"`
	SampledAt          time.Time          `json:"sampled_at"`
	ProcessesAvailable bool               `json:"processes_available"`
	ProcessesErrorCode string             `json:"processes_error_code,omitempty"`
	ProcessesTotal     int                `json:"processes_total"`
	ProcessesTruncated bool               `json:"processes_truncated"`
	Processes          []ProcessTelemetry `json:"processes"`
	ServicesAvailable  bool               `json:"services_available"`
	ServicesErrorCode  string             `json:"services_error_code,omitempty"`
	ServicesTotal      int                `json:"services_total"`
	ServicesTruncated  bool               `json:"services_truncated"`
	Services           []ServiceTelemetry `json:"services"`
}

type ProcessTelemetry struct {
	PID           int     `json:"pid"`
	Name          string  `json:"name"`
	Unit          string  `json:"unit,omitempty"`
	CPUPercent    float64 `json:"cpu_percent"`
	RSSBytes      int64   `json:"rss_bytes"`
	UptimeSeconds int64   `json:"uptime_seconds"`
}

type ServiceTelemetry struct {
	Unit            string  `json:"unit"`
	Description     string  `json:"description,omitempty"`
	ActiveState     string  `json:"active_state"`
	SubState        string  `json:"sub_state"`
	CPUPercent      float64 `json:"cpu_percent"`
	CPUPeakPercent  float64 `json:"cpu_peak_percent"`
	MemoryBytes     int64   `json:"memory_bytes"`
	MemoryPeakBytes int64   `json:"memory_peak_bytes"`
	Tasks           int64   `json:"tasks"`
	Restarts        int64   `json:"restarts"`
	MainPID         int     `json:"main_pid"`
}

type TelemetrySnapshotResponse struct {
	Accepted   bool      `json:"accepted"`
	ServerTime time.Time `json:"server_time"`
}

type DesiredSnapshot struct {
	SchemaVersion int                  `json:"schema_version"`
	NodeID        string               `json:"node_id"`
	Version       int64                `json:"version"`
	Adapter       string               `json:"adapter"`
	Users         []DesiredUser        `json:"users"`
	Kicks         []DesiredKick        `json:"kicks"`
	SUI           *DesiredSUI          `json:"s_ui,omitempty"`
	VLESSReality  *DesiredVLESSReality `json:"vless_reality,omitempty"`
	GeneratedAt   time.Time            `json:"generated_at"`
}

type DesiredSUI struct {
	TargetInboundIDs []int64 `json:"target_inbound_ids"`
}

type DesiredVLESSReality struct {
	ListenPort          int    `json:"listen_port"`
	ServerName          string `json:"server_name"`
	HandshakeServer     string `json:"handshake_server"`
	HandshakeServerPort int    `json:"handshake_server_port"`
	Flow                string `json:"flow"`
	Network             string `json:"network"`
	KeyGeneration       int64  `json:"key_generation"`
}

type DesiredUser struct {
	ID             string            `json:"id"`
	Username       string            `json:"username"`
	Credential     DesiredCredential `json:"credential"`
	Enabled        bool              `json:"enabled"`
	ExpiresAt      *time.Time        `json:"expires_at"`
	QuotaState     string            `json:"quota_state"`
	ManagementMode string            `json:"management_mode,omitempty"`
	RemoteClientID int64             `json:"remote_client_id,omitempty"`
}

type DesiredCredential struct {
	Ref            string `json:"ref"`
	Fingerprint    string `json:"fingerprint"`
	Protocol       string `json:"protocol,omitempty"`
	VerifierSHA256 string `json:"verifier_sha256,omitempty"`
}

type DesiredKick struct {
	UserID     string `json:"user_id"`
	Generation int64  `json:"generation"`
}

type DesiredEnvelope struct {
	Snapshot  DesiredSnapshot `json:"snapshot"`
	SHA256    string          `json:"sha256"`
	CreatedAt time.Time       `json:"created_at"`
}

type DesiredAckRequest struct {
	Status       string                  `json:"status"`
	SnapshotHash string                  `json:"snapshot_hash"`
	Adapter      string                  `json:"adapter"`
	DurationMS   int64                   `json:"duration_ms"`
	ErrorCode    string                  `json:"error_code,omitempty"`
	Message      string                  `json:"message,omitempty"`
	Reality      *AppliedRealityMaterial `json:"reality,omitempty"`
}

type AppliedRealityMaterial struct {
	KeyGeneration int64  `json:"key_generation"`
	PublicKey     string `json:"public_key"`
	ShortID       string `json:"short_id"`
}

type TrafficBatchesRequest struct {
	Batches []TrafficBatch `json:"batches"`
}

type TrafficBatch struct {
	ID             string         `json:"id"`
	InstallationID string         `json:"installation_id"`
	SourceEpoch    string         `json:"source_epoch"`
	Sequence       int64          `json:"sequence"`
	SampledAt      time.Time      `json:"sampled_at"`
	Items          []TrafficDelta `json:"items"`
}

type TrafficDelta struct {
	UserID        string `json:"user_id"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
}

type TrafficBatchesResponse struct {
	Results    []TrafficBatchResult `json:"results"`
	ServerTime time.Time            `json:"server_time"`
}

type TrafficBatchResult struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	ErrorCode string `json:"error_code,omitempty"`
}

type OnlineSnapshotRequest struct {
	SnapshotID     string       `json:"snapshot_id"`
	InstallationID string       `json:"installation_id"`
	SampledAt      time.Time    `json:"sampled_at"`
	Users          []OnlineUser `json:"users"`
}

type OnlineUser struct {
	UserID      string `json:"user_id"`
	Connections int    `json:"connections"`
}

type OnlineSnapshotResponse struct {
	Accepted   bool      `json:"accepted"`
	ServerTime time.Time `json:"server_time"`
}

type CredentialMaterialRequest struct {
	CredentialRef  string `json:"credential_ref"`
	DesiredVersion int64  `json:"desired_version"`
	SnapshotSHA256 string `json:"snapshot_sha256"`
}

type CredentialMaterialResponse struct {
	CredentialRef string `json:"credential_ref"`
	Secret        string `json:"secret"`
}

type SUIReportRequest struct {
	InstallationID string                 `json:"installation_id"`
	Adapter        AdapterInfo            `json:"adapter"`
	Core           CoreInfo               `json:"core"`
	Inbounds       []SUIDiscoveredInbound `json:"inbounds"`
	Clients        []SUIDiscoveredClient  `json:"clients"`
	SampledAt      time.Time              `json:"sampled_at"`
}

type SUIDiscoveredInbound struct {
	RemoteID   int64  `json:"remote_id"`
	Tag        string `json:"tag"`
	Type       string `json:"type"`
	Listen     string `json:"listen,omitempty"`
	ListenPort int    `json:"listen_port"`
}

type SUIDiscoveredClient struct {
	RemoteID              int64   `json:"remote_id"`
	Name                  string  `json:"name"`
	Enabled               bool    `json:"enabled"`
	InboundIDs            []int64 `json:"inbound_ids"`
	UploadBytes           int64   `json:"upload_bytes"`
	DownloadBytes         int64   `json:"download_bytes"`
	ExpiresAt             int64   `json:"expires_at"`
	Online                bool    `json:"online"`
	Group                 string  `json:"group,omitempty"`
	Description           string  `json:"description,omitempty"`
	MappedUserID          string  `json:"mapped_user_id,omitempty"`
	ManagementMode        string  `json:"management_mode,omitempty"`
	CredentialFingerprint string  `json:"credential_fingerprint,omitempty"`
}

type SUIReportResponse struct {
	Accepted   bool      `json:"accepted"`
	ServerTime time.Time `json:"server_time"`
}

type NodeOperation struct {
	ID        string    `json:"id"`
	Sequence  int64     `json:"sequence"`
	Type      string    `json:"type"`
	MaxLines  int       `json:"max_lines,omitempty"`
	Target    string    `json:"target,omitempty"`
	Attempt   int       `json:"attempt"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type NodeOperationsResponse struct {
	Operations []NodeOperation `json:"operations"`
	ServerTime time.Time       `json:"server_time"`
}

type OperationResultRequest struct {
	Sequence     int64     `json:"sequence"`
	Status       string    `json:"status"`
	Output       string    `json:"output,omitempty"`
	ErrorCode    string    `json:"error_code,omitempty"`
	ErrorMessage string    `json:"error_message,omitempty"`
	RolledBack   bool      `json:"rolled_back"`
	Backup       *Backup   `json:"backup,omitempty"`
	CompletedAt  time.Time `json:"completed_at"`
}

type Backup struct {
	LocalPath string `json:"local_path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}
