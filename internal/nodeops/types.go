package nodeops

import (
	"time"

	"github.com/Sen62455/PolyFleet/internal/protocol"
)

type HelperRequest struct {
	Operation    *protocol.NodeOperation `json:"operation,omitempty"`
	RealityApply *RealityApplyRequest    `json:"reality_apply,omitempty"`
	RealityProbe *RealityProbeRequest    `json:"reality_probe,omitempty"`
}

type HelperResponse struct {
	Sequence       int64                            `json:"sequence"`
	Status         string                           `json:"status"`
	Output         string                           `json:"output,omitempty"`
	ErrorCode      string                           `json:"error_code,omitempty"`
	ErrorMessage   string                           `json:"error_message,omitempty"`
	RolledBack     bool                             `json:"rolled_back"`
	Backup         *protocol.Backup                 `json:"backup,omitempty"`
	AppliedVersion int64                            `json:"applied_version,omitempty"`
	SnapshotSHA256 string                           `json:"snapshot_sha256,omitempty"`
	Reality        *protocol.AppliedRealityMaterial `json:"reality,omitempty"`
	RealityProbe   *RealityProbeResult              `json:"reality_probe,omitempty"`
	CompletedAt    time.Time                        `json:"completed_at"`
}

// RealityProbeRequest intentionally has no caller-controlled fields. The root
// helper probes only the adapter contract fixed by its startup configuration.
type RealityProbeRequest struct{}

type RealityProbeResult struct {
	AdapterStatus    string    `json:"adapter_status"`
	AdapterVersion   string    `json:"adapter_version,omitempty"`
	AdapterErrorCode string    `json:"adapter_error_code,omitempty"`
	CoreVersion      string    `json:"core_version,omitempty"`
	CoreRunning      bool      `json:"core_running"`
	ProbedAt         time.Time `json:"probed_at"`
}

type RealityApplyRequest struct {
	RequestID      string               `json:"request_id"`
	NodeID         string               `json:"node_id"`
	Version        int64                `json:"version"`
	SnapshotSHA256 string               `json:"snapshot_sha256"`
	Settings       RealityApplySettings `json:"settings"`
	Users          []RealityApplyUser   `json:"users"`
}

type RealityApplySettings struct {
	ListenPort          int    `json:"listen_port"`
	ServerName          string `json:"server_name"`
	HandshakeServer     string `json:"handshake_server"`
	HandshakeServerPort int    `json:"handshake_server_port"`
	Flow                string `json:"flow"`
	Network             string `json:"network"`
	KeyGeneration       int64  `json:"key_generation"`
	APISecret           string `json:"api_secret"`
}

type RealityApplyUser struct {
	UserID string `json:"user_id"`
	UUID   string `json:"uuid"`
}

func (response HelperResponse) ProtocolResult() protocol.OperationResultRequest {
	return protocol.OperationResultRequest{
		Sequence: response.Sequence, Status: response.Status,
		Output: response.Output, ErrorCode: response.ErrorCode,
		ErrorMessage: response.ErrorMessage, RolledBack: response.RolledBack,
		Backup: response.Backup, CompletedAt: response.CompletedAt,
	}
}
