package nodeops

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Sen62455/PolyFleet/internal/protocol"
)

type realityBinaryFileInfo struct {
	owner struct {
		Uid uint32
		Gid uint32
	}
}

func (info realityBinaryFileInfo) Name() string       { return "sing-box" }
func (info realityBinaryFileInfo) Size() int64        { return 1 }
func (info realityBinaryFileInfo) Mode() fs.FileMode  { return 0o755 }
func (info realityBinaryFileInfo) ModTime() time.Time { return time.Time{} }
func (info realityBinaryFileInfo) IsDir() bool        { return false }
func (info realityBinaryFileInfo) Sys() any           { return info.owner }

func testRealityHelper(t *testing.T) (*Helper, RealityApplyRequest, string) {
	t.Helper()
	root := t.TempDir()
	configPath := filepath.Join(root, "etc", "sing-box", "hyfleet-reality.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		t.Fatalf("MkdirAll(config) error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte("{\"known_good\":true}\n"), 0o640); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	binaryPath := filepath.Join(root, "sing-box")
	if err := os.WriteFile(binaryPath, []byte("test sing-box binary\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(sing-box) error = %v", err)
	}
	stateDir := filepath.Join(root, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(state) error = %v", err)
	}
	helper := &Helper{
		ServiceUnit: "hyfleet-sing-box-reality.service", CoreConfigPath: configPath,
		SingBoxBinaryPath:   binaryPath,
		RealityIdentityPath: filepath.Join(stateDir, "identity.json"),
		RealityAppliedPath:  filepath.Join(stateDir, "applied.json"),
		BackupDir:           filepath.Join(root, "backups"), LedgerDir: filepath.Join(root, "ledger"),
		CheckTCPListener: func(context.Context, int) error {
			return nil
		},
		Now: func() time.Time { return time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC) },
	}
	digest := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	request := RealityApplyRequest{
		RequestID: uuid.NewString(), NodeID: uuid.NewString(), Version: 1,
		SnapshotSHA256: digest,
		Settings: RealityApplySettings{
			ListenPort: 18443, ServerName: "www.microsoft.com",
			HandshakeServer: "www.microsoft.com", HandshakeServerPort: 443,
			Flow: "xtls-rprx-vision", Network: "tcp", KeyGeneration: 1,
			APISecret: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		},
		Users: []RealityApplyUser{{UserID: uuid.NewString(), UUID: uuid.NewString()}},
	}
	return helper, request, configPath
}

func TestRenderRealityConfigDisablesCredentialBearingCoreLogs(t *testing.T) {
	_, request, _ := testRealityHelper(t)
	identity, err := newRealityIdentity(request.NodeID, request.Settings.KeyGeneration)
	if err != nil {
		t.Fatalf("newRealityIdentity() error = %v", err)
	}
	rendered, err := renderRealityConfig(request, identity)
	if err != nil {
		t.Fatalf("renderRealityConfig() error = %v", err)
	}
	var configuration struct {
		Log map[string]any `json:"log"`
	}
	if err := json.Unmarshal(rendered, &configuration); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if disabled, ok := configuration.Log["disabled"].(bool); !ok || !disabled {
		t.Fatalf("rendered log contract = %#v, want disabled", configuration.Log)
	}
	if len(configuration.Log) != 1 {
		t.Fatalf("rendered log contract enables additional output: %#v", configuration.Log)
	}
}

func TestRealityApplyChecksActivatesAndReplaysWithoutSecrets(t *testing.T) {
	helper, request, configPath := testRealityHelper(t)
	commands := 0
	helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if isRealityVersionCommand(helper, name, arguments) {
			return supportedRealityVersionOutput(), nil
		}
		commands++
		switch commands {
		case 1:
			if name != helper.SingBoxBinaryPath || len(arguments) != 3 || arguments[0] != "check" ||
				arguments[1] != "-c" || !strings.Contains(arguments[2], ".hyfleet-reality-candidate-") {
				t.Fatalf("unexpected check command: %s %v", name, arguments)
			}
			return nil, nil
		case 2:
			if name != "systemctl" || strings.Join(arguments, " ") != "restart hyfleet-sing-box-reality.service" {
				t.Fatalf("unexpected restart command: %s %v", name, arguments)
			}
			return nil, nil
		case 3:
			return []byte("active\n"), nil
		default:
			t.Fatalf("unexpected command %d: %s %v", commands, name, arguments)
			return nil, nil
		}
	}

	result := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if result.Status != "succeeded" || result.Reality == nil || result.Reality.KeyGeneration != 1 ||
		result.AppliedVersion != 1 || result.SnapshotSHA256 != request.SnapshotSHA256 {
		t.Fatalf("Reality apply response = %#v", result)
	}
	configured, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config) error = %v", err)
	}
	if !strings.Contains(string(configured), `"type": "vless"`) ||
		!strings.Contains(string(configured), request.Users[0].UUID) ||
		strings.Contains(string(configured), `"network"`) ||
		!strings.Contains(string(configured), `"flow": "xtls-rprx-vision"`) {
		t.Fatalf("generated configuration = %s", configured)
	}
	encodedResult, _ := json.Marshal(result)
	if strings.Contains(string(encodedResult), request.Users[0].UUID) || strings.Contains(string(encodedResult), "private_key") {
		t.Fatalf("helper response leaked secret material: %s", encodedResult)
	}
	replayed := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if replayed.Status != "succeeded" || replayed.Reality == nil ||
		replayed.Reality.PublicKey != result.Reality.PublicKey || commands != 3 {
		t.Fatalf("idempotent replay = %#v, commands=%d", replayed, commands)
	}
}

func TestRealityApplyWithUnchangedConfigChecksHealthWithoutRestart(t *testing.T) {
	helper, request, configPath := testRealityHelper(t)
	helper.RunCommand = successfulRealityCommands(t, helper)
	first := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if first.Status != "succeeded" {
		t.Fatalf("initial Reality apply = %#v", first)
	}

	request.Version = 2
	request.RequestID = uuid.NewString()
	request.SnapshotSHA256 = base64.RawURLEncoding.EncodeToString(bytesFilled(32, 9))
	listenerChecks := 0
	helper.CheckTCPListener = func(_ context.Context, port int) error {
		listenerChecks++
		if port != request.Settings.ListenPort {
			t.Fatalf("listener port = %d, want %d", port, request.Settings.ListenPort)
		}
		return nil
	}
	commands := 0
	helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if isRealityVersionCommand(helper, name, arguments) {
			return supportedRealityVersionOutput(), nil
		}
		commands++
		if name != "systemctl" || strings.Join(arguments, " ") !=
			"is-active hyfleet-sing-box-reality.service" {
			t.Fatalf("unchanged apply invoked disruptive command: %s %v", name, arguments)
		}
		return []byte("active\n"), nil
	}
	result := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if result.Status != "succeeded" || commands != 1 || listenerChecks != 1 || result.Backup != nil {
		t.Fatalf("unchanged Reality apply = %#v; commands=%d listener_checks=%d", result, commands, listenerChecks)
	}
	configured, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(configured), request.Users[0].UUID) {
		t.Fatalf("unchanged Reality config was lost: %v", err)
	}
}

func TestRealityApplyWithUnchangedConfigFailsWhenServiceIsUnhealthy(t *testing.T) {
	helper, request, _ := testRealityHelper(t)
	helper.RunCommand = successfulRealityCommands(t, helper)
	first := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if first.Status != "succeeded" {
		t.Fatalf("initial Reality apply = %#v", first)
	}

	request.Version = 2
	request.RequestID = uuid.NewString()
	request.SnapshotSHA256 = base64.RawURLEncoding.EncodeToString(bytesFilled(32, 10))
	helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if isRealityVersionCommand(helper, name, arguments) {
			return supportedRealityVersionOutput(), nil
		}
		if name != "systemctl" || strings.Join(arguments, " ") !=
			"is-active hyfleet-sing-box-reality.service" {
			t.Fatalf("unexpected health command: %s %v", name, arguments)
		}
		return []byte("inactive\n"), errors.New("inactive")
	}
	result := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if result.Status != "failed" || result.ErrorCode != "reality_health_failed" ||
		result.RolledBack || result.Backup != nil {
		t.Fatalf("unhealthy unchanged Reality apply = %#v", result)
	}
	applied, ok, err := helper.loadRealityApplied()
	if err != nil || !ok || applied.Version != 1 {
		t.Fatalf("failed health apply advanced local state: %#v, %v, %v", applied, ok, err)
	}
}

func TestRealityRestartRequiresManagedListenerHealth(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		listenerResults  []error
		wantStatus       string
		wantRolledBack   bool
		wantCommandCalls int
	}{
		{
			name: "healthy after restart", listenerResults: []error{nil},
			wantStatus: "succeeded", wantCommandCalls: 2,
		},
		{
			name:            "healthy only after rollback",
			listenerResults: []error{errors.New("listener unavailable"), nil},
			wantStatus:      "failed", wantRolledBack: true, wantCommandCalls: 4,
		},
		{
			name: "unhealthy after rollback",
			listenerResults: []error{
				errors.New("listener unavailable"), errors.New("listener still unavailable"),
			},
			wantStatus: "failed", wantCommandCalls: 4,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			helper, request, configPath := testRealityHelper(t)
			identity, err := newRealityIdentity(request.NodeID, request.Settings.KeyGeneration)
			if err != nil {
				t.Fatalf("newRealityIdentity() error = %v", err)
			}
			config, err := renderRealityConfig(request, identity)
			if err != nil {
				t.Fatalf("renderRealityConfig() error = %v", err)
			}
			if err := os.WriteFile(configPath, config, 0o640); err != nil {
				t.Fatalf("WriteFile(config) error = %v", err)
			}

			commandCalls := 0
			helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
				commandCalls++
				if name != "systemctl" || len(arguments) != 2 ||
					arguments[1] != helper.ServiceUnit {
					t.Fatalf("unexpected command: %s %v", name, arguments)
				}
				switch arguments[0] {
				case "restart":
					return nil, nil
				case "is-active":
					return []byte("active\n"), nil
				default:
					t.Fatalf("unexpected systemctl action: %v", arguments)
					return nil, nil
				}
			}
			listenerCalls := 0
			helper.CheckTCPListener = func(_ context.Context, port int) error {
				if port != request.Settings.ListenPort {
					t.Fatalf("listener port = %d, want %d", port, request.Settings.ListenPort)
				}
				if listenerCalls >= len(testCase.listenerResults) {
					t.Fatalf("unexpected listener check %d", listenerCalls+1)
				}
				result := testCase.listenerResults[listenerCalls]
				listenerCalls++
				return result
			}

			operation := protocol.NodeOperation{
				ID: uuid.NewString(), Sequence: 1, Type: "restart_core", Attempt: 1,
			}
			result := helper.Handle(t.Context(), HelperRequest{Operation: &operation})
			if result.Status != testCase.wantStatus || result.RolledBack != testCase.wantRolledBack ||
				commandCalls != testCase.wantCommandCalls || listenerCalls != len(testCase.listenerResults) {
				t.Fatalf(
					"restart result = %#v; command calls = %d; listener calls = %d",
					result, commandCalls, listenerCalls,
				)
			}
			if result.Status == "failed" && result.ErrorCode != "core_restart_failed" {
				t.Fatalf("restart error code = %q", result.ErrorCode)
			}
		})
	}
}

func TestRealityInitialApplyCreatesConfigurationWithoutBackup(t *testing.T) {
	helper, request, configPath := testRealityHelper(t)
	if err := os.Remove(configPath); err != nil {
		t.Fatalf("Remove(config) error = %v", err)
	}
	parentUID, parentGID := testFileOwnership(t, filepath.Dir(configPath))
	helper.RunCommand = successfulRealityCommands(t, helper)

	result := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if result.Status != "succeeded" || result.Backup != nil || result.Reality == nil {
		t.Fatalf("initial Reality apply = %#v", result)
	}
	info, err := os.Lstat(configPath)
	if err != nil || !info.Mode().IsRegular() ||
		(runtime.GOOS != "windows" && info.Mode().Perm() != 0o640) {
		t.Fatalf("created config info = %#v, error = %v", info, err)
	}
	uid, gid := testFileOwnership(t, configPath)
	if runtime.GOOS != "windows" && (uid != parentUID || gid != parentGID) {
		t.Fatalf("created config ownership = %d:%d, parent = %d:%d", uid, gid, parentUID, parentGID)
	}
}

func TestRealityInitialApplyCleansLinkedConfigWhenPublicationVerificationFails(t *testing.T) {
	helper, request, configPath := testRealityHelper(t)
	if err := os.Remove(configPath); err != nil {
		t.Fatalf("Remove(config) error = %v", err)
	}
	helper.RunCommand = successfulRealityCommands(t, helper)
	helper.afterRealityConfigLink = func(candidatePath, targetPath string) {
		candidateInfo, candidateErr := os.Lstat(candidatePath)
		targetInfo, targetErr := os.Lstat(targetPath)
		if candidateErr != nil || targetErr != nil || !os.SameFile(candidateInfo, targetInfo) {
			t.Fatalf("linked config identity = (%#v, %v, %#v, %v)", candidateInfo, candidateErr, targetInfo, targetErr)
		}
		if err := os.WriteFile(targetPath, []byte("tampered\n"), 0o640); err != nil {
			t.Fatalf("WriteFile(linked config) error = %v", err)
		}
	}

	result := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if result.Status != "failed" || result.ErrorCode != "reality_config_replace_failed" ||
		!result.RolledBack || result.Backup != nil {
		t.Fatalf("linked config verification result = %#v", result)
	}
	assertRealityInitialStateRemoved(t, helper, configPath)
}

func TestRealityInitialApplyPreservesReplacementWhenPublicationVerificationFails(t *testing.T) {
	helper, request, configPath := testRealityHelper(t)
	if err := os.Remove(configPath); err != nil {
		t.Fatalf("Remove(config) error = %v", err)
	}
	helper.RunCommand = successfulRealityCommands(t, helper)
	helper.afterRealityConfigLink = func(_, targetPath string) {
		if err := os.Remove(targetPath); err != nil {
			t.Fatalf("Remove(linked config) error = %v", err)
		}
		if err := os.WriteFile(targetPath, []byte("racing-owner\n"), 0o640); err != nil {
			t.Fatalf("WriteFile(replacement config) error = %v", err)
		}
	}

	result := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if result.Status != "failed" || result.ErrorCode != "reality_config_replace_failed" ||
		result.RolledBack || result.Backup != nil {
		t.Fatalf("replacement config verification result = %#v", result)
	}
	contents, err := os.ReadFile(configPath)
	if err != nil || string(contents) != "racing-owner\n" {
		t.Fatalf("replacement config contents = %q, error = %v", contents, err)
	}
	if _, err := os.Lstat(helper.RealityIdentityPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement failure retained generated identity: %v", err)
	}
}

func TestRealityInitialApplyDoesNotOverwriteRacingTarget(t *testing.T) {
	helper, request, configPath := testRealityHelper(t)
	if err := os.Remove(configPath); err != nil {
		t.Fatalf("Remove(config) error = %v", err)
	}
	commands := 0
	helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if isRealityVersionCommand(helper, name, arguments) {
			return supportedRealityVersionOutput(), nil
		}
		commands++
		if commands != 1 || name != helper.SingBoxBinaryPath {
			t.Fatalf("unexpected command %d: %s %v", commands, name, arguments)
		}
		if err := os.WriteFile(configPath, []byte("racing-owner\n"), 0o640); err != nil {
			t.Fatalf("WriteFile(racing target) error = %v", err)
		}
		return nil, nil
	}

	result := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if result.Status != "failed" || result.ErrorCode != "reality_config_replace_failed" ||
		result.RolledBack || result.Backup != nil {
		t.Fatalf("racing target result = %#v", result)
	}
	contents, err := os.ReadFile(configPath)
	if err != nil || string(contents) != "racing-owner\n" {
		t.Fatalf("racing target contents = %q, error = %v", contents, err)
	}
	if _, err := os.Lstat(helper.RealityIdentityPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("racing target failure retained generated identity: %v", err)
	}
}

func TestRealityInitialApplyRestartFailureStopsAndRemovesCreatedState(t *testing.T) {
	helper, request, configPath := testRealityHelper(t)
	if err := os.Remove(configPath); err != nil {
		t.Fatalf("Remove(config) error = %v", err)
	}
	commands := 0
	helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if isRealityVersionCommand(helper, name, arguments) {
			return supportedRealityVersionOutput(), nil
		}
		commands++
		switch commands {
		case 1:
			return nil, nil
		case 2:
			return nil, errors.New("restart failed")
		case 3:
			return []byte("failed\n"), errors.New("inactive")
		case 4:
			if name != "systemctl" || strings.Join(arguments, " ") != "stop hyfleet-sing-box-reality.service" {
				t.Fatalf("unexpected stop command: %s %v", name, arguments)
			}
			return nil, nil
		case 5:
			return []byte("inactive\n"), errors.New("exit status 3")
		default:
			t.Fatalf("unexpected command %d: %s %v", commands, name, arguments)
			return nil, nil
		}
	}

	result := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if result.Status != "failed" || result.ErrorCode != "reality_restart_failed" ||
		!result.RolledBack || result.Backup != nil || result.Reality != nil {
		t.Fatalf("initial restart failure = %#v", result)
	}
	assertRealityInitialStateRemoved(t, helper, configPath)
}

func TestRealityInitialApplyMissingListenerStopsAndRemovesCreatedState(t *testing.T) {
	helper, request, configPath := testRealityHelper(t)
	if err := os.Remove(configPath); err != nil {
		t.Fatalf("Remove(config) error = %v", err)
	}
	helper.CheckTCPListener = func(_ context.Context, port int) error {
		if port != request.Settings.ListenPort {
			t.Fatalf("listener port = %d, want %d", port, request.Settings.ListenPort)
		}
		return errors.New("listener missing")
	}
	commands := 0
	helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if isRealityVersionCommand(helper, name, arguments) {
			return supportedRealityVersionOutput(), nil
		}
		commands++
		switch commands {
		case 1, 2, 4:
			return nil, nil
		case 3:
			return []byte("active\n"), nil
		case 5:
			return []byte("inactive\n"), errors.New("exit status 3")
		default:
			t.Fatalf("unexpected command %d: %s %v", commands, name, arguments)
			return nil, nil
		}
	}

	result := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if result.Status != "failed" || result.ErrorCode != "reality_health_failed" || !result.RolledBack {
		t.Fatalf("initial listener failure = %#v", result)
	}
	assertRealityInitialStateRemoved(t, helper, configPath)
}

func TestRealityInitialApplyAppliedStateFailureStopsAndRemovesCreatedState(t *testing.T) {
	helper, request, configPath := testRealityHelper(t)
	if err := os.Remove(configPath); err != nil {
		t.Fatalf("Remove(config) error = %v", err)
	}
	helper.RealityAppliedPath = filepath.Join(filepath.Dir(helper.RealityAppliedPath), "missing", "applied.json")
	commands := 0
	helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if isRealityVersionCommand(helper, name, arguments) {
			return supportedRealityVersionOutput(), nil
		}
		commands++
		switch commands {
		case 1, 2, 4:
			return nil, nil
		case 3:
			return []byte("active\n"), nil
		case 5:
			return []byte("inactive\n"), errors.New("exit status 3")
		default:
			t.Fatalf("unexpected command count %d", commands)
			return nil, nil
		}
	}

	result := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if result.Status != "failed" || result.ErrorCode != "reality_applied_state_persist_failed" ||
		!result.RolledBack {
		t.Fatalf("initial applied-state failure = %#v", result)
	}
	assertRealityInitialStateRemoved(t, helper, configPath)
}

func TestRealityInitialApplyRollbackRequiresSuccessfulStopAndInactiveState(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		stopErr     error
		activeState string
	}{
		{name: "stop fails", stopErr: errors.New("stop failed"), activeState: "inactive\n"},
		{name: "unit remains active", activeState: "active\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			helper, request, configPath := testRealityHelper(t)
			if err := os.Remove(configPath); err != nil {
				t.Fatalf("Remove(config) error = %v", err)
			}
			commands := 0
			helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
				if isRealityVersionCommand(helper, name, arguments) {
					return supportedRealityVersionOutput(), nil
				}
				commands++
				switch commands {
				case 1:
					return nil, nil
				case 2:
					return nil, errors.New("restart failed")
				case 3:
					return []byte("failed\n"), errors.New("inactive")
				case 4:
					return nil, testCase.stopErr
				case 5:
					return []byte(testCase.activeState), errors.New("is-active status")
				default:
					t.Fatalf("unexpected command count %d", commands)
					return nil, nil
				}
			}

			result := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
			if result.Status != "failed" || result.ErrorCode != "reality_restart_rollback_failed" ||
				result.RolledBack {
				t.Fatalf("incomplete initial rollback = %#v", result)
			}
		})
	}
}

func TestRealityInitialApplyRejectsUnsafeParentAndSymlinkTarget(t *testing.T) {
	t.Run("unsafe parent", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows permissions are represented by ACLs")
		}
		helper, request, configPath := testRealityHelper(t)
		if err := os.Remove(configPath); err != nil {
			t.Fatalf("Remove(config) error = %v", err)
		}
		if err := os.Chmod(filepath.Dir(configPath), 0o770); err != nil {
			t.Fatalf("Chmod(parent) error = %v", err)
		}
		assertRealityConfigGuardFailure(t, helper, request)
	})

	t.Run("symlink target", func(t *testing.T) {
		helper, request, configPath := testRealityHelper(t)
		target := filepath.Join(filepath.Dir(configPath), "outside.json")
		if err := os.WriteFile(target, []byte("outside\n"), 0o640); err != nil {
			t.Fatalf("WriteFile(target) error = %v", err)
		}
		if err := os.Remove(configPath); err != nil {
			t.Fatalf("Remove(config) error = %v", err)
		}
		if err := os.Symlink(target, configPath); err != nil {
			t.Skipf("Symlink is unavailable: %v", err)
		}
		assertRealityConfigGuardFailure(t, helper, request)
		contents, err := os.ReadFile(target)
		if err != nil || string(contents) != "outside\n" {
			t.Fatalf("symlink target changed: %q, error = %v", contents, err)
		}
	})

	t.Run("group writable target", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows permissions are represented by ACLs")
		}
		helper, request, configPath := testRealityHelper(t)
		if err := os.Chmod(configPath, 0o660); err != nil {
			t.Fatalf("Chmod(config) error = %v", err)
		}
		assertRealityConfigGuardFailure(t, helper, request)
		contents, err := os.ReadFile(configPath)
		if err != nil || string(contents) != "{\"known_good\":true}\n" {
			t.Fatalf("group-writable config changed: %q, error = %v", contents, err)
		}
	})

	t.Run("world readable target", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows permissions are represented by ACLs")
		}
		helper, request, configPath := testRealityHelper(t)
		if err := os.Chmod(configPath, 0o644); err != nil {
			t.Fatalf("Chmod(config) error = %v", err)
		}
		assertRealityConfigGuardFailure(t, helper, request)
		contents, err := os.ReadFile(configPath)
		if err != nil || string(contents) != "{\"known_good\":true}\n" {
			t.Fatalf("world-readable config changed: %q, error = %v", contents, err)
		}
	})
}

func TestRealityCandidateMutationAfterCheckIsRejected(t *testing.T) {
	helper, request, configPath := testRealityHelper(t)
	helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if isRealityVersionCommand(helper, name, arguments) {
			return supportedRealityVersionOutput(), nil
		}
		if name != helper.SingBoxBinaryPath || len(arguments) != 3 ||
			arguments[0] != "check" || arguments[1] != "-c" {
			t.Fatalf("unexpected command: %s %v", name, arguments)
		}
		if err := os.WriteFile(arguments[2], []byte("tampered after check\n"), 0o640); err != nil {
			t.Fatalf("tamper candidate: %v", err)
		}
		return nil, nil
	}

	result := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if result.Status != "failed" || result.ErrorCode != "reality_config_guard_failed" ||
		result.RolledBack || result.Backup != nil {
		t.Fatalf("mutated candidate result = %#v", result)
	}
	configured, err := os.ReadFile(configPath)
	if err != nil || string(configured) != "{\"known_good\":true}\n" {
		t.Fatalf("mutated candidate changed known-good config: %q, %v", configured, err)
	}
}

func TestRealityExistingConfigurationPreservesMetadataOnApplyAndRollback(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		restartFail bool
	}{
		{name: "successful apply"},
		{name: "failed apply rollback", restartFail: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			helper, request, configPath := testRealityHelper(t)
			if err := os.Chmod(configPath, 0o640); err != nil {
				t.Fatalf("Chmod(config) error = %v", err)
			}
			before, err := os.Lstat(configPath)
			if err != nil {
				t.Fatalf("Lstat(config) error = %v", err)
			}
			beforeUID, beforeGID := testFileOwnership(t, configPath)
			if testCase.restartFail {
				commands := 0
				helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
					if isRealityVersionCommand(helper, name, arguments) {
						return supportedRealityVersionOutput(), nil
					}
					commands++
					switch commands {
					case 1, 4:
						return nil, nil
					case 2:
						return nil, errors.New("restart failed")
					case 3:
						return []byte("failed\n"), errors.New("inactive")
					case 5:
						return []byte("active\n"), nil
					default:
						t.Fatalf("unexpected command count %d", commands)
						return nil, nil
					}
				}
			} else {
				helper.RunCommand = successfulRealityCommands(t, helper)
			}

			result := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
			if testCase.restartFail {
				if result.Status != "failed" || !result.RolledBack {
					t.Fatalf("rollback result = %#v", result)
				}
				contents, _ := os.ReadFile(configPath)
				if string(contents) != "{\"known_good\":true}\n" {
					t.Fatalf("rollback contents = %q", contents)
				}
			} else if result.Status != "succeeded" {
				t.Fatalf("apply result = %#v", result)
			}
			after, err := os.Lstat(configPath)
			if err != nil || after.Mode().Perm() != before.Mode().Perm() {
				t.Fatalf("config mode after apply = %v, before = %v, error = %v", after.Mode(), before.Mode(), err)
			}
			afterUID, afterGID := testFileOwnership(t, configPath)
			if runtime.GOOS != "windows" && (afterUID != beforeUID || afterGID != beforeGID) {
				t.Fatalf("config ownership after apply = %d:%d, before = %d:%d", afterUID, afterGID, beforeUID, beforeGID)
			}
		})
	}
}

func TestRealityApplyCheckFailureLeavesKnownGoodConfiguration(t *testing.T) {
	helper, request, configPath := testRealityHelper(t)
	helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if isRealityVersionCommand(helper, name, arguments) {
			return supportedRealityVersionOutput(), nil
		}
		if name != helper.SingBoxBinaryPath {
			t.Fatalf("unexpected command after failed check: %s %v", name, arguments)
		}
		return []byte("private_key=must-be-redacted"), errors.New("exit status 1")
	}
	result := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if result.Status != "failed" || result.ErrorCode != "reality_config_check_failed" || result.Reality != nil {
		t.Fatalf("Reality check failure = %#v", result)
	}
	configured, _ := os.ReadFile(configPath)
	if string(configured) != "{\"known_good\":true}\n" {
		t.Fatalf("check failure changed config: %q", configured)
	}
	if _, err := os.Stat(helper.RealityIdentityPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed initial apply retained generated identity: %v", err)
	}
}

func TestRealityApplyRestartFailureRollsBackConfigurationAndIdentity(t *testing.T) {
	helper, request, configPath := testRealityHelper(t)
	commands := 0
	helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if isRealityVersionCommand(helper, name, arguments) {
			return supportedRealityVersionOutput(), nil
		}
		commands++
		switch commands {
		case 1:
			return nil, nil
		case 2:
			return nil, errors.New("restart failed")
		case 3:
			return []byte("failed"), errors.New("inactive")
		case 4:
			return nil, nil
		case 5:
			return []byte("active"), nil
		default:
			t.Fatalf("unexpected command %d: %s %v", commands, name, arguments)
			return nil, nil
		}
	}
	result := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if result.Status != "failed" || result.ErrorCode != "reality_restart_failed" ||
		!result.RolledBack || result.Reality != nil {
		t.Fatalf("Reality restart failure = %#v", result)
	}
	configured, _ := os.ReadFile(configPath)
	if string(configured) != "{\"known_good\":true}\n" {
		t.Fatalf("restart rollback config = %q", configured)
	}
	if _, err := os.Stat(helper.RealityIdentityPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restart rollback retained generated identity: %v", err)
	}
}

func TestRealityApplyMissingListenerRollsBackConfigurationAndIdentity(t *testing.T) {
	helper, request, configPath := testRealityHelper(t)
	checks := 0
	helper.CheckTCPListener = func(_ context.Context, port int) error {
		checks++
		if port != request.Settings.ListenPort {
			t.Fatalf("listener port = %d, want %d", port, request.Settings.ListenPort)
		}
		if checks == 1 {
			return errors.New("listener missing")
		}
		return nil
	}
	commands := 0
	helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if isRealityVersionCommand(helper, name, arguments) {
			return supportedRealityVersionOutput(), nil
		}
		commands++
		switch commands {
		case 1:
			if name != helper.SingBoxBinaryPath {
				t.Fatalf("unexpected check command: %s %v", name, arguments)
			}
			return nil, nil
		case 2, 4:
			if name != "systemctl" || len(arguments) < 1 || arguments[0] != "restart" {
				t.Fatalf("unexpected restart command: %s %v", name, arguments)
			}
			return nil, nil
		case 3, 5:
			return []byte("active"), nil
		default:
			t.Fatalf("unexpected command %d: %s %v", commands, name, arguments)
			return nil, nil
		}
	}

	result := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if result.Status != "failed" || result.ErrorCode != "reality_health_failed" ||
		!result.RolledBack || result.Reality != nil {
		t.Fatalf("Reality listener failure = %#v", result)
	}
	configured, _ := os.ReadFile(configPath)
	if string(configured) != "{\"known_good\":true}\n" {
		t.Fatalf("listener rollback config = %q", configured)
	}
	if _, err := os.Stat(helper.RealityIdentityPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("listener rollback retained generated identity: %v", err)
	}
}

func TestRealityApplyRejectsChangedIdentityAtSameGeneration(t *testing.T) {
	helper, request, _ := testRealityHelper(t)
	helper.RunCommand = successfulRealityCommands(t, helper)
	first := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if first.Status != "succeeded" {
		t.Fatalf("initial Reality apply = %#v", first)
	}
	replacement, err := newRealityIdentity(request.NodeID, 1)
	if err != nil || writeRestrictedJSON(helper.RealityIdentityPath, replacement) != nil {
		t.Fatalf("replace identity error = %v", err)
	}
	request.Version = 2
	request.RequestID = uuid.NewString()
	request.SnapshotSHA256 = base64.RawURLEncoding.EncodeToString(bytesFilled(32, 1))
	result := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if result.Status != "failed" || result.ErrorCode != "reality_identity_mismatch" || result.Reality != nil {
		t.Fatalf("same-generation identity replacement = %#v", result)
	}
}

func TestRealityApplyRotatesIdentityAtHigherGeneration(t *testing.T) {
	helper, request, configPath := testRealityHelper(t)
	helper.RunCommand = successfulRealityCommands(t, helper)
	first := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if first.Status != "succeeded" || first.Reality == nil {
		t.Fatalf("initial Reality apply = %#v", first)
	}
	firstPublicKey := first.Reality.PublicKey
	firstShortID := first.Reality.ShortID

	request.Version = 2
	request.RequestID = uuid.NewString()
	request.SnapshotSHA256 = base64.RawURLEncoding.EncodeToString(bytesFilled(32, 2))
	request.Settings.KeyGeneration = 2
	helper.RunCommand = successfulRealityCommands(t, helper)
	second := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if second.Status != "succeeded" || second.Reality == nil ||
		second.Reality.KeyGeneration != 2 || second.AppliedVersion != 2 ||
		second.Reality.PublicKey == firstPublicKey || second.Reality.ShortID == firstShortID {
		t.Fatalf("rotated Reality apply = %#v", second)
	}
	identity, ok, err := helper.loadRealityIdentity()
	if err != nil || !ok || identity.KeyGeneration != 2 ||
		identity.PublicKey != second.Reality.PublicKey || identity.ShortID != second.Reality.ShortID {
		t.Fatalf("rotated Reality identity = (%#v, %v, %v)", identity, ok, err)
	}
	applied, ok, err := helper.loadRealityApplied()
	if err != nil || !ok || applied.Version != 2 || applied.Reality.KeyGeneration != 2 ||
		applied.Reality.PublicKey != second.Reality.PublicKey {
		t.Fatalf("rotated Reality applied state = (%#v, %v, %v)", applied, ok, err)
	}
	configured, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(configured), identity.PrivateKey) ||
		strings.Contains(string(configured), firstPublicKey) {
		t.Fatalf("rotated Reality configuration is stale: %v", err)
	}
}

func TestRealityBinaryProductionPathRequiresRootOwnership(t *testing.T) {
	rootOwned := realityBinaryFileInfo{}
	if !validRealityBinaryInfo("/usr/bin/sing-box", rootOwned) {
		t.Fatal("root-owned production sing-box binary was rejected")
	}
	nonRoot := realityBinaryFileInfo{}
	nonRoot.owner.Uid = 1000
	nonRoot.owner.Gid = 1000
	if validRealityBinaryInfo("/usr/bin/sing-box", nonRoot) {
		t.Fatal("non-root production sing-box binary was accepted")
	}
	if !validRealityBinaryInfo("/tmp/sing-box", nonRoot) {
		t.Fatal("test binary path unexpectedly enforced production ownership")
	}
}

func TestRealityProductionConfigMetadataRequiresRootUser(t *testing.T) {
	parent := metadataFileInfo{mode: os.ModeDir | 0o750}
	parent.owner.Uid = 0
	parent.owner.Gid = 991
	if !validRealityConfigParentInfo("/etc/sing-box", parent, nil) {
		t.Fatal("root-owned production config parent was rejected")
	}
	target := metadataFileInfo{mode: 0o640, size: 1}
	target.owner.Uid = 0
	target.owner.Gid = 991
	if !validRealityConfigTargetInfo("/etc/sing-box/hyfleet-reality.json", target) {
		t.Fatal("root-owned production config with service group was rejected")
	}
	if runtime.GOOS == "windows" {
		return
	}
	parent.owner.Uid = 1000
	target.owner.Uid = 1000
	if validRealityConfigParentInfo("/etc/sing-box", parent, nil) ||
		validRealityConfigTargetInfo("/etc/sing-box/hyfleet-reality.json", target) {
		t.Fatal("non-root production config metadata was accepted")
	}
	if !validRealityConfigParentInfo("/tmp/test/sing-box", parent, nil) ||
		!validRealityConfigTargetInfo("/tmp/test/sing-box/config.json", target) {
		t.Fatal("test paths unexpectedly enforced production ownership")
	}
}

func TestSupportedRealitySingBoxVersionRequiresHyFleetBuild(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		output    string
		supported bool
	}{
		{name: "HyFleet build", output: "sing-box version 1.13.18-hyfleet-utls1.8.7-api2\n", supported: true},
		{name: "HyFleet build with details", output: "sing-box version 1.13.18-hyfleet-utls1.8.7-api2\n\nEnvironment: go1.26.5 linux/amd64\n", supported: true},
		{name: "official build", output: "sing-box version 1.13.18\n"},
		{name: "different dependency build", output: "sing-box version 1.13.18-hyfleet-utls1.8.6\n"},
		{name: "extra first-line field", output: "sing-box version 1.13.18-hyfleet-utls1.8.7 unexpected\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := isSupportedRealitySingBoxVersion([]byte(testCase.output)); got != testCase.supported {
				t.Fatalf("isSupportedRealitySingBoxVersion() = %v, want %v", got, testCase.supported)
			}
		})
	}
}

func TestRealityProductionBinaryChecksumRejectsTampering(t *testing.T) {
	if expectedRealitySingBoxSHA256() == "" {
		t.Skip("unsupported architecture")
	}
	payload := []byte("tampered Reality binary")
	if validRealitySingBoxChecksum("/usr/bin/sing-box", bytes.NewReader(payload), int64(len(payload))) {
		t.Fatal("tampered production binary passed the pinned checksum guard")
	}
	if !validRealitySingBoxChecksum("/tmp/test-sing-box", bytes.NewReader(payload), int64(len(payload))) {
		t.Fatal("test path unexpectedly enforced the production checksum guard")
	}
}

func TestRealityListenerSampleBindsPortToStableMainPID(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		networkFile string
		listener    string
		wantHealthy bool
	}{
		{name: "owned IPv4 listener", networkFile: "tcp", listener: "7001", wantHealthy: true},
		{name: "owned IPv6 listener", networkFile: "tcp6", listener: "7001", wantHealthy: true},
		{name: "other process owns port", networkFile: "tcp", listener: "7999"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			procRoot := t.TempDir()
			writeTestProcProcess(t, procRoot, 42, "12345", "7001")
			writeTestProcTCP(t, procRoot, testCase.networkFile, 18443, testCase.listener)
			helper := &Helper{
				ServiceUnit: "hyfleet-sing-box-reality.service", ProcRoot: procRoot,
				RunCommand: stableRealityProcessCommands(t, 42, nil),
			}
			identity, healthy := helper.realityListenerSample(t.Context(), 18443)
			if healthy != testCase.wantHealthy || (healthy && identity != "42:12345") {
				t.Fatalf("listener sample = (%q, %v), want healthy %v", identity, healthy, testCase.wantHealthy)
			}
		})
	}
}

func TestRealityListenerSampleRejectsMainPIDAndStartTimeChanges(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		pidOutputs  []string
		mutateStart bool
	}{
		{name: "MainPID changes", pidOutputs: []string{"42", "43"}},
		{name: "start time changes", pidOutputs: []string{"42", "42"}, mutateStart: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			procRoot := t.TempDir()
			writeTestProcProcess(t, procRoot, 42, "12345", "7001")
			writeTestProcTCP(t, procRoot, "tcp", 18443, "7001")
			pidCalls := 0
			helper := &Helper{
				ServiceUnit: "hyfleet-sing-box-reality.service", ProcRoot: procRoot,
				RunCommand: func(_ context.Context, name string, arguments ...string) ([]byte, error) {
					if name != "systemctl" {
						t.Fatalf("unexpected command: %s %v", name, arguments)
					}
					if len(arguments) > 0 && arguments[0] == "is-active" {
						if testCase.mutateStart {
							writeTestProcStat(t, procRoot, 42, "54321")
						}
						return []byte("active\n"), nil
					}
					output := testCase.pidOutputs[pidCalls]
					pidCalls++
					return []byte(output + "\n"), nil
				},
			}
			if identity, healthy := helper.realityListenerSample(t.Context(), 18443); healthy || identity != "" {
				t.Fatalf("unstable listener sample = (%q, %v)", identity, healthy)
			}
		})
	}
}

func TestRealityListenerHealthRequiresThreeStableSamples(t *testing.T) {
	procRoot := t.TempDir()
	writeTestProcProcess(t, procRoot, 42, "12345", "7001")
	writeTestProcTCP(t, procRoot, "tcp6", 18443, "7001")
	pidCalls := 0
	helper := &Helper{
		ServiceUnit: "hyfleet-sing-box-reality.service", ProcRoot: procRoot,
		RunCommand: func(_ context.Context, name string, arguments ...string) ([]byte, error) {
			if name != "systemctl" {
				t.Fatalf("unexpected command: %s %v", name, arguments)
			}
			if len(arguments) > 0 && arguments[0] == "is-active" {
				return []byte("active\n"), nil
			}
			pidCalls++
			return []byte("42\n"), nil
		},
	}
	if err := helper.waitTCPListener(t.Context(), 18443); err != nil {
		t.Fatalf("waitTCPListener() error = %v", err)
	}
	if pidCalls != 6 {
		t.Fatalf("MainPID calls = %d, want 6 for three stable samples", pidCalls)
	}
}

func writeTestProcProcess(t *testing.T, root string, pid int, startTime, socketInode string) {
	t.Helper()
	fdDir := filepath.Join(root, fmt.Sprintf("%d", pid), "fd")
	if err := os.MkdirAll(fdDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(proc fd) error = %v", err)
	}
	writeTestProcStat(t, root, pid, startTime)
	if err := os.Symlink("socket:["+socketInode+"]", filepath.Join(fdDir, "3")); err != nil {
		t.Skipf("socket symlink unavailable: %v", err)
	}
}

func writeTestProcStat(t *testing.T, root string, pid int, startTime string) {
	t.Helper()
	fields := make([]string, 20)
	for index := range fields {
		fields[index] = "0"
	}
	fields[0] = "S"
	fields[19] = startTime
	body := fmt.Sprintf("%d (sing-box test) %s\n", pid, strings.Join(fields, " "))
	if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%d", pid), "stat"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(proc stat) error = %v", err)
	}
}

func writeTestProcTCP(t *testing.T, root, name string, port int, inode string) {
	t.Helper()
	netDir := filepath.Join(root, "net")
	if err := os.MkdirAll(netDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(proc net) error = %v", err)
	}
	body := fmt.Sprintf(
		"sl local_address rem_address st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode\n"+
			"0: 00000000000000000000000000000000:%04X 00000000000000000000000000000000:0000 0A 0:0 00:0 0 0 0 %s\n",
		port, inode,
	)
	if err := os.WriteFile(filepath.Join(netDir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(proc net) error = %v", err)
	}
}

func stableRealityProcessCommands(t *testing.T, pid int, activeError error) CommandFunc {
	t.Helper()
	return func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if name != "systemctl" || len(arguments) == 0 {
			t.Fatalf("unexpected command: %s %v", name, arguments)
		}
		if arguments[0] == "is-active" {
			return []byte("active\n"), activeError
		}
		return []byte(fmt.Sprintf("%d\n", pid)), nil
	}
}

func successfulRealityCommands(t *testing.T, helper *Helper) CommandFunc {
	t.Helper()
	return func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if isRealityVersionCommand(helper, name, arguments) {
			return supportedRealityVersionOutput(), nil
		}
		if name == helper.SingBoxBinaryPath && len(arguments) == 3 && arguments[0] == "check" {
			return nil, nil
		}
		if name == "systemctl" && len(arguments) > 0 && arguments[0] == "restart" {
			return nil, nil
		}
		if name == "systemctl" && len(arguments) > 0 && arguments[0] == "is-active" {
			return []byte("active"), nil
		}
		t.Fatalf("unexpected command: %s %v", name, arguments)
		return nil, nil
	}
}

func isRealityVersionCommand(helper *Helper, name string, arguments []string) bool {
	return name == helper.SingBoxBinaryPath && len(arguments) == 1 && arguments[0] == "version"
}

func supportedRealityVersionOutput() []byte {
	return []byte("sing-box version " + supportedRealitySingBoxVersion + "\n")
}

func bytesFilled(length int, value byte) []byte {
	result := make([]byte, length)
	for index := range result {
		result[index] = value
	}
	return result
}

func assertRealityInitialStateRemoved(t *testing.T, helper *Helper, configPath string) {
	t.Helper()
	for name, path := range map[string]string{
		"configuration": configPath,
		"identity":      helper.RealityIdentityPath,
		"applied state": helper.RealityAppliedPath,
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s retained after initial apply rollback: %v", name, err)
		}
	}
}

func assertRealityConfigGuardFailure(t *testing.T, helper *Helper, request RealityApplyRequest) {
	t.Helper()
	helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if isRealityVersionCommand(helper, name, arguments) {
			return supportedRealityVersionOutput(), nil
		}
		t.Fatalf("config guard invoked command: %s %v", name, arguments)
		return nil, nil
	}
	result := helper.Handle(t.Context(), HelperRequest{RealityApply: &request})
	if result.Status != "failed" || result.ErrorCode != "reality_config_guard_failed" || result.RolledBack {
		t.Fatalf("config guard result = %#v", result)
	}
}

func testFileOwnership(t *testing.T, path string) (uint64, uint64) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return 0, 0
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat(%s) error = %v", path, err)
	}
	stat := reflect.ValueOf(info.Sys())
	if stat.Kind() == reflect.Pointer && !stat.IsNil() {
		stat = stat.Elem()
	}
	if stat.Kind() != reflect.Struct {
		t.Fatalf("ownership metadata for %s is unavailable", path)
	}
	uid := stat.FieldByName("Uid")
	gid := stat.FieldByName("Gid")
	if !uid.IsValid() || !gid.IsValid() || !uid.CanUint() || !gid.CanUint() {
		t.Fatalf("ownership metadata for %s is unavailable", path)
	}
	return uid.Uint(), gid.Uint()
}
