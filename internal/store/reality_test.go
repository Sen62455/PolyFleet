package store

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/Sen62455/PolyFleet/internal/store/migrations"
	"github.com/google/uuid"
)

func TestVLESSRealityCredentialSnapshotMaterialAndSubscriptionLifecycle(t *testing.T) {
	ctx := t.Context()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	masterKey := bytes.Repeat([]byte{0x91}, 32)
	node := createVLESSRealityTestNode(t, database, "Reality Tokyo", now)
	otherNode := createVLESSRealityTestNode(t, database, "Reality Osaka", now)

	user, credentials, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "reality-user", Enabled: true,
		NodeIDs: []string{node.ID}, Now: now.Add(time.Second),
	}, masterKey)
	if err != nil || len(credentials) != 1 || len(user.Assignments) != 1 {
		t.Fatalf("CreateUser() = (%#v, %#v, %v)", user, credentials, err)
	}
	credential := credentials[0]
	if _, err := uuid.Parse(credential.Secret); err != nil {
		t.Fatalf("VLESS credential %q is not a UUID: %v", credential.Secret, err)
	}
	if credential.Assignment.CredentialProtocol != CredentialProtocolVLESS {
		t.Fatalf("credential protocol = %q", credential.Assignment.CredentialProtocol)
	}
	var storedProtocol string
	var ciphertext []byte
	if err := database.DB().QueryRowContext(ctx, `
		SELECT protocol, secret_ciphertext FROM user_credentials WHERE id = ?
	`, credential.Assignment.DesiredCredentialID).Scan(&storedProtocol, &ciphertext); err != nil {
		t.Fatalf("read stored VLESS credential: %v", err)
	}
	if storedProtocol != CredentialProtocolVLESS || bytes.Contains(ciphertext, []byte(credential.Secret)) {
		t.Fatal("VLESS credential was stored with the wrong protocol or in plaintext")
	}

	node, err = database.GetNode(ctx, node.ID)
	if err != nil || node.VLESSReality == nil {
		t.Fatalf("GetNode() = (%#v, %v)", node, err)
	}
	envelope, err := database.GetDesiredSnapshot(ctx, node.ID, node.DesiredVersion)
	if err != nil {
		t.Fatalf("GetDesiredSnapshot() error = %v", err)
	}
	if envelope.Snapshot.SchemaVersion != 2 || envelope.Snapshot.VLESSReality == nil ||
		envelope.Snapshot.VLESSReality.Flow != "xtls-rprx-vision" ||
		envelope.Snapshot.VLESSReality.Network != "tcp" ||
		len(envelope.Snapshot.Users) != 1 ||
		envelope.Snapshot.Users[0].Credential.Protocol != CredentialProtocolVLESS ||
		envelope.Snapshot.Users[0].Credential.VerifierSHA256 != "" {
		t.Fatalf("unexpected VLESS Reality snapshot: %#v", envelope.Snapshot)
	}
	var canonical []byte
	err = database.DB().QueryRowContext(ctx, `
		SELECT canonical_json FROM node_snapshots WHERE node_id = ? AND version = ?
	`, node.ID, node.DesiredVersion).Scan(&canonical)
	if err != nil {
		t.Fatalf("read canonical snapshot: %v", err)
	}
	if bytes.Contains(canonical, []byte(credential.Secret)) {
		t.Fatal("desired snapshot contains plaintext VLESS UUID")
	}
	hash, err := base64.RawURLEncoding.DecodeString(envelope.SHA256)
	if err != nil {
		t.Fatalf("decode snapshot hash: %v", err)
	}
	identity := AgentIdentity{
		NodeID: node.ID, AdapterType: AdapterSingBoxVLESSReality, Enabled: true,
	}
	material, err := database.GetCredentialMaterial(
		ctx, identity, credential.Assignment.DesiredCredentialID,
		node.DesiredVersion, hash, masterKey,
	)
	if err != nil || material != credential.Secret {
		t.Fatalf("GetCredentialMaterial() = (%q, %v)", material, err)
	}
	disabledIdentity := identity
	disabledIdentity.Enabled = false
	if _, err := database.GetCredentialMaterial(
		ctx, disabledIdentity, credential.Assignment.DesiredCredentialID,
		node.DesiredVersion, hash, masterKey,
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("disabled-node GetCredentialMaterial() error = %v", err)
	}
	if _, err := database.UpdateAssignment(ctx, user.ID, node.ID, AssignmentUpdate{
		Enabled: false, TrafficLimitBytes: 0, Now: now.Add(1500 * time.Millisecond),
	}); err != nil {
		t.Fatalf("disable Reality assignment: %v", err)
	}
	disabledNode, err := database.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNode(disabled assignment) error = %v", err)
	}
	disabledEnvelope, err := database.GetDesiredSnapshot(ctx, node.ID, disabledNode.DesiredVersion)
	if err != nil {
		t.Fatalf("GetDesiredSnapshot(disabled assignment) error = %v", err)
	}
	disabledHash, err := base64.RawURLEncoding.DecodeString(disabledEnvelope.SHA256)
	if err != nil {
		t.Fatalf("decode disabled snapshot hash: %v", err)
	}
	if _, err := database.GetCredentialMaterial(
		ctx, identity, credential.Assignment.DesiredCredentialID,
		disabledNode.DesiredVersion, disabledHash, masterKey,
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("disabled-user GetCredentialMaterial() error = %v", err)
	}
	if _, err := database.UpdateAssignment(ctx, user.ID, node.ID, AssignmentUpdate{
		Enabled: true, TrafficLimitBytes: 0, Now: now.Add(1750 * time.Millisecond),
	}); err != nil {
		t.Fatalf("re-enable Reality assignment: %v", err)
	}
	node, err = database.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNode(re-enabled assignment) error = %v", err)
	}
	envelope, err = database.GetDesiredSnapshot(ctx, node.ID, node.DesiredVersion)
	if err != nil {
		t.Fatalf("GetDesiredSnapshot(re-enabled assignment) error = %v", err)
	}
	hash, err = base64.RawURLEncoding.DecodeString(envelope.SHA256)
	if err != nil {
		t.Fatalf("decode re-enabled snapshot hash: %v", err)
	}
	if _, err := database.GetCredentialMaterial(ctx, AgentIdentity{
		NodeID: otherNode.ID, AdapterType: AdapterSingBoxVLESSReality, Enabled: true,
	}, credential.Assignment.DesiredCredentialID, node.DesiredVersion, hash, masterKey); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("cross-node GetCredentialMaterial() error = %v", err)
	}

	if err := database.AcknowledgeDesired(
		ctx, identity, node.DesiredVersion, hash, "applied", "", "", now.Add(2*time.Second),
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("Reality ACK without material error = %v", err)
	}
	publicKey := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	appliedMaterial := &protocol.AppliedRealityMaterial{
		KeyGeneration: 1, PublicKey: publicKey, ShortID: "0123456789abcdef",
	}
	if err := database.AcknowledgeDesiredWithMaterial(
		ctx, identity, node.DesiredVersion, hash, "applied", "", "",
		appliedMaterial, now.Add(3*time.Second),
	); err != nil {
		t.Fatalf("AcknowledgeDesiredWithMaterial() error = %v", err)
	}
	markRealityDataPlaneHealthy(t, database, node.ID, now.Add(3500*time.Millisecond))
	conflictingMaterial := &protocol.AppliedRealityMaterial{
		KeyGeneration: 1,
		PublicKey:     base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x43}, 32)),
		ShortID:       "fedcba9876543210",
	}
	if err := database.AcknowledgeDesiredWithMaterial(
		ctx, identity, node.DesiredVersion, hash, "applied", "", "",
		conflictingMaterial, now.Add(4*time.Second),
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("same-generation Reality identity replacement error = %v", err)
	}
	node, err = database.GetNode(ctx, node.ID)
	if err != nil || node.VLESSReality == nil ||
		node.VLESSReality.PublicKey != publicKey ||
		node.VLESSReality.ShortID != appliedMaterial.ShortID ||
		node.VLESSReality.MaterialAppliedVersion != node.AppliedVersion {
		t.Fatalf("applied Reality settings = %#v, error = %v", node.VLESSReality, err)
	}

	issued, err := database.CreateSubscriptionToken(ctx, NewSubscriptionToken{
		ID: uuid.NewString(), UserID: user.ID, Name: "reality",
		Now: now.Add(5 * time.Second),
	})
	if err != nil {
		t.Fatalf("CreateSubscriptionToken() error = %v", err)
	}
	subscription, err := database.ResolveSubscription(
		ctx, cryptoutil.TokenHash(issued.Secret), "sing-box", now.Add(6*time.Second), masterKey,
	)
	if err != nil || len(subscription.Endpoints) != 1 {
		t.Fatalf("ResolveSubscription() = (%#v, %v)", subscription, err)
	}
	endpoint := subscription.Endpoints[0]
	if endpoint.Protocol != CredentialProtocolVLESS ||
		endpoint.AdapterType != AdapterSingBoxVLESSReality || endpoint.Credential != credential.Secret ||
		endpoint.Flow != "xtls-rprx-vision" || endpoint.Network != "tcp" ||
		endpoint.RealityPublicKey != publicKey || endpoint.RealityShortID != "0123456789abcdef" {
		t.Fatalf("unexpected VLESS subscription endpoint: %#v", endpoint)
	}
}

func TestVLESSRealityCredentialMaterialAuthorizationMatrix(t *testing.T) {
	ctx := t.Context()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	masterKey := bytes.Repeat([]byte{0x92}, 32)
	node := createVLESSRealityTestNode(t, database, "Reality material matrix", now)
	expiresAt := now.Add(time.Hour)
	user, credentials, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "reality-material-matrix", Enabled: true,
		ExpiresAt: &expiresAt, NodeIDs: []string{node.ID}, Now: now.Add(time.Second),
	}, masterKey)
	if err != nil || len(credentials) != 1 || len(user.Assignments) != 1 {
		t.Fatalf("CreateUser() = (%#v, %#v, %v)", user, credentials, err)
	}
	credentialRef := credentials[0].Assignment.DesiredCredentialID
	node, err = database.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	envelope, err := database.GetDesiredSnapshot(ctx, node.ID, node.DesiredVersion)
	if err != nil {
		t.Fatalf("GetDesiredSnapshot() error = %v", err)
	}
	hash, err := base64.RawURLEncoding.DecodeString(envelope.SHA256)
	if err != nil {
		t.Fatalf("decode snapshot hash: %v", err)
	}
	identity := AgentIdentity{
		NodeID: node.ID, InstallationID: uuid.NewString(),
		AdapterType: AdapterSingBoxVLESSReality, Enabled: true,
	}
	assertDenied := func(name string, testIdentity AgentIdentity, ref string, version int64, digest []byte) {
		t.Helper()
		if _, err := database.GetCredentialMaterial(
			ctx, testIdentity, ref, version, digest, masterKey,
		); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("%s GetCredentialMaterial() error = %v, want ErrUnauthorized", name, err)
		}
	}

	wrongAdapter := identity
	wrongAdapter.AdapterType = "s_ui"
	assertDenied("wrong adapter", wrongAdapter, credentialRef, node.DesiredVersion, hash)
	assertDenied("wrong ref", identity, uuid.NewString(), node.DesiredVersion, hash)
	staleEnvelope, err := database.GetDesiredSnapshot(ctx, node.ID, node.DesiredVersion-1)
	if err != nil {
		t.Fatalf("GetDesiredSnapshot(stale) error = %v", err)
	}
	staleHash, err := base64.RawURLEncoding.DecodeString(staleEnvelope.SHA256)
	if err != nil {
		t.Fatalf("decode stale snapshot hash: %v", err)
	}
	assertDenied("stale desired version and hash", identity, credentialRef,
		staleEnvelope.Snapshot.Version, staleHash)
	assertDenied("wrong hash", identity, credentialRef, node.DesiredVersion,
		bytes.Repeat([]byte{0x7f}, sha256.Size))

	if _, err := database.DB().ExecContext(ctx,
		"UPDATE user_credentials SET protocol = 'hysteria2' WHERE id = ?", credentialRef,
	); err != nil {
		t.Fatalf("set wrong credential protocol: %v", err)
	}
	assertDenied("wrong protocol", identity, credentialRef, node.DesiredVersion, hash)
	if _, err := database.DB().ExecContext(ctx,
		"UPDATE user_credentials SET protocol = 'vless' WHERE id = ?", credentialRef,
	); err != nil {
		t.Fatalf("restore credential protocol: %v", err)
	}

	for _, state := range []string{"retired", "revoked"} {
		if _, err := database.DB().ExecContext(ctx,
			"UPDATE user_credentials SET state = ? WHERE id = ?", state, credentialRef,
		); err != nil {
			t.Fatalf("set credential state %s: %v", state, err)
		}
		assertDenied(state+" credential", identity, credentialRef, node.DesiredVersion, hash)
	}
	if _, err := database.DB().ExecContext(ctx,
		"UPDATE user_credentials SET state = 'staged' WHERE id = ?", credentialRef,
	); err != nil {
		t.Fatalf("restore credential state: %v", err)
	}

	if _, err := database.UpdateUser(ctx, user.ID, UpdateUser{
		Username: user.Username, DisplayName: user.DisplayName, Notes: user.Notes,
		Enabled: true, ExpiresAt: &expiresAt, TrafficLimitBytes: 0,
		Now: expiresAt,
	}); err != nil {
		t.Fatalf("expire user through desired state: %v", err)
	}
	expiredNode, err := database.GetNode(ctx, node.ID)
	if err != nil || expiredNode.DesiredVersion <= node.DesiredVersion {
		t.Fatalf("expired node desired state = %#v, error = %v", expiredNode, err)
	}
	expiredEnvelope, err := database.GetDesiredSnapshot(
		ctx, expiredNode.ID, expiredNode.DesiredVersion,
	)
	if err != nil || len(expiredEnvelope.Snapshot.Users) != 1 ||
		expiredEnvelope.Snapshot.Users[0].Enabled {
		t.Fatalf("expired desired snapshot = %#v, error = %v", expiredEnvelope.Snapshot, err)
	}
	expiredHash, err := base64.RawURLEncoding.DecodeString(expiredEnvelope.SHA256)
	if err != nil {
		t.Fatalf("decode expired snapshot hash: %v", err)
	}
	assertDenied("superseded pre-expiry snapshot", identity, credentialRef, node.DesiredVersion, hash)
	assertDenied("expired user", identity, credentialRef, expiredNode.DesiredVersion, expiredHash)
}

func TestVLESSRealitySupportsQuotasKicksAndRetry(t *testing.T) {
	ctx := t.Context()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	node := createVLESSRealityTestNode(t, database, "Reality boundaries", now)
	user, _, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "reality-boundary", Enabled: true,
		NodeIDs: []string{node.ID}, Now: now.Add(time.Second),
	}, bytes.Repeat([]byte{0xc4}, 32))
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	user, err = database.UpdateAssignment(ctx, user.ID, node.ID, AssignmentUpdate{
		Enabled: true, TrafficLimitBytes: 1024, Now: now.Add(2 * time.Second),
	})
	if err != nil || user.Assignments[0].TrafficLimitBytes != 1024 {
		t.Fatalf("Reality assignment quota error = %v", err)
	}
	user, err = database.UpdateUser(ctx, user.ID, UpdateUser{
		Username: user.Username, Enabled: true, TrafficLimitBytes: 1024,
		Now: now.Add(3 * time.Second),
	})
	if err != nil || user.TrafficLimitBytes != 1024 {
		t.Fatalf("Reality global quota error = %v", err)
	}
	if count, err := database.RequestUserKick(ctx, user.ID, node.ID, now.Add(4*time.Second)); count != 1 || err != nil {
		t.Fatalf("Reality kick = %d, error = %v", count, err)
	}
	node, err = database.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNode(after kick) error = %v", err)
	}
	desired, err := database.GetDesiredSnapshot(ctx, node.ID, node.DesiredVersion)
	if err != nil || len(desired.Snapshot.Users) != 1 ||
		desired.Snapshot.Users[0].QuotaState != "active" || len(desired.Snapshot.Kicks) != 1 {
		t.Fatalf("Reality quota/kick desired state = %#v, error = %v", desired.Snapshot, err)
	}
	if _, err := database.DB().ExecContext(ctx, `
		UPDATE nodes SET agent_installation_id = ?, status = 'online' WHERE id = ?
	`, uuid.NewString(), node.ID); err != nil {
		t.Fatalf("bind Reality test Agent: %v", err)
	}
	before, err := database.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNode(before retry) error = %v", err)
	}
	retried, err := database.RetryNodeSync(ctx, node.ID, now.Add(5*time.Second))
	if err != nil || retried.DesiredVersion != before.DesiredVersion+1 {
		t.Fatalf("RetryNodeSync(Reality) = %#v, error = %v", retried, err)
	}
}

func TestVLESSRealityNodeArchiveWaitsForUnassignmentAcknowledgement(t *testing.T) {
	ctx := t.Context()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	masterKey := bytes.Repeat([]byte{0xc5}, 32)
	node := createVLESSRealityTestNode(t, database, "Reality archive guard", now)
	user, _, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "reality-archive", Enabled: true,
		NodeIDs: []string{node.ID}, Now: now.Add(time.Second),
	}, masterKey)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	installationID := uuid.NewString()
	if _, err := database.DB().ExecContext(ctx, `
		UPDATE nodes SET agent_installation_id = ? WHERE id = ?
	`, installationID, node.ID); err != nil {
		t.Fatalf("bind Reality test Agent: %v", err)
	}
	node, err = database.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	initial, err := database.GetDesiredSnapshot(ctx, node.ID, node.DesiredVersion)
	if err != nil {
		t.Fatalf("GetDesiredSnapshot(initial) error = %v", err)
	}
	initialHash, _ := base64.RawURLEncoding.DecodeString(initial.SHA256)
	material := &protocol.AppliedRealityMaterial{
		KeyGeneration: 1,
		PublicKey:     base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x45}, 32)),
		ShortID:       "0123456789abcdef",
	}
	identity := AgentIdentity{
		NodeID: node.ID, InstallationID: installationID,
		AdapterType: AdapterSingBoxVLESSReality, Enabled: true,
	}
	if err := database.AcknowledgeDesiredWithMaterial(
		ctx, identity, node.DesiredVersion, initialHash, "applied", "", "",
		material, now.Add(2*time.Second),
	); err != nil {
		t.Fatalf("AcknowledgeDesiredWithMaterial(initial) error = %v", err)
	}
	if err := database.UnassignUser(ctx, user.ID, node.ID, now.Add(3*time.Second)); err != nil {
		t.Fatalf("UnassignUser() error = %v", err)
	}
	node, err = database.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNode(unassigned) error = %v", err)
	}
	if err := database.ArchiveNode(ctx, node.ID, now.Add(4*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("ArchiveNode(pending removal) error = %v, want ErrConflict", err)
	}
	empty, err := database.GetDesiredSnapshot(ctx, node.ID, node.DesiredVersion)
	if err != nil || len(empty.Snapshot.Users) != 0 {
		t.Fatalf("empty Reality snapshot = %#v, error = %v", empty.Snapshot, err)
	}
	emptyHash, _ := base64.RawURLEncoding.DecodeString(empty.SHA256)
	if err := database.AcknowledgeDesiredWithMaterial(
		ctx, identity, node.DesiredVersion, emptyHash, "applied", "", "",
		material, now.Add(5*time.Second),
	); err != nil {
		t.Fatalf("AcknowledgeDesiredWithMaterial(removal) error = %v", err)
	}
	if err := database.ArchiveNode(ctx, node.ID, now.Add(6*time.Second)); err != nil {
		t.Fatalf("ArchiveNode(applied removal) error = %v", err)
	}
}

func TestVLESSRealitySubscriptionRequiresConfirmedDataPlaneState(t *testing.T) {
	ctx := t.Context()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	masterKey := bytes.Repeat([]byte{0xb7}, 32)
	node := createVLESSRealityTestNode(t, database, "Reality eligibility", now)
	user, _, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "reality-eligibility", Enabled: true,
		NodeIDs: []string{node.ID}, Now: now.Add(time.Second),
	}, masterKey)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	node, err = database.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	envelope, err := database.GetDesiredSnapshot(ctx, node.ID, node.DesiredVersion)
	if err != nil {
		t.Fatalf("GetDesiredSnapshot() error = %v", err)
	}
	hash, err := base64.RawURLEncoding.DecodeString(envelope.SHA256)
	if err != nil {
		t.Fatalf("decode snapshot hash: %v", err)
	}
	material := &protocol.AppliedRealityMaterial{
		KeyGeneration: 1,
		PublicKey:     base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x62}, 32)),
		ShortID:       "0123456789abcdef",
	}
	identity := AgentIdentity{NodeID: node.ID, AdapterType: AdapterSingBoxVLESSReality, Enabled: true}
	if err := database.AcknowledgeDesiredWithMaterial(
		ctx, identity, node.DesiredVersion, hash, "applied", "", "", material,
		now.Add(2*time.Second),
	); err != nil {
		t.Fatalf("AcknowledgeDesiredWithMaterial() error = %v", err)
	}
	markRealityDataPlaneHealthy(t, database, node.ID, now.Add(3*time.Second))
	issued, err := database.CreateSubscriptionToken(ctx, NewSubscriptionToken{
		ID: uuid.NewString(), UserID: user.ID, Name: "eligibility", Now: now.Add(4 * time.Second),
	})
	if err != nil {
		t.Fatalf("CreateSubscriptionToken() error = %v", err)
	}
	mismatchedCredentialID := uuid.NewString()
	if _, err := database.DB().ExecContext(ctx, `
		INSERT INTO user_credentials(
			id, user_id, node_id, protocol, secret_ciphertext, verifier_sha256,
			secret_fingerprint, key_version, state, created_at, applied_at
		)
		SELECT ?, user_id, node_id, protocol, secret_ciphertext, verifier_sha256,
		       secret_fingerprint, key_version, 'applied', created_at, applied_at
		FROM user_credentials
		WHERE id = (
			SELECT desired_credential_id FROM node_user_assignments
			WHERE user_id = ? AND node_id = ?
		)
	`, mismatchedCredentialID, user.ID, node.ID); err != nil {
		t.Fatalf("create mismatched Reality credential fixture: %v", err)
	}

	assertState := func(wantEndpoints int, wantReason string) {
		t.Helper()
		subscription, resolveErr := database.ResolveSubscription(
			ctx, cryptoutil.TokenHash(issued.Secret), "uri", now.Add(5*time.Second), masterKey,
		)
		if resolveErr != nil || len(subscription.Endpoints) != wantEndpoints {
			t.Fatalf("ResolveSubscription() = %#v, %v; want %d endpoints", subscription, resolveErr, wantEndpoints)
		}
		refreshed, getErr := database.GetUser(ctx, user.ID)
		if getErr != nil || len(refreshed.Assignments) != 1 {
			t.Fatalf("GetUser() = %#v, %v", refreshed, getErr)
		}
		assignment := refreshed.Assignments[0]
		if assignment.SubscriptionEligible != (wantReason == "") ||
			assignment.SubscriptionReason != wantReason {
			t.Fatalf("assignment eligibility = %#v; want reason %q", assignment, wantReason)
		}
	}
	reset := func() {
		t.Helper()
		if _, resetErr := database.DB().ExecContext(ctx, `
			UPDATE nodes
			SET adapter_status = 'compatible', core_running = 1, status = 'online',
			    applied_version = desired_version
			WHERE id = ?
		`, node.ID); resetErr != nil {
			t.Fatalf("reset Reality node: %v", resetErr)
		}
		if _, resetErr := database.DB().ExecContext(ctx, `
			UPDATE node_user_assignments
			SET applied_credential_id = desired_credential_id,
			    applied_version = (SELECT applied_version FROM nodes WHERE id = ?),
			    state = 'applied'
			WHERE user_id = ? AND node_id = ?
		`, node.ID, user.ID, node.ID); resetErr != nil {
			t.Fatalf("reset Reality assignment: %v", resetErr)
		}
		if _, resetErr := database.DB().ExecContext(ctx, `
			UPDATE node_vless_reality
			SET applied_key_generation = desired_key_generation,
			    material_applied_version = (SELECT applied_version FROM nodes WHERE id = ?)
			WHERE node_id = ?
		`, node.ID, node.ID); resetErr != nil {
			t.Fatalf("reset Reality material: %v", resetErr)
		}
	}

	assertState(1, "")
	for _, adapterStatus := range []string{"unknown", "incompatible", "unavailable", "not_configured"} {
		t.Run("adapter_"+adapterStatus, func(t *testing.T) {
			reset()
			if _, err := database.DB().ExecContext(ctx,
				"UPDATE nodes SET adapter_status = ? WHERE id = ?", adapterStatus, node.ID,
			); err != nil {
				t.Fatalf("set adapter status: %v", err)
			}
			assertState(0, "adapter_not_compatible")
		})
	}
	for _, testCase := range []struct {
		name       string
		statement  string
		arguments  []any
		wantReason string
	}{
		{
			name: "core down", statement: "UPDATE nodes SET core_running = 0 WHERE id = ?",
			arguments: []any{node.ID}, wantReason: "core_not_running",
		},
		{
			name: "credential reference mismatch",
			statement: `UPDATE node_user_assignments SET applied_credential_id = ?
				WHERE user_id = ? AND node_id = ?`,
			arguments: []any{mismatchedCredentialID, user.ID, node.ID}, wantReason: "applied_state_mismatch",
		},
		{
			name:      "assignment node version mismatch",
			statement: "UPDATE nodes SET applied_version = applied_version + 1 WHERE id = ?",
			arguments: []any{node.ID}, wantReason: "applied_state_mismatch",
		},
		{
			name:      "material assignment version mismatch",
			statement: "UPDATE node_vless_reality SET material_applied_version = material_applied_version - 1 WHERE node_id = ?",
			arguments: []any{node.ID}, wantReason: "reality_material_missing",
		},
		{
			name:      "key generation mismatch",
			statement: "UPDATE node_vless_reality SET desired_key_generation = applied_key_generation + 1 WHERE node_id = ?",
			arguments: []any{node.ID}, wantReason: "reality_material_missing",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			reset()
			if _, err := database.DB().ExecContext(ctx, testCase.statement, testCase.arguments...); err != nil {
				t.Fatalf("mutate Reality state: %v", err)
			}
			assertState(0, testCase.wantReason)
		})
	}

	for _, nodeStatus := range []string{"stale", "offline"} {
		t.Run("preserves_"+nodeStatus, func(t *testing.T) {
			reset()
			if _, err := database.DB().ExecContext(ctx,
				"UPDATE nodes SET status = ? WHERE id = ?", nodeStatus, node.ID,
			); err != nil {
				t.Fatalf("set node status: %v", err)
			}
			assertState(1, "")
		})
	}
}

func TestRealityEnrollmentRequiresAndPersistsCapabilities(t *testing.T) {
	ctx := t.Context()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	node := createVLESSRealityTestNode(t, database, "Reality capability", now)
	admin := Admin{
		ID: uuid.NewString(), Username: "reality-admin", PasswordHash: "unused",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateAdmin(ctx, admin); err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	issued, err := database.CreateEnrollmentToken(ctx, node.ID, admin.ID, now, 10*time.Minute)
	if err != nil {
		t.Fatalf("CreateEnrollmentToken() error = %v", err)
	}
	facts := EnrollmentFacts{
		InstallationID: uuid.NewString(), RequestID: uuid.NewString(), AgentVersion: "test",
		AdapterType: AdapterSingBoxVLESSReality, CoreName: "sing-box",
		Capabilities: []string{"desired_state_v2", "credential_material_v1"},
	}
	masterKey := bytes.Repeat([]byte{0xa2}, 32)
	if _, err := database.EnrollAgent(ctx, issued.Token, facts, masterKey, now); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("EnrollAgent(missing capability) error = %v", err)
	}
	facts.Capabilities = append(facts.Capabilities, "sing_box_vless_reality", "desired_state_v2")
	if _, err := database.EnrollAgent(ctx, issued.Token, facts, masterKey, now); err != nil {
		t.Fatalf("EnrollAgent() error = %v", err)
	}
	capabilities, err := database.ListAgentCapabilities(ctx, node.ID)
	if err != nil || fmt.Sprint(capabilities) !=
		"[credential_material_v1 desired_state_v2 sing_box_vless_reality]" {
		t.Fatalf("ListAgentCapabilities() = (%v, %v)", capabilities, err)
	}
	has, err := database.HasAgentCapabilities(
		ctx, node.ID, "desired_state_v2", "sing_box_vless_reality",
	)
	if err != nil || !has {
		t.Fatalf("HasAgentCapabilities() = (%v, %v)", has, err)
	}
}

func TestRealityHeartbeatCapabilityUpgradeTriggersOneResync(t *testing.T) {
	ctx := t.Context()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	node := createVLESSRealityTestNode(t, database, "Reality capability upgrade", now)
	installationID := uuid.NewString()
	if _, err := database.DB().ExecContext(ctx, `
		UPDATE nodes SET agent_installation_id = ? WHERE id = ?
	`, installationID, node.ID); err != nil {
		t.Fatalf("bind Reality node: %v", err)
	}
	for _, capability := range []string{
		"desired_state_v2", "credential_material_v1", "sing_box_vless_reality",
	} {
		if _, err := database.DB().ExecContext(ctx, `
			INSERT INTO node_agent_capabilities(node_id, capability, reported_at)
			VALUES (?, ?, ?)
		`, node.ID, capability, now.UnixMilli()); err != nil {
			t.Fatalf("seed capability %s: %v", capability, err)
		}
	}
	identity := AgentIdentity{
		NodeID: node.ID, InstallationID: installationID,
		AdapterType: AdapterSingBoxVLESSReality, Enabled: true,
	}
	capabilities := []string{
		"desired_state_v2", "credential_material_v1", "sing_box_vless_reality",
		"traffic_stats_v1", "traffic_outbox_v1", "online_snapshot_v1",
		"kick_generation_v1", "reality_user_control_v1",
	}
	heartbeat := protocol.HeartbeatRequest{
		InstallationID: installationID, Capabilities: capabilities,
		Agent:     protocol.AgentInfo{Version: "test", Protocol: protocol.MajorVersion},
		Core:      protocol.CoreInfo{Name: "sing-box", Running: true},
		Adapter:   protocol.AdapterInfo{Name: AdapterSingBoxVLESSReality, Status: "compatible"},
		Host:      protocol.HostMetrics{MemoryTotalBytes: 1, DiskTotalBytes: 1},
		SampledAt: now.Add(time.Minute),
	}
	desiredVersion, err := database.RecordHeartbeat(ctx, identity, heartbeat, now.Add(time.Minute))
	if err != nil || desiredVersion != 2 {
		t.Fatalf("RecordHeartbeat(upgrade) = (%d, %v), want version 2", desiredVersion, err)
	}
	upgraded, err := database.GetNode(ctx, node.ID)
	if err != nil || upgraded.DesiredVersion != 2 || upgraded.Status != "pending" {
		t.Fatalf("node after capability upgrade = (%#v, %v)", upgraded, err)
	}
	if _, err := database.GetDesiredSnapshot(ctx, node.ID, 2); err != nil {
		t.Fatalf("GetDesiredSnapshot(2) error = %v", err)
	}
	heartbeat.SampledAt = now.Add(2 * time.Minute)
	desiredVersion, err = database.RecordHeartbeat(ctx, identity, heartbeat, now.Add(2*time.Minute))
	if err != nil || desiredVersion != 2 {
		t.Fatalf("RecordHeartbeat(repeat) = (%d, %v), want unchanged version 2", desiredVersion, err)
	}
}

func TestVLESSRealityKeyGenerationCannotDecrease(t *testing.T) {
	ctx := t.Context()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	node := createVLESSRealityTestNode(t, database, "Reality generation", now)

	update := UpdateNode{
		Name: node.Name, Provider: node.Provider, Region: node.Region,
		AdapterType: node.AdapterType, PublicHost: node.PublicHost,
		PublicPort: node.PublicPort, SNI: node.SNI, Enabled: node.Enabled,
		VLESSReality: &VLESSRealitySettings{
			HandshakeServer: "www.cloudflare.com", HandshakeServerPort: 443,
			DesiredKeyGeneration: 2,
		},
		Now: now.Add(time.Second),
	}
	node, err = database.UpdateNode(ctx, node.ID, update)
	if err != nil || node.VLESSReality == nil || node.VLESSReality.DesiredKeyGeneration != 2 {
		t.Fatalf("UpdateNode(generation 2) = (%#v, %v)", node, err)
	}
	version := node.DesiredVersion
	update.VLESSReality.DesiredKeyGeneration = 1
	update.Now = now.Add(2 * time.Second)
	if _, err := database.UpdateNode(ctx, node.ID, update); !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateNode(decreased generation) error = %v", err)
	}
	node, err = database.GetNode(ctx, node.ID)
	if err != nil || node.DesiredVersion != version || node.VLESSReality == nil ||
		node.VLESSReality.DesiredKeyGeneration != 2 {
		t.Fatalf("node after rejected generation decrease = (%#v, %v)", node, err)
	}
}

func TestRotateVLESSRealityIdentityWithholdsAndRestoresSubscription(t *testing.T) {
	ctx := t.Context()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	masterKey := bytes.Repeat([]byte{0xd1}, 32)
	node := createVLESSRealityTestNode(t, database, "Reality rotation", now)
	user, credentials, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "reality-rotate", Enabled: true,
		NodeIDs: []string{node.ID}, Now: now.Add(time.Second),
	}, masterKey)
	if err != nil || len(credentials) != 1 {
		t.Fatalf("CreateUser() = (%#v, %#v, %v)", user, credentials, err)
	}
	if _, err := database.DB().ExecContext(ctx,
		"UPDATE nodes SET agent_installation_id = ? WHERE id = ?", uuid.NewString(), node.ID,
	); err != nil {
		t.Fatalf("bind Reality Agent: %v", err)
	}
	node, err = database.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	envelope, err := database.GetDesiredSnapshot(ctx, node.ID, node.DesiredVersion)
	if err != nil {
		t.Fatalf("GetDesiredSnapshot() error = %v", err)
	}
	hash, err := base64.RawURLEncoding.DecodeString(envelope.SHA256)
	if err != nil {
		t.Fatalf("decode snapshot hash: %v", err)
	}
	identity := AgentIdentity{NodeID: node.ID, AdapterType: AdapterSingBoxVLESSReality, Enabled: true}
	firstMaterial := &protocol.AppliedRealityMaterial{
		KeyGeneration: 1,
		PublicKey:     base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x51}, 32)),
		ShortID:       "0123456789abcdef",
	}
	if err := database.AcknowledgeDesiredWithMaterial(
		ctx, identity, node.DesiredVersion, hash, "applied", "", "", firstMaterial,
		now.Add(2*time.Second),
	); err != nil {
		t.Fatalf("initial Reality ACK: %v", err)
	}
	markRealityDataPlaneHealthy(t, database, node.ID, now.Add(2500*time.Millisecond))
	issued, err := database.CreateSubscriptionToken(ctx, NewSubscriptionToken{
		ID: uuid.NewString(), UserID: user.ID, Name: "rotation", Now: now.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatalf("CreateSubscriptionToken() error = %v", err)
	}
	before, err := database.ResolveSubscription(
		ctx, cryptoutil.TokenHash(issued.Secret), "uri", now.Add(4*time.Second), masterKey,
	)
	if err != nil || len(before.Endpoints) != 1 {
		t.Fatalf("subscription before rotation = (%#v, %v)", before, err)
	}
	originalCredentialID := credentials[0].Assignment.DesiredCredentialID
	originalVersion := node.DesiredVersion

	rotated, err := database.RotateVLESSRealityIdentity(
		ctx, node.ID, 1, originalVersion, now.Add(5*time.Second),
	)
	if err != nil || rotated.VLESSReality == nil ||
		rotated.VLESSReality.DesiredKeyGeneration != 2 ||
		rotated.VLESSReality.AppliedKeyGeneration != 1 ||
		rotated.DesiredVersion != originalVersion+1 || rotated.AppliedVersion != originalVersion {
		t.Fatalf("RotateVLESSRealityIdentity() = (%#v, %v)", rotated, err)
	}
	rotatedEnvelope, err := database.GetDesiredSnapshot(ctx, node.ID, rotated.DesiredVersion)
	if err != nil || rotatedEnvelope.Snapshot.VLESSReality == nil ||
		rotatedEnvelope.Snapshot.VLESSReality.KeyGeneration != 2 {
		t.Fatalf("rotated desired snapshot = (%#v, %v)", rotatedEnvelope, err)
	}
	pendingUser, err := database.GetUser(ctx, user.ID)
	if err != nil || len(pendingUser.Assignments) != 1 ||
		pendingUser.Assignments[0].State != "pending" ||
		pendingUser.Assignments[0].DesiredCredentialID != originalCredentialID {
		t.Fatalf("pending rotation assignment = (%#v, %v)", pendingUser.Assignments, err)
	}
	pendingSubscription, err := database.ResolveSubscription(
		ctx, cryptoutil.TokenHash(issued.Secret), "uri", now.Add(6*time.Second), masterKey,
	)
	if err != nil || len(pendingSubscription.Endpoints) != 0 {
		t.Fatalf("subscription during rotation = (%#v, %v)", pendingSubscription, err)
	}
	if _, err := database.RotateVLESSRealityIdentity(
		ctx, node.ID, 1, originalVersion, now.Add(7*time.Second),
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale rotation retry error = %v", err)
	}
	rotatedHash, err := base64.RawURLEncoding.DecodeString(rotatedEnvelope.SHA256)
	if err != nil {
		t.Fatalf("decode rotated hash: %v", err)
	}
	if err := database.AcknowledgeDesiredWithMaterial(
		ctx, identity, originalVersion, hash, "applied", "", "", firstMaterial,
		now.Add(8*time.Second),
	); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale generation ACK error = %v", err)
	}
	secondMaterial := &protocol.AppliedRealityMaterial{
		KeyGeneration: 2,
		PublicKey:     base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x52}, 32)),
		ShortID:       "fedcba9876543210",
	}
	if err := database.AcknowledgeDesiredWithMaterial(
		ctx, identity, rotated.DesiredVersion, rotatedHash, "applied", "", "",
		secondMaterial, now.Add(9*time.Second),
	); err != nil {
		t.Fatalf("rotated Reality ACK: %v", err)
	}
	restored, err := database.ResolveSubscription(
		ctx, cryptoutil.TokenHash(issued.Secret), "uri", now.Add(10*time.Second), masterKey,
	)
	if err != nil || len(restored.Endpoints) != 1 ||
		restored.Endpoints[0].RealityPublicKey != secondMaterial.PublicKey ||
		restored.Endpoints[0].RealityShortID != secondMaterial.ShortID {
		t.Fatalf("subscription after rotation = (%#v, %v)", restored, err)
	}
}

func TestRotateVLESSRealityIdentityRequiresSupportedAppliedNode(t *testing.T) {
	ctx := t.Context()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	reality := createVLESSRealityTestNode(t, database, "Reality pending rotation", now)
	if _, err := database.RotateVLESSRealityIdentity(ctx, reality.ID, 1, 1, now.Add(time.Second)); !errors.Is(err, ErrPending) {
		t.Fatalf("unenrolled rotation error = %v", err)
	}
	native, err := database.CreateNode(ctx, NewNode{
		ID: uuid.NewString(), Name: "Native rotation", AdapterType: "native_hysteria2",
		Enabled: true, Now: now,
	})
	if err != nil {
		t.Fatalf("CreateNode(native) error = %v", err)
	}
	if _, err := database.RotateVLESSRealityIdentity(ctx, native.ID, 1, 1, now.Add(time.Second)); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("native rotation error = %v", err)
	}
	if _, err := database.RotateVLESSRealityIdentity(ctx, uuid.NewString(), 1, 1, now.Add(time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing rotation error = %v", err)
	}
}

func TestVLESSRealityMigrationPreservesLegacyHysteriaCredential(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = legacy.Close() })
	if _, err := legacy.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("disable legacy foreign keys: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)
	`); err != nil {
		t.Fatalf("create migration table: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	for index := 1; index <= 10; index++ {
		name := fmt.Sprintf("%04d_", index)
		entries, err := migrations.Files.ReadDir(".")
		if err != nil {
			t.Fatalf("read migrations: %v", err)
		}
		migrationName := ""
		for _, entry := range entries {
			if len(entry.Name()) >= len(name) && entry.Name()[:len(name)] == name {
				migrationName = entry.Name()
				break
			}
		}
		if migrationName == "" {
			t.Fatalf("migration prefix %s not found", name)
		}
		body, err := migrations.Files.ReadFile(migrationName)
		if err != nil {
			t.Fatalf("read %s: %v", migrationName, err)
		}
		if _, err := legacy.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", migrationName, err)
		}
		if _, err := legacy.ExecContext(ctx,
			"INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)",
			migrationName, now.UnixMilli(),
		); err != nil {
			t.Fatalf("record %s: %v", migrationName, err)
		}
	}
	nodeID, userID, credentialID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	assignmentID := uuid.NewString()
	masterKey := bytes.Repeat([]byte{0xb3}, 32)
	secret := "legacy-hysteria-secret"
	verifier := sha256.Sum256([]byte(secret))
	ciphertext, err := cryptoutil.Seal(masterKey, []byte(secret), credentialAAD(
		credentialID, userID, nodeID, CredentialProtocolHY2, credentialKeyVersion,
	))
	if err != nil {
		t.Fatalf("seal legacy credential: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO nodes(id, name, adapter_type, created_at, updated_at)
		VALUES (?, 'legacy-hy2', 'native_hysteria2', ?, ?)
	`, nodeID, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatalf("seed legacy node: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO users(id, username, created_at, updated_at)
		VALUES (?, 'legacy-user', ?, ?)
	`, userID, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatalf("seed legacy user: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO user_credentials(
			id, user_id, node_id, protocol, secret_ciphertext, verifier_sha256,
			secret_fingerprint, key_version, state, created_at
		) VALUES (?, ?, ?, 'hysteria2', ?, ?, 'fp_legacy', 1, 'staged', ?)
	`, credentialID, userID, nodeID, ciphertext, verifier[:], now.UnixMilli()); err != nil {
		t.Fatalf("seed legacy credential: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO node_user_assignments(
			id, node_id, user_id, desired_credential_id, state, created_at, updated_at
		) VALUES (?, ?, ?, ?, 'pending', ?, ?)
	`, assignmentID, nodeID, userID, credentialID, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatalf("seed legacy assignment: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open(upgrade) error = %v", err)
	}
	defer database.Close()
	revealed, err := database.RevealAssignmentCredential(ctx, userID, nodeID, masterKey)
	if err != nil || revealed.Secret != secret ||
		revealed.Assignment.CredentialProtocol != CredentialProtocolHY2 {
		t.Fatalf("RevealAssignmentCredential(upgraded) = (%#v, %v)", revealed, err)
	}
	rows, err := database.DB().QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("VLESS Reality migration left a foreign-key violation")
	}
}

func createVLESSRealityTestNode(
	t *testing.T,
	database *Store,
	name string,
	now time.Time,
) Node {
	t.Helper()
	node, err := database.CreateNode(t.Context(), NewNode{
		ID: uuid.NewString(), Name: name, AdapterType: AdapterSingBoxVLESSReality,
		PublicHost: "reality.example.com", PublicPort: 8443,
		SNI: "www.cloudflare.com", Enabled: true,
		VLESSReality: &VLESSRealitySettings{
			HandshakeServer: "www.cloudflare.com", HandshakeServerPort: 443,
			DesiredKeyGeneration: 1,
		},
		Now: now,
	})
	if err != nil {
		t.Fatalf("CreateNode(%s) error = %v", name, err)
	}
	return node
}

func markRealityDataPlaneHealthy(t *testing.T, database *Store, nodeID string, now time.Time) {
	t.Helper()
	if _, err := database.DB().ExecContext(t.Context(), `
		UPDATE nodes
		SET adapter_status = 'compatible', adapter_version = '1.13.18',
		    adapter_error_code = '', adapter_last_probed_at = ?,
		    core_name = 'sing-box', core_version = '1.13.18', core_running = 1,
		    updated_at = ?
		WHERE id = ?
	`, now.UnixMilli(), now.UnixMilli(), nodeID); err != nil {
		t.Fatalf("mark Reality data plane healthy: %v", err)
	}
}
