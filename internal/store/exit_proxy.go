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

const (
	ExitProxyKindRentedIP    = "rented_ip"
	ExitProxyKindLandingVPS  = "landing_vps"
	ExitProxyProtocolSOCKS5  = "socks5"
	ExitProxyProtocolHTTP    = "http"
	exitProxyPasswordVersion = 1
)

type ExitProxyAssignment struct {
	NodeID     string
	NodeName   string
	PublicHost string
}

type ExitProxy struct {
	ServerSide         bool
	ID                 string
	Name               string
	Kind               string
	Protocol           string
	Host               string
	Port               int
	Username           string
	PasswordSet        bool
	PasswordCiphertext []byte
	TLS                bool
	SkipCertVerify     bool
	UDP                bool
	Notes              string
	Assignments        []ExitProxyAssignment
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type NewExitProxy struct {
	ServerSide     bool
	ID             string
	Name           string
	Kind           string
	Protocol       string
	Host           string
	Port           int
	Username       string
	Password       string
	TLS            bool
	SkipCertVerify bool
	UDP            bool
	Notes          string
	Now            time.Time
	MasterKey      []byte
}

type UpdateExitProxy struct {
	ServerSide     *bool
	Name           string
	Kind           string
	Protocol       string
	Host           string
	Port           int
	Username       string
	Password       *string
	TLS            bool
	SkipCertVerify bool
	UDP            bool
	Notes          string
	Now            time.Time
	MasterKey      []byte
}

type SubscriptionExitProxy struct {
	ServerSide     bool
	ID             string
	Name           string
	Kind           string
	Protocol       string
	Host           string
	Port           int
	Username       string
	Password       string
	TLS            bool
	SkipCertVerify bool
	UDP            bool
}

func exitProxyAAD(id string) []byte {
	return []byte(strings.Join([]string{"exit-proxy", id, fmt.Sprintf("v%d", exitProxyPasswordVersion)}, "\x00"))
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func normalizeExitProxy(
	name, kind, protocol, host, username, notes string,
	port int, tls, skipCertVerify, udp bool,
) (ExitProxy, error) {
	proxy := ExitProxy{
		Name:           strings.TrimSpace(name),
		Kind:           strings.TrimSpace(kind),
		Protocol:       strings.TrimSpace(protocol),
		Host:           strings.TrimSpace(host),
		Port:           port,
		Username:       strings.TrimSpace(username),
		TLS:            tls,
		SkipCertVerify: skipCertVerify,
		UDP:            udp,
		Notes:          strings.TrimSpace(notes),
	}
	if len(proxy.Name) < 2 || len(proxy.Name) > 64 {
		return ExitProxy{}, fmt.Errorf("%w: name must be 2 to 64 characters", ErrUnsupported)
	}
	if proxy.Kind != ExitProxyKindRentedIP && proxy.Kind != ExitProxyKindLandingVPS {
		return ExitProxy{}, fmt.Errorf("%w: unsupported exit proxy kind", ErrUnsupported)
	}
	if proxy.Protocol != ExitProxyProtocolSOCKS5 && proxy.Protocol != ExitProxyProtocolHTTP {
		return ExitProxy{}, fmt.Errorf("%w: unsupported exit proxy protocol", ErrUnsupported)
	}
	if proxy.Host == "" || len(proxy.Host) > 253 {
		return ExitProxy{}, fmt.Errorf("%w: host is required", ErrUnsupported)
	}
	if proxy.Port < 1 || proxy.Port > 65535 {
		return ExitProxy{}, fmt.Errorf("%w: port must be between 1 and 65535", ErrUnsupported)
	}
	if len(proxy.Username) > 128 {
		return ExitProxy{}, fmt.Errorf("%w: username is too long", ErrUnsupported)
	}
	if len(proxy.Notes) > 512 {
		return ExitProxy{}, fmt.Errorf("%w: notes are too long", ErrUnsupported)
	}
	if proxy.Protocol != ExitProxyProtocolHTTP {
		proxy.TLS = false
		proxy.SkipCertVerify = false
	}
	if !proxy.TLS {
		proxy.SkipCertVerify = false
	}
	if proxy.Protocol != ExitProxyProtocolSOCKS5 {
		proxy.UDP = false
	}
	return proxy, nil
}

func sealExitProxyPassword(masterKey []byte, id, password string) ([]byte, error) {
	password = strings.TrimSpace(password)
	if password == "" {
		return nil, nil
	}
	if len(password) > 256 {
		return nil, fmt.Errorf("%w: password is too long", ErrUnsupported)
	}
	ciphertext, err := cryptoutil.Seal(masterKey, []byte(password), exitProxyAAD(id))
	if err != nil {
		return nil, fmt.Errorf("seal exit proxy password: %w", err)
	}
	return ciphertext, nil
}

func openExitProxyPassword(masterKey, ciphertext []byte, id string) (string, error) {
	if len(ciphertext) == 0 {
		return "", nil
	}
	plaintext, err := cryptoutil.Open(masterKey, ciphertext, exitProxyAAD(id))
	if err != nil {
		return "", fmt.Errorf("open exit proxy password: %w", err)
	}
	return string(plaintext), nil
}

func scanExitProxy(row rowScanner) (ExitProxy, error) {
	var proxy ExitProxy
	var tls, skipCertVerify, udp, serverSide int
	var created, updated int64
	if err := row.Scan(
		&proxy.ID, &proxy.Name, &proxy.Kind, &proxy.Protocol, &proxy.Host, &proxy.Port,
		&proxy.Username, &proxy.PasswordCiphertext, &tls, &skipCertVerify, &udp, &proxy.Notes,
		&created, &updated, &serverSide,
	); err != nil {
		return ExitProxy{}, err
	}
	proxy.ServerSide = serverSide == 1
	proxy.TLS = tls == 1
	proxy.SkipCertVerify = skipCertVerify == 1
	proxy.UDP = udp == 1
	proxy.PasswordSet = len(proxy.PasswordCiphertext) > 0
	proxy.CreatedAt = unixTime(created)
	proxy.UpdatedAt = unixTime(updated)
	proxy.Assignments = []ExitProxyAssignment{}
	return proxy, nil
}

func (s *Store) attachExitProxyAssignments(ctx context.Context, proxies []ExitProxy) error {
	if len(proxies) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(exit_proxy_id, ''), id, name, public_host
		FROM nodes
		WHERE archived_at IS NULL AND exit_proxy_id IS NOT NULL
		ORDER BY name COLLATE NOCASE, id
	`)
	if err != nil {
		return fmt.Errorf("list exit proxy assignments: %w", err)
	}
	defer rows.Close()
	byID := make(map[string][]ExitProxyAssignment, len(proxies))
	for rows.Next() {
		var assignment ExitProxyAssignment
		var proxyID string
		if err := rows.Scan(&proxyID, &assignment.NodeID, &assignment.NodeName, &assignment.PublicHost); err != nil {
			return fmt.Errorf("scan exit proxy assignment: %w", err)
		}
		byID[proxyID] = append(byID[proxyID], assignment)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate exit proxy assignments: %w", err)
	}
	for index := range proxies {
		assignments := byID[proxies[index].ID]
		if assignments == nil {
			assignments = []ExitProxyAssignment{}
		}
		proxies[index].Assignments = assignments
		proxies[index].PasswordCiphertext = nil
	}
	return nil
}

func (s *Store) ListExitProxies(ctx context.Context) ([]ExitProxy, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, kind, protocol, host, port, username, password_ciphertext,
		       tls, skip_cert_verify, udp, notes, created_at, updated_at, server_side
		FROM exit_proxies
		ORDER BY name COLLATE NOCASE, id
	`)
	if err != nil {
		return nil, fmt.Errorf("list exit proxies: %w", err)
	}
	defer rows.Close()
	proxies := make([]ExitProxy, 0)
	for rows.Next() {
		proxy, err := scanExitProxy(rows)
		if err != nil {
			return nil, fmt.Errorf("scan exit proxy: %w", err)
		}
		proxies = append(proxies, proxy)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate exit proxies: %w", err)
	}
	if err := s.attachExitProxyAssignments(ctx, proxies); err != nil {
		return nil, err
	}
	return proxies, nil
}

func (s *Store) GetExitProxy(ctx context.Context, id string) (ExitProxy, error) {
	proxy, err := scanExitProxy(s.db.QueryRowContext(ctx, `
		SELECT id, name, kind, protocol, host, port, username, password_ciphertext,
		       tls, skip_cert_verify, udp, notes, created_at, updated_at, server_side
		FROM exit_proxies WHERE id = ?
	`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ExitProxy{}, ErrNotFound
	}
	if err != nil {
		return ExitProxy{}, fmt.Errorf("get exit proxy: %w", err)
	}
	proxies := []ExitProxy{proxy}
	if err := s.attachExitProxyAssignments(ctx, proxies); err != nil {
		return ExitProxy{}, err
	}
	return proxies[0], nil
}

func (s *Store) getExitProxyMap(ctx context.Context, ids []string) (map[string]ExitProxy, error) {
	result := make(map[string]ExitProxy, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	placeholders := make([]string, 0, len(ids))
	arguments := make([]any, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		placeholders = append(placeholders, "?")
		arguments = append(arguments, id)
	}
	if len(placeholders) == 0 {
		return result, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, kind, protocol, host, port, username, password_ciphertext,
		       tls, skip_cert_verify, udp, notes, created_at, updated_at, server_side
		FROM exit_proxies WHERE id IN (`+strings.Join(placeholders, ",")+`)
	`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list exit proxies by id: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		proxy, err := scanExitProxy(rows)
		if err != nil {
			return nil, fmt.Errorf("scan exit proxy: %w", err)
		}
		proxy.PasswordCiphertext = nil
		result[proxy.ID] = proxy
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate exit proxies by id: %w", err)
	}
	return result, nil
}

func (s *Store) attachNodeExitProxies(ctx context.Context, nodes []Node) error {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node.ExitProxyID != "" {
			ids = append(ids, node.ExitProxyID)
		}
	}
	proxies, err := s.getExitProxyMap(ctx, ids)
	if err != nil {
		return err
	}
	for index := range nodes {
		proxy, ok := proxies[nodes[index].ExitProxyID]
		if !ok {
			continue
		}
		summary := proxy
		nodes[index].ExitProxy = &summary
	}
	return nil
}

func (s *Store) CreateExitProxy(ctx context.Context, input NewExitProxy) (ExitProxy, error) {
	normalized, err := normalizeExitProxy(
		input.Name, input.Kind, input.Protocol, input.Host, input.Username, input.Notes,
		input.Port, input.TLS, input.SkipCertVerify, input.UDP,
	)
	if err != nil {
		return ExitProxy{}, err
	}
	if input.ID == "" {
		return ExitProxy{}, fmt.Errorf("%w: exit proxy id is required", ErrUnsupported)
	}
	ciphertext, err := sealExitProxyPassword(input.MasterKey, input.ID, input.Password)
	if err != nil {
		return ExitProxy{}, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO exit_proxies(
			id, name, kind, protocol, host, port, username, password_ciphertext,
			tls, skip_cert_verify, udp, notes, created_at, updated_at, server_side
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.ID, normalized.Name, normalized.Kind, normalized.Protocol, normalized.Host,
		normalized.Port, normalized.Username, ciphertext, boolInt(normalized.TLS),
		boolInt(normalized.SkipCertVerify), boolInt(normalized.UDP), normalized.Notes,
		input.Now.UnixMilli(), input.Now.UnixMilli(), boolInt(input.ServerSide))
	if err != nil {
		if isUniqueConstraint(err) {
			return ExitProxy{}, ErrConflict
		}
		return ExitProxy{}, fmt.Errorf("insert exit proxy: %w", err)
	}
	return s.GetExitProxy(ctx, input.ID)
}

func (s *Store) UpdateExitProxy(ctx context.Context, id string, input UpdateExitProxy) (ExitProxy, error) {
	normalized, err := normalizeExitProxy(
		input.Name, input.Kind, input.Protocol, input.Host, input.Username, input.Notes,
		input.Port, input.TLS, input.SkipCertVerify, input.UDP,
	)
	if err != nil {
		return ExitProxy{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ExitProxy{}, fmt.Errorf("begin update exit proxy: %w", err)
	}
	var current []byte
	err = tx.QueryRowContext(ctx, `SELECT password_ciphertext FROM exit_proxies WHERE id = ?`, id).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return ExitProxy{}, ErrNotFound
	}
	if err != nil {
		_ = tx.Rollback()
		return ExitProxy{}, fmt.Errorf("read exit proxy password: %w", err)
	}
	ciphertext := current
	if input.Password != nil {
		ciphertext, err = sealExitProxyPassword(input.MasterKey, id, *input.Password)
		if err != nil {
			_ = tx.Rollback()
			return ExitProxy{}, err
		}
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE exit_proxies SET name = ?, kind = ?, protocol = ?, host = ?, port = ?,
			username = ?, password_ciphertext = ?, tls = ?, skip_cert_verify = ?, udp = ?,
			notes = ?, updated_at = ?, server_side = COALESCE(?, server_side)
		WHERE id = ?
	`, normalized.Name, normalized.Kind, normalized.Protocol, normalized.Host, normalized.Port,
		normalized.Username, ciphertext, boolInt(normalized.TLS), boolInt(normalized.SkipCertVerify),
		boolInt(normalized.UDP), normalized.Notes, input.Now.UnixMilli(), input.ServerSide, id)
	if err != nil {
		_ = tx.Rollback()
		if isUniqueConstraint(err) {
			return ExitProxy{}, ErrConflict
		}
		return ExitProxy{}, fmt.Errorf("update exit proxy: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ExitProxy{}, fmt.Errorf("commit exit proxy: %w", err)
	}
	return s.GetExitProxy(ctx, id)
}

func (s *Store) DeleteExitProxy(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM exit_proxies WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete exit proxy: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("exit proxy delete rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) OpenExitProxyPassword(ctx context.Context, id string, masterKey []byte) (string, error) {
	var ciphertext []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT password_ciphertext FROM exit_proxies WHERE id = ?
	`, id).Scan(&ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read exit proxy password: %w", err)
	}
	return openExitProxyPassword(masterKey, ciphertext, id)
}

func (s *Store) ReplaceExitProxyNodes(ctx context.Context, proxyID string, nodeIDs []string, now time.Time) (ExitProxy, error) {
	if _, err := s.GetExitProxy(ctx, proxyID); err != nil {
		return ExitProxy{}, err
	}
	uniqueIDs := make([]string, 0, len(nodeIDs))
	seen := make(map[string]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" {
			continue
		}
		if _, exists := seen[nodeID]; exists {
			continue
		}
		seen[nodeID] = struct{}{}
		uniqueIDs = append(uniqueIDs, nodeID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ExitProxy{}, fmt.Errorf("begin replace exit proxy nodes: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes SET exit_proxy_id = NULL, updated_at = ?
		WHERE archived_at IS NULL AND exit_proxy_id = ?
	`, now.UnixMilli(), proxyID); err != nil {
		_ = tx.Rollback()
		return ExitProxy{}, fmt.Errorf("clear exit proxy nodes: %w", err)
	}
	for _, nodeID := range uniqueIDs {
		result, err := tx.ExecContext(ctx, `
			UPDATE nodes SET exit_proxy_id = ?, updated_at = ?
			WHERE id = ? AND archived_at IS NULL
		`, proxyID, now.UnixMilli(), nodeID)
		if err != nil {
			_ = tx.Rollback()
			return ExitProxy{}, fmt.Errorf("assign exit proxy node: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			_ = tx.Rollback()
			return ExitProxy{}, fmt.Errorf("assign exit proxy rows: %w", err)
		}
		if affected == 0 {
			_ = tx.Rollback()
			return ExitProxy{}, ErrNotFound
		}
	}
	if err := tx.Commit(); err != nil {
		return ExitProxy{}, fmt.Errorf("commit exit proxy nodes: %w", err)
	}
	return s.GetExitProxy(ctx, proxyID)
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}
