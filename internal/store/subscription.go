package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
)

const subscriptionTokenMarker = "hys_"

var subscriptionFormatOrder = []string{"uri", "base64", "clash", "sing-box"}

type SubscriptionToken struct {
	ID             string
	UserID         string
	Name           string
	TokenPrefix    string
	AllowedFormats []string
	ExpiresAt      *time.Time
	LastUsedAt     *time.Time
	RevokedAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type NewSubscriptionToken struct {
	ID             string
	UserID         string
	Name           string
	AllowedFormats []string
	ExpiresAt      *time.Time
	Now            time.Time
}

type IssuedSubscriptionToken struct {
	Token  SubscriptionToken
	Secret string
}

type SubscriptionEndpoint struct {
	NodeID             string
	NodeName           string
	AdapterType        string
	Protocol           string
	PublicHost         string
	PublicPort         int
	SNI                string
	TLSInsecure        bool
	TLSCertFingerprint string
	TLSPublicKeySHA256 string
	Credential         string
	Flow               string
	Network            string
	RealityPublicKey   string
	RealityShortID     string
}

type Subscription struct {
	UserID               string
	Username             string
	TrafficUploadBytes   int64
	TrafficDownloadBytes int64
	TrafficLimitBytes    int64
	ExpiresAt            *time.Time
	Endpoints            []SubscriptionEndpoint
}

func SupportedSubscriptionFormats() []string {
	return append([]string(nil), subscriptionFormatOrder...)
}

func NormalizeSubscriptionFormats(formats []string) ([]string, error) {
	if len(formats) == 0 {
		return SupportedSubscriptionFormats(), nil
	}
	requested := make(map[string]struct{}, len(formats))
	for _, format := range formats {
		format = strings.TrimSpace(strings.ToLower(format))
		if format == "" {
			return nil, errors.New("subscription format cannot be empty")
		}
		requested[format] = struct{}{}
	}
	result := make([]string, 0, len(requested))
	for _, format := range subscriptionFormatOrder {
		if _, ok := requested[format]; ok {
			result = append(result, format)
			delete(requested, format)
		}
	}
	if len(requested) != 0 {
		return nil, errors.New("unsupported subscription format")
	}
	return result, nil
}

func (s *Store) ListSubscriptionTokens(ctx context.Context, userID string) ([]SubscriptionToken, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM users WHERE id = ? AND archived_at IS NULL
	`, userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find subscription user: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, name, token_prefix, allowed_formats, expires_at,
		       last_used_at, revoked_at, created_at, updated_at
		FROM subscription_tokens WHERE user_id = ?
		ORDER BY created_at DESC, id DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list subscription tokens: %w", err)
	}
	defer rows.Close()
	result := make([]SubscriptionToken, 0)
	for rows.Next() {
		token, err := scanSubscriptionToken(rows)
		if err != nil {
			return nil, fmt.Errorf("scan subscription token: %w", err)
		}
		result = append(result, token)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscription tokens: %w", err)
	}
	return result, nil
}

func (s *Store) CreateSubscriptionToken(
	ctx context.Context,
	input NewSubscriptionToken,
) (IssuedSubscriptionToken, error) {
	formats, err := NormalizeSubscriptionFormats(input.AllowedFormats)
	if err != nil {
		return IssuedSubscriptionToken{}, err
	}
	secret, err := newSubscriptionToken()
	if err != nil {
		return IssuedSubscriptionToken{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IssuedSubscriptionToken{}, fmt.Errorf("begin create subscription token: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM users WHERE id = ? AND archived_at IS NULL
	`, input.UserID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IssuedSubscriptionToken{}, ErrNotFound
		}
		return IssuedSubscriptionToken{}, fmt.Errorf("find subscription user: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO subscription_tokens(
			id, user_id, name, token_hash, token_prefix, allowed_formats,
			expires_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.ID, input.UserID, input.Name, cryptoutil.TokenHash(secret),
		tokenPrefix(secret), strings.Join(formats, ","), nullableUnixMilli(input.ExpiresAt),
		input.Now.UnixMilli(), input.Now.UnixMilli())
	if err != nil {
		return IssuedSubscriptionToken{}, fmt.Errorf("insert subscription token: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return IssuedSubscriptionToken{}, fmt.Errorf("commit subscription token: %w", err)
	}
	return IssuedSubscriptionToken{Token: SubscriptionToken{
		ID: input.ID, UserID: input.UserID, Name: input.Name,
		TokenPrefix: tokenPrefix(secret), AllowedFormats: formats,
		ExpiresAt: input.ExpiresAt, CreatedAt: input.Now, UpdatedAt: input.Now,
	}, Secret: secret}, nil
}

func (s *Store) RotateSubscriptionToken(
	ctx context.Context,
	userID, tokenID string,
	now time.Time,
) (IssuedSubscriptionToken, error) {
	secret, err := newSubscriptionToken()
	if err != nil {
		return IssuedSubscriptionToken{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IssuedSubscriptionToken{}, fmt.Errorf("begin rotate subscription token: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var name, formatsValue string
	var expiresAt, revokedAt sql.NullInt64
	var createdAt int64
	err = tx.QueryRowContext(ctx, `
		SELECT t.name, t.allowed_formats, t.expires_at, t.revoked_at, t.created_at
		FROM subscription_tokens t
		JOIN users u ON u.id = t.user_id AND u.archived_at IS NULL
		WHERE t.id = ? AND t.user_id = ?
	`, tokenID, userID).Scan(&name, &formatsValue, &expiresAt, &revokedAt, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return IssuedSubscriptionToken{}, ErrNotFound
	}
	if err != nil {
		return IssuedSubscriptionToken{}, fmt.Errorf("read subscription token: %w", err)
	}
	if revokedAt.Valid {
		return IssuedSubscriptionToken{}, ErrConflict
	}
	if expiresAt.Valid && !now.Before(unixTime(expiresAt.Int64)) {
		return IssuedSubscriptionToken{}, ErrExpired
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE subscription_tokens
		SET token_hash = ?, token_prefix = ?, last_used_at = NULL, updated_at = ?
		WHERE id = ? AND user_id = ?
	`, cryptoutil.TokenHash(secret), tokenPrefix(secret), now.UnixMilli(), tokenID, userID); err != nil {
		return IssuedSubscriptionToken{}, fmt.Errorf("rotate subscription token: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return IssuedSubscriptionToken{}, fmt.Errorf("commit rotated subscription token: %w", err)
	}
	formats, err := parseStoredFormats(formatsValue)
	if err != nil {
		return IssuedSubscriptionToken{}, err
	}
	return IssuedSubscriptionToken{Token: SubscriptionToken{
		ID: tokenID, UserID: userID, Name: name, TokenPrefix: tokenPrefix(secret),
		AllowedFormats: formats, ExpiresAt: nullableTime(expiresAt),
		CreatedAt: unixTime(createdAt), UpdatedAt: now,
	}, Secret: secret}, nil
}

func (s *Store) RevokeSubscriptionToken(
	ctx context.Context,
	userID, tokenID string,
	now time.Time,
) error {
	var exists int
	if err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM subscription_tokens t
		JOIN users u ON u.id = t.user_id AND u.archived_at IS NULL
		WHERE t.id = ? AND t.user_id = ?
	`, tokenID, userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("find subscription token: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE subscription_tokens
		SET revoked_at = COALESCE(revoked_at, ?), updated_at = ?
		WHERE id = ? AND user_id = ?
	`, now.UnixMilli(), now.UnixMilli(), tokenID, userID); err != nil {
		return fmt.Errorf("revoke subscription token: %w", err)
	}
	return nil
}

func (s *Store) ResolveSubscription(
	ctx context.Context,
	tokenHash []byte,
	format string,
	now time.Time,
	masterKey []byte,
) (Subscription, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Subscription{}, fmt.Errorf("begin resolve subscription: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var tokenID, userID, username, allowedFormats string
	var trafficUploadBytes, trafficDownloadBytes, trafficLimitBytes int64
	var userExpiresAt, tokenExpiresAt sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		SELECT t.id, u.id, u.username, t.allowed_formats,
		       u.traffic_upload_bytes, u.traffic_download_bytes,
		       u.traffic_limit_bytes, u.expires_at, t.expires_at
		FROM subscription_tokens t
		JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ? AND t.revoked_at IS NULL
		  AND (t.expires_at IS NULL OR t.expires_at > ?)
		  AND u.archived_at IS NULL AND u.enabled = 1
		  AND (u.expires_at IS NULL OR u.expires_at > ?)
		  AND u.quota_state <> 'limited'
	`, tokenHash, now.UnixMilli(), now.UnixMilli()).Scan(
		&tokenID, &userID, &username, &allowedFormats,
		&trafficUploadBytes, &trafficDownloadBytes, &trafficLimitBytes,
		&userExpiresAt, &tokenExpiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Subscription{}, ErrUnauthorized
	}
	if err != nil {
		return Subscription{}, fmt.Errorf("authenticate subscription token: %w", err)
	}
	formats, err := parseStoredFormats(allowedFormats)
	if err != nil || !containsFormat(formats, format) {
		return Subscription{}, ErrUnauthorized
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT n.id, n.name, n.adapter_type, n.public_host, n.public_port, n.sni,
		       n.tls_insecure,
		       n.tls_cert_fingerprint, n.tls_public_key_sha256,
		       c.id, c.protocol, c.secret_ciphertext, c.key_version,
		       COALESCE(r.public_key, ''), COALESCE(r.short_id, '')
		FROM node_user_assignments a
		JOIN nodes n ON n.id = a.node_id
		JOIN user_credentials c ON c.id = a.applied_credential_id
		LEFT JOIN node_vless_reality r ON r.node_id = n.id
		WHERE a.user_id = ? AND n.archived_at IS NULL
		  AND n.adapter_type IN (
		      'native_hysteria2', 's_ui', 'sing_box_vless_reality'
		  ) AND n.enabled = 1
		  AND n.status NOT IN ('pending', 'degraded', 'disabled')
		  AND n.public_host <> '' AND a.enabled = 1
		  AND a.quota_state <> 'limited' AND a.state = 'applied'
		  AND a.management_mode = 'managed'
		  AND a.applied_version >= a.desired_version
		  AND a.applied_credential_id IS NOT NULL AND c.state = 'applied'
		  AND (
		      n.adapter_type <> 'sing_box_vless_reality' OR (
		          n.adapter_status = 'compatible' AND n.core_running = 1
		          AND a.applied_credential_id = a.desired_credential_id
		          AND a.applied_version = n.applied_version
		          AND c.protocol = 'vless' AND r.public_key <> '' AND r.short_id <> ''
		          AND r.applied_key_generation = r.desired_key_generation
		          AND r.material_applied_version = a.applied_version
		      )
		  )
		ORDER BY n.name COLLATE NOCASE, n.id
	`, userID)
	if err != nil {
		return Subscription{}, fmt.Errorf("read subscription endpoints: %w", err)
	}
	endpoints := make([]SubscriptionEndpoint, 0)
	for rows.Next() {
		var endpoint SubscriptionEndpoint
		var credentialID, credentialProtocol string
		var ciphertext []byte
		var keyVersion, tlsInsecure int
		if err := rows.Scan(
			&endpoint.NodeID, &endpoint.NodeName, &endpoint.AdapterType,
			&endpoint.PublicHost,
			&endpoint.PublicPort, &endpoint.SNI, &tlsInsecure,
			&endpoint.TLSCertFingerprint, &endpoint.TLSPublicKeySHA256,
			&credentialID, &credentialProtocol, &ciphertext, &keyVersion,
			&endpoint.RealityPublicKey, &endpoint.RealityShortID,
		); err != nil {
			_ = rows.Close()
			return Subscription{}, fmt.Errorf("scan subscription endpoint: %w", err)
		}
		if keyVersion != credentialKeyVersion {
			_ = rows.Close()
			return Subscription{}, errors.New("subscription credential key version is unsupported")
		}
		secret, err := cryptoutil.Open(masterKey, ciphertext, credentialAAD(
			credentialID, userID, endpoint.NodeID, credentialProtocol, keyVersion,
		))
		if err != nil {
			_ = rows.Close()
			return Subscription{}, fmt.Errorf("open subscription credential: %w", err)
		}
		endpoint.TLSInsecure = tlsInsecure == 1
		endpoint.Protocol = credentialProtocol
		if credentialProtocol == CredentialProtocolVLESS {
			endpoint.Flow = "xtls-rprx-vision"
			endpoint.Network = "tcp"
		}
		endpoint.Credential = string(secret)
		endpoints = append(endpoints, endpoint)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return Subscription{}, fmt.Errorf("iterate subscription endpoints: %w", err)
	}
	if err := rows.Close(); err != nil {
		return Subscription{}, fmt.Errorf("close subscription endpoints: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE subscription_tokens SET last_used_at = ?, updated_at = ? WHERE id = ?
	`, now.UnixMilli(), now.UnixMilli(), tokenID); err != nil {
		return Subscription{}, fmt.Errorf("touch subscription token: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Subscription{}, fmt.Errorf("commit resolved subscription: %w", err)
	}
	return Subscription{
		UserID:               userID,
		Username:             username,
		TrafficUploadBytes:   trafficUploadBytes,
		TrafficDownloadBytes: trafficDownloadBytes,
		TrafficLimitBytes:    trafficLimitBytes,
		ExpiresAt:            earliestSubscriptionExpiry(userExpiresAt, tokenExpiresAt),
		Endpoints:            endpoints,
	}, nil
}

func earliestSubscriptionExpiry(values ...sql.NullInt64) *time.Time {
	var earliest *time.Time
	for _, value := range values {
		if !value.Valid {
			continue
		}
		candidate := unixTime(value.Int64)
		if earliest == nil || candidate.Before(*earliest) {
			copy := candidate
			earliest = &copy
		}
	}
	return earliest
}

func scanSubscriptionToken(row rowScanner) (SubscriptionToken, error) {
	var token SubscriptionToken
	var allowedFormats string
	var expiresAt, lastUsedAt, revokedAt sql.NullInt64
	var createdAt, updatedAt int64
	if err := row.Scan(
		&token.ID, &token.UserID, &token.Name, &token.TokenPrefix, &allowedFormats,
		&expiresAt, &lastUsedAt, &revokedAt, &createdAt, &updatedAt,
	); err != nil {
		return SubscriptionToken{}, err
	}
	formats, err := parseStoredFormats(allowedFormats)
	if err != nil {
		return SubscriptionToken{}, err
	}
	token.AllowedFormats = formats
	token.ExpiresAt = nullableTime(expiresAt)
	token.LastUsedAt = nullableTime(lastUsedAt)
	token.RevokedAt = nullableTime(revokedAt)
	token.CreatedAt = unixTime(createdAt)
	token.UpdatedAt = unixTime(updatedAt)
	return token, nil
}

func newSubscriptionToken() (string, error) {
	random, err := cryptoutil.RandomToken(32)
	if err != nil {
		return "", err
	}
	return subscriptionTokenMarker + random, nil
}

func tokenPrefix(token string) string {
	const visible = 12
	if len(token) <= visible {
		return token
	}
	return token[:visible]
}

func parseStoredFormats(value string) ([]string, error) {
	return NormalizeSubscriptionFormats(strings.Split(value, ","))
}

func containsFormat(formats []string, target string) bool {
	for _, format := range formats {
		if format == target {
			return true
		}
	}
	return false
}
