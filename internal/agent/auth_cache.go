package agent

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/Sen62455/PolyFleet/internal/protocol"
)

const (
	authCacheSchemaVersion = 1
	maxCachedAuthUsers     = 10000
)

type cachedAuthUser struct {
	ID             string     `json:"id"`
	Username       string     `json:"username"`
	CredentialRef  string     `json:"credential_ref"`
	Fingerprint    string     `json:"fingerprint"`
	VerifierSHA256 string     `json:"verifier_sha256"`
	Enabled        bool       `json:"enabled"`
	ExpiresAt      *time.Time `json:"expires_at"`
	QuotaState     string     `json:"quota_state"`
}

type authCacheDocument struct {
	SchemaVersion  int              `json:"schema_version"`
	NodeID         string           `json:"node_id"`
	DesiredVersion int64            `json:"desired_version"`
	SnapshotHash   string           `json:"snapshot_hash"`
	AppliedAt      time.Time        `json:"applied_at"`
	Users          []cachedAuthUser `json:"users"`
}

type authEntry struct {
	ID         string
	Verifier   [sha256.Size]byte
	Enabled    bool
	ExpiresAt  *time.Time
	QuotaState string
}

type AuthCache struct {
	path     string
	mu       sync.RWMutex
	document authCacheDocument
	entries  map[[sha256.Size]byte]authEntry
	dummy    [sha256.Size]byte
}

func LoadAuthCache(path string) (*AuthCache, error) {
	if path == "" {
		return nil, errors.New("native auth cache path is required")
	}
	cache := &AuthCache{
		path: path,
		document: authCacheDocument{
			SchemaVersion: authCacheSchemaVersion,
			Users:         []cachedAuthUser{},
		},
		entries: make(map[[sha256.Size]byte]authEntry),
		dummy:   sha256.Sum256([]byte("hyfleet-native-auth-dummy-verifier")),
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cache, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read native auth cache: %w", err)
	}
	var document authCacheDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse native auth cache: %w", err)
	}
	entries, err := buildAuthIndex(document)
	if err != nil {
		return nil, fmt.Errorf("validate native auth cache: %w", err)
	}
	cache.document = document
	cache.entries = entries
	return cache, nil
}

func (cache *AuthCache) Apply(
	snapshot protocol.DesiredSnapshot,
	snapshotHash string,
	appliedAt time.Time,
) error {
	cache.mu.RLock()
	currentNodeID := cache.document.NodeID
	currentVersion := cache.document.DesiredVersion
	currentHash := cache.document.SnapshotHash
	cache.mu.RUnlock()
	if currentNodeID != "" && currentNodeID != snapshot.NodeID {
		return errors.New("native auth snapshot belongs to another node")
	}
	if snapshot.Version < currentVersion ||
		(snapshot.Version == currentVersion && currentHash != "" && currentHash != snapshotHash) {
		return errors.New("native auth snapshot is stale or conflicts with cached version")
	}
	document := authCacheDocument{
		SchemaVersion:  authCacheSchemaVersion,
		NodeID:         snapshot.NodeID,
		DesiredVersion: snapshot.Version,
		SnapshotHash:   snapshotHash,
		AppliedAt:      appliedAt.UTC(),
		Users:          make([]cachedAuthUser, 0, len(snapshot.Users)),
	}
	for _, user := range snapshot.Users {
		expiresAt := user.ExpiresAt
		if expiresAt != nil {
			normalized := expiresAt.UTC()
			expiresAt = &normalized
		}
		document.Users = append(document.Users, cachedAuthUser{
			ID: user.ID, Username: user.Username, CredentialRef: user.Credential.Ref,
			Fingerprint:    user.Credential.Fingerprint,
			VerifierSHA256: user.Credential.VerifierSHA256, Enabled: user.Enabled,
			ExpiresAt: expiresAt, QuotaState: user.QuotaState,
		})
	}
	entries, err := buildAuthIndex(document)
	if err != nil {
		return err
	}
	if err := saveAuthCache(cache.path, document); err != nil {
		return err
	}
	cache.mu.Lock()
	cache.document = document
	cache.entries = entries
	cache.mu.Unlock()
	return nil
}

func (cache *AuthCache) Authenticate(secret string, now time.Time) (string, bool) {
	verifier := sha256.Sum256([]byte(secret))
	cache.mu.RLock()
	entry, found := cache.entries[verifier]
	dummy := cache.dummy
	cache.mu.RUnlock()
	if !found {
		_ = subtle.ConstantTimeCompare(verifier[:], dummy[:])
		return "", false
	}
	if subtle.ConstantTimeCompare(verifier[:], entry.Verifier[:]) != 1 || !entry.Enabled {
		return "", false
	}
	if entry.ExpiresAt != nil && !now.UTC().Before(*entry.ExpiresAt) {
		return "", false
	}
	if entry.QuotaState == "limited" {
		return "", false
	}
	return entry.ID, true
}

func (cache *AuthCache) Metadata() (string, int64, int) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.document.NodeID, cache.document.DesiredVersion, len(cache.entries)
}

func buildAuthIndex(document authCacheDocument) (map[[sha256.Size]byte]authEntry, error) {
	if document.SchemaVersion != authCacheSchemaVersion {
		return nil, errors.New("unsupported auth cache schema version")
	}
	if document.DesiredVersion < 0 || len(document.Users) > maxCachedAuthUsers {
		return nil, errors.New("auth cache version or user count is invalid")
	}
	if document.DesiredVersion > 0 {
		if _, err := uuid.Parse(document.NodeID); err != nil {
			return nil, errors.New("auth cache node ID is invalid")
		}
		hash, err := base64.RawURLEncoding.DecodeString(document.SnapshotHash)
		if err != nil || len(hash) != sha256.Size {
			return nil, errors.New("auth cache snapshot hash is invalid")
		}
	}
	entries := make(map[[sha256.Size]byte]authEntry, len(document.Users))
	seenUsers := make(map[string]struct{}, len(document.Users))
	for index, user := range document.Users {
		if _, err := uuid.Parse(user.ID); err != nil || len(user.Username) < 1 || len(user.Username) > 64 {
			return nil, fmt.Errorf("auth cache user %d has invalid identity", index)
		}
		if _, err := uuid.Parse(user.CredentialRef); err != nil ||
			user.Fingerprint == "" || len(user.Fingerprint) > 64 {
			return nil, fmt.Errorf("auth cache user %d has invalid credential metadata", index)
		}
		if user.QuotaState != "unlimited" && user.QuotaState != "active" && user.QuotaState != "limited" {
			return nil, fmt.Errorf("auth cache user %d has invalid quota state", index)
		}
		decoded, err := base64.RawURLEncoding.DecodeString(user.VerifierSHA256)
		if err != nil || len(decoded) != sha256.Size {
			return nil, fmt.Errorf("auth cache user %d has invalid verifier", index)
		}
		var verifier [sha256.Size]byte
		copy(verifier[:], decoded)
		if _, exists := entries[verifier]; exists {
			return nil, errors.New("auth cache contains a duplicate verifier")
		}
		if _, exists := seenUsers[user.ID]; exists {
			return nil, errors.New("auth cache contains a duplicate user")
		}
		seenUsers[user.ID] = struct{}{}
		entries[verifier] = authEntry{
			ID: user.ID, Verifier: verifier, Enabled: user.Enabled,
			ExpiresAt: user.ExpiresAt, QuotaState: user.QuotaState,
		}
	}
	return entries, nil
}

func saveAuthCache(path string, document authCacheDocument) error {
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode native auth cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create native auth cache directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".auth-cache-*")
	if err != nil {
		return fmt.Errorf("create temporary native auth cache: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set native auth cache permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write native auth cache: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync native auth cache: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close native auth cache: %w", err)
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return fmt.Errorf("replace native auth cache: %w", err)
	}
	return nil
}
