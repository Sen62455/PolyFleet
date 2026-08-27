package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

type NodeAsset struct {
	NodeID             string
	NodeName           string
	Plan               string
	PurchasedAt        *time.Time
	ExpiresAt          *time.Time
	RenewalCycleMonths int
	AutoRenew          bool
	Notes              string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type NodeAssetInput struct {
	Plan               string
	PurchasedAt        *time.Time
	ExpiresAt          *time.Time
	RenewalCycleMonths int
	AutoRenew          bool
	Notes              string
	Now                time.Time
}

func scanNodeAsset(row rowScanner) (NodeAsset, error) {
	var asset NodeAsset
	var purchasedAt, expiresAt sql.NullInt64
	var autoRenew int
	var createdAt, updatedAt int64
	if err := row.Scan(
		&asset.NodeID, &asset.NodeName, &asset.Plan, &purchasedAt, &expiresAt,
		&asset.RenewalCycleMonths, &autoRenew, &asset.Notes, &createdAt, &updatedAt,
	); err != nil {
		return NodeAsset{}, err
	}
	asset.PurchasedAt = nullableTime(purchasedAt)
	asset.ExpiresAt = nullableTime(expiresAt)
	asset.AutoRenew = autoRenew == 1
	asset.CreatedAt = unixTime(createdAt)
	asset.UpdatedAt = unixTime(updatedAt)
	return asset, nil
}

const nodeAssetColumns = `
	n.id, n.name, COALESCE(a.plan, ''), a.purchased_at, a.expires_at,
	COALESCE(a.renewal_cycle_months, 0), COALESCE(a.auto_renew, 0),
	COALESCE(a.notes, ''), COALESCE(a.created_at, n.created_at),
	COALESCE(a.updated_at, n.updated_at)
`

func (s *Store) ListNodeAssets(ctx context.Context) ([]NodeAsset, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+nodeAssetColumns+`
		FROM nodes n LEFT JOIN node_assets a ON a.node_id = n.id
		WHERE n.archived_at IS NULL ORDER BY n.name COLLATE NOCASE
	`)
	if err != nil {
		return nil, fmt.Errorf("list node assets: %w", err)
	}
	defer rows.Close()
	assets := make([]NodeAsset, 0)
	for rows.Next() {
		asset, err := scanNodeAsset(rows)
		if err != nil {
			return nil, fmt.Errorf("scan node asset: %w", err)
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate node assets: %w", err)
	}
	return assets, nil
}

func (s *Store) UpsertNodeAsset(
	ctx context.Context,
	nodeID string,
	input NodeAssetInput,
) (NodeAsset, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM nodes WHERE id = ? AND archived_at IS NULL
	`, nodeID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NodeAsset{}, ErrNotFound
		}
		return NodeAsset{}, fmt.Errorf("find asset node: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO node_assets(
			node_id, plan, purchased_at, expires_at, renewal_cycle_months,
			auto_renew, notes, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			plan = excluded.plan,
			purchased_at = excluded.purchased_at,
			expires_at = excluded.expires_at,
			renewal_cycle_months = excluded.renewal_cycle_months,
			auto_renew = excluded.auto_renew,
			notes = excluded.notes,
			updated_at = excluded.updated_at
	`, nodeID, input.Plan, nullableUnixMilli(input.PurchasedAt), nullableUnixMilli(input.ExpiresAt),
		input.RenewalCycleMonths, boolInt(input.AutoRenew), input.Notes,
		input.Now.UnixMilli(), input.Now.UnixMilli()); err != nil {
		return NodeAsset{}, fmt.Errorf("upsert node asset: %w", err)
	}
	asset, err := scanNodeAsset(s.db.QueryRowContext(ctx, `
		SELECT `+nodeAssetColumns+`
		FROM nodes n LEFT JOIN node_assets a ON a.node_id = n.id
		WHERE n.id = ? AND n.archived_at IS NULL
	`, nodeID))
	if err != nil {
		return NodeAsset{}, fmt.Errorf("read node asset: %w", err)
	}
	return asset, nil
}

type SubscriptionOperation struct {
	TokenID              string
	UserID               string
	Username             string
	DisplayName          string
	Name                 string
	TokenPrefix          string
	AllowedFormats       []string
	Status               string
	TokenExpiresAt       *time.Time
	UserExpiresAt        *time.Time
	LastUsedAt           *time.Time
	LastTrafficAt        *time.Time
	RevokedAt            *time.Time
	TrafficLimitBytes    int64
	TrafficUploadBytes   int64
	TrafficDownloadBytes int64
	TrafficUsedBytes     int64
	AssignmentCount      int
	OnlineNodes          int
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type SubscriptionOperationFilter struct {
	Status string
	Search string
	Limit  int
	Offset int
	Now    time.Time
}

type SubscriptionOperationUpdate struct {
	TokenExpiresAtSet bool
	TokenExpiresAt    *time.Time
	UserExpiresAtSet  bool
	UserExpiresAt     *time.Time
	TrafficLimitBytes *int64
	Revoke            bool
	Now               time.Time
}

const subscriptionOperationsCTE = `
	WITH subscription_view AS (
		SELECT t.id AS token_id, t.user_id, u.username, u.display_name, t.name,
		       t.token_prefix, t.allowed_formats, t.expires_at AS token_expires_at,
		       u.expires_at AS user_expires_at, t.last_used_at, u.last_traffic_at,
		       t.revoked_at, u.traffic_limit_bytes, u.traffic_upload_bytes,
		       u.traffic_download_bytes, u.traffic_used_bytes,
		       t.created_at, MAX(t.updated_at, u.updated_at) AS updated_at,
		       CASE
		         WHEN t.revoked_at IS NOT NULL THEN 'revoked'
		         WHEN (t.expires_at IS NOT NULL AND t.expires_at <= ?)
		           OR (u.expires_at IS NOT NULL AND u.expires_at <= ?) THEN 'expired'
		         WHEN u.enabled = 0 THEN 'disabled'
		         WHEN u.traffic_limit_bytes > 0
		           AND u.traffic_used_bytes >= u.traffic_limit_bytes THEN 'exhausted'
		         WHEN (t.expires_at IS NOT NULL AND t.expires_at <= ?)
		           OR (u.expires_at IS NOT NULL AND u.expires_at <= ?) THEN 'expiring'
		         ELSE 'active'
		       END AS operation_status
		FROM subscription_tokens t
		JOIN users u ON u.id = t.user_id AND u.archived_at IS NULL
	)
`

func (s *Store) ListSubscriptionOperations(
	ctx context.Context,
	filter SubscriptionOperationFilter,
) ([]SubscriptionOperation, int, error) {
	if filter.Limit < 1 || filter.Limit > 200 {
		filter.Limit = 50
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	if filter.Now.IsZero() {
		filter.Now = time.Now().UTC()
	}
	filter.Status = strings.TrimSpace(strings.ToLower(filter.Status))
	validStatuses := map[string]bool{
		"": true, "all": true, "active": true, "expiring": true,
		"exhausted": true, "expired": true, "revoked": true, "disabled": true,
	}
	if !validStatuses[filter.Status] {
		return nil, 0, ErrUnsupported
	}
	now := filter.Now.UnixMilli()
	cutoff := filter.Now.Add(7 * 24 * time.Hour).UnixMilli()
	baseArgs := []any{now, now, cutoff, cutoff}
	where := []string{"1 = 1"}
	args := append([]any(nil), baseArgs...)
	if filter.Status != "" && filter.Status != "all" {
		where = append(where, "operation_status = ?")
		args = append(args, filter.Status)
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		where = append(where, `(token_id LIKE ? ESCAPE '\' OR username LIKE ? ESCAPE '\' OR display_name LIKE ? ESCAPE '\' OR name LIKE ? ESCAPE '\' OR token_prefix LIKE ? ESCAPE '\')`)
		pattern := "%" + escapeLike(search) + "%"
		args = append(args, pattern, pattern, pattern, pattern, pattern)
	}
	whereSQL := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx,
		subscriptionOperationsCTE+`SELECT COUNT(*) FROM subscription_view WHERE `+whereSQL,
		args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count subscription operations: %w", err)
	}
	queryArgs := append(append([]any(nil), args...), filter.Limit, filter.Offset)
	rows, err := s.db.QueryContext(ctx, subscriptionOperationsCTE+`
		SELECT v.token_id, v.user_id, v.username, v.display_name, v.name,
		       v.token_prefix, v.allowed_formats, v.operation_status,
		       v.token_expires_at, v.user_expires_at, v.last_used_at,
		       v.last_traffic_at, v.revoked_at, v.traffic_limit_bytes,
		       v.traffic_upload_bytes, v.traffic_download_bytes, v.traffic_used_bytes,
		       (SELECT COUNT(*) FROM node_user_assignments a WHERE a.user_id = v.user_id),
		       (SELECT COUNT(DISTINCT o.node_id) FROM node_online_users o
		        WHERE o.user_id = v.user_id AND o.connections > 0),
		       v.created_at, v.updated_at
		FROM subscription_view v WHERE `+whereSQL+`
		ORDER BY CASE v.operation_status
		           WHEN 'exhausted' THEN 0 WHEN 'expired' THEN 1 WHEN 'expiring' THEN 2
		           WHEN 'disabled' THEN 3 WHEN 'active' THEN 4 ELSE 5 END,
		         COALESCE(v.last_used_at, v.created_at) DESC
		LIMIT ? OFFSET ?
	`, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list subscription operations: %w", err)
	}
	defer rows.Close()
	result := make([]SubscriptionOperation, 0)
	for rows.Next() {
		var item SubscriptionOperation
		var formats string
		var tokenExpiry, userExpiry, lastUsed, lastTraffic, revoked sql.NullInt64
		var createdAt, updatedAt int64
		if err := rows.Scan(
			&item.TokenID, &item.UserID, &item.Username, &item.DisplayName, &item.Name,
			&item.TokenPrefix, &formats, &item.Status, &tokenExpiry, &userExpiry,
			&lastUsed, &lastTraffic, &revoked, &item.TrafficLimitBytes,
			&item.TrafficUploadBytes, &item.TrafficDownloadBytes, &item.TrafficUsedBytes,
			&item.AssignmentCount, &item.OnlineNodes, &createdAt, &updatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan subscription operation: %w", err)
		}
		item.AllowedFormats, err = parseStoredFormats(formats)
		if err != nil {
			return nil, 0, fmt.Errorf("parse subscription operation formats: %w", err)
		}
		item.TokenExpiresAt = nullableTime(tokenExpiry)
		item.UserExpiresAt = nullableTime(userExpiry)
		item.LastUsedAt = nullableTime(lastUsed)
		item.LastTrafficAt = nullableTime(lastTraffic)
		item.RevokedAt = nullableTime(revoked)
		item.CreatedAt = unixTime(createdAt)
		item.UpdatedAt = unixTime(updatedAt)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate subscription operations: %w", err)
	}
	return result, total, nil
}

func (s *Store) GetSubscriptionOperation(
	ctx context.Context,
	tokenID string,
	now time.Time,
) (SubscriptionOperation, error) {
	items, _, err := s.ListSubscriptionOperations(ctx, SubscriptionOperationFilter{
		Search: tokenID, Limit: 200, Now: now,
	})
	if err != nil {
		return SubscriptionOperation{}, err
	}
	for _, item := range items {
		if item.TokenID == tokenID {
			return item, nil
		}
	}
	return SubscriptionOperation{}, ErrNotFound
}

func (s *Store) UpdateSubscriptionTokenExpiry(
	ctx context.Context,
	tokenID string,
	expiresAt *time.Time,
	now time.Time,
) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE subscription_tokens SET expires_at = ?, updated_at = ?
		WHERE id = ? AND revoked_at IS NULL
	`, nullableUnixMilli(expiresAt), now.UnixMilli(), tokenID)
	if err != nil {
		return fmt.Errorf("update subscription token expiry: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read subscription token update: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdateSubscriptionOperation(
	ctx context.Context,
	tokenID string,
	input SubscriptionOperationUpdate,
) (SubscriptionOperation, error) {
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubscriptionOperation{}, fmt.Errorf("begin subscription operation update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var userID, username, displayName, notes string
	var enabled int
	var userExpiresAt sql.NullInt64
	var trafficLimitBytes int64
	if err := tx.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.display_name, u.notes, u.enabled,
		       u.expires_at, u.traffic_limit_bytes
		FROM subscription_tokens t
		JOIN users u ON u.id = t.user_id AND u.archived_at IS NULL
		WHERE t.id = ?
	`, tokenID).Scan(
		&userID, &username, &displayName, &notes, &enabled,
		&userExpiresAt, &trafficLimitBytes,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SubscriptionOperation{}, ErrNotFound
		}
		return SubscriptionOperation{}, fmt.Errorf("read subscription update target: %w", err)
	}

	if input.UserExpiresAtSet || input.TrafficLimitBytes != nil {
		expiresAt := nullableTime(userExpiresAt)
		if input.UserExpiresAtSet {
			expiresAt = input.UserExpiresAt
		}
		if input.TrafficLimitBytes != nil {
			trafficLimitBytes = *input.TrafficLimitBytes
		}
		if err := updateUserTx(ctx, tx, userID, UpdateUser{
			Username: username, DisplayName: displayName, Notes: notes,
			Enabled: enabled == 1, ExpiresAt: expiresAt,
			TrafficLimitBytes: trafficLimitBytes, Now: input.Now,
		}); err != nil {
			return SubscriptionOperation{}, err
		}
	}

	if input.TokenExpiresAtSet {
		result, err := tx.ExecContext(ctx, `
			UPDATE subscription_tokens SET expires_at = ?, updated_at = ?
			WHERE id = ? AND user_id = ? AND revoked_at IS NULL
		`, nullableUnixMilli(input.TokenExpiresAt), input.Now.UnixMilli(), tokenID, userID)
		if err != nil {
			return SubscriptionOperation{}, fmt.Errorf("update subscription token expiry: %w", err)
		}
		if count, err := result.RowsAffected(); err != nil {
			return SubscriptionOperation{}, fmt.Errorf("read subscription token update: %w", err)
		} else if count == 0 {
			return SubscriptionOperation{}, ErrConflict
		}
	}
	if input.Revoke {
		if _, err := tx.ExecContext(ctx, `
			UPDATE subscription_tokens
			SET revoked_at = COALESCE(revoked_at, ?), updated_at = ?
			WHERE id = ? AND user_id = ?
		`, input.Now.UnixMilli(), input.Now.UnixMilli(), tokenID, userID); err != nil {
			return SubscriptionOperation{}, fmt.Errorf("revoke subscription token: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return SubscriptionOperation{}, fmt.Errorf("commit subscription operation update: %w", err)
	}
	return s.GetSubscriptionOperation(ctx, tokenID, input.Now)
}

type TrafficPoint struct {
	BucketAt      time.Time
	UploadBytes   int64
	DownloadBytes int64
}

type TrafficRank struct {
	ID            string
	Name          string
	UploadBytes   int64
	DownloadBytes int64
}

type TrafficReport struct {
	From                  time.Time
	To                    time.Time
	UploadBytes           int64
	DownloadBytes         int64
	PreviousUploadBytes   int64
	PreviousDownloadBytes int64
	Daily                 []TrafficPoint
	PreviousDaily         []TrafficPoint
	TopUsers              []TrafficRank
	TopNodes              []TrafficRank
}

func (s *Store) TrafficReport(
	ctx context.Context,
	from, to time.Time,
	limit int,
) (TrafficReport, error) {
	if !from.Before(to) {
		return TrafficReport{}, ErrUnsupported
	}
	if limit < 1 || limit > 50 {
		limit = 8
	}
	report := TrafficReport{From: from, To: to, Daily: []TrafficPoint{}, PreviousDaily: []TrafficPoint{}, TopUsers: []TrafficRank{}, TopNodes: []TrafficRank{}}
	window := to.Sub(from)
	previousFrom := from.Add(-window)
	var err error
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(CASE WHEN sampled_at >= ? THEN upload_bytes ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN sampled_at >= ? THEN download_bytes ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN sampled_at >= ? AND sampled_at < ? THEN upload_bytes ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN sampled_at >= ? AND sampled_at < ? THEN download_bytes ELSE 0 END), 0)
		FROM traffic_batches WHERE sampled_at >= ? AND sampled_at < ?
	`, from.UnixMilli(), from.UnixMilli(), previousFrom.UnixMilli(), from.UnixMilli(),
		previousFrom.UnixMilli(), from.UnixMilli(), previousFrom.UnixMilli(), to.UnixMilli()).Scan(
		&report.UploadBytes, &report.DownloadBytes,
		&report.PreviousUploadBytes, &report.PreviousDownloadBytes,
	); err != nil {
		return TrafficReport{}, fmt.Errorf("read traffic totals: %w", err)
	}
	report.Daily, err = s.dailyTraffic(ctx, from, to)
	if err != nil {
		return TrafficReport{}, err
	}
	report.PreviousDaily, err = s.dailyTraffic(ctx, previousFrom, from)
	if err != nil {
		return TrafficReport{}, err
	}
	report.TopNodes, err = s.trafficRanks(ctx, `
		SELECT b.node_id, n.name, COALESCE(SUM(b.upload_bytes), 0), COALESCE(SUM(b.download_bytes), 0)
		FROM traffic_batches b JOIN nodes n ON n.id = b.node_id AND n.archived_at IS NULL
		WHERE b.sampled_at >= ? AND b.sampled_at < ?
		GROUP BY b.node_id, n.name
		ORDER BY SUM(b.upload_bytes + b.download_bytes) DESC LIMIT ?
	`, from, to, limit)
	if err != nil {
		return TrafficReport{}, err
	}
	report.TopUsers, err = s.trafficRanks(ctx, `
		SELECT i.user_id, u.username, COALESCE(SUM(i.upload_bytes), 0), COALESCE(SUM(i.download_bytes), 0)
		FROM traffic_batch_items i
		JOIN traffic_batches b ON b.id = i.batch_id
		JOIN users u ON u.id = i.user_id AND u.archived_at IS NULL
		WHERE b.sampled_at >= ? AND b.sampled_at < ? AND i.disposition = 'accounted'
		GROUP BY i.user_id, u.username
		ORDER BY SUM(i.upload_bytes + i.download_bytes) DESC LIMIT ?
	`, from, to, limit)
	if err != nil {
		return TrafficReport{}, err
	}
	return report, nil
}

func (s *Store) dailyTraffic(ctx context.Context, from, to time.Time) ([]TrafficPoint, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT (sampled_at / 86400000) * 86400000 AS bucket,
		       COALESCE(SUM(upload_bytes), 0), COALESCE(SUM(download_bytes), 0)
		FROM traffic_batches WHERE sampled_at >= ? AND sampled_at < ?
		GROUP BY bucket ORDER BY bucket
	`, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("read traffic series: %w", err)
	}
	points := make(map[int64]TrafficPoint)
	for rows.Next() {
		var bucket int64
		var point TrafficPoint
		if err := rows.Scan(&bucket, &point.UploadBytes, &point.DownloadBytes); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan traffic series: %w", err)
		}
		point.BucketAt = unixTime(bucket)
		points[bucket] = point
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close traffic series: %w", err)
	}
	result := make([]TrafficPoint, 0, int(to.Sub(from).Hours()/24))
	day := from.UTC().Truncate(24 * time.Hour)
	for day.Before(to) {
		bucket := day.UnixMilli()
		point := points[bucket]
		point.BucketAt = day
		result = append(result, point)
		day = day.Add(24 * time.Hour)
	}
	return result, nil
}

func (s *Store) trafficRanks(
	ctx context.Context,
	query string,
	from, to time.Time,
	limit int,
) ([]TrafficRank, error) {
	rows, err := s.db.QueryContext(ctx, query, from.UnixMilli(), to.UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("read traffic ranking: %w", err)
	}
	defer rows.Close()
	result := make([]TrafficRank, 0)
	for rows.Next() {
		var rank TrafficRank
		if err := rows.Scan(&rank.ID, &rank.Name, &rank.UploadBytes, &rank.DownloadBytes); err != nil {
			return nil, fmt.Errorf("scan traffic ranking: %w", err)
		}
		if rank.UploadBytes > math.MaxInt64-rank.DownloadBytes {
			return nil, errors.New("traffic ranking exceeds integer range")
		}
		result = append(result, rank)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate traffic ranking: %w", err)
	}
	return result, nil
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}
