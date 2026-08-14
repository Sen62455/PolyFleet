package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/google/uuid"
)

func TestSUIReadOnlyImportAdoptionAndCredentialBinding(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	masterKey := bytes.Repeat([]byte{0x52}, 32)
	node := createTestNode(t, database, "sui-node", "s_ui", now)
	installationID := uuid.NewString()
	if _, err := database.DB().ExecContext(ctx, `
		UPDATE nodes SET agent_installation_id = ? WHERE id = ?
	`, installationID, node.ID); err != nil {
		t.Fatalf("bind S-UI Agent: %v", err)
	}
	identity := AgentIdentity{
		NodeID: node.ID, InstallationID: installationID, AdapterType: "s_ui", Enabled: true,
	}
	probedAt := now.Add(time.Second)
	sentinel := "sentinel-secret-must-not-persist"
	report := protocol.SUIReportRequest{
		InstallationID: installationID,
		Adapter: protocol.AdapterInfo{
			Name: "s_ui", Version: "v1.5.3", Status: "compatible", LastProbedAt: &probedAt,
		},
		Core: protocol.CoreInfo{Name: "sing-box", Running: true},
		Inbounds: []protocol.SUIDiscoveredInbound{{
			RemoteID: 7, Tag: "hy2-in", Type: "hysteria2", Listen: "::", ListenPort: 443,
		}},
		Clients: []protocol.SUIDiscoveredClient{{
			RemoteID: 41, Name: "existing-client", Enabled: true, InboundIDs: []int64{7},
			UploadBytes: 123, DownloadBytes: 456, Description: sentinel,
		}},
		SampledAt: probedAt,
	}
	if err := database.RecordSUIReport(ctx, identity, report, probedAt); err != nil {
		t.Fatalf("RecordSUIReport() error = %v", err)
	}
	state, err := database.GetSUIState(ctx, node.ID)
	if err != nil || len(state.Inbounds) != 1 || len(state.Clients) != 1 ||
		state.AdapterVersion != "v1.5.3" {
		t.Fatalf("GetSUIState() = %#v, error = %v", state, err)
	}
	encodedState, _ := json.Marshal(state)
	if bytes.Contains(encodedState, []byte(sentinel)) {
		t.Fatal("S-UI discovery persisted an untrusted sentinel field")
	}
	if _, err := database.SetSUITargetInbounds(ctx, node.ID, []int64{7}, probedAt); err != nil {
		t.Fatalf("SetSUITargetInbounds() error = %v", err)
	}
	user, _, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "mapped-user", Enabled: true, Now: probedAt,
	}, masterKey)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	user, err = database.ImportSUIClient(
		ctx, node.ID, 41, user.ID, probedAt.Add(time.Second), masterKey,
	)
	if err != nil || len(user.Assignments) != 1 {
		t.Fatalf("ImportSUIClient() = %#v, error = %v", user, err)
	}
	assignment := user.Assignments[0]
	if assignment.ManagementMode != "read_only" || assignment.RemoteClientID != 41 {
		t.Fatalf("read-only assignment = %#v", assignment)
	}
	if _, err := database.RevealAssignmentCredential(ctx, user.ID, node.ID, masterKey); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("RevealAssignmentCredential(read-only) error = %v", err)
	}
	if _, err := database.UpdateAssignment(ctx, user.ID, node.ID, AssignmentUpdate{
		Enabled: false, TrafficLimitBytes: 1, Now: probedAt.Add(time.Second),
	}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("UpdateAssignment(read-only) error = %v", err)
	}
	if _, _, err := database.RotateAssignmentCredential(
		ctx, user.ID, node.ID, probedAt.Add(time.Second), masterKey,
	); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("RotateAssignmentCredential(read-only) error = %v", err)
	}
	if count, err := database.RequestUserKick(
		ctx, user.ID, "", probedAt.Add(time.Second),
	); count != 0 || !errors.Is(err, ErrNotFound) {
		t.Fatalf("RequestUserKick(read-only S-UI) = %d, error = %v", count, err)
	}
	node, _ = database.GetNode(ctx, node.ID)
	readOnlyEnvelope, err := database.GetDesiredSnapshot(ctx, node.ID, node.DesiredVersion)
	if err != nil || readOnlyEnvelope.Snapshot.SUI == nil ||
		len(readOnlyEnvelope.Snapshot.SUI.TargetInboundIDs) != 1 ||
		len(readOnlyEnvelope.Snapshot.Users) != 1 ||
		readOnlyEnvelope.Snapshot.Users[0].ManagementMode != "read_only" ||
		readOnlyEnvelope.Snapshot.Users[0].Credential.VerifierSHA256 != "" {
		t.Fatalf("read-only desired snapshot = %#v, error = %v", readOnlyEnvelope, err)
	}
	if _, err := database.AdoptSUIClient(
		ctx, node.ID, 41, "existing-client", probedAt.Add(2*time.Second),
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("AdoptSUIClient(before read-only apply) error = %v", err)
	}
	readOnlyHash, _ := base64.RawURLEncoding.DecodeString(readOnlyEnvelope.SHA256)
	if err := database.AcknowledgeDesired(
		ctx, identity, readOnlyEnvelope.Snapshot.Version, readOnlyHash,
		"applied", "", "", probedAt.Add(2*time.Second),
	); err != nil {
		t.Fatalf("AcknowledgeDesired(read-only) error = %v", err)
	}
	if _, err := database.SetSUITargetInbounds(
		ctx, node.ID, nil, probedAt.Add(3*time.Second),
	); err != nil {
		t.Fatalf("SetSUITargetInbounds(clear before adoption) error = %v", err)
	}
	ackCurrentSUIDesired(t, database, identity, node.ID, probedAt.Add(4*time.Second))
	if _, err := database.AdoptSUIClient(
		ctx, node.ID, 41, "existing-client", probedAt.Add(5*time.Second),
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("AdoptSUIClient(without targets) error = %v", err)
	}
	if _, err := database.SetSUITargetInbounds(
		ctx, node.ID, []int64{7}, probedAt.Add(6*time.Second),
	); err != nil {
		t.Fatalf("SetSUITargetInbounds(restore) error = %v", err)
	}
	ackCurrentSUIDesired(t, database, identity, node.ID, probedAt.Add(7*time.Second))
	user, err = database.AdoptSUIClient(
		ctx, node.ID, 41, "existing-client", probedAt.Add(8*time.Second),
	)
	if err != nil || user.Assignments[0].ManagementMode != "managed" {
		t.Fatalf("AdoptSUIClient() = %#v, error = %v", user, err)
	}
	if _, err := database.SetSUITargetInbounds(
		ctx, node.ID, nil, probedAt.Add(9*time.Second),
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("SetSUITargetInbounds(clear managed targets) error = %v", err)
	}
	revealed, err := database.RevealAssignmentCredential(ctx, user.ID, node.ID, masterKey)
	if err != nil || revealed.Secret == "" {
		t.Fatalf("RevealAssignmentCredential(managed) error = %v", err)
	}
	node, _ = database.GetNode(ctx, node.ID)
	managedEnvelope, err := database.GetDesiredSnapshot(ctx, node.ID, node.DesiredVersion)
	if err != nil || managedEnvelope.Snapshot.Users[0].ManagementMode != "managed" {
		t.Fatalf("managed desired snapshot = %#v, error = %v", managedEnvelope, err)
	}
	managedHash, _ := base64.RawURLEncoding.DecodeString(managedEnvelope.SHA256)
	material, err := database.GetCredentialMaterial(
		ctx, identity, assignment.DesiredCredentialID, managedEnvelope.Snapshot.Version,
		managedHash, masterKey,
	)
	if err != nil || material != revealed.Secret {
		t.Fatalf("GetCredentialMaterial() = (%q, %v)", material, err)
	}
	wrongHash := bytes.Repeat([]byte{0x7f}, 32)
	if _, err := database.GetCredentialMaterial(
		ctx, identity, assignment.DesiredCredentialID, managedEnvelope.Snapshot.Version,
		wrongHash, masterKey,
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("GetCredentialMaterial(wrong hash) error = %v", err)
	}
	if _, err := database.GetCredentialMaterial(
		ctx, AgentIdentity{NodeID: uuid.NewString(), AdapterType: "s_ui"},
		assignment.DesiredCredentialID, managedEnvelope.Snapshot.Version, managedHash, masterKey,
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("GetCredentialMaterial(cross-node) error = %v", err)
	}
	canonical, _ := json.Marshal(managedEnvelope.Snapshot)
	if bytes.Contains(canonical, []byte(revealed.Secret)) {
		t.Fatal("managed S-UI snapshot contains credential plaintext")
	}
}

func TestRotateUserCredentialsSkipsReadOnlySUIAssignments(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	masterKey := bytes.Repeat([]byte{0x62}, 32)
	nativeNode := createTestNode(t, database, "native", "native_hysteria2", now)
	suiNode := createTestNode(t, database, "sui", "s_ui", now)
	installationID := uuid.NewString()
	if _, err := database.DB().ExecContext(ctx, `
		UPDATE nodes SET agent_installation_id = ? WHERE id = ?
	`, installationID, suiNode.ID); err != nil {
		t.Fatalf("bind S-UI Agent: %v", err)
	}
	identity := AgentIdentity{
		NodeID: suiNode.ID, InstallationID: installationID, AdapterType: "s_ui", Enabled: true,
	}
	report := protocol.SUIReportRequest{
		InstallationID: installationID,
		Adapter: protocol.AdapterInfo{
			Name: "s_ui", Version: "v1.5.3", Status: "compatible", LastProbedAt: &now,
		},
		Core: protocol.CoreInfo{Name: "sing-box", Running: true},
		Inbounds: []protocol.SUIDiscoveredInbound{{
			RemoteID: 7, Tag: "hy2", Type: "hysteria2", ListenPort: 443,
		}},
		Clients: []protocol.SUIDiscoveredClient{{
			RemoteID: 41, Name: "existing", Enabled: true, InboundIDs: []int64{7},
		}},
		SampledAt: now,
	}
	if err := database.RecordSUIReport(ctx, identity, report, now); err != nil {
		t.Fatalf("RecordSUIReport() error = %v", err)
	}
	user, _, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "mixed-rotation", Enabled: true,
		NodeIDs: []string{nativeNode.ID}, Now: now.Add(time.Second),
	}, masterKey)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	ackCurrentDesired(t, database, nativeNode.ID, now.Add(2*time.Second))
	user, err = database.ImportSUIClient(
		ctx, suiNode.ID, 41, user.ID, now.Add(3*time.Second), masterKey,
	)
	if err != nil {
		t.Fatalf("ImportSUIClient() error = %v", err)
	}
	before := make(map[string]string, len(user.Assignments))
	for _, assignment := range user.Assignments {
		before[assignment.NodeID] = assignment.DesiredCredentialID
	}
	rotatedUser, rotated, err := database.RotateUserCredentials(
		ctx, user.ID, now.Add(4*time.Second), masterKey,
	)
	if err != nil || len(rotated) != 1 || rotated[0].Assignment.NodeID != nativeNode.ID {
		t.Fatalf("RotateUserCredentials() = (%#v, %#v, %v)", rotatedUser, rotated, err)
	}
	for _, assignment := range rotatedUser.Assignments {
		if assignment.NodeID == suiNode.ID &&
			(assignment.ManagementMode != "read_only" ||
				assignment.DesiredCredentialID != before[suiNode.ID]) {
			t.Fatalf("read-only assignment changed during global rotation: %#v", assignment)
		}
	}
}

func ackCurrentSUIDesired(
	t *testing.T,
	database *Store,
	identity AgentIdentity,
	nodeID string,
	now time.Time,
) {
	t.Helper()
	node, err := database.GetNode(context.Background(), nodeID)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	envelope, err := database.GetDesiredSnapshot(context.Background(), nodeID, node.DesiredVersion)
	if err != nil {
		t.Fatalf("GetDesiredSnapshot() error = %v", err)
	}
	hash, err := base64.RawURLEncoding.DecodeString(envelope.SHA256)
	if err != nil {
		t.Fatalf("decode desired hash: %v", err)
	}
	if err := database.AcknowledgeDesired(
		context.Background(), identity, node.DesiredVersion, hash,
		"applied", "", "", now,
	); err != nil {
		t.Fatalf("AcknowledgeDesired(S-UI) error = %v", err)
	}
}
