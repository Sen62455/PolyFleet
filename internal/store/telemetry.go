package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Sen62455/PolyFleet/internal/protocol"
)

type NodeTelemetry struct {
	Supported          bool
	SampledAt          *time.Time
	ProcessesAvailable bool
	ProcessesErrorCode string
	ProcessesSampledAt *time.Time
	ProcessesTotal     int
	ProcessesTruncated bool
	Processes          []protocol.ProcessTelemetry
	ServicesAvailable  bool
	ServicesErrorCode  string
	ServicesSampledAt  *time.Time
	ServicesTotal      int
	ServicesTruncated  bool
	Services           []protocol.ServiceTelemetry
}

func (s *Store) RecordNodeTelemetry(
	ctx context.Context,
	identity AgentIdentity,
	snapshot protocol.TelemetrySnapshotRequest,
	now time.Time,
) error {
	if identity.InstallationID != snapshot.InstallationID {
		return ErrConflict
	}
	processesJSON, err := json.Marshal(snapshot.Processes)
	if err != nil {
		return fmt.Errorf("encode process telemetry: %w", err)
	}
	servicesJSON, err := json.Marshal(snapshot.Services)
	if err != nil {
		return fmt.Errorf("encode service telemetry: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin telemetry snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var installationID string
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(agent_installation_id, '')
		FROM nodes WHERE id = ? AND archived_at IS NULL
	`, identity.NodeID).Scan(&installationID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("find telemetry node: %w", err)
	}
	if installationID != identity.InstallationID {
		return ErrConflict
	}
	var processSampledAt any
	if snapshot.ProcessesAvailable {
		processSampledAt = snapshot.SampledAt.UnixMilli()
	}
	var serviceSampledAt any
	if snapshot.ServicesAvailable {
		serviceSampledAt = snapshot.SampledAt.UnixMilli()
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO node_telemetry_snapshots(
			node_id, sampled_at,
			processes_available, processes_error_code, processes_total,
			processes_truncated, processes_sampled_at, processes_json,
			services_available, services_error_code, services_total,
			services_truncated, services_sampled_at, services_json, received_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			sampled_at = excluded.sampled_at,
			processes_available = excluded.processes_available,
			processes_error_code = excluded.processes_error_code,
			processes_total = CASE WHEN excluded.processes_available = 1
				THEN excluded.processes_total ELSE node_telemetry_snapshots.processes_total END,
			processes_truncated = CASE WHEN excluded.processes_available = 1
				THEN excluded.processes_truncated ELSE node_telemetry_snapshots.processes_truncated END,
			processes_sampled_at = CASE WHEN excluded.processes_available = 1
				THEN excluded.processes_sampled_at ELSE node_telemetry_snapshots.processes_sampled_at END,
			processes_json = CASE WHEN excluded.processes_available = 1
				THEN excluded.processes_json ELSE node_telemetry_snapshots.processes_json END,
			services_available = excluded.services_available,
			services_error_code = excluded.services_error_code,
			services_total = CASE WHEN excluded.services_available = 1
				THEN excluded.services_total ELSE node_telemetry_snapshots.services_total END,
			services_truncated = CASE WHEN excluded.services_available = 1
				THEN excluded.services_truncated ELSE node_telemetry_snapshots.services_truncated END,
			services_sampled_at = CASE WHEN excluded.services_available = 1
				THEN excluded.services_sampled_at ELSE node_telemetry_snapshots.services_sampled_at END,
			services_json = CASE WHEN excluded.services_available = 1
				THEN excluded.services_json ELSE node_telemetry_snapshots.services_json END,
			received_at = excluded.received_at
		WHERE excluded.sampled_at >= node_telemetry_snapshots.sampled_at
	`, identity.NodeID, snapshot.SampledAt.UnixMilli(), boolInt(snapshot.ProcessesAvailable),
		snapshot.ProcessesErrorCode, snapshot.ProcessesTotal, boolInt(snapshot.ProcessesTruncated),
		processSampledAt, processesJSON, boolInt(snapshot.ServicesAvailable),
		snapshot.ServicesErrorCode, snapshot.ServicesTotal, boolInt(snapshot.ServicesTruncated),
		serviceSampledAt, servicesJSON, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("upsert telemetry snapshot: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit telemetry snapshot: %w", err)
	}
	return nil
}

func (s *Store) GetNodeTelemetry(ctx context.Context, nodeID string) (NodeTelemetry, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM nodes WHERE id = ? AND archived_at IS NULL
	`, nodeID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return NodeTelemetry{}, ErrNotFound
	} else if err != nil {
		return NodeTelemetry{}, fmt.Errorf("find telemetry node: %w", err)
	}
	var result NodeTelemetry
	var sampledAt int64
	var processesSampledAt, servicesSampledAt sql.NullInt64
	var processesAvailable, processesTruncated int
	var servicesAvailable, servicesTruncated int
	var processesJSON, servicesJSON []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT sampled_at, processes_available, processes_error_code,
		       processes_total, processes_truncated, processes_sampled_at, processes_json,
		       services_available, services_error_code,
		       services_total, services_truncated, services_sampled_at, services_json
		FROM node_telemetry_snapshots WHERE node_id = ?
	`, nodeID).Scan(
		&sampledAt, &processesAvailable, &result.ProcessesErrorCode,
		&result.ProcessesTotal, &processesTruncated, &processesSampledAt, &processesJSON,
		&servicesAvailable, &result.ServicesErrorCode,
		&result.ServicesTotal, &servicesTruncated, &servicesSampledAt, &servicesJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		result.Processes = []protocol.ProcessTelemetry{}
		result.Services = []protocol.ServiceTelemetry{}
		return result, nil
	}
	if err != nil {
		return NodeTelemetry{}, fmt.Errorf("read telemetry snapshot: %w", err)
	}
	result.Supported = true
	sampled := unixTime(sampledAt)
	result.SampledAt = &sampled
	result.ProcessesAvailable = processesAvailable == 1
	result.ProcessesSampledAt = nullableTime(processesSampledAt)
	result.ProcessesTruncated = processesTruncated == 1
	result.ServicesAvailable = servicesAvailable == 1
	result.ServicesSampledAt = nullableTime(servicesSampledAt)
	result.ServicesTruncated = servicesTruncated == 1
	if err := json.Unmarshal(processesJSON, &result.Processes); err != nil {
		return NodeTelemetry{}, fmt.Errorf("decode process telemetry: %w", err)
	}
	if err := json.Unmarshal(servicesJSON, &result.Services); err != nil {
		return NodeTelemetry{}, fmt.Errorf("decode service telemetry: %w", err)
	}
	if result.Processes == nil {
		result.Processes = []protocol.ProcessTelemetry{}
	}
	if result.Services == nil {
		result.Services = []protocol.ServiceTelemetry{}
	}
	return result, nil
}
