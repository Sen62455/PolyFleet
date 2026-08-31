package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNativeUserCredentialAndSnapshotLifecycle(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	masterKey := bytes.Repeat([]byte{0x31}, 32)
	nodeOne := createTestNode(t, database, "native-one", "native_hysteria2", now)
	nodeTwo := createTestNode(t, database, "native-two", "native_hysteria2", now)
	nonNative := createTestNode(t, database, "standalone-node", "standalone_sing_box", now)
	if _, err := database.DB().ExecContext(ctx, `
		UPDATE nodes SET agent_installation_id = ? WHERE id = ?
	`, uuid.NewString(), nodeOne.ID); err != nil {
		t.Fatalf("mark node enrolled: %v", err)
	}

	user, credentials, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "alice", DisplayName: "Alice", Enabled: true,
		NodeIDs: []string{nodeOne.ID, nodeTwo.ID}, Now: now.Add(time.Second),
	}, masterKey)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if len(user.Assignments) != 2 || len(credentials) != 2 {
		t.Fatalf("assignments = %d, credentials = %d", len(user.Assignments), len(credentials))
	}
	if credentials[0].Secret == credentials[1].Secret {
		t.Fatal("credentials were reused across nodes")
	}
	for _, credential := range credentials {
		var ciphertext []byte
		if err := database.DB().QueryRowContext(ctx, `
			SELECT secret_ciphertext FROM user_credentials WHERE id = ?
		`, credential.Assignment.DesiredCredentialID).Scan(&ciphertext); err != nil {
			t.Fatalf("read ciphertext: %v", err)
		}
		if bytes.Contains(ciphertext, []byte(credential.Secret)) {
			t.Fatal("credential plaintext appears in stored ciphertext")
		}
		revealed, err := database.RevealAssignmentCredential(ctx, user.ID, credential.Assignment.NodeID, masterKey)
		if err != nil || revealed.Secret != credential.Secret {
			t.Fatalf("RevealAssignmentCredential() = (%q, %v)", revealed.Secret, err)
		}
		node, err := database.GetNode(ctx, credential.Assignment.NodeID)
		if err != nil {
			t.Fatalf("GetNode() error = %v", err)
		}
		envelope, err := database.GetDesiredSnapshot(ctx, node.ID, node.DesiredVersion)
		if err != nil || len(envelope.Snapshot.Users) != 1 {
			t.Fatalf("desired snapshot users = %d, error = %v", len(envelope.Snapshot.Users), err)
		}
		wantVerifier := sha256.Sum256([]byte(credential.Secret))
		if envelope.Snapshot.Users[0].Credential.VerifierSHA256 !=
			base64.RawURLEncoding.EncodeToString(wantVerifier[:]) {
			t.Fatal("desired snapshot verifier does not match generated credential")
		}
		if bytes.Contains([]byte(envelope.Snapshot.Users[0].Credential.VerifierSHA256), []byte(credential.Secret)) {
			t.Fatal("desired snapshot contains credential plaintext")
		}
	}

	if _, _, err := database.AssignUser(ctx, user.ID, nonNative.ID, 0, now, masterKey); err != ErrUnsupported {
		t.Fatalf("AssignUser(non-native) error = %v, want ErrUnsupported", err)
	}

	expiresAt := now.Add(2 * time.Hour)
	user, err = database.UpdateUser(ctx, user.ID, UpdateUser{
		Username: "alice", DisplayName: "Alice", Enabled: false,
		ExpiresAt: &expiresAt, Now: now.Add(2 * time.Second),
	})
	if err != nil || user.Enabled || user.ExpiresAt == nil {
		t.Fatalf("UpdateUser() = %#v, error = %v", user, err)
	}
	for _, assignment := range user.Assignments {
		node, _ := database.GetNode(ctx, assignment.NodeID)
		envelope, err := database.GetDesiredSnapshot(ctx, node.ID, node.DesiredVersion)
		if err != nil || envelope.Snapshot.Users[0].Enabled {
			t.Fatalf("disabled user snapshot = %#v, error = %v", envelope.Snapshot.Users, err)
		}
		hash, _ := base64.RawURLEncoding.DecodeString(envelope.SHA256)
		if err := database.AcknowledgeDesired(ctx, AgentIdentity{
			NodeID: node.ID, AdapterType: "native_hysteria2", Enabled: true,
		}, node.DesiredVersion, hash, "applied", "", "", now.Add(3*time.Second)); err != nil {
			t.Fatalf("AcknowledgeDesired() error = %v", err)
		}
	}
	user, err = database.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	for _, assignment := range user.Assignments {
		if assignment.State != "applied" || assignment.AppliedVersion != assignment.DesiredVersion {
			t.Fatalf("assignment was not marked applied: %#v", assignment)
		}
	}

	if err := database.UnassignUser(ctx, user.ID, nodeOne.ID, now.Add(4*time.Second)); err != nil {
		t.Fatalf("UnassignUser() error = %v", err)
	}
	nodeOne, _ = database.GetNode(ctx, nodeOne.ID)
	empty, err := database.GetDesiredSnapshot(ctx, nodeOne.ID, nodeOne.DesiredVersion)
	if err != nil || len(empty.Snapshot.Users) != 0 {
		t.Fatalf("unassigned snapshot users = %d, error = %v", len(empty.Snapshot.Users), err)
	}
	if err := database.ArchiveNode(ctx, nodeOne.ID, now.Add(5*time.Second)); !errors.Is(err, ErrPending) {
		t.Fatalf("ArchiveNode(pending unassignment) error = %v, want ErrPending", err)
	}
	emptyHash, err := base64.RawURLEncoding.DecodeString(empty.SHA256)
	if err != nil {
		t.Fatalf("decode unassigned snapshot hash: %v", err)
	}
	if err := database.AcknowledgeDesired(ctx, AgentIdentity{
		NodeID: nodeOne.ID, AdapterType: "native_hysteria2", Enabled: true,
	}, nodeOne.DesiredVersion, emptyHash, "applied", "", "", now.Add(6*time.Second)); err != nil {
		t.Fatalf("AcknowledgeDesired(unassignment) error = %v", err)
	}
	if err := database.ArchiveNode(ctx, nodeOne.ID, now.Add(7*time.Second)); err != nil {
		t.Fatalf("ArchiveNode(applied unassignment) error = %v", err)
	}
}

func TestArchivedUserAssignmentStopsBlockingNodeAfterSnapshotApplies(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	node := createTestNode(t, database, "archive-node", "native_hysteria2", now)
	user, _, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "archive-user", Enabled: true,
		NodeIDs: []string{node.ID}, Now: now.Add(time.Second),
	}, bytes.Repeat([]byte{0x72}, 32))
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if err := database.ArchiveNode(ctx, node.ID, now.Add(2*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("ArchiveNode(active assignment) error = %v, want ErrConflict", err)
	}
	if err := database.ArchiveUser(ctx, user.ID, now.Add(3*time.Second)); err != nil {
		t.Fatalf("ArchiveUser() error = %v", err)
	}
	if err := database.ArchiveNode(ctx, node.ID, now.Add(4*time.Second)); !errors.Is(err, ErrPending) {
		t.Fatalf("ArchiveNode(pending removal) error = %v, want ErrPending", err)
	}
	node, err = database.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	envelope, err := database.GetDesiredSnapshot(ctx, node.ID, node.DesiredVersion)
	if err != nil || len(envelope.Snapshot.Users) != 0 || len(envelope.Snapshot.Kicks) != 1 {
		t.Fatalf("archived user snapshot = %#v, error = %v", envelope.Snapshot, err)
	}
	hash, err := base64.RawURLEncoding.DecodeString(envelope.SHA256)
	if err != nil {
		t.Fatalf("decode desired hash: %v", err)
	}
	if err := database.AcknowledgeDesired(ctx, AgentIdentity{
		NodeID: node.ID, AdapterType: "native_hysteria2", Enabled: true,
	}, node.DesiredVersion, hash, "applied", "", "", now.Add(5*time.Second)); err != nil {
		t.Fatalf("AcknowledgeDesired() error = %v", err)
	}
	if err := database.ArchiveNode(ctx, node.ID, now.Add(6*time.Second)); err != nil {
		t.Fatalf("ArchiveNode(applied archived assignment) error = %v", err)
	}
}

func TestForceArchiveAllowsDisabledNodeWithUnconfirmedRemoval(t *testing.T) {
	database, err := Open(t.Context(), filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	node := createTestNode(t, database, "force-archive-node", "native_hysteria2", now)
	user, _, err := database.CreateUser(t.Context(), NewUser{
		ID: uuid.NewString(), Username: "force-archive-user", Enabled: true,
		NodeIDs: []string{node.ID}, Now: now.Add(time.Second),
	}, bytes.Repeat([]byte{0x61}, 32))
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if err := database.ArchiveUser(t.Context(), user.ID, now.Add(2*time.Second)); err != nil {
		t.Fatalf("ArchiveUser() error = %v", err)
	}
	if err := database.ArchiveNodeWithForce(
		t.Context(), node.ID, true, now.Add(3*time.Second),
	); !errors.Is(err, ErrNodeEnabled) {
		t.Fatalf("ArchiveNodeWithForce(enabled) error = %v, want ErrNodeEnabled", err)
	}
	if _, err := database.DB().ExecContext(t.Context(), `
		UPDATE nodes SET enabled = 0, status = 'disabled' WHERE id = ?
	`, node.ID); err != nil {
		t.Fatalf("disable force archive node: %v", err)
	}
	if err := database.ArchiveNodeWithForce(
		t.Context(), node.ID, true, now.Add(4*time.Second),
	); err != nil {
		t.Fatalf("ArchiveNodeWithForce(disabled) error = %v", err)
	}
	if _, err := database.GetNode(t.Context(), node.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetNode(force archived) error = %v, want ErrNotFound", err)
	}
}

func createTestNode(t *testing.T, database *Store, name, adapter string, now time.Time) Node {
	t.Helper()
	node, err := database.CreateNode(context.Background(), NewNode{
		ID: uuid.NewString(), Name: name, AdapterType: adapter, Enabled: true, Now: now,
	})
	if err != nil {
		t.Fatalf("CreateNode(%s) error = %v", name, err)
	}
	return node
}
