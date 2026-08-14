package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/protocol"
)

type State struct {
	InstallationID             string                           `json:"installation_id"`
	NodeID                     string                           `json:"node_id,omitempty"`
	NodeCredential             string                           `json:"node_credential,omitempty"`
	AppliedVersion             int64                            `json:"applied_version"`
	AppliedSnapshotHash        string                           `json:"applied_snapshot_hash,omitempty"`
	PendingEnrollmentRequestID string                           `json:"pending_enrollment_request_id,omitempty"`
	PendingAckVersion          int64                            `json:"pending_ack_version,omitempty"`
	PendingAckHash             string                           `json:"pending_ack_hash,omitempty"`
	PendingAckReality          *protocol.AppliedRealityMaterial `json:"pending_ack_reality,omitempty"`
}

func LoadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{InstallationID: cryptoutil.NewID()}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read Agent state: %w", err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("parse Agent state: %w", err)
	}
	if state.InstallationID == "" {
		return State{}, errors.New("Agent state has no installation ID")
	}
	return state, nil
}

func SaveState(path string, state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Agent state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create Agent state directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".agent-state-*")
	if err != nil {
		return fmt.Errorf("create temporary Agent state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set Agent state permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write Agent state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync Agent state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Agent state: %w", err)
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return fmt.Errorf("replace Agent state: %w", err)
	}
	return nil
}
