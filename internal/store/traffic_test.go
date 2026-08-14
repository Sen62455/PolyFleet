package store

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Sen62455/PolyFleet/internal/protocol"
)

func TestTrafficIngestionIsIdempotentAndEnforcesNodeAndGlobalQuotas(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	masterKey := bytes.Repeat([]byte{0x44}, 32)
	nodeOne := createTestNode(t, database, "traffic-one", "native_hysteria2", now)
	nodeTwo := createTestNode(t, database, "traffic-two", "native_hysteria2", now)
	user, _, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "traffic-user", Enabled: true,
		TrafficLimitBytes: 180, NodeIDs: []string{nodeOne.ID, nodeTwo.ID}, Now: now,
	}, masterKey)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	user, err = database.UpdateAssignment(ctx, user.ID, nodeOne.ID, AssignmentUpdate{
		Enabled: true, TrafficLimitBytes: 100, Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("UpdateAssignment() error = %v", err)
	}
	installationOne := uuid.NewString()
	installationTwo := uuid.NewString()
	epochOne := uuid.NewString()
	epochTwo := uuid.NewString()
	identityOne := AgentIdentity{NodeID: nodeOne.ID, InstallationID: installationOne, AdapterType: "native_hysteria2", Enabled: true}
	identityTwo := AgentIdentity{NodeID: nodeTwo.ID, InstallationID: installationTwo, AdapterType: "native_hysteria2", Enabled: true}

	first := trafficBatch(installationOne, epochOne, 1, now.Add(2*time.Second), user.ID, 60, 20)
	result, err := database.IngestTrafficBatch(ctx, identityOne, first, now.Add(2*time.Second))
	if err != nil || result.Status != "accepted" {
		t.Fatalf("IngestTrafficBatch(first) = %#v, error = %v", result, err)
	}
	result, err = database.IngestTrafficBatch(ctx, identityOne, first, now.Add(3*time.Second))
	if err != nil || result.Status != "duplicate" {
		t.Fatalf("IngestTrafficBatch(duplicate) = %#v, error = %v", result, err)
	}
	conflict := trafficBatch(installationOne, epochOne, 1, now.Add(3*time.Second), user.ID, 1, 1)
	result, err = database.IngestTrafficBatch(ctx, identityOne, conflict, now.Add(3*time.Second))
	if err != nil || result.Status != "rejected" || result.ErrorCode != "sequence_conflict" {
		t.Fatalf("IngestTrafficBatch(sequence conflict) = %#v, error = %v", result, err)
	}

	second := trafficBatch(installationOne, epochOne, 2, now.Add(4*time.Second), user.ID, 30, 0)
	if result, err = database.IngestTrafficBatch(ctx, identityOne, second, now.Add(4*time.Second)); err != nil || result.Status != "accepted" {
		t.Fatalf("IngestTrafficBatch(node limit) = %#v, error = %v", result, err)
	}
	user, err = database.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	one := assignmentForNode(t, user, nodeOne.ID)
	if user.TrafficUsedBytes != 110 || user.QuotaState != "active" ||
		one.TrafficUsedBytes != 110 || one.QuotaState != "limited" || one.KickGeneration != 1 {
		t.Fatalf("unexpected node quota state: user=%#v assignment=%#v", user, one)
	}

	third := trafficBatch(installationTwo, epochTwo, 1, now.Add(5*time.Second), user.ID, 40, 40)
	if result, err = database.IngestTrafficBatch(ctx, identityTwo, third, now.Add(5*time.Second)); err != nil || result.Status != "accepted" {
		t.Fatalf("IngestTrafficBatch(global limit) = %#v, error = %v", result, err)
	}
	user, err = database.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUser(global limit) error = %v", err)
	}
	one = assignmentForNode(t, user, nodeOne.ID)
	two := assignmentForNode(t, user, nodeTwo.ID)
	if user.TrafficUsedBytes != 190 || user.QuotaState != "limited" ||
		one.KickGeneration != 1 || two.KickGeneration != 1 {
		t.Fatalf("unexpected global quota state: user=%#v one=%#v two=%#v", user, one, two)
	}
	for _, nodeID := range []string{nodeOne.ID, nodeTwo.ID} {
		node, getErr := database.GetNode(ctx, nodeID)
		if getErr != nil {
			t.Fatalf("GetNode(%s) error = %v", nodeID, getErr)
		}
		desired, getErr := database.GetDesiredSnapshot(ctx, nodeID, node.DesiredVersion)
		if getErr != nil || len(desired.Snapshot.Users) != 1 || desired.Snapshot.Users[0].QuotaState != "limited" {
			t.Fatalf("effective desired quota for %s = %#v, error = %v", nodeID, desired.Snapshot.Users, getErr)
		}
	}

	unknownID := uuid.NewString()
	unknown := trafficBatch(installationTwo, epochTwo, 2, now.Add(6*time.Second), unknownID, 12, 8)
	if result, err = database.IngestTrafficBatch(ctx, identityTwo, unknown, now.Add(6*time.Second)); err != nil || result.Status != "accepted" {
		t.Fatalf("IngestTrafficBatch(unknown) = %#v, error = %v", result, err)
	}
	nodeTwo, err = database.GetNode(ctx, nodeTwo.ID)
	if err != nil || nodeTwo.TrafficUnattributedBytes != 20 ||
		nodeTwo.TrafficUploadBytes != 52 || nodeTwo.TrafficDownloadBytes != 48 {
		t.Fatalf("node unattributed traffic = %#v, error = %v", nodeTwo, err)
	}
	user, _ = database.GetUser(ctx, user.ID)
	if user.TrafficUsedBytes != 190 {
		t.Fatalf("unknown traffic changed user total to %d", user.TrafficUsedBytes)
	}
}

func TestOnlineSnapshotsAndKickGenerations(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	node := createTestNode(t, database, "online-node", "native_hysteria2", now)
	user, _, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "online-user", Enabled: true,
		NodeIDs: []string{node.ID}, Now: now,
	}, bytes.Repeat([]byte{0x55}, 32))
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	identity := AgentIdentity{NodeID: node.ID, InstallationID: uuid.NewString(), Enabled: true}
	sampledAt := now.Add(time.Second)
	accepted, err := database.RecordOnlineSnapshot(ctx, identity, protocol.OnlineSnapshotRequest{
		SnapshotID: uuid.NewString(), InstallationID: identity.InstallationID, SampledAt: sampledAt,
		Users: []protocol.OnlineUser{
			{UserID: user.ID, Connections: 2},
			{UserID: uuid.NewString(), Connections: 3},
		},
	}, now.Add(2*time.Second))
	if err != nil || !accepted {
		t.Fatalf("RecordOnlineSnapshot() = %v, error = %v", accepted, err)
	}
	accepted, err = database.RecordOnlineSnapshot(ctx, identity, protocol.OnlineSnapshotRequest{
		SnapshotID: uuid.NewString(), InstallationID: identity.InstallationID,
		SampledAt: sampledAt, Users: nil,
	}, now.Add(3*time.Second))
	if err != nil || accepted {
		t.Fatalf("equal-time snapshot = %v, error = %v, want ignored", accepted, err)
	}
	node, _ = database.GetNode(ctx, node.ID)
	user, _ = database.GetUser(ctx, user.ID)
	assignment := assignmentForNode(t, user, node.ID)
	if node.OnlineUsers != 1 || node.OnlineConnections != 5 || node.OnlineUnknownUsers != 1 ||
		assignment.OnlineConnections != 2 {
		t.Fatalf("online state: node=%#v assignment=%#v", node, assignment)
	}

	count, err := database.RequestUserKick(ctx, user.ID, "", now.Add(4*time.Second))
	if err != nil || count != 1 {
		t.Fatalf("RequestUserKick() = %d, error = %v", count, err)
	}
	count, err = database.RequestUserKick(ctx, user.ID, node.ID, now.Add(5*time.Second))
	if err != nil || count != 1 {
		t.Fatalf("RequestUserKick(node) = %d, error = %v", count, err)
	}
	user, _ = database.GetUser(ctx, user.ID)
	if assignmentForNode(t, user, node.ID).KickGeneration != 2 {
		t.Fatalf("manual kick generation = %d, want 2", assignmentForNode(t, user, node.ID).KickGeneration)
	}
}

func TestExpiredUserEnforcementIsIdempotent(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	node := createTestNode(t, database, "expiry-node", "native_hysteria2", now)
	expiresAt := now.Add(time.Minute)
	user, _, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "expiry-user", Enabled: true,
		ExpiresAt: &expiresAt, NodeIDs: []string{node.ID}, Now: now,
	}, bytes.Repeat([]byte{0x66}, 32))
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if count, err := database.EnforceExpiredUsers(ctx, now, 100); err != nil || count != 0 {
		t.Fatalf("early EnforceExpiredUsers() = %d, error = %v", count, err)
	}
	if count, err := database.EnforceExpiredUsers(ctx, expiresAt, 100); err != nil || count != 1 {
		t.Fatalf("EnforceExpiredUsers() = %d, error = %v", count, err)
	}
	if count, err := database.EnforceExpiredUsers(ctx, expiresAt.Add(time.Second), 100); err != nil || count != 0 {
		t.Fatalf("repeated EnforceExpiredUsers() = %d, error = %v", count, err)
	}
	user, _ = database.GetUser(ctx, user.ID)
	if user.ExpiryEnforcedAt == nil || assignmentForNode(t, user, node.ID).KickGeneration != 1 {
		t.Fatalf("expiry state = %#v", user)
	}
}

func trafficBatch(installationID, sourceEpoch string, sequence int64, sampledAt time.Time, userID string, upload, download int64) protocol.TrafficBatch {
	return protocol.TrafficBatch{
		ID: uuid.NewString(), InstallationID: installationID, SourceEpoch: sourceEpoch,
		Sequence: sequence, SampledAt: sampledAt,
		Items: []protocol.TrafficDelta{{UserID: userID, UploadBytes: upload, DownloadBytes: download}},
	}
}

func assignmentForNode(t *testing.T, user User, nodeID string) UserAssignment {
	t.Helper()
	for _, assignment := range user.Assignments {
		if assignment.NodeID == nodeID {
			return assignment
		}
	}
	t.Fatalf("user %s has no assignment for node %s", user.ID, nodeID)
	return UserAssignment{}
}
