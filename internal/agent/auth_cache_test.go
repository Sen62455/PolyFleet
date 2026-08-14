package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/google/uuid"
)

func TestNativeAuthCachePersistsAndEnforcesStateOffline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth-cache.json")
	cache, err := LoadAuthCache(path)
	if err != nil {
		t.Fatalf("LoadAuthCache() error = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	activeSecret := "active-generated-secret"
	disabledSecret := "disabled-generated-secret"
	expiredSecret := "expired-generated-secret"
	snapshot := protocol.DesiredSnapshot{
		SchemaVersion: 1, NodeID: uuid.NewString(), Version: 4,
		Adapter: "native_hysteria2", GeneratedAt: now,
		Users: []protocol.DesiredUser{
			desiredAuthUser("active", activeSecret, true, nil, "unlimited"),
			desiredAuthUser("disabled", disabledSecret, false, nil, "unlimited"),
			desiredAuthUser("expired", expiredSecret, true, timePointer(now.Add(-time.Second)), "unlimited"),
		},
	}
	hash := snapshotHash(snapshot)
	if err := cache.Apply(snapshot, hash, now); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	activeID := snapshot.Users[0].ID
	if id, ok := cache.Authenticate(activeSecret, now); !ok || id != activeID {
		t.Fatalf("active authentication = (%q, %v)", id, ok)
	}
	for _, secret := range []string{"wrong", disabledSecret, expiredSecret} {
		if id, ok := cache.Authenticate(secret, now); ok || id != "" {
			t.Fatalf("denied authentication for %q = (%q, %v)", secret, id, ok)
		}
	}

	reloaded, err := LoadAuthCache(path)
	if err != nil {
		t.Fatalf("LoadAuthCache(restart) error = %v", err)
	}
	if id, ok := reloaded.Authenticate(activeSecret, now.Add(time.Hour)); !ok || id != activeID {
		t.Fatalf("offline restart authentication = (%q, %v)", id, ok)
	}

	rollback := snapshot
	rollback.Version = 3
	if err := reloaded.Apply(rollback, snapshotHash(rollback), now); err == nil {
		t.Fatal("Apply() accepted a rollback snapshot")
	}
	conflict := snapshot
	conflict.Users = nil
	if err := reloaded.Apply(conflict, snapshotHash(conflict), now); err == nil {
		t.Fatal("Apply() accepted a conflicting snapshot at the same version")
	}
}

func TestHysteriaHTTPAuthenticationContract(t *testing.T) {
	cache, err := LoadAuthCache(filepath.Join(t.TempDir(), "auth-cache.json"))
	if err != nil {
		t.Fatalf("LoadAuthCache() error = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	secret := "http-contract-generated-secret"
	user := desiredAuthUser("http-user", secret, true, nil, "unlimited")
	snapshot := protocol.DesiredSnapshot{
		SchemaVersion: 1, NodeID: uuid.NewString(), Version: 1,
		Adapter: "native_hysteria2", Users: []protocol.DesiredUser{user}, GeneratedAt: now,
	}
	if err := cache.Apply(snapshot, snapshotHash(snapshot), now); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	handler := newHysteriaAuthHandler(cache, "/hysteria/auth", func() time.Time { return now })

	accepted := authRequest(t, handler, `{"addr":"192.0.2.1:1234","auth":"`+secret+`","tx":1024}`)
	if accepted.Code != http.StatusOK {
		t.Fatalf("accepted status = %d", accepted.Code)
	}
	var acceptedBody hysteriaAuthResponse
	if err := json.Unmarshal(accepted.Body.Bytes(), &acceptedBody); err != nil ||
		!acceptedBody.OK || acceptedBody.ID != user.ID {
		t.Fatalf("accepted response = %#v, error = %v", acceptedBody, err)
	}

	for name, body := range map[string]string{
		"wrong secret": `{"addr":"192.0.2.1:1234","auth":"wrong","tx":1024}`,
		"malformed":    `{`,
		"negative tx":  `{"addr":"192.0.2.1:1234","auth":"` + secret + `","tx":-1}`,
		"too large":    `{"auth":"` + strings.Repeat("x", maxHysteriaAuthBodyBytes) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			denied := authRequest(t, handler, body)
			if denied.Code != http.StatusOK {
				t.Fatalf("denied status = %d, want 200", denied.Code)
			}
			var result hysteriaAuthResponse
			if err := json.Unmarshal(denied.Body.Bytes(), &result); err != nil || result.OK || result.ID != "" {
				t.Fatalf("denied response = %#v, error = %v", result, err)
			}
		})
	}
}

func desiredAuthUser(
	username, secret string,
	enabled bool,
	expiresAt *time.Time,
	quotaState string,
) protocol.DesiredUser {
	verifier := sha256.Sum256([]byte(secret))
	return protocol.DesiredUser{
		ID: uuid.NewString(), Username: username, Enabled: enabled,
		ExpiresAt: expiresAt, QuotaState: quotaState,
		Credential: protocol.DesiredCredential{
			Ref: uuid.NewString(), Fingerprint: "fp_test",
			VerifierSHA256: base64.RawURLEncoding.EncodeToString(verifier[:]),
		},
	}
}

func snapshotHash(snapshot protocol.DesiredSnapshot) string {
	canonical, _ := json.Marshal(snapshot)
	hash := sha256.Sum256(canonical)
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func timePointer(value time.Time) *time.Time { return &value }

func authRequest(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/hysteria/auth", bytes.NewBufferString(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
