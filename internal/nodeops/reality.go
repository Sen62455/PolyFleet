package nodeops

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/google/uuid"
)

const (
	maxHelperRequestBytes             = 512 * 1024
	maxRealityUsers                   = 4096
	maxRealityIdentityBytes           = 8 * 1024
	maxRealityAppliedBytes            = 8 * 1024
	supportedRealitySingBoxVersion    = "1.13.18-hyfleet-utls1.8.7-api2"
	realitySingBoxSHA256AMD64         = "17b2fac82abaaf51c50632f21bb64412afe899868c3c44500c3274d189134928"
	realitySingBoxSHA256ARM64         = "46e52d1ccde00ef5cde7415fb01c1b103720d394d858b559ab297f07a16bbd8c"
	maxRealitySingBoxVersionOutputLen = 16 * 1024
)

var (
	realityDigestPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	realityLabelPattern  = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
)

type realityIdentity struct {
	NodeID        string `json:"node_id"`
	KeyGeneration int64  `json:"key_generation"`
	PrivateKey    string `json:"private_key"`
	PublicKey     string `json:"public_key"`
	ShortID       string `json:"short_id"`
}

type realityAppliedState struct {
	NodeID         string                          `json:"node_id"`
	Version        int64                           `json:"version"`
	SnapshotSHA256 string                          `json:"snapshot_sha256"`
	Reality        protocol.AppliedRealityMaterial `json:"reality"`
	AppliedAt      time.Time                       `json:"applied_at"`
}

type singBoxRealityConfig struct {
	Log          singBoxLog          `json:"log"`
	Experimental singBoxExperimental `json:"experimental"`
	Inbounds     []singBoxInbound    `json:"inbounds"`
	Outbounds    []singBoxOutbound   `json:"outbounds"`
	Route        singBoxRoute        `json:"route"`
}

type singBoxExperimental struct {
	ClashAPI singBoxClashAPI `json:"clash_api"`
}

type singBoxClashAPI struct {
	ExternalController string `json:"external_controller"`
	Secret             string `json:"secret"`
	HyFleetOnly        bool   `json:"hyfleet_only"`
}

type singBoxLog struct {
	Disabled bool `json:"disabled"`
}

type singBoxInbound struct {
	Type       string             `json:"type"`
	Tag        string             `json:"tag"`
	Listen     string             `json:"listen"`
	ListenPort int                `json:"listen_port"`
	Users      []singBoxVLESSUser `json:"users"`
	TLS        singBoxTLS         `json:"tls"`
}

type singBoxVLESSUser struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
	Flow string `json:"flow"`
}

type singBoxTLS struct {
	Enabled    bool           `json:"enabled"`
	ServerName string         `json:"server_name"`
	Reality    singBoxReality `json:"reality"`
}

type singBoxReality struct {
	Enabled           bool                    `json:"enabled"`
	Handshake         singBoxRealityHandshake `json:"handshake"`
	PrivateKey        string                  `json:"private_key"`
	ShortID           []string                `json:"short_id"`
	MaxTimeDifference string                  `json:"max_time_difference"`
}

type singBoxRealityHandshake struct {
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
}

type singBoxOutbound struct {
	Type string `json:"type"`
	Tag  string `json:"tag"`
}

type singBoxRoute struct {
	Final string `json:"final"`
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("helper request contains trailing JSON data")
	}
	return nil
}

func (helper *Helper) applyReality(ctx context.Context, request RealityApplyRequest) HelperResponse {
	response := HelperResponse{
		Status: "failed", AppliedVersion: request.Version,
		SnapshotSHA256: request.SnapshotSHA256, CompletedAt: helper.now(),
	}
	if err := helper.validateRealityApply(request); err != nil {
		response.ErrorCode = "reality_apply_invalid"
		response.ErrorMessage = err.Error()
		return response
	}
	if err := helper.validateRealityBinary(ctx); err != nil {
		response.ErrorCode = "reality_binary_incompatible"
		response.ErrorMessage = err.Error()
		return response
	}
	applied, hasApplied, err := helper.loadRealityApplied()
	if err != nil {
		response.ErrorCode = "reality_applied_state_invalid"
		response.ErrorMessage = err.Error()
		return response
	} else if hasApplied {
		if applied.NodeID != request.NodeID {
			response.ErrorCode = "reality_node_mismatch"
			response.ErrorMessage = "local Reality applied state belongs to another node"
			return response
		}
		if request.Version < applied.Version ||
			(request.Version == applied.Version && request.SnapshotSHA256 != applied.SnapshotSHA256) {
			response.ErrorCode = "reality_version_conflict"
			response.ErrorMessage = "Reality desired version conflicts with local applied state"
			return response
		}
	}

	priorIdentity, hadPriorIdentity, err := helper.loadRealityIdentity()
	if err != nil {
		response.ErrorCode = "reality_identity_failed"
		response.ErrorMessage = err.Error()
		return response
	}
	if hadPriorIdentity && priorIdentity.NodeID != request.NodeID {
		response.ErrorCode = "reality_identity_failed"
		response.ErrorMessage = "Reality identity belongs to another node"
		return response
	}
	if hasApplied && request.Settings.KeyGeneration < applied.Reality.KeyGeneration {
		response.ErrorCode = "reality_identity_mismatch"
		response.ErrorMessage = "desired Reality identity generation is stale"
		return response
	}
	if hasApplied && request.Settings.KeyGeneration == applied.Reality.KeyGeneration {
		if !hadPriorIdentity || priorIdentity.KeyGeneration != applied.Reality.KeyGeneration ||
			priorIdentity.PublicKey != applied.Reality.PublicKey ||
			priorIdentity.ShortID != applied.Reality.ShortID {
			response.ErrorCode = "reality_identity_mismatch"
			response.ErrorMessage = "local Reality identity does not match the applied generation"
			return response
		}
		if request.Version == applied.Version {
			response.Status = "succeeded"
			response.Reality = &applied.Reality
			return response
		}
	}
	identity, err := helper.ensureRealityIdentity(request.NodeID, request.Settings.KeyGeneration)
	if err != nil {
		response.ErrorCode = "reality_identity_failed"
		response.ErrorMessage = err.Error()
		return response
	}
	identityChanged := !hadPriorIdentity || !sameRealityIdentity(priorIdentity, identity)
	candidate, err := renderRealityConfig(request, identity)
	if err != nil {
		response.ErrorCode = "reality_render_failed"
		response.ErrorMessage = err.Error()
		return response
	}
	var backup *protocol.Backup
	var createdConfig os.FileInfo
	var applyStage string
	var rolledBack bool
	unchanged := false
	if hasApplied && !identityChanged {
		unchanged = helper.realityConfigMatches(candidate)
	}
	if unchanged {
		if err = helper.checkRealityServiceHealth(ctx, request.Settings.ListenPort); err != nil {
			applyStage = "health"
		}
	} else {
		backup, createdConfig, applyStage, rolledBack, err = helper.applyRealityCandidate(
			ctx, request.RequestID, request.Settings.ListenPort, candidate,
		)
	}
	response.Backup = backup
	response.RolledBack = rolledBack
	if err != nil {
		if identityChanged {
			if restoreErr := helper.restoreRealityIdentity(priorIdentity, hadPriorIdentity); restoreErr != nil {
				response.RolledBack = false
			}
		}
		response.ErrorCode = "reality_" + applyStage + "_failed"
		response.ErrorMessage = err.Error()
		return response
	}
	material := protocol.AppliedRealityMaterial{
		KeyGeneration: identity.KeyGeneration,
		PublicKey:     identity.PublicKey,
		ShortID:       identity.ShortID,
	}
	state := realityAppliedState{
		NodeID: request.NodeID, Version: request.Version,
		SnapshotSHA256: request.SnapshotSHA256, Reality: material, AppliedAt: helper.now(),
	}
	if err := helper.saveRealityApplied(state); err != nil {
		rolledBack := helper.rollbackRealityApply(ctx, backup, createdConfig)
		if identityChanged {
			if restoreErr := helper.restoreRealityIdentity(priorIdentity, hadPriorIdentity); restoreErr != nil {
				rolledBack = false
			}
		}
		if restoreErr := helper.restoreRealityApplied(applied, hasApplied); restoreErr != nil {
			rolledBack = false
		}
		response.RolledBack = rolledBack
		if rolledBack {
			response.ErrorCode = "reality_applied_state_persist_failed"
			response.ErrorMessage = "could not persist Reality applied state; previous service state restored"
		} else {
			response.ErrorCode = "reality_applied_state_rollback_failed"
			response.ErrorMessage = "could not persist Reality applied state or recover the previous service"
		}
		return response
	}
	response.Status = "succeeded"
	response.Reality = &material
	response.CompletedAt = helper.now()
	return response
}

func (helper *Helper) realityConfigMatches(candidate []byte) bool {
	info, err := os.Lstat(helper.CoreConfigPath)
	if err != nil || !validRealityConfigTargetInfo(helper.CoreConfigPath, info) {
		return false
	}
	_, err = verifyRealityConfigFile(
		helper.CoreConfigPath, info, info, info.Mode().Perm(), candidate,
	)
	return err == nil
}

func (helper *Helper) checkRealityServiceHealth(ctx context.Context, listenPort int) error {
	active, err := helper.command(ctx, "systemctl", "is-active", helper.ServiceUnit)
	if err != nil || strings.TrimSpace(string(active)) != "active" {
		return errors.New("managed sing-box service is not active")
	}
	if err := helper.waitTCPListener(ctx, listenPort); err != nil {
		return errors.New("managed sing-box TCP listener is not ready")
	}
	return nil
}

func sameRealityIdentity(left, right realityIdentity) bool {
	return left.NodeID == right.NodeID && left.KeyGeneration == right.KeyGeneration &&
		left.PrivateKey == right.PrivateKey && left.PublicKey == right.PublicKey &&
		left.ShortID == right.ShortID
}

func (helper *Helper) validateRealityApply(request RealityApplyRequest) error {
	if helper.SingBoxBinaryPath == "" || helper.RealityIdentityPath == "" ||
		helper.RealityAppliedPath == "" || helper.CoreConfigPath == "" {
		return errors.New("Reality helper is not configured")
	}
	if _, err := uuid.Parse(request.RequestID); err != nil {
		return errors.New("request_id is not a UUID")
	}
	if _, err := uuid.Parse(request.NodeID); err != nil {
		return errors.New("node_id is not a UUID")
	}
	if request.Version < 1 || !realityDigestPattern.MatchString(request.SnapshotSHA256) {
		return errors.New("desired version or snapshot digest is invalid")
	}
	settings := request.Settings
	if settings.ListenPort < 1 || settings.ListenPort > 65535 {
		return errors.New("Reality listen port is invalid")
	}
	if !validRealityDNSName(settings.ServerName) || !validRealityDNSName(settings.HandshakeServer) {
		return errors.New("Reality server name or handshake server is invalid")
	}
	if settings.HandshakeServerPort != 443 || settings.Flow != "xtls-rprx-vision" ||
		settings.Network != "tcp" || settings.KeyGeneration < 1 || !validRealityAPISecret(settings.APISecret) {
		return errors.New("Reality settings are outside the managed profile")
	}
	if len(request.Users) > maxRealityUsers {
		return errors.New("Reality user count exceeds limit")
	}
	seenIDs := make(map[string]struct{}, len(request.Users))
	seenUUIDs := make(map[string]struct{}, len(request.Users))
	for _, user := range request.Users {
		userID, idErr := uuid.Parse(user.UserID)
		credential, credentialErr := uuid.Parse(user.UUID)
		if idErr != nil || credentialErr != nil || userID.String() != strings.ToLower(user.UserID) ||
			credential.String() != strings.ToLower(user.UUID) {
			return errors.New("Reality user ID or credential UUID is invalid")
		}
		if _, duplicate := seenIDs[user.UserID]; duplicate {
			return errors.New("Reality user IDs must be unique")
		}
		if _, duplicate := seenUUIDs[user.UUID]; duplicate {
			return errors.New("Reality credential UUIDs must be unique")
		}
		seenIDs[user.UserID] = struct{}{}
		seenUUIDs[user.UUID] = struct{}{}
	}
	return nil
}

func (helper *Helper) validateRealityBinary(ctx context.Context) error {
	before, err := os.Lstat(helper.SingBoxBinaryPath)
	if err != nil || !validRealityBinaryInfo(helper.SingBoxBinaryPath, before) {
		return errors.New("managed sing-box binary is not a secure regular file")
	}
	opened, err := os.Open(helper.SingBoxBinaryPath)
	if err != nil {
		return errors.New("managed sing-box binary could not be opened")
	}
	openedInfo, statErr := opened.Stat()
	if statErr != nil || !os.SameFile(before, openedInfo) ||
		!validRealityBinaryInfo(helper.SingBoxBinaryPath, openedInfo) {
		_ = opened.Close()
		return errors.New("managed sing-box binary changed while opening")
	}
	if !validRealitySingBoxChecksum(helper.SingBoxBinaryPath, opened, openedInfo.Size()) {
		_ = opened.Close()
		return errors.New("managed sing-box checksum is not supported")
	}
	if err := opened.Close(); err != nil {
		return errors.New("managed sing-box binary changed while opening")
	}
	output, commandErr := helper.command(ctx, helper.SingBoxBinaryPath, "version")
	if commandErr != nil || len(output) > maxRealitySingBoxVersionOutputLen ||
		!isSupportedRealitySingBoxVersion(output) {
		return errors.New("managed sing-box version is not supported")
	}
	after, err := os.Lstat(helper.SingBoxBinaryPath)
	if err != nil || !validRealityBinaryInfo(helper.SingBoxBinaryPath, after) || !os.SameFile(before, after) ||
		before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return errors.New("managed sing-box binary changed during version check")
	}
	return nil
}

func validRealitySingBoxChecksum(path string, reader io.Reader, size int64) bool {
	if path != "/usr/bin/sing-box" {
		return true
	}
	expected := expectedRealitySingBoxSHA256()
	digest := sha256.New()
	written, err := io.Copy(digest, io.LimitReader(reader, size+1))
	return err == nil && written == size && expected != "" &&
		hex.EncodeToString(digest.Sum(nil)) == expected
}

func expectedRealitySingBoxSHA256() string {
	switch runtime.GOARCH {
	case "amd64":
		return realitySingBoxSHA256AMD64
	case "arm64":
		return realitySingBoxSHA256ARM64
	default:
		return ""
	}
}

func validRealityBinaryInfo(path string, info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 1 || permissionsTooOpen(info.Mode(), 0o022) {
		return false
	}
	if path == "/usr/bin/sing-box" || path == "/usr/local/bin/sing-box" {
		return fileOwnedByRoot(info)
	}
	return true
}

func isSupportedRealitySingBoxVersion(output []byte) bool {
	lines := strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return false
	}
	fields := strings.Fields(lines[0])
	return len(fields) == 3 && fields[0] == "sing-box" && fields[1] == "version" &&
		fields[2] == supportedRealitySingBoxVersion
}

func validRealityDNSName(value string) bool {
	if value == "" || len(value) > 253 || value != strings.ToLower(value) ||
		strings.HasSuffix(value, ".") || net.ParseIP(value) != nil || !strings.Contains(value, ".") {
		return false
	}
	labels := strings.Split(value, ".")
	for _, label := range labels {
		if !realityLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

func renderRealityConfig(request RealityApplyRequest, identity realityIdentity) ([]byte, error) {
	users := make([]singBoxVLESSUser, 0, len(request.Users))
	for _, user := range request.Users {
		users = append(users, singBoxVLESSUser{
			Name: user.UserID, UUID: user.UUID, Flow: request.Settings.Flow,
		})
	}
	configuration := singBoxRealityConfig{
		Log: singBoxLog{Disabled: true},
		Experimental: singBoxExperimental{ClashAPI: singBoxClashAPI{
			ExternalController: "127.0.0.1:18083", Secret: request.Settings.APISecret,
			HyFleetOnly: true,
		}},
		Inbounds: []singBoxInbound{{
			Type: "vless", Tag: "hyfleet-vless-reality-in", Listen: "::",
			ListenPort: request.Settings.ListenPort, Users: users,
			TLS: singBoxTLS{
				Enabled: true, ServerName: request.Settings.ServerName,
				Reality: singBoxReality{
					Enabled: true,
					Handshake: singBoxRealityHandshake{
						Server:     request.Settings.HandshakeServer,
						ServerPort: request.Settings.HandshakeServerPort,
					},
					PrivateKey: identity.PrivateKey, ShortID: []string{identity.ShortID},
					MaxTimeDifference: "1m",
				},
			},
		}},
		Outbounds: []singBoxOutbound{{Type: "direct", Tag: "direct"}},
		Route:     singBoxRoute{Final: "direct"},
	}
	data, err := json.MarshalIndent(configuration, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode managed sing-box configuration: %w", err)
	}
	return append(data, '\n'), nil
}

func validRealityAPISecret(value string) bool {
	if len(value) < 43 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func (helper *Helper) ensureRealityIdentity(nodeID string, generation int64) (realityIdentity, error) {
	identity, ok, err := helper.loadRealityIdentity()
	if err != nil {
		return realityIdentity{}, err
	}
	if ok {
		if identity.NodeID != nodeID {
			return realityIdentity{}, errors.New("Reality identity belongs to another node")
		}
		if generation < identity.KeyGeneration {
			return realityIdentity{}, errors.New("Reality key generation is stale")
		}
		if generation == identity.KeyGeneration {
			return identity, nil
		}
	}
	identity, err = newRealityIdentity(nodeID, generation)
	if err != nil {
		return realityIdentity{}, err
	}
	if err := writeRestrictedJSON(helper.RealityIdentityPath, identity); err != nil {
		return realityIdentity{}, fmt.Errorf("persist Reality identity: %w", err)
	}
	return identity, nil
}

func newRealityIdentity(nodeID string, generation int64) (realityIdentity, error) {
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return realityIdentity{}, fmt.Errorf("generate Reality key: %w", err)
	}
	shortID := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, shortID); err != nil {
		return realityIdentity{}, fmt.Errorf("generate Reality short ID: %w", err)
	}
	return realityIdentity{
		NodeID: nodeID, KeyGeneration: generation,
		PrivateKey: base64.RawURLEncoding.EncodeToString(privateKey.Bytes()),
		PublicKey:  base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()),
		ShortID:    hex.EncodeToString(shortID),
	}, nil
}

func (helper *Helper) loadRealityIdentity() (realityIdentity, bool, error) {
	var identity realityIdentity
	ok, err := readRestrictedJSON(helper.RealityIdentityPath, maxRealityIdentityBytes, &identity)
	if err != nil || !ok {
		return identity, ok, err
	}
	if _, err := uuid.Parse(identity.NodeID); err != nil || identity.KeyGeneration < 1 ||
		!regexp.MustCompile(`^[a-f0-9]{16}$`).MatchString(identity.ShortID) {
		return realityIdentity{}, false, errors.New("Reality identity file is invalid")
	}
	privateBytes, err := base64.RawURLEncoding.DecodeString(identity.PrivateKey)
	if err != nil {
		return realityIdentity{}, false, errors.New("Reality private key is invalid")
	}
	privateKey, err := ecdh.X25519().NewPrivateKey(privateBytes)
	if err != nil || base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()) != identity.PublicKey {
		return realityIdentity{}, false, errors.New("Reality identity key pair is invalid")
	}
	return identity, true, nil
}

func (helper *Helper) loadRealityApplied() (realityAppliedState, bool, error) {
	var state realityAppliedState
	ok, err := readRestrictedJSON(helper.RealityAppliedPath, maxRealityAppliedBytes, &state)
	if err != nil || !ok {
		return state, ok, err
	}
	if state.Version < 1 || !realityDigestPattern.MatchString(state.SnapshotSHA256) ||
		state.Reality.KeyGeneration < 1 || state.Reality.PublicKey == "" || state.Reality.ShortID == "" {
		return realityAppliedState{}, false, errors.New("Reality applied state file is invalid")
	}
	return state, true, nil
}

func (helper *Helper) saveRealityApplied(state realityAppliedState) error {
	return writeRestrictedJSON(helper.RealityAppliedPath, state)
}

func readRestrictedJSON(path string, limit int64, destination any) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		permissionsTooOpen(info.Mode(), 0o077) || info.Size() < 1 || info.Size() > limit {
		return false, errors.New("restricted local state file is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return false, errors.New("open restricted local state file")
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, limit+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || ensureJSONEOF(decoder) != nil {
		return false, errors.New("parse restricted local state file")
	}
	return true, nil
}

func writeRestrictedJSON(path string, value any) error {
	parent := filepath.Dir(path)
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("restricted state parent is invalid")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(parent, ".hyfleet-reality-state-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		info, inspectErr := os.Lstat(path)
		if inspectErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
			permissionsTooOpen(info.Mode(), 0o077) {
			return errors.New("existing restricted local state file is invalid")
		}
		if err := replaceHelperFile(temporaryPath, path); err != nil {
			return err
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(temporaryPath, path); err != nil {
			return err
		}
	} else {
		return err
	}
	return syncDirectory(parent)
}

func (helper *Helper) restoreRealityIdentity(identity realityIdentity, existed bool) error {
	if existed {
		return writeRestrictedJSON(helper.RealityIdentityPath, identity)
	}
	info, err := os.Lstat(helper.RealityIdentityPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("generated Reality identity path is invalid")
	}
	if err := os.Remove(helper.RealityIdentityPath); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(helper.RealityIdentityPath))
}

func (helper *Helper) restoreRealityApplied(state realityAppliedState, existed bool) error {
	if existed {
		return writeRestrictedJSON(helper.RealityAppliedPath, state)
	}
	info, err := os.Lstat(helper.RealityAppliedPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("generated Reality applied state path is invalid")
	}
	if err := os.Remove(helper.RealityAppliedPath); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(helper.RealityAppliedPath))
}

func (helper *Helper) rollbackRealityApply(
	ctx context.Context, backup *protocol.Backup, createdConfig os.FileInfo,
) bool {
	if createdConfig != nil {
		return helper.rollbackCreatedRealityApply(ctx, createdConfig)
	}
	rollbackPort := helper.backupRealityListenPort(backup)
	if backup == nil || helper.restoreBackup(backup.LocalPath) != nil {
		return false
	}
	if err := syncDirectory(filepath.Dir(helper.CoreConfigPath)); err != nil {
		return false
	}
	_, _ = helper.command(ctx, "systemctl", "restart", helper.ServiceUnit)
	active, err := helper.command(ctx, "systemctl", "is-active", helper.ServiceUnit)
	if err != nil || strings.TrimSpace(string(active)) != "active" {
		return false
	}
	return rollbackPort == 0 || helper.waitTCPListener(ctx, rollbackPort) == nil
}

func (helper *Helper) rollbackCreatedRealityApply(ctx context.Context, createdConfig os.FileInfo) bool {
	_, stopErr := helper.command(ctx, "systemctl", "stop", helper.ServiceUnit)
	removed := removeCreatedRealityConfig(helper.CoreConfigPath, createdConfig)
	activeOutput, _ := helper.command(ctx, "systemctl", "is-active", helper.ServiceUnit)
	return stopErr == nil && removed &&
		strings.TrimSpace(string(activeOutput)) == "inactive"
}

func removeCreatedRealityConfig(path string, createdConfig os.FileInfo) bool {
	parent := filepath.Dir(path)
	info, inspectErr := os.Lstat(path)
	switch {
	case errors.Is(inspectErr, os.ErrNotExist):
		// The linked name is already absent, but its removal still needs to be durable.
	case inspectErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(createdConfig, info):
		return false
	default:
		if err := os.Remove(path); err != nil {
			return false
		}
	}
	if err := syncDirectory(parent); err != nil {
		return false
	}
	_, confirmErr := os.Lstat(path)
	return errors.Is(confirmErr, os.ErrNotExist)
}

func (helper *Helper) applyRealityCandidate(
	ctx context.Context, requestID string, listenPort int, candidate []byte,
) (*protocol.Backup, os.FileInfo, string, bool, error) {
	parent := filepath.Dir(helper.CoreConfigPath)
	parentInfo, err := os.Lstat(parent)
	if !validRealityConfigParentInfo(parent, parentInfo, err) {
		return nil, nil, "config_guard", false, errors.New("managed sing-box configuration parent is invalid")
	}
	currentInfo, targetErr := os.Lstat(helper.CoreConfigPath)
	creatingConfig := errors.Is(targetErr, os.ErrNotExist)
	if targetErr != nil && !creatingConfig {
		return nil, nil, "config_guard", false, errors.New("managed sing-box configuration target is invalid")
	}
	if !creatingConfig && !validRealityConfigTargetInfo(helper.CoreConfigPath, currentInfo) {
		return nil, nil, "config_guard", false, errors.New("managed sing-box configuration target is invalid")
	}
	temporary, err := os.CreateTemp(parent, ".hyfleet-reality-candidate-*")
	if err != nil {
		return nil, nil, "candidate_write", false, errors.New("could not create sing-box candidate")
	}
	candidatePath := temporary.Name()
	defer func() {
		if candidatePath != "" {
			_ = os.Remove(candidatePath)
		}
	}()
	candidateMode := os.FileMode(0o640)
	ownershipSource := parentInfo
	if !creatingConfig {
		candidateMode = currentInfo.Mode().Perm()
		ownershipSource = currentInfo
	}
	if err := inheritFileOwnership(temporary, ownershipSource); err != nil {
		_ = temporary.Close()
		return nil, nil, "candidate_write", false, errors.New("could not set sing-box candidate ownership")
	}
	if err := temporary.Chmod(candidateMode); err != nil {
		_ = temporary.Close()
		return nil, nil, "candidate_write", false, errors.New("could not secure sing-box candidate")
	}
	if _, err := temporary.Write(candidate); err != nil {
		_ = temporary.Close()
		return nil, nil, "candidate_write", false, errors.New("could not write sing-box candidate")
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return nil, nil, "candidate_write", false, errors.New("could not sync sing-box candidate")
	}
	if err := temporary.Close(); err != nil {
		return nil, nil, "candidate_write", false, errors.New("could not close sing-box candidate")
	}
	candidateInfo, err := verifyRealityConfigFile(
		candidatePath, nil, ownershipSource, candidateMode, candidate,
	)
	if err != nil {
		return nil, nil, "candidate_write", false, errors.New("could not verify sing-box candidate")
	}
	if _, err := helper.command(ctx, helper.SingBoxBinaryPath, "check", "-c", candidatePath); err != nil {
		return nil, nil, "config_check", false, errors.New("sing-box rejected the generated configuration")
	}
	candidateInfo, err = verifyRealityConfigFile(
		candidatePath, candidateInfo, ownershipSource, candidateMode, candidate,
	)
	if err != nil {
		return nil, nil, "config_guard", false, errors.New("sing-box candidate changed during configuration check")
	}
	latestParent, err := os.Lstat(parent)
	if !validRealityConfigParentInfo(parent, latestParent, err) ||
		!os.SameFile(parentInfo, latestParent) || !sameFileOwnership(parentInfo, latestParent) ||
		!samePermissions(parentInfo.Mode(), latestParent.Mode()) {
		return nil, nil, "config_guard", false, errors.New("managed sing-box configuration parent changed")
	}
	var backup *protocol.Backup
	var createdConfig os.FileInfo
	if creatingConfig {
		candidateInfo, err = verifyRealityConfigFile(
			candidatePath, candidateInfo, ownershipSource, candidateMode, candidate,
		)
		if err != nil {
			return nil, nil, "config_guard", false, errors.New("sing-box candidate changed before publication")
		}
		if err := os.Link(candidatePath, helper.CoreConfigPath); err != nil {
			return nil, nil, "config_replace", false, errors.New("could not create managed sing-box configuration")
		}
		if helper.afterRealityConfigLink != nil {
			helper.afterRealityConfigLink(candidatePath, helper.CoreConfigPath)
		}
		publishedInfo, inspectErr := verifyRealityConfigFile(
			helper.CoreConfigPath, candidateInfo, ownershipSource, candidateMode, candidate,
		)
		if inspectErr != nil {
			rolledBack := removeCreatedRealityConfig(helper.CoreConfigPath, candidateInfo)
			return nil, candidateInfo, "config_replace", rolledBack, errors.New("could not verify managed sing-box configuration")
		}
		createdConfig = publishedInfo
		if err := os.Remove(candidatePath); err != nil {
			rolledBack := helper.rollbackRealityApply(ctx, nil, createdConfig)
			if removeErr := os.Remove(candidatePath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				rolledBack = false
			}
			return nil, createdConfig, "config_replace", rolledBack, errors.New("could not finalize managed sing-box configuration")
		}
		candidatePath = ""
	} else {
		latestTarget, inspectErr := os.Lstat(helper.CoreConfigPath)
		if inspectErr != nil || !validRealityConfigTargetInfo(helper.CoreConfigPath, latestTarget) ||
			!os.SameFile(currentInfo, latestTarget) || !sameFileOwnership(currentInfo, latestTarget) ||
			!samePermissions(currentInfo.Mode(), latestTarget.Mode()) {
			return nil, nil, "config_guard", false, errors.New("managed sing-box configuration target changed")
		}
		backup, err = helper.createBackup(requestID)
		if err != nil {
			return nil, nil, "backup", false, errors.New("could not back up managed sing-box configuration")
		}
		latestTarget, inspectErr = os.Lstat(helper.CoreConfigPath)
		if inspectErr != nil || !validRealityConfigTargetInfo(helper.CoreConfigPath, latestTarget) ||
			!os.SameFile(currentInfo, latestTarget) || !sameFileOwnership(currentInfo, latestTarget) ||
			!samePermissions(currentInfo.Mode(), latestTarget.Mode()) {
			return backup, nil, "config_guard", false, errors.New("managed sing-box configuration changed during backup")
		}
		candidateInfo, err = verifyRealityConfigFile(
			candidatePath, candidateInfo, ownershipSource, candidateMode, candidate,
		)
		if err != nil {
			return backup, nil, "config_guard", false, errors.New("sing-box candidate changed before publication")
		}
		if err := replaceHelperFile(candidatePath, helper.CoreConfigPath); err != nil {
			return backup, nil, "config_replace", false, errors.New("could not activate managed sing-box configuration")
		}
		candidatePath = ""
	}
	if err := syncDirectory(parent); err != nil {
		rolledBack := helper.rollbackRealityApply(ctx, backup, createdConfig)
		return backup, createdConfig, "config_sync", rolledBack, errors.New("could not sync managed sing-box configuration")
	}
	publishedInfo, verifyErr := verifyRealityConfigFile(
		helper.CoreConfigPath, candidateInfo, ownershipSource, candidateMode, candidate,
	)
	latestParent, parentErr := os.Lstat(parent)
	if verifyErr != nil || !validRealityConfigParentInfo(parent, latestParent, parentErr) ||
		!os.SameFile(parentInfo, latestParent) || !sameFileOwnership(parentInfo, latestParent) ||
		!samePermissions(parentInfo.Mode(), latestParent.Mode()) {
		rolledBack := helper.rollbackRealityApply(ctx, backup, createdConfig)
		return backup, createdConfig, "config_guard", rolledBack, errors.New("managed sing-box configuration publication could not be verified")
	}
	if creatingConfig {
		createdConfig = publishedInfo
	}
	restartOutput, restartErr := helper.command(ctx, "systemctl", "restart", helper.ServiceUnit)
	activeOutput, activeErr := helper.command(ctx, "systemctl", "is-active", helper.ServiceUnit)
	_ = restartOutput
	if restartErr == nil && activeErr == nil && strings.TrimSpace(string(activeOutput)) == "active" {
		if err := helper.waitTCPListener(ctx, listenPort); err == nil {
			return backup, createdConfig, "", false, nil
		}
		restartErr = errors.New("managed sing-box TCP listener is not ready")
	}
	rolledBack := helper.rollbackRealityApply(ctx, backup, createdConfig)
	if !rolledBack {
		return backup, createdConfig, "restart_rollback", false, errors.New("sing-box restart failed and rollback did not recover the service")
	}
	stage := "restart"
	if activeErr == nil && strings.TrimSpace(string(activeOutput)) == "active" {
		stage = "health"
	}
	return backup, createdConfig, stage, true, errors.New("sing-box activation failed; previous service state restored")
}

func validRealityConfigParentInfo(path string, info os.FileInfo, inspectErr error) bool {
	if inspectErr != nil || info == nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		permissionsTooOpen(info.Mode(), 0o022) {
		return false
	}
	return !requiresRealityConfigRootOwner(path) || fileOwnedByRootUser(info)
}

func validRealityConfigTargetInfo(path string, info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		permissionsTooOpen(info.Mode(), 0o007|0o022) || info.Size() < 0 ||
		info.Size() > maxConfigBackupBytes {
		return false
	}
	return !requiresRealityConfigRootOwner(path) || fileOwnedByRootUser(info)
}

func verifyRealityConfigFile(
	path string,
	expectedFile os.FileInfo,
	ownershipSource os.FileInfo,
	expectedMode os.FileMode,
	expectedContent []byte,
) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil || !validRealityConfigTargetInfo(path, info) ||
		!exactPermissions(info.Mode(), expectedMode.Perm()) || !sameFileOwnership(info, ownershipSource) ||
		(expectedFile != nil && !os.SameFile(expectedFile, info)) ||
		info.Size() != int64(len(expectedContent)) {
		return nil, errors.New("managed sing-box configuration metadata is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("managed sing-box configuration could not be opened")
	}
	openedInfo, statErr := file.Stat()
	content, readErr := io.ReadAll(io.LimitReader(file, int64(len(expectedContent))+1))
	closeErr := file.Close()
	if statErr != nil || readErr != nil || closeErr != nil || !os.SameFile(info, openedInfo) ||
		!validRealityConfigTargetInfo(path, openedInfo) ||
		!exactPermissions(openedInfo.Mode(), expectedMode.Perm()) ||
		!sameFileOwnership(openedInfo, ownershipSource) || !bytes.Equal(content, expectedContent) {
		return nil, errors.New("managed sing-box configuration changed while opening")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, after) || !validRealityConfigTargetInfo(path, after) ||
		!exactPermissions(after.Mode(), expectedMode.Perm()) || !sameFileOwnership(after, ownershipSource) ||
		after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return nil, errors.New("managed sing-box configuration changed while verifying")
	}
	return after, nil
}

func inheritFileOwnership(file *os.File, source os.FileInfo) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	owner, ok := ownershipOf(source)
	if !ok {
		return errors.New("file ownership metadata is unavailable")
	}
	return file.Chown(int(owner.uid), int(owner.gid))
}

func permissionsTooOpen(mode os.FileMode, mask os.FileMode) bool {
	// Windows FileMode permissions do not represent the ACL enforced by the OS.
	return runtime.GOOS != "windows" && mode.Perm()&mask != 0
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	err = directory.Sync()
	if runtime.GOOS == "windows" {
		return nil
	}
	return err
}

func (helper *Helper) waitTCPListener(ctx context.Context, port int) error {
	if helper.CheckTCPListener != nil {
		return helper.CheckTCPListener(ctx, port)
	}
	deadline := time.NewTimer(5 * time.Second)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	stableSamples := 0
	stableIdentity := ""
	for {
		identity, healthy := helper.realityListenerSample(ctx, port)
		if healthy {
			if identity == stableIdentity {
				stableSamples++
			} else {
				stableIdentity = identity
				stableSamples = 1
			}
			if stableSamples >= 3 {
				return nil
			}
		} else {
			stableIdentity = ""
			stableSamples = 0
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("TCP listener health check timed out")
		case <-ticker.C:
		}
	}
}

func (helper *Helper) realityListenerSample(ctx context.Context, port int) (string, bool) {
	pid, ok := helper.realityMainPID(ctx)
	if !ok {
		return "", false
	}
	procRoot := helper.ProcRoot
	if procRoot == "" {
		procRoot = "/proc"
	}
	startTime, ok := readProcStartTime(procRoot, pid)
	if !ok {
		return "", false
	}
	ownedSockets, ok := readProcSocketInodes(procRoot, pid)
	if !ok || len(ownedSockets) == 0 {
		return "", false
	}
	listeners := procTCPListenerInodes(procRoot, port)
	ownedListener := false
	for inode := range listeners {
		if _, found := ownedSockets[inode]; found {
			ownedListener = true
			break
		}
	}
	if !ownedListener {
		return "", false
	}
	active, err := helper.command(ctx, "systemctl", "is-active", helper.ServiceUnit)
	if err != nil || strings.TrimSpace(string(active)) != "active" {
		return "", false
	}
	confirmedPID, ok := helper.realityMainPID(ctx)
	if !ok || confirmedPID != pid {
		return "", false
	}
	confirmedStartTime, ok := readProcStartTime(procRoot, pid)
	if !ok || confirmedStartTime != startTime {
		return "", false
	}
	return fmt.Sprintf("%d:%s", pid, startTime), true
}

func (helper *Helper) realityMainPID(ctx context.Context) (int, bool) {
	output, err := helper.command(
		ctx, "systemctl", "show", "--property=MainPID", "--value", helper.ServiceUnit,
	)
	value := strings.TrimSpace(string(output))
	if err != nil || value == "" || strings.ContainsAny(value, "\r\n\t ") {
		return 0, false
	}
	pid, err := strconv.Atoi(value)
	return pid, err == nil && pid > 1
}

func readProcStartTime(procRoot string, pid int) (string, bool) {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil || len(data) == 0 || len(data) > 16*1024 {
		return "", false
	}
	closingParenthesis := strings.LastIndexByte(string(data), ')')
	if closingParenthesis < 0 || closingParenthesis+2 >= len(data) {
		return "", false
	}
	fields := strings.Fields(string(data[closingParenthesis+1:]))
	if len(fields) <= 19 {
		return "", false
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return "", false
	}
	return fields[19], true
}

func readProcSocketInodes(procRoot string, pid int) (map[string]struct{}, bool) {
	fdPath := filepath.Join(procRoot, strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(fdPath)
	if err != nil || len(entries) > 4096 {
		return nil, false
	}
	inodes := make(map[string]struct{})
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join(fdPath, entry.Name()))
		if err != nil || !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
			continue
		}
		inode := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
		if inode == "" {
			continue
		}
		if _, err := strconv.ParseUint(inode, 10, 64); err == nil {
			inodes[inode] = struct{}{}
		}
	}
	return inodes, true
}

func procTCPListenerInodes(procRoot string, port int) map[string]struct{} {
	listeners := make(map[string]struct{})
	wantedPort := strings.ToUpper(fmt.Sprintf("%04X", port))
	for _, name := range []string{"tcp", "tcp6"} {
		path := filepath.Join(procRoot, "net", name)
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 2*1024*1024+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(data) > 2*1024*1024 {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "0A" {
				continue
			}
			separator := strings.LastIndexByte(fields[1], ':')
			if separator >= 0 && strings.EqualFold(fields[1][separator+1:], wantedPort) {
				if _, parseErr := strconv.ParseUint(fields[9], 10, 64); parseErr == nil {
					listeners[fields[9]] = struct{}{}
				}
			}
		}
	}
	return listeners
}

func (helper *Helper) managedRealityListenPort() int {
	info, err := os.Lstat(helper.CoreConfigPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		permissionsTooOpen(info.Mode(), 0o007|0o022) || info.Size() < 1 ||
		info.Size() > maxConfigBackupBytes {
		return 0
	}
	data, err := os.ReadFile(helper.CoreConfigPath)
	if err != nil || int64(len(data)) != info.Size() {
		return 0
	}
	return realityListenPort(data)
}

func (helper *Helper) backupRealityListenPort(backup *protocol.Backup) int {
	if backup == nil || !strings.HasSuffix(backup.LocalPath, ".bak") || !helper.isBackupPath(backup.LocalPath) {
		return 0
	}
	data, err := os.ReadFile(backup.LocalPath)
	if err != nil || len(data) > int(maxConfigBackupBytes) {
		return 0
	}
	return realityListenPort(data)
}

func realityListenPort(data []byte) int {
	var config singBoxRealityConfig
	if json.Unmarshal(data, &config) != nil {
		return 0
	}
	listenPort := 0
	for _, inbound := range config.Inbounds {
		if inbound.Type == "vless" && inbound.Tag == "hyfleet-vless-reality-in" &&
			inbound.ListenPort > 0 && inbound.ListenPort <= 65535 {
			if listenPort != 0 {
				return 0
			}
			listenPort = inbound.ListenPort
		}
	}
	return listenPort
}
