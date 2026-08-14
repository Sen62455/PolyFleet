package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
)

func TestSubscriptionTokenAndAppliedCredentialLifecycle(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	masterKey := bytes.Repeat([]byte{0x51}, 32)
	certificateFingerprint := strings.TrimSuffix(strings.Repeat("AB:", 32), ":")
	publicKeyPin := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	node, err := database.CreateNode(ctx, NewNode{
		ID: uuid.NewString(), Name: "Tokyo edge", AdapterType: "native_hysteria2",
		PublicHost: "hy2.example.com", PublicPort: 8443, SNI: "edge.example.com",
		TLSCertFingerprint: certificateFingerprint, TLSPublicKeySHA256: publicKeyPin,
		Enabled: true, Now: now,
	})
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	user, credentials, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "subscriber", Enabled: true,
		NodeIDs: []string{node.ID}, Now: now.Add(time.Second),
	}, masterKey)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	firstSecret := credentials[0].Secret
	issued, err := database.CreateSubscriptionToken(ctx, NewSubscriptionToken{
		ID: uuid.NewString(), UserID: user.ID, Name: "primary",
		Now: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("CreateSubscriptionToken() error = %v", err)
	}
	if !bytes.HasPrefix([]byte(issued.Secret), []byte("hys_")) ||
		len(issued.Token.AllowedFormats) != 4 {
		t.Fatalf("unexpected issued token: %#v", issued.Token)
	}
	var storedHash []byte
	var storedPrefix string
	if err := database.DB().QueryRowContext(ctx, `
		SELECT token_hash, token_prefix FROM subscription_tokens WHERE id = ?
	`, issued.Token.ID).Scan(&storedHash, &storedPrefix); err != nil {
		t.Fatalf("read stored token: %v", err)
	}
	if !bytes.Equal(storedHash, cryptoutil.TokenHash(issued.Secret)) || storedPrefix == issued.Secret {
		t.Fatal("subscription token was not stored as a one-way hash and short prefix")
	}

	beforeApply, err := database.ResolveSubscription(
		ctx, cryptoutil.TokenHash(issued.Secret), "base64", now.Add(3*time.Second), masterKey,
	)
	if err != nil || len(beforeApply.Endpoints) != 0 {
		t.Fatalf("pending subscription endpoints = %#v, error = %v", beforeApply.Endpoints, err)
	}
	ackCurrentDesired(t, database, node.ID, now.Add(4*time.Second))
	applied, err := database.ResolveSubscription(
		ctx, cryptoutil.TokenHash(issued.Secret), "uri", now.Add(5*time.Second), masterKey,
	)
	if err != nil || len(applied.Endpoints) != 1 || applied.Endpoints[0].Credential != firstSecret ||
		applied.Endpoints[0].PublicHost != "hy2.example.com" || applied.Endpoints[0].PublicPort != 8443 ||
		applied.Endpoints[0].TLSCertFingerprint != certificateFingerprint ||
		applied.Endpoints[0].TLSPublicKeySHA256 != publicKeyPin {
		t.Fatalf("applied subscription = %#v, error = %v", applied, err)
	}

	rotatedUser, rotated, err := database.RotateAssignmentCredential(
		ctx, user.ID, node.ID, now.Add(6*time.Second), masterKey,
	)
	if err != nil || rotated.Secret == "" || rotated.Secret == firstSecret ||
		len(rotatedUser.Assignments) != 1 || rotatedUser.Assignments[0].State != "pending" {
		t.Fatalf("RotateAssignmentCredential() = (%#v, %#v, %v)", rotatedUser, rotated, err)
	}
	duringRotation, err := database.ResolveSubscription(
		ctx, cryptoutil.TokenHash(issued.Secret), "base64", now.Add(7*time.Second), masterKey,
	)
	if err != nil || len(duringRotation.Endpoints) != 0 {
		t.Fatalf("rotation-pending endpoints = %#v, error = %v", duringRotation.Endpoints, err)
	}
	ackCurrentDesired(t, database, node.ID, now.Add(8*time.Second))
	afterRotation, err := database.ResolveSubscription(
		ctx, cryptoutil.TokenHash(issued.Secret), "sing-box", now.Add(9*time.Second), masterKey,
	)
	if err != nil || len(afterRotation.Endpoints) != 1 ||
		afterRotation.Endpoints[0].Credential != rotated.Secret {
		t.Fatalf("rotated subscription = %#v, error = %v", afterRotation, err)
	}
	var oldState, newState string
	if err := database.DB().QueryRowContext(ctx, `
		SELECT state FROM user_credentials WHERE id = ?
	`, credentials[0].Assignment.DesiredCredentialID).Scan(&oldState); err != nil {
		t.Fatalf("read old credential state: %v", err)
	}
	if err := database.DB().QueryRowContext(ctx, `
		SELECT state FROM user_credentials WHERE id = ?
	`, rotated.Assignment.DesiredCredentialID).Scan(&newState); err != nil {
		t.Fatalf("read new credential state: %v", err)
	}
	if oldState != "retired" || newState != "applied" {
		t.Fatalf("credential states = %q, %q", oldState, newState)
	}

	restricted, err := database.CreateSubscriptionToken(ctx, NewSubscriptionToken{
		ID: uuid.NewString(), UserID: user.ID, Name: "clash only",
		AllowedFormats: []string{"clash"}, Now: now.Add(10 * time.Second),
	})
	if err != nil {
		t.Fatalf("CreateSubscriptionToken(restricted) error = %v", err)
	}
	if _, err := database.ResolveSubscription(
		ctx, cryptoutil.TokenHash(restricted.Secret), "base64", now.Add(11*time.Second), masterKey,
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("ResolveSubscription(disallowed format) error = %v", err)
	}

	if err := database.RevokeSubscriptionToken(ctx, user.ID, issued.Token.ID, now.Add(12*time.Second)); err != nil {
		t.Fatalf("RevokeSubscriptionToken() error = %v", err)
	}
	if err := database.RevokeSubscriptionToken(ctx, user.ID, issued.Token.ID, now.Add(13*time.Second)); err != nil {
		t.Fatalf("RevokeSubscriptionToken(idempotent) error = %v", err)
	}
	if _, err := database.ResolveSubscription(
		ctx, cryptoutil.TokenHash(issued.Secret), "uri", now.Add(14*time.Second), masterKey,
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("ResolveSubscription(revoked) error = %v", err)
	}
}

func TestAppliedAssignmentRemainsUsableAfterLaterNodeSnapshot(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	now := time.Now().UTC().Truncate(time.Millisecond)
	masterKey := bytes.Repeat([]byte{0x58}, 32)
	node, err := database.CreateNode(ctx, NewNode{
		ID: uuid.NewString(), Name: "shared-node", AdapterType: "native_hysteria2",
		PublicHost: "shared.example.com", PublicPort: 443, Enabled: true, Now: now,
	})
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	primary, _, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "primary", Enabled: true,
		NodeIDs: []string{node.ID}, Now: now.Add(time.Second),
	}, masterKey)
	if err != nil {
		t.Fatalf("CreateUser(primary) error = %v", err)
	}
	ackCurrentDesired(t, database, node.ID, now.Add(2*time.Second))
	issued, err := database.CreateSubscriptionToken(ctx, NewSubscriptionToken{
		ID: uuid.NewString(), UserID: primary.ID, Name: "primary",
		Now: now.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatalf("CreateSubscriptionToken() error = %v", err)
	}

	if _, _, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "secondary", Enabled: true,
		NodeIDs: []string{node.ID}, Now: now.Add(4 * time.Second),
	}, masterKey); err != nil {
		t.Fatalf("CreateUser(secondary) error = %v", err)
	}
	ackCurrentDesired(t, database, node.ID, now.Add(5*time.Second))

	refreshed, err := database.GetUser(ctx, primary.ID)
	if err != nil || len(refreshed.Assignments) != 1 {
		t.Fatalf("GetUser(primary) = %#v, error = %v", refreshed, err)
	}
	assignment := refreshed.Assignments[0]
	if assignment.State != "applied" || assignment.AppliedVersion <= assignment.DesiredVersion ||
		!assignment.SubscriptionEligible || assignment.SubscriptionReason != "" {
		t.Fatalf("later node snapshot made assignment unavailable: %#v", assignment)
	}

	subscription, err := database.ResolveSubscription(
		ctx, cryptoutil.TokenHash(issued.Secret), "clash", now.Add(6*time.Second), masterKey,
	)
	if err != nil || len(subscription.Endpoints) != 1 || subscription.Endpoints[0].NodeID != node.ID {
		t.Fatalf("ResolveSubscription() = %#v, error = %v", subscription, err)
	}
	if _, _, err := database.RotateAssignmentCredential(
		ctx, primary.ID, node.ID, now.Add(7*time.Second), masterKey,
	); err != nil {
		t.Fatalf("RotateAssignmentCredential(after later snapshot) error = %v", err)
	}
}

func TestGlobalCredentialRotationIsAtomicWhenAssignmentIsPending(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	masterKey := bytes.Repeat([]byte{0x61}, 32)
	nodeOne := createTestNode(t, database, "one", "native_hysteria2", now)
	nodeTwo := createTestNode(t, database, "two", "native_hysteria2", now)
	user, _, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "rotate-all", Enabled: true,
		NodeIDs: []string{nodeOne.ID, nodeTwo.ID}, Now: now.Add(time.Second),
	}, masterKey)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	ackCurrentDesired(t, database, nodeOne.ID, now.Add(2*time.Second))
	ackCurrentDesired(t, database, nodeTwo.ID, now.Add(2*time.Second))
	if _, _, err := database.RotateAssignmentCredential(
		ctx, user.ID, nodeOne.ID, now.Add(3*time.Second), masterKey,
	); err != nil {
		t.Fatalf("RotateAssignmentCredential() error = %v", err)
	}
	var before int
	if err := database.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM user_credentials WHERE user_id = ?
	`, user.ID).Scan(&before); err != nil {
		t.Fatalf("count credentials before global rotation: %v", err)
	}
	if _, _, err := database.RotateUserCredentials(
		ctx, user.ID, now.Add(4*time.Second), masterKey,
	); !errors.Is(err, ErrPending) {
		t.Fatalf("RotateUserCredentials(pending) error = %v", err)
	}
	var after int
	if err := database.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM user_credentials WHERE user_id = ?
	`, user.ID).Scan(&after); err != nil {
		t.Fatalf("count credentials after rejected rotation: %v", err)
	}
	if after != before {
		t.Fatalf("rejected global rotation created credentials: before=%d after=%d", before, after)
	}
	ackCurrentDesired(t, database, nodeOne.ID, now.Add(5*time.Second))
	rotatedUser, rotated, err := database.RotateUserCredentials(
		ctx, user.ID, now.Add(6*time.Second), masterKey,
	)
	if err != nil || len(rotated) != 2 || len(rotatedUser.Assignments) != 2 {
		t.Fatalf("RotateUserCredentials() = (%#v, %d credentials, %v)", rotatedUser, len(rotated), err)
	}
}

func TestSubscriptionEligibilityRejectsUnavailableUsersAndEndpoints(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	masterKey := bytes.Repeat([]byte{0x71}, 32)
	node, err := database.CreateNode(ctx, NewNode{
		ID: uuid.NewString(), Name: "eligible", AdapterType: "native_hysteria2",
		PublicHost: "eligible.example.com", PublicPort: 443, Enabled: true, Now: now,
	})
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	user, _, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "eligibility", Enabled: true,
		NodeIDs: []string{node.ID}, Now: now.Add(time.Second),
	}, masterKey)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	ackCurrentDesired(t, database, node.ID, now.Add(2*time.Second))
	issued, err := database.CreateSubscriptionToken(ctx, NewSubscriptionToken{
		ID: uuid.NewString(), UserID: user.ID, Name: "eligibility", Now: now.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatalf("CreateSubscriptionToken() error = %v", err)
	}
	resolve := func(at time.Time) (Subscription, error) {
		return database.ResolveSubscription(
			ctx, cryptoutil.TokenHash(issued.Secret), "uri", at, masterKey,
		)
	}
	if subscription, err := resolve(now.Add(4 * time.Second)); err != nil || len(subscription.Endpoints) != 1 {
		t.Fatalf("initial eligible subscription = %#v, error = %v", subscription, err)
	}

	for _, test := range []struct {
		name      string
		statement string
		arguments []any
		wantAuth  bool
	}{
		{name: "disabled user", statement: "UPDATE users SET enabled = 0 WHERE id = ?", arguments: []any{user.ID}, wantAuth: false},
		{name: "expired user", statement: "UPDATE users SET expires_at = ? WHERE id = ?", arguments: []any{now.UnixMilli(), user.ID}, wantAuth: false},
		{name: "global quota", statement: "UPDATE users SET quota_state = 'limited' WHERE id = ?", arguments: []any{user.ID}, wantAuth: false},
		{name: "disabled assignment", statement: "UPDATE node_user_assignments SET enabled = 0 WHERE user_id = ?", arguments: []any{user.ID}, wantAuth: true},
		{name: "assignment quota", statement: "UPDATE node_user_assignments SET quota_state = 'limited' WHERE user_id = ?", arguments: []any{user.ID}, wantAuth: true},
		{name: "failed assignment", statement: "UPDATE node_user_assignments SET state = 'failed' WHERE user_id = ?", arguments: []any{user.ID}, wantAuth: true},
		{name: "degraded node", statement: "UPDATE nodes SET status = 'degraded' WHERE id = ?", arguments: []any{node.ID}, wantAuth: true},
		{name: "missing endpoint", statement: "UPDATE nodes SET public_host = '' WHERE id = ?", arguments: []any{node.ID}, wantAuth: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := database.DB().ExecContext(ctx, `
				UPDATE users SET enabled = 1, expires_at = NULL, quota_state = 'unlimited'
				WHERE id = ?
			`, user.ID); err != nil {
				t.Fatalf("reset eligibility user: %v", err)
			}
			if _, err := database.DB().ExecContext(ctx, `
				UPDATE node_user_assignments
				SET enabled = 1, quota_state = 'unlimited', state = 'applied'
				WHERE user_id = ?
			`, user.ID); err != nil {
				t.Fatalf("reset eligibility assignment: %v", err)
			}
			if _, err := database.DB().ExecContext(ctx, `
				UPDATE nodes SET status = 'online', public_host = 'eligible.example.com'
				WHERE id = ?
			`, node.ID); err != nil {
				t.Fatalf("reset eligibility node: %v", err)
			}
			if _, err := database.DB().ExecContext(ctx, test.statement, test.arguments...); err != nil {
				t.Fatalf("apply eligibility state: %v", err)
			}
			subscription, err := resolve(now.Add(5 * time.Second))
			if test.wantAuth {
				if err != nil || len(subscription.Endpoints) != 0 {
					t.Fatalf("filtered subscription = %#v, error = %v", subscription, err)
				}
			} else if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("ResolveSubscription() error = %v, want ErrUnauthorized", err)
			}
		})
	}

	expiresAt := now.Add(10 * time.Second)
	expired, err := database.CreateSubscriptionToken(ctx, NewSubscriptionToken{
		ID: uuid.NewString(), UserID: user.ID, Name: "expires", ExpiresAt: &expiresAt,
		Now: now.Add(6 * time.Second),
	})
	if err != nil {
		t.Fatalf("CreateSubscriptionToken(expiring) error = %v", err)
	}
	if _, err := database.ResolveSubscription(
		ctx, cryptoutil.TokenHash(expired.Secret), "uri", expiresAt, masterKey,
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("ResolveSubscription(expired token) error = %v", err)
	}
}

func ackCurrentDesired(t *testing.T, database *Store, nodeID string, now time.Time) {
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
	if err := database.AcknowledgeDesired(context.Background(), AgentIdentity{
		NodeID: nodeID, AdapterType: "native_hysteria2", Enabled: true,
	}, node.DesiredVersion, hash, "applied", "", "", now); err != nil {
		t.Fatalf("AcknowledgeDesired() error = %v", err)
	}
}
