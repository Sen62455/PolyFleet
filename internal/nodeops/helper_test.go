package nodeops

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/google/uuid"
)

type metadataFileInfo struct {
	mode  fs.FileMode
	size  int64
	owner struct {
		Uid uint32
		Gid uint32
	}
}

func (info metadataFileInfo) Name() string       { return "metadata" }
func (info metadataFileInfo) Size() int64        { return info.size }
func (info metadataFileInfo) Mode() fs.FileMode  { return info.mode }
func (info metadataFileInfo) ModTime() time.Time { return time.Time{} }
func (info metadataFileInfo) IsDir() bool        { return info.mode.IsDir() }
func (info metadataFileInfo) Sys() any           { return info.owner }

func TestDecodeHelperRequestRequiresOneStrictBoundedJSONAction(t *testing.T) {
	operation := protocol.NodeOperation{
		ID: uuid.NewString(), Sequence: 1, Type: "probe_core", Attempt: 1,
	}
	valid, err := json.Marshal(HelperRequest{Operation: &operation})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	decoded, err := DecodeHelperRequest(bytes.NewReader(valid))
	if err != nil || decoded.Operation == nil || decoded.Operation.ID != operation.ID {
		t.Fatalf("DecodeHelperRequest(valid) = %#v, %v", decoded, err)
	}

	for name, body := range map[string][]byte{
		"missing action":   []byte(`{}`),
		"multiple actions": []byte(`{"operation":{"id":"` + operation.ID + `","sequence":1,"type":"probe_core","attempt":1},"reality_probe":{}}`),
		"unknown field":    []byte(`{"reality_probe":{},"unexpected":true}`),
		"trailing value":   []byte(`{"reality_probe":{}} {}`),
		"oversize":         bytes.Repeat([]byte(" "), maxHelperRequestBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeHelperRequest(bytes.NewReader(body)); err == nil {
				t.Fatal("DecodeHelperRequest() accepted invalid request")
			}
		})
	}
}

func testHelper(t *testing.T, configBody string) (*Helper, string) {
	t.Helper()
	root := t.TempDir()
	configPath := filepath.Join(root, "etc", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	return &Helper{
		ServiceUnit: "hysteria-server.service", CoreConfigPath: configPath,
		BackupDir: filepath.Join(root, "backups"), LedgerDir: filepath.Join(root, "ledger"),
		Now: func() time.Time { return now },
	}, configPath
}

func testDirectoryHelper(t *testing.T) (*Helper, string) {
	t.Helper()
	root := t.TempDir()
	configPath := filepath.Join(root, "etc", "sing-box", "conf")
	if err := os.MkdirAll(filepath.Join(configPath, "nested"), 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	files := map[string]string{
		"00_log.json":                "{\"log\":{\"level\":\"info\"}}\n",
		"12_hysteria2_inbounds.json": "{\"inbounds\":[{\"type\":\"hysteria2\"}]}\n",
		"nested/route.json":          "{\"route\":{}}\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(configPath, filepath.FromSlash(name)), []byte(body), 0o640); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	return &Helper{
		ServiceUnit: "sing-box.service", CoreConfigPath: configPath,
		BackupDir: filepath.Join(root, "backups"), LedgerDir: filepath.Join(root, "ledger"),
		Now: func() time.Time { return now },
	}, configPath
}

func TestNewHelperRejectsOptionLikeUnitAndUnsupportedConfigPath(t *testing.T) {
	if _, err := NewHelper("--help", "/etc/hysteria/config.yaml"); err == nil {
		t.Fatal("NewHelper() accepted an option-like service unit")
	}
	if _, err := NewHelper("hysteria-server.service", "/etc/other/config.yaml"); err == nil {
		t.Fatal("NewHelper() accepted a config path outside writable systemd directories")
	}
	if _, err := NewHelper("sing-box.service", "/etc/sing-box/config.json"); err != nil {
		t.Fatalf("NewHelper() rejected supported sing-box config: %v", err)
	}
}

func TestNewRealityHelperUsesBoundedStateAndBackupDirectories(t *testing.T) {
	helper, err := NewRealityHelper(
		"hyfleet-sing-box-reality.service",
		"/etc/sing-box/hyfleet-reality.json",
		"/usr/bin/sing-box",
		"/var/lib/hyfleet-agent-ops/reality-hyfleet-sing-box-reality.json",
		"/var/lib/hyfleet-agent-ops",
		"/var/lib/hyfleet-backups",
	)
	if err != nil {
		t.Fatalf("NewRealityHelper() error = %v", err)
	}
	if helper.LedgerDir != "/var/lib/hyfleet-agent-ops" ||
		helper.BackupDir != "/var/lib/hyfleet-backups" ||
		helper.RealityAppliedPath != "/var/lib/hyfleet-agent-ops/reality-hyfleet-sing-box-reality-applied.json" {
		t.Fatalf("unexpected Reality helper paths: %#v", helper)
	}

	validArguments := []string{
		"hyfleet-sing-box-reality.service",
		"/etc/sing-box/hyfleet-reality.json",
		"/usr/bin/sing-box",
		"/var/lib/hyfleet-agent-ops/reality-hyfleet-sing-box-reality.json",
		"/var/lib/hyfleet-agent-ops",
		"/var/lib/hyfleet-backups",
	}
	for name, mutation := range map[string]func([]string){
		"arbitrary service unit": func(arguments []string) {
			arguments[0] = "sing-box.service"
		},
		"arbitrary config path": func(arguments []string) {
			arguments[1] = "/etc/sing-box/config.json"
		},
		"arbitrary state directory": func(arguments []string) {
			arguments[4] = "/var/lib/hyfleet-agent-ops-test-lab"
		},
		"arbitrary backup directory": func(arguments []string) {
			arguments[5] = "/var/lib/hyfleet-backups-test-lab"
		},
		"identity directory mismatch": func(arguments []string) {
			arguments[3] = "/var/lib/hyfleet-agent-ops-lab/reality.json"
		},
		"arbitrary identity file": func(arguments []string) {
			arguments[3] = "/var/lib/hyfleet-agent-ops/reality-other.json"
		},
		"lab service only": func(arguments []string) {
			arguments[0] = "hyfleet-sing-box-reality-lab.service"
		},
		"lab config only": func(arguments []string) {
			arguments[1] = "/etc/sing-box/hyfleet-reality-lab.json"
		},
		"lab state only": func(arguments []string) {
			arguments[3] = "/var/lib/hyfleet-agent-ops-lab/reality-hyfleet-sing-box-reality-lab.json"
			arguments[4] = "/var/lib/hyfleet-agent-ops-lab"
		},
	} {
		t.Run(name, func(t *testing.T) {
			arguments := append([]string(nil), validArguments...)
			mutation(arguments)
			if _, err := NewRealityHelper(
				arguments[0], arguments[1], arguments[2], arguments[3], arguments[4], arguments[5],
			); err == nil {
				t.Fatal("NewRealityHelper() accepted an unbounded or mismatched path")
			}
		})
	}
}

func TestRealityProbeReportsCompatibilityAndCoreStateWithoutCommandOutput(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		active      bool
		activeError error
	}{
		{name: "active", active: true},
		{name: "inactive", activeError: errors.New("exit status 3")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			helper, _, configPath := testRealityHelper(t)
			if err := os.WriteFile(configPath, []byte(
				`{"inbounds":[{"type":"vless","tag":"hyfleet-vless-reality-in","listen_port":18443}]}`,
			), 0o640); err != nil {
				t.Fatalf("WriteFile(probe config) error = %v", err)
			}
			helper.CheckTCPListener = func(_ context.Context, port int) error {
				if port != 18443 {
					t.Fatalf("probe listener port = %d, want 18443", port)
				}
				return nil
			}
			helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
				if isRealityVersionCommand(helper, name, arguments) {
					return append(supportedRealityVersionOutput(), []byte("private-version-output")...), nil
				}
				if name != "systemctl" || strings.Join(arguments, " ") !=
					"is-active hyfleet-sing-box-reality.service" {
					t.Fatalf("unexpected probe command: %s %v", name, arguments)
				}
				if testCase.active {
					return []byte("active\n"), nil
				}
				return []byte("inactive\nprivate-service-output"), testCase.activeError
			}

			result := helper.Handle(t.Context(), HelperRequest{RealityProbe: &RealityProbeRequest{}})
			if result.Status != "succeeded" || result.RealityProbe == nil ||
				result.RealityProbe.AdapterStatus != "compatible" ||
				result.RealityProbe.AdapterVersion != supportedRealitySingBoxVersion ||
				result.RealityProbe.CoreVersion != supportedRealitySingBoxVersion ||
				result.RealityProbe.CoreRunning != testCase.active ||
				result.RealityProbe.AdapterErrorCode != "" {
				t.Fatalf("Reality probe response = %#v", result)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("Marshal(probe) error = %v", err)
			}
			for _, forbidden := range []string{
				"private-version-output", "private-service-output", helper.SingBoxBinaryPath,
			} {
				if forbidden != "" && strings.Contains(string(encoded), forbidden) {
					t.Fatalf("Reality probe leaked command output or path: %s", encoded)
				}
			}
		})
	}
}

func TestRealityProbeReportsUnsupportedBinaryAsIncompatible(t *testing.T) {
	helper, _, _ := testRealityHelper(t)
	helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if !isRealityVersionCommand(helper, name, arguments) {
			t.Fatalf("probe continued after incompatible binary: %s %v", name, arguments)
		}
		return []byte("sing-box version 1.99.0\nsecret-build-details"), nil
	}

	result := helper.Handle(t.Context(), HelperRequest{RealityProbe: &RealityProbeRequest{}})
	if result.Status != "succeeded" || result.RealityProbe == nil ||
		result.RealityProbe.AdapterStatus != "incompatible" ||
		result.RealityProbe.AdapterErrorCode != "reality_binary_incompatible" ||
		result.RealityProbe.AdapterVersion != "" || result.RealityProbe.CoreVersion != "" ||
		result.RealityProbe.CoreRunning {
		t.Fatalf("unsupported Reality probe response = %#v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil || strings.Contains(string(encoded), "1.99.0") ||
		strings.Contains(string(encoded), "secret-build-details") {
		t.Fatalf("incompatible Reality probe leaked output: %s, %v", encoded, err)
	}
}

func TestHelperRejectsMissingOrMultipleActions(t *testing.T) {
	helper, realityApply, _ := testRealityHelper(t)
	operation := protocol.NodeOperation{
		ID: uuid.NewString(), Sequence: 1, Type: "probe_core", Attempt: 1,
	}
	for _, request := range []HelperRequest{
		{},
		{Operation: &operation, RealityProbe: &RealityProbeRequest{}},
		{RealityApply: &realityApply, RealityProbe: &RealityProbeRequest{}},
	} {
		result := helper.Handle(t.Context(), request)
		if result.Status != "failed" || result.ErrorCode != "helper_request_invalid" ||
			result.RealityProbe != nil {
			t.Fatalf("invalid helper action selection response = %#v", result)
		}
	}
}

func TestRealityHelperBackupReportsConfiguredHostPath(t *testing.T) {
	helper, _ := testHelper(t, "known-good: true\n")
	helper.BackupDir = filepath.Join(t.TempDir(), "hyfleet-backups-lab")
	operation := protocol.NodeOperation{
		ID: uuid.NewString(), Sequence: 1, Type: "backup_config", Attempt: 1,
	}
	result := helper.Handle(t.Context(), HelperRequest{Operation: &operation})
	if result.Status != "succeeded" || result.Backup == nil ||
		filepath.Dir(result.Backup.LocalPath) != helper.BackupDir {
		t.Fatalf("backup did not report configured host path: %#v", result)
	}
}

func TestHelperCreatesRestrictedBackupDirectoryAndFile(t *testing.T) {
	helper, _ := testHelper(t, "known-good: true\n")
	operation := protocol.NodeOperation{
		ID: uuid.NewString(), Sequence: 1, Type: "backup_config", Attempt: 1,
	}
	result := helper.Handle(t.Context(), HelperRequest{Operation: &operation})
	if result.Status != "succeeded" || result.Backup == nil {
		t.Fatalf("backup result = %#v", result)
	}
	directoryInfo, err := os.Lstat(helper.BackupDir)
	if err != nil || !validBackupDirectoryInfo(helper.BackupDir, directoryInfo) {
		t.Fatalf("backup directory metadata = %#v, %v", directoryInfo, err)
	}
	backupInfo, err := os.Lstat(result.Backup.LocalPath)
	if err != nil || !validBackupFileInfo(helper.BackupDir, backupInfo) {
		t.Fatalf("backup file metadata = %#v, %v", backupInfo, err)
	}
	if runtime.GOOS != "windows" &&
		(directoryInfo.Mode().Perm() != 0o700 || backupInfo.Mode().Perm() != 0o600) {
		t.Fatalf("backup permissions = dir %o file %o", directoryInfo.Mode().Perm(), backupInfo.Mode().Perm())
	}
}

func TestHelperRejectsUnsafeExistingBackupDirectory(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		helper, _ := testHelper(t, "known-good: true\n")
		target := filepath.Join(t.TempDir(), "target")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatalf("Mkdir(target) error = %v", err)
		}
		if err := os.Symlink(target, helper.BackupDir); err != nil {
			t.Skipf("symlink is unavailable: %v", err)
		}
		if err := helper.prepareBackupDir(); err == nil {
			t.Fatal("prepareBackupDir() accepted a symlink")
		}
	})

	t.Run("wrong mode", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows permissions are represented by ACLs")
		}
		helper, _ := testHelper(t, "known-good: true\n")
		if err := os.Mkdir(helper.BackupDir, 0o750); err != nil {
			t.Fatalf("Mkdir(backup) error = %v", err)
		}
		if err := helper.prepareBackupDir(); err == nil {
			t.Fatal("prepareBackupDir() repaired and accepted an unsafe existing directory")
		}
		info, err := os.Lstat(helper.BackupDir)
		if err != nil || info.Mode().Perm() != 0o750 {
			t.Fatalf("unsafe directory metadata changed: %#v, %v", info, err)
		}
	})
}

func TestHelperFileRestorePreservesDestinationMetadata(t *testing.T) {
	helper, configPath := testHelper(t, "known-good: true\n")
	if runtime.GOOS != "windows" {
		if err := os.Chmod(configPath, 0o640); err != nil {
			t.Fatalf("Chmod(config) error = %v", err)
		}
	}
	before, err := os.Lstat(configPath)
	if err != nil {
		t.Fatalf("Lstat(config) error = %v", err)
	}
	backup, err := helper.createBackup(uuid.NewString())
	if err != nil {
		t.Fatalf("createBackup() error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte("broken: true\n"), before.Mode().Perm()); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	if err := helper.restoreBackup(backup.LocalPath); err != nil {
		t.Fatalf("restoreBackup() error = %v", err)
	}
	after, err := os.Lstat(configPath)
	contents, readErr := os.ReadFile(configPath)
	if err != nil || readErr != nil || string(contents) != "known-good: true\n" ||
		!samePermissions(before.Mode(), after.Mode()) || !sameFileOwnership(before, after) {
		t.Fatalf("restored config metadata/content = %#v %q, %v, %v", after, contents, err, readErr)
	}
}

func TestHelperRejectsUnsafeBackupMetadataForReuseAndRestore(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "symlink",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				target := path + ".target"
				if err := os.Rename(path, target); err != nil {
					t.Fatalf("move backup target: %v", err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skipf("symlink is unavailable: %v", err)
				}
			},
		},
		{
			name: "group readable",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				if runtime.GOOS == "windows" {
					t.Skip("Windows permissions are represented by ACLs")
				}
				if err := os.Chmod(path, 0o640); err != nil {
					t.Fatalf("chmod backup: %v", err)
				}
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			helper, _ := testHelper(t, "known-good: true\n")
			operationID := uuid.NewString()
			backup, err := helper.createBackup(operationID)
			if err != nil {
				t.Fatalf("createBackup() error = %v", err)
			}
			testCase.mutate(t, backup.LocalPath)
			if _, err := helper.backupMetadata(backup.LocalPath); err == nil {
				t.Fatal("backupMetadata() accepted unsafe metadata")
			}
			if err := helper.restoreBackup(backup.LocalPath); err == nil {
				t.Fatal("restoreBackup() accepted unsafe metadata")
			}
		})
	}
}

func TestBackupProductionMetadataContract(t *testing.T) {
	directory := metadataFileInfo{mode: os.ModeDir | 0o700}
	directory.owner.Uid = 0
	directory.owner.Gid = 0
	backup := metadataFileInfo{mode: 0o600, size: 1}
	backup.owner.Uid = 0
	backup.owner.Gid = 0
	if !validBackupDirectoryInfo("/var/lib/hyfleet-backups", directory) ||
		!validBackupFileInfo("/var/lib/hyfleet-backups-lab", backup) {
		t.Fatal("root-owned production backup metadata was rejected")
	}
	if runtime.GOOS == "windows" {
		return
	}
	directory.owner.Gid = 1000
	backup.owner.Uid = 1000
	if validBackupDirectoryInfo("/var/lib/hyfleet-backups", directory) ||
		validBackupFileInfo("/var/lib/hyfleet-backups-lab", backup) {
		t.Fatal("non-root production backup metadata was accepted")
	}
	if !validBackupDirectoryInfo(filepath.Join(t.TempDir(), "backups"), directory) ||
		!validBackupFileInfo(filepath.Join(t.TempDir(), "backups"), backup) {
		t.Fatal("test backup paths unexpectedly enforced production ownership")
	}
}

func TestHelperRestartFailureRestoresExactPreRestartBackupAndIsIdempotent(t *testing.T) {
	helper, configPath := testHelper(t, "known-good: true\n")
	backupOperation := protocol.NodeOperation{
		ID: uuid.NewString(), Sequence: 1, Type: "backup_config", Attempt: 1,
	}
	backup := helper.Handle(t.Context(), HelperRequest{Operation: &backupOperation})
	if backup.Status != "succeeded" || backup.Backup == nil {
		t.Fatalf("initial backup response = %#v", backup)
	}
	if err := os.WriteFile(configPath, []byte("broken: true\n"), 0o600); err != nil {
		t.Fatalf("write broken config: %v", err)
	}
	commands := 0
	helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		commands++
		if name != "systemctl" {
			t.Fatalf("unexpected command: %s %v", name, arguments)
		}
		switch commands {
		case 1:
			return []byte("restart rejected"), errors.New("exit status 1")
		case 2:
			return []byte("inactive"), errors.New("exit status 3")
		case 3:
			return nil, nil
		case 4:
			return []byte("active"), nil
		default:
			t.Fatalf("unexpected command count %d", commands)
			return nil, nil
		}
	}
	restartOperation := protocol.NodeOperation{
		ID: uuid.NewString(), Sequence: 2, Type: "restart_core", Attempt: 1,
	}
	result := helper.Handle(t.Context(), HelperRequest{Operation: &restartOperation})
	if result.Status != "failed" || result.ErrorCode != "core_restart_failed" || !result.RolledBack {
		t.Fatalf("restart result = %#v", result)
	}
	restored, err := os.ReadFile(configPath)
	if err != nil || string(restored) != "broken: true\n" {
		t.Fatalf("restored config = %q, error = %v", restored, err)
	}
	replayed := helper.Handle(t.Context(), HelperRequest{Operation: &restartOperation})
	if commands != 4 || replayed.Status != result.Status || !replayed.RolledBack {
		t.Fatalf("helper replay executed side effect: commands=%d replay=%#v", commands, replayed)
	}
}

func TestHelperDirectoryBackupAndRestartRollback(t *testing.T) {
	helper, configPath := testDirectoryHelper(t)
	backupOperation := protocol.NodeOperation{
		ID: uuid.NewString(), Sequence: 1, Type: "backup_config", Attempt: 1,
	}
	backup := helper.Handle(t.Context(), HelperRequest{Operation: &backupOperation})
	if backup.Status != "succeeded" || backup.Backup == nil ||
		!strings.HasSuffix(backup.Backup.LocalPath, "-conf.tar.gz") || backup.Backup.SizeBytes < 1 {
		t.Fatalf("directory backup response = %#v", backup)
	}
	if err := os.WriteFile(
		filepath.Join(configPath, "12_hysteria2_inbounds.json"),
		[]byte("{\"broken\":true}\n"), 0o640,
	); err != nil {
		t.Fatalf("write broken config: %v", err)
	}
	if err := os.Remove(filepath.Join(configPath, "00_log.json")); err != nil {
		t.Fatalf("remove known config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configPath, "unexpected.json"), []byte("{}\n"), 0o640); err != nil {
		t.Fatalf("write unexpected config: %v", err)
	}
	commands := 0
	helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		commands++
		if name != "systemctl" {
			t.Fatalf("unexpected command: %s %v", name, arguments)
		}
		switch commands {
		case 1:
			return []byte("restart rejected"), errors.New("exit status 1")
		case 2:
			return []byte("inactive"), errors.New("exit status 3")
		case 3:
			return nil, nil
		case 4:
			return []byte("active"), nil
		default:
			t.Fatalf("unexpected command count %d", commands)
			return nil, nil
		}
	}
	restartOperation := protocol.NodeOperation{
		ID: uuid.NewString(), Sequence: 2, Type: "restart_core", Attempt: 1,
	}
	result := helper.Handle(t.Context(), HelperRequest{Operation: &restartOperation})
	if result.Status != "failed" || !result.RolledBack || result.Backup == nil {
		t.Fatalf("directory restart result = %#v", result)
	}
	restored, err := os.ReadFile(filepath.Join(configPath, "12_hysteria2_inbounds.json"))
	if err != nil || string(restored) != "{\"broken\":true}\n" {
		t.Fatalf("restored directory config = %q, error = %v", restored, err)
	}
	if _, err := os.Stat(filepath.Join(configPath, "00_log.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file absent before restart was recreated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configPath, "unexpected.json")); err != nil {
		t.Fatalf("file present before restart was removed: %v", err)
	}
}

func TestHelperDirectoryBackupRejectsSymlinksAndOversizedTrees(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		helper, configPath := testDirectoryHelper(t)
		outsidePath := filepath.Join(t.TempDir(), "outside.json")
		if err := os.WriteFile(outsidePath, []byte("secret\n"), 0o600); err != nil {
			t.Fatalf("write outside file: %v", err)
		}
		if err := os.Symlink(outsidePath, filepath.Join(configPath, "linked.json")); err != nil {
			t.Skipf("symlink is unavailable on this platform: %v", err)
		}
		operation := protocol.NodeOperation{
			ID: uuid.NewString(), Sequence: 1, Type: "backup_config", Attempt: 1,
		}
		result := helper.Handle(t.Context(), HelperRequest{Operation: &operation})
		if result.Status != "failed" || result.ErrorCode != "config_backup_failed" ||
			!strings.Contains(result.ErrorMessage, "symbolic link") {
			t.Fatalf("symlink backup response = %#v", result)
		}
	})

	t.Run("size", func(t *testing.T) {
		helper, configPath := testDirectoryHelper(t)
		oversized, err := os.OpenFile(
			filepath.Join(configPath, "oversized.json"), os.O_CREATE|os.O_WRONLY, 0o600,
		)
		if err != nil {
			t.Fatalf("create oversized file: %v", err)
		}
		if err := oversized.Truncate(maxConfigBackupBytes + 1); err != nil {
			_ = oversized.Close()
			t.Fatalf("truncate oversized file: %v", err)
		}
		if err := oversized.Close(); err != nil {
			t.Fatalf("close oversized file: %v", err)
		}
		operation := protocol.NodeOperation{
			ID: uuid.NewString(), Sequence: 1, Type: "backup_config", Attempt: 1,
		}
		result := helper.Handle(t.Context(), HelperRequest{Operation: &operation})
		if result.Status != "failed" || result.ErrorCode != "config_backup_failed" ||
			!strings.Contains(result.ErrorMessage, "size limit") {
			t.Fatalf("oversized backup response = %#v", result)
		}
	})
}

func TestDirectoryRestoreRejectsArchiveTraversal(t *testing.T) {
	helper, configPath := testDirectoryHelper(t)
	if err := helper.prepareBackupDir(); err != nil {
		t.Fatalf("prepareBackupDir() error = %v", err)
	}
	backupPath := filepath.Join(helper.BackupDir, "malicious-conf.tar.gz")
	archive, err := os.OpenFile(backupPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("create malicious archive: %v", err)
	}
	gzipWriter := gzip.NewWriter(archive)
	tarWriter := tar.NewWriter(gzipWriter)
	body := []byte("escaped\n")
	if err := tarWriter.WriteHeader(&tar.Header{
		Name: "../escaped.json", Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatalf("write malicious header: %v", err)
	}
	if _, err := tarWriter.Write(body); err != nil {
		t.Fatalf("write malicious body: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close malicious archive: %v", err)
	}
	if err := helper.restoreBackup(backupPath); err == nil {
		t.Fatal("restoreBackup() accepted archive traversal")
	}
	escapedPath := filepath.Join(filepath.Dir(configPath), "escaped.json")
	if _, err := os.Stat(escapedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archive traversal wrote outside restore directory: %v", err)
	}
}

func TestHelperLogOutputIsBoundedAndRedacted(t *testing.T) {
	helper, _ := testHelper(t, "config: true\n")
	helper.RunCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if name != "journalctl" || len(arguments) < 5 {
			t.Fatalf("unexpected log command: %s %v", name, arguments)
		}
		return []byte(
			"authorization: Bearer private-value\npassword=private-password\n" +
				strings.Repeat("bounded log line\n", 250),
		), nil
	}
	operation := protocol.NodeOperation{
		ID: uuid.NewString(), Sequence: 1, Type: "tail_core_log", MaxLines: 100, Attempt: 1,
	}
	result := helper.Handle(t.Context(), HelperRequest{Operation: &operation})
	if result.Status != "succeeded" || len(result.Output) > MaxOutputSize ||
		strings.Count(result.Output, "\n") >= 100 ||
		strings.Contains(result.Output, "private-value") ||
		strings.Contains(result.Output, "private-password") {
		t.Fatalf("bounded log result = %#v", result)
	}
}
