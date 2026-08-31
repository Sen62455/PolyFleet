package nodeops

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/google/uuid"
)

const (
	maxConfigBackupBytes        int64 = 8 * 1024 * 1024
	maxConfigBackupArchiveBytes int64 = maxConfigBackupBytes + 1024*1024
	maxConfigBackupEntries            = 512
	maxConfigBackupDepth              = 16
	maxHelperResponseBytes      int   = 64 * 1024
)

var helperUnitPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@:-]{0,127}$`)

type CommandFunc func(context.Context, string, ...string) ([]byte, error)
type ListenerCheckFunc func(context.Context, int) error

type Helper struct {
	ServiceUnit            string
	CoreConfigPath         string
	SingBoxBinaryPath      string
	RealityIdentityPath    string
	RealityAppliedPath     string
	BackupDir              string
	LedgerDir              string
	RunCommand             CommandFunc
	CheckTCPListener       ListenerCheckFunc
	ProcRoot               string
	Now                    func() time.Time
	afterRealityConfigLink func(candidatePath, targetPath string)
}

func NewRealityHelper(
	serviceUnit, coreConfigPath, binaryPath, identityPath, operationsStateDir, backupDir string,
) (*Helper, error) {
	if !validRealityHelperTuple(
		serviceUnit, coreConfigPath, identityPath, operationsStateDir, backupDir,
	) {
		return nil, errors.New("invalid helper Reality deployment tuple")
	}
	helper, err := NewHelper(serviceUnit, coreConfigPath)
	if err != nil {
		return nil, err
	}
	if binaryPath != "/usr/bin/sing-box" {
		return nil, errors.New("invalid helper sing-box binary path")
	}
	if !validRealityHelperDir(operationsStateDir, "/var/lib/hyfleet-agent-ops") {
		return nil, errors.New("invalid helper operations state directory")
	}
	if !validRealityHelperDir(backupDir, "/var/lib/hyfleet-backups") {
		return nil, errors.New("invalid helper backup directory")
	}
	if !pathpkg.IsAbs(identityPath) || pathpkg.Clean(identityPath) != identityPath ||
		pathpkg.Dir(identityPath) != operationsStateDir || pathpkg.Ext(identityPath) != ".json" ||
		len(identityPath) > 256 {
		return nil, errors.New("invalid helper Reality identity path")
	}
	helper.SingBoxBinaryPath = binaryPath
	helper.RealityIdentityPath = identityPath
	helper.RealityAppliedPath = strings.TrimSuffix(identityPath, ".json") + "-applied.json"
	helper.LedgerDir = operationsStateDir
	helper.BackupDir = backupDir
	return helper, nil
}

func validRealityHelperTuple(serviceUnit, coreConfigPath, identityPath, stateDir, backupDir string) bool {
	return serviceUnit == "hyfleet-sing-box-reality.service" &&
		coreConfigPath == "/etc/sing-box/hyfleet-reality.json" &&
		identityPath == "/var/lib/hyfleet-agent-ops/reality-hyfleet-sing-box-reality.json" &&
		stateDir == "/var/lib/hyfleet-agent-ops" &&
		backupDir == "/var/lib/hyfleet-backups"
}

func validRealityHelperDir(value, productionPath string) bool {
	return pathpkg.IsAbs(value) && pathpkg.Clean(value) == value && value == productionPath
}

func NewHelper(serviceUnit, coreConfigPath string) (*Helper, error) {
	if !helperUnitPattern.MatchString(serviceUnit) {
		return nil, errors.New("invalid helper service unit")
	}
	if coreConfigPath != "" {
		if !pathpkg.IsAbs(coreConfigPath) || pathpkg.Clean(coreConfigPath) != coreConfigPath ||
			(!strings.HasPrefix(coreConfigPath, "/etc/hysteria/") &&
				!strings.HasPrefix(coreConfigPath, "/etc/sing-box/")) {
			return nil, errors.New("invalid helper core config path")
		}
	}
	return &Helper{
		ServiceUnit: serviceUnit, CoreConfigPath: coreConfigPath,
		BackupDir:  "/var/lib/hyfleet-backups",
		LedgerDir:  "/var/lib/hyfleet-agent-ops",
		RunCommand: runBoundedCommand,
		Now:        func() time.Time { return time.Now().UTC() },
	}, nil
}

func (helper *Helper) Serve(ctx context.Context, reader io.Reader, writer io.Writer) error {
	request, err := DecodeHelperRequest(reader)
	if err != nil {
		return err
	}
	return EncodeHelperResponse(writer, helper.Handle(ctx, request))
}

func DecodeHelperRequest(reader io.Reader) (HelperRequest, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxHelperRequestBytes+1))
	if err != nil {
		return HelperRequest{}, fmt.Errorf("read helper request: %w", err)
	}
	if len(data) > maxHelperRequestBytes {
		return HelperRequest{}, errors.New("helper request exceeds size limit")
	}
	var request HelperRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return HelperRequest{}, fmt.Errorf("decode helper request: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return HelperRequest{}, err
	}
	if err := ValidateHelperRequest(request); err != nil {
		return HelperRequest{}, err
	}
	return request, nil
}

func ValidateHelperRequest(request HelperRequest) error {
	actions := 0
	if request.Operation != nil {
		actions++
	}
	if request.RealityApply != nil {
		actions++
	}
	if request.RealityProbe != nil {
		actions++
	}
	if actions != 1 {
		return errors.New("helper request must contain exactly one action")
	}
	return nil
}

func EncodeHelperResponse(writer io.Writer, response HelperResponse) error {
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(response); err != nil {
		return fmt.Errorf("encode helper response: %w", err)
	}
	if encoded.Len() > maxHelperResponseBytes {
		return errors.New("helper response exceeds size limit")
	}
	written, err := writer.Write(encoded.Bytes())
	if err != nil {
		return fmt.Errorf("write helper response: %w", err)
	}
	if written != encoded.Len() {
		return io.ErrShortWrite
	}
	return nil
}

func (helper *Helper) Handle(ctx context.Context, request HelperRequest) HelperResponse {
	if err := ValidateHelperRequest(request); err != nil {
		return HelperResponse{
			Status: "failed", ErrorCode: "helper_request_invalid",
			ErrorMessage: err.Error(), CompletedAt: helper.now(),
		}
	}
	if request.RealityApply != nil {
		return helper.applyReality(ctx, *request.RealityApply)
	}
	if request.RealityProbe != nil {
		return helper.probeReality(ctx)
	}
	operation := *request.Operation
	if cached, ok := helper.loadResult(operation); ok {
		return cached
	}
	response := HelperResponse{
		Sequence: operation.Sequence, Status: "failed", CompletedAt: helper.now(),
	}
	if err := validateHelperOperation(operation); err != nil {
		response.ErrorCode = "operation_invalid"
		response.ErrorMessage = SanitizeMessage(err.Error(), 512)
		return response
	}
	switch operation.Type {
	case "probe_core":
		response = helper.probeCore(ctx, operation)
	case "ping":
		response = helper.pingTarget(ctx, operation)
	case "restart_core":
		response = helper.restartCore(ctx, operation)
	case "tail_core_log":
		response = helper.tailCoreLog(ctx, operation)
	case "backup_config":
		response = helper.backupConfig(operation)
	}
	response.Sequence = operation.Sequence
	response.Output = SanitizeOutput(response.Output, MaxLogLines, MaxOutputSize)
	response.ErrorMessage = SanitizeMessage(response.ErrorMessage, 512)
	if response.CompletedAt.IsZero() {
		response.CompletedAt = helper.now()
	}
	if err := helper.saveResult(operation, response); err != nil {
		return HelperResponse{
			Sequence: operation.Sequence, Status: "failed",
			ErrorCode:    "operation_result_persist_failed",
			ErrorMessage: SanitizeMessage(err.Error(), 512), CompletedAt: helper.now(),
		}
	}
	return response
}

func (helper *Helper) probeReality(ctx context.Context) HelperResponse {
	probedAt := helper.now()
	result := &RealityProbeResult{
		AdapterStatus: "incompatible", AdapterErrorCode: "reality_binary_incompatible",
		ProbedAt: probedAt,
	}
	response := HelperResponse{
		Status: "succeeded", RealityProbe: result, CompletedAt: probedAt,
	}
	if err := helper.validateRealityBinary(ctx); err != nil {
		return response
	}
	result.AdapterStatus = "compatible"
	result.AdapterVersion = supportedRealitySingBoxVersion
	result.AdapterErrorCode = ""
	result.CoreVersion = supportedRealitySingBoxVersion
	output, err := helper.command(ctx, "systemctl", "is-active", helper.ServiceUnit)
	if err == nil && strings.TrimSpace(string(output)) == "active" {
		listenPort := helper.managedRealityListenPort()
		result.CoreRunning = listenPort != 0 && helper.waitTCPListener(ctx, listenPort) == nil
	}
	return response
}

func validateHelperOperation(operation protocol.NodeOperation) error {
	if _, err := uuid.Parse(operation.ID); err != nil {
		return errors.New("operation ID is not a UUID")
	}
	if operation.Sequence < 1 || operation.Attempt < 1 {
		return errors.New("operation sequence or attempt is invalid")
	}
	switch operation.Type {
	case "probe_core", "restart_core", "backup_config":
		if operation.MaxLines != 0 || operation.Target != "" {
			return errors.New("operation does not accept max_lines")
		}
	case "ping":
		if operation.MaxLines != 0 || net.ParseIP(operation.Target) == nil {
			return errors.New("ping target is invalid")
		}
	case "tail_core_log":
		if operation.MaxLines < 1 || operation.MaxLines > MaxLogLines || operation.Target != "" {
			return errors.New("log line limit is invalid")
		}
	default:
		return errors.New("operation type is unsupported")
	}
	return nil
}

func (helper *Helper) pingTarget(ctx context.Context, operation protocol.NodeOperation) HelperResponse {
	response := HelperResponse{Sequence: operation.Sequence, CompletedAt: helper.now()}
	pingContext, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	output, err := helper.command(
		pingContext, "ping", "-n", "-c", "4", "-W", "2", operation.Target,
	)
	response.Output = string(output)
	response.CompletedAt = helper.now()
	if err != nil {
		response.Status = "failed"
		response.ErrorCode = "ping_failed"
		response.ErrorMessage = commandErrorMessage(err, "target did not respond to ping")
		return response
	}
	response.Status = "succeeded"
	return response
}

func (helper *Helper) probeCore(ctx context.Context, operation protocol.NodeOperation) HelperResponse {
	output, err := helper.command(ctx, "systemctl", "is-active", helper.ServiceUnit)
	response := HelperResponse{
		Sequence:    operation.Sequence,
		Output:      SanitizeOutput(string(output), operation.MaxLines, MaxOutputSize),
		CompletedAt: helper.now(),
	}
	if err != nil || strings.TrimSpace(string(output)) != "active" {
		response.Status = "failed"
		response.ErrorCode = "core_inactive"
		response.ErrorMessage = commandErrorMessage(err, "core service is not active")
		return response
	}
	response.Status = "succeeded"
	return response
}

func (helper *Helper) tailCoreLog(ctx context.Context, operation protocol.NodeOperation) HelperResponse {
	output, err := helper.command(
		ctx, "journalctl", "-u", helper.ServiceUnit, "-n",
		fmt.Sprintf("%d", operation.MaxLines), "--no-pager", "-o", "short-iso",
	)
	response := HelperResponse{
		Sequence:    operation.Sequence,
		Output:      SanitizeOutput(string(output), operation.MaxLines, MaxOutputSize),
		CompletedAt: helper.now(),
	}
	if err != nil {
		response.Status = "failed"
		response.ErrorCode = "core_log_failed"
		response.ErrorMessage = commandErrorMessage(err, "could not read core log")
		return response
	}
	response.Status = "succeeded"
	return response
}

func (helper *Helper) backupConfig(operation protocol.NodeOperation) HelperResponse {
	response := HelperResponse{Sequence: operation.Sequence, CompletedAt: helper.now()}
	if helper.CoreConfigPath == "" {
		response.Status = "failed"
		response.ErrorCode = "core_config_not_configured"
		response.ErrorMessage = "core_config_path is not configured for this adapter"
		return response
	}
	backup, err := helper.createBackup(operation.ID)
	if err != nil {
		response.Status = "failed"
		response.ErrorCode = "config_backup_failed"
		response.ErrorMessage = err.Error()
		return response
	}
	response.Status = "succeeded"
	response.Backup = backup
	response.Output = "configuration backup created"
	return response
}

func (helper *Helper) restartCore(ctx context.Context, operation protocol.NodeOperation) HelperResponse {
	response := HelperResponse{Sequence: operation.Sequence, CompletedAt: helper.now()}
	var preRestartBackup *protocol.Backup
	realityListenPort := 0
	if helper.CoreConfigPath != "" {
		backup, err := helper.createBackup(operation.ID)
		if err != nil {
			response.Status = "failed"
			response.ErrorCode = "config_backup_failed"
			response.ErrorMessage = err.Error()
			return response
		}
		preRestartBackup = backup
		response.Backup = backup
	}
	if helper.managesVLESSReality() {
		realityListenPort = helper.backupRealityListenPort(preRestartBackup)
		if realityListenPort == 0 {
			response.Status = "failed"
			response.ErrorCode = "core_restart_failed"
			response.ErrorMessage = "managed Reality listen port is invalid"
			return response
		}
	}
	restartOutput, restartErr := helper.command(ctx, "systemctl", "restart", helper.ServiceUnit)
	activeOutput, activeErr := helper.command(ctx, "systemctl", "is-active", helper.ServiceUnit)
	response.Output = string(restartOutput) + "\n" + string(activeOutput)
	healthErr := error(nil)
	if restartErr == nil && activeErr == nil && strings.TrimSpace(string(activeOutput)) == "active" &&
		realityListenPort != 0 {
		healthErr = helper.waitTCPListener(ctx, realityListenPort)
	}
	if restartErr == nil && activeErr == nil && healthErr == nil &&
		strings.TrimSpace(string(activeOutput)) == "active" {
		response.Status = "succeeded"
		response.CompletedAt = helper.now()
		return response
	}
	rollbackSource := ""
	if preRestartBackup != nil {
		rollbackSource = preRestartBackup.LocalPath
	}
	if rollbackSource != "" && helper.CoreConfigPath != "" {
		if err := helper.restoreBackup(rollbackSource); err == nil {
			_, _ = helper.command(ctx, "systemctl", "restart", helper.ServiceUnit)
			rollbackActive, rollbackErr := helper.command(ctx, "systemctl", "is-active", helper.ServiceUnit)
			rollbackHealthErr := error(nil)
			if rollbackErr == nil && strings.TrimSpace(string(rollbackActive)) == "active" &&
				realityListenPort != 0 {
				rollbackHealthErr = helper.waitTCPListener(ctx, realityListenPort)
			}
			if rollbackErr == nil && rollbackHealthErr == nil &&
				strings.TrimSpace(string(rollbackActive)) == "active" {
				response.RolledBack = true
			}
		}
	}
	response.Status = "failed"
	response.ErrorCode = "core_restart_failed"
	response.ErrorMessage = commandErrorMessage(errors.Join(restartErr, activeErr, healthErr), "core restart failed")
	response.CompletedAt = helper.now()
	return response
}

func (helper *Helper) managesVLESSReality() bool {
	return helper.SingBoxBinaryPath != "" && helper.RealityIdentityPath != "" &&
		helper.RealityAppliedPath != ""
}

func (helper *Helper) createBackup(operationID string) (*protocol.Backup, error) {
	if err := helper.prepareBackupDir(); err != nil {
		return nil, err
	}
	if existing, ok := helper.existingOperationBackup(operationID); ok {
		return helper.backupMetadata(existing)
	}
	info, err := os.Lstat(helper.CoreConfigPath)
	if err != nil {
		return nil, fmt.Errorf("inspect core configuration: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("core configuration cannot be a symbolic link")
	}
	switch {
	case info.Mode().IsRegular():
		return helper.createFileBackup(operationID, info)
	case info.IsDir():
		return helper.createDirectoryBackup(operationID, info)
	default:
		return nil, errors.New("core configuration must be a bounded regular file or directory")
	}
}

func (helper *Helper) prepareBackupDir() error {
	before, err := os.Lstat(helper.BackupDir)
	created := false
	switch {
	case err == nil:
		if !validBackupDirectoryInfo(helper.BackupDir, before) {
			return errors.New("backup path is not a secure directory")
		}
	case errors.Is(err, os.ErrNotExist):
		if err := os.Mkdir(helper.BackupDir, 0o700); err != nil {
			return fmt.Errorf("create backup directory: %w", err)
		}
		created = true
	default:
		return fmt.Errorf("inspect backup directory: %w", err)
	}
	pathInfo, err := os.Lstat(helper.BackupDir)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.IsDir() {
		return errors.New("backup path is not a secure directory")
	}
	directory, err := os.Open(helper.BackupDir)
	if err != nil {
		return fmt.Errorf("open backup directory: %w", err)
	}
	openedInfo, statErr := directory.Stat()
	if statErr != nil || !openedInfo.IsDir() || !os.SameFile(pathInfo, openedInfo) {
		_ = directory.Close()
		return errors.New("backup directory changed while opening")
	}
	if created {
		if err := directory.Chmod(0o700); err != nil {
			_ = directory.Close()
			return fmt.Errorf("secure backup directory: %w", err)
		}
		if requiresBackupRootOwner(helper.BackupDir) {
			if err := directory.Chown(0, 0); err != nil {
				_ = directory.Close()
				return fmt.Errorf("secure backup directory ownership: %w", err)
			}
		}
	}
	securedInfo, statErr := directory.Stat()
	closeErr := directory.Close()
	after, inspectErr := os.Lstat(helper.BackupDir)
	if statErr != nil || closeErr != nil || inspectErr != nil ||
		!validBackupDirectoryInfo(helper.BackupDir, securedInfo) ||
		!validBackupDirectoryInfo(helper.BackupDir, after) ||
		!os.SameFile(pathInfo, securedInfo) || !os.SameFile(securedInfo, after) ||
		!sameFileOwnership(securedInfo, after) {
		return errors.New("backup directory metadata could not be verified")
	}
	return nil
}

func (helper *Helper) createFileBackup(operationID string, info os.FileInfo) (*protocol.Backup, error) {
	if info.Size() > maxConfigBackupBytes {
		return nil, errors.New("core configuration exceeds backup size limit")
	}
	source, err := os.Open(helper.CoreConfigPath)
	if err != nil {
		return nil, fmt.Errorf("open core configuration: %w", err)
	}
	defer source.Close()
	openedInfo, err := source.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return nil, errors.New("core configuration changed while opening")
	}
	name := fmt.Sprintf(
		"%d-%s-%s.bak", helper.now().UnixMilli(), operationID,
		filepath.Base(helper.CoreConfigPath),
	)
	destinationPath := filepath.Join(helper.BackupDir, name)
	temporary, err := os.CreateTemp(helper.BackupDir, ".backup-*")
	if err != nil {
		return nil, fmt.Errorf("create temporary backup: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return nil, fmt.Errorf("secure temporary backup: %w", err)
	}
	if err := helper.secureBackupFile(temporary); err != nil {
		_ = temporary.Close()
		return nil, err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(source, maxConfigBackupBytes+1))
	if err != nil || written > maxConfigBackupBytes {
		_ = temporary.Close()
		return nil, errors.New("copy core configuration failed or exceeded size limit")
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return nil, fmt.Errorf("sync configuration backup: %w", err)
	}
	temporaryInfo, statErr := temporary.Stat()
	if statErr != nil || !validBackupFileInfo(helper.BackupDir, temporaryInfo) {
		_ = temporary.Close()
		return nil, errors.New("temporary configuration backup metadata is invalid")
	}
	if err := temporary.Close(); err != nil {
		return nil, fmt.Errorf("close configuration backup: %w", err)
	}
	if err := helper.publishBackup(temporaryPath, destinationPath, temporaryInfo, written); err != nil {
		return nil, err
	}
	return &protocol.Backup{
		LocalPath: destinationPath, SHA256: hex.EncodeToString(hash.Sum(nil)), SizeBytes: written,
	}, nil
}

func (helper *Helper) createDirectoryBackup(operationID string, rootInfo os.FileInfo) (*protocol.Backup, error) {
	name := fmt.Sprintf(
		"%d-%s-%s.tar.gz", helper.now().UnixMilli(), operationID,
		filepath.Base(helper.CoreConfigPath),
	)
	destinationPath := filepath.Join(helper.BackupDir, name)
	temporary, err := os.CreateTemp(helper.BackupDir, ".backup-*")
	if err != nil {
		return nil, fmt.Errorf("create temporary backup: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return nil, fmt.Errorf("secure temporary backup: %w", err)
	}
	if err := helper.secureBackupFile(temporary); err != nil {
		_ = temporary.Close()
		return nil, err
	}
	hash := sha256.New()
	gzipWriter := gzip.NewWriter(io.MultiWriter(temporary, hash))
	gzipWriter.Header.Name = filepath.Base(helper.CoreConfigPath)
	gzipWriter.Header.ModTime = helper.now()
	tarWriter := tar.NewWriter(gzipWriter)
	entries := 0
	var totalBytes int64
	walkErr := filepath.WalkDir(helper.CoreConfigPath, func(currentPath string, entry fs.DirEntry, walkError error) error {
		if walkError != nil {
			return walkError
		}
		if currentPath == helper.CoreConfigPath {
			return nil
		}
		entries++
		if entries > maxConfigBackupEntries {
			return errors.New("core configuration directory has too many entries")
		}
		relativePath, err := filepath.Rel(helper.CoreConfigPath, currentPath)
		if err != nil {
			return errors.New("core configuration entry path is invalid")
		}
		archiveName := filepath.ToSlash(relativePath)
		if err := validateArchiveName(archiveName); err != nil {
			return err
		}
		entryInfo, err := os.Lstat(currentPath)
		if err != nil {
			return err
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			return errors.New("core configuration directory contains a symbolic link")
		}
		header, err := tar.FileInfoHeader(entryInfo, "")
		if err != nil {
			return err
		}
		header.Name = archiveName
		header.Uid = 0
		header.Gid = 0
		header.Uname = ""
		header.Gname = ""
		switch {
		case entryInfo.IsDir():
			header.Name += "/"
			return tarWriter.WriteHeader(header)
		case entryInfo.Mode().IsRegular():
			totalBytes += entryInfo.Size()
			if entryInfo.Size() < 0 || totalBytes > maxConfigBackupBytes {
				return errors.New("core configuration directory exceeds backup size limit")
			}
			source, err := os.Open(currentPath)
			if err != nil {
				return err
			}
			openedInfo, statErr := source.Stat()
			if statErr != nil || !os.SameFile(entryInfo, openedInfo) || openedInfo.Size() != entryInfo.Size() {
				_ = source.Close()
				return errors.New("core configuration entry changed while opening")
			}
			if err := tarWriter.WriteHeader(header); err != nil {
				_ = source.Close()
				return err
			}
			written, copyErr := io.Copy(tarWriter, io.LimitReader(source, entryInfo.Size()+1))
			closeErr := source.Close()
			if copyErr != nil || closeErr != nil || written != entryInfo.Size() {
				return errors.New("copy core configuration entry failed")
			}
			return nil
		default:
			return errors.New("core configuration directory contains an unsupported entry")
		}
	})
	if walkErr == nil {
		currentInfo, err := os.Lstat(helper.CoreConfigPath)
		if err != nil || !os.SameFile(rootInfo, currentInfo) || !currentInfo.IsDir() {
			walkErr = errors.New("core configuration directory changed during backup")
		}
	}
	if walkErr != nil {
		_ = tarWriter.Close()
		_ = gzipWriter.Close()
		_ = temporary.Close()
		return nil, walkErr
	}
	if err := tarWriter.Close(); err != nil {
		_ = gzipWriter.Close()
		_ = temporary.Close()
		return nil, fmt.Errorf("finalize configuration archive: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		_ = temporary.Close()
		return nil, fmt.Errorf("compress configuration archive: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return nil, fmt.Errorf("sync configuration backup: %w", err)
	}
	archiveInfo, err := temporary.Stat()
	if err != nil || archiveInfo.Size() > maxConfigBackupArchiveBytes {
		_ = temporary.Close()
		return nil, errors.New("configuration archive exceeds backup size limit")
	}
	temporaryInfo, statErr := temporary.Stat()
	if statErr != nil || !validBackupFileInfo(helper.BackupDir, temporaryInfo) {
		_ = temporary.Close()
		return nil, errors.New("temporary configuration backup metadata is invalid")
	}
	if err := temporary.Close(); err != nil {
		return nil, fmt.Errorf("close configuration backup: %w", err)
	}
	if err := helper.publishBackup(
		temporaryPath, destinationPath, temporaryInfo, archiveInfo.Size(),
	); err != nil {
		return nil, err
	}
	return &protocol.Backup{
		LocalPath: destinationPath, SHA256: hex.EncodeToString(hash.Sum(nil)),
		SizeBytes: archiveInfo.Size(),
	}, nil
}

func (helper *Helper) existingOperationBackup(operationID string) (string, bool) {
	entries, err := os.ReadDir(helper.BackupDir)
	if err != nil {
		return "", false
	}
	suffix, err := helper.configBackupSuffix()
	if err != nil {
		return "", false
	}
	needle := "-" + operationID + "-"
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink == 0 && strings.Contains(entry.Name(), needle) &&
			strings.HasSuffix(entry.Name(), "-"+suffix) {
			return filepath.Join(helper.BackupDir, entry.Name()), true
		}
	}
	return "", false
}

func (helper *Helper) backupMetadata(backupPath string) (*protocol.Backup, error) {
	if err := helper.validateBackupDir(); err != nil {
		return nil, err
	}
	if !helper.isBackupPath(backupPath) {
		return nil, errors.New("existing configuration backup is invalid")
	}
	file, info, err := helper.openVerifiedBackup(backupPath, maxConfigBackupArchiveBytes)
	if err != nil {
		return nil, errors.New("existing configuration backup is invalid")
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maxConfigBackupArchiveBytes+1))
	verifiedInfo, verifyErr := helper.verifyOpenBackup(backupPath, file, info)
	closeErr := file.Close()
	if err != nil || verifyErr != nil || closeErr != nil || written > maxConfigBackupArchiveBytes ||
		written != info.Size() || verifiedInfo.Size() != written {
		return nil, errors.New("read existing configuration backup failed")
	}
	return &protocol.Backup{
		LocalPath: backupPath, SHA256: hex.EncodeToString(hash.Sum(nil)), SizeBytes: written,
	}, nil
}

func (helper *Helper) restoreBackup(backupPath string) error {
	if err := helper.validateBackupDir(); err != nil {
		return err
	}
	backupInfo, err := os.Lstat(backupPath)
	if err != nil || !validBackupFileInfo(helper.BackupDir, backupInfo) ||
		backupInfo.Size() > maxConfigBackupArchiveBytes || !helper.isBackupPath(backupPath) {
		return errors.New("rollback backup is invalid")
	}
	destinationInfo, err := os.Lstat(helper.CoreConfigPath)
	if err != nil || destinationInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("rollback destination is invalid")
	}
	switch {
	case destinationInfo.Mode().IsRegular() && strings.HasSuffix(backupPath, ".bak"):
		return helper.restoreFileBackup(backupPath, destinationInfo)
	case destinationInfo.IsDir() && strings.HasSuffix(backupPath, ".tar.gz"):
		return helper.restoreDirectoryBackup(backupPath, destinationInfo)
	default:
		return errors.New("rollback backup type does not match destination")
	}
}

func (helper *Helper) restoreFileBackup(backupPath string, destinationInfo os.FileInfo) error {
	source, backupInfo, err := helper.openVerifiedBackup(backupPath, maxConfigBackupBytes)
	if err != nil {
		return errors.New("rollback file backup is invalid")
	}
	temporary, err := os.CreateTemp(filepath.Dir(helper.CoreConfigPath), ".hyfleet-rollback-*")
	if err != nil {
		_ = source.Close()
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(destinationInfo.Mode().Perm()); err != nil {
		_ = source.Close()
		_ = temporary.Close()
		return err
	}
	if err := inheritFileOwnership(temporary, destinationInfo); err != nil {
		_ = source.Close()
		_ = temporary.Close()
		return err
	}
	written, err := io.Copy(temporary, io.LimitReader(source, maxConfigBackupBytes+1))
	_, verifyErr := helper.verifyOpenBackup(backupPath, source, backupInfo)
	closeSourceErr := source.Close()
	if err != nil || verifyErr != nil || closeSourceErr != nil ||
		written > maxConfigBackupBytes || written != backupInfo.Size() {
		_ = temporary.Close()
		return errors.New("rollback backup exceeds size limit or could not be copied")
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := replaceHelperFile(temporaryPath, helper.CoreConfigPath); err != nil {
		return err
	}
	restoredInfo, err := os.Lstat(helper.CoreConfigPath)
	if err != nil || !restoredInfo.Mode().IsRegular() || restoredInfo.Mode()&os.ModeSymlink != 0 ||
		!samePermissions(restoredInfo.Mode(), destinationInfo.Mode()) ||
		!sameFileOwnership(restoredInfo, destinationInfo) || restoredInfo.Size() != backupInfo.Size() {
		return errors.New("restored configuration metadata is invalid")
	}
	return nil
}

func (helper *Helper) restoreDirectoryBackup(backupPath string, destinationInfo os.FileInfo) error {
	archive, backupInfo, err := helper.openVerifiedBackup(backupPath, maxConfigBackupArchiveBytes)
	if err != nil {
		return errors.New("rollback directory backup is invalid")
	}
	temporaryRoot, err := os.MkdirTemp(filepath.Dir(helper.CoreConfigPath), ".hyfleet-rollback-*")
	if err != nil {
		_ = archive.Close()
		return fmt.Errorf("create rollback directory: %w", err)
	}
	defer os.RemoveAll(temporaryRoot)
	if err := os.Chmod(temporaryRoot, destinationInfo.Mode().Perm()); err != nil {
		_ = archive.Close()
		return fmt.Errorf("prepare rollback directory: %w", err)
	}
	if runtime.GOOS != "windows" {
		owner, ok := ownershipOf(destinationInfo)
		if !ok || os.Chown(temporaryRoot, int(owner.uid), int(owner.gid)) != nil {
			_ = archive.Close()
			return errors.New("preserve rollback directory ownership")
		}
	}
	if err := extractDirectoryBackup(archive, temporaryRoot); err != nil {
		_ = archive.Close()
		return err
	}
	_, verifyErr := helper.verifyOpenBackup(backupPath, archive, backupInfo)
	closeArchiveErr := archive.Close()
	if verifyErr != nil || closeArchiveErr != nil {
		return errors.New("rollback directory backup changed while reading")
	}
	if err := replaceHelperDirectory(temporaryRoot, helper.CoreConfigPath); err != nil {
		return err
	}
	restoredInfo, err := os.Lstat(helper.CoreConfigPath)
	if err != nil || !restoredInfo.IsDir() || restoredInfo.Mode()&os.ModeSymlink != 0 ||
		!samePermissions(restoredInfo.Mode(), destinationInfo.Mode()) ||
		!sameFileOwnership(restoredInfo, destinationInfo) {
		return errors.New("restored configuration directory metadata is invalid")
	}
	return nil
}

func (helper *Helper) configBackupSuffix() (string, error) {
	info, err := os.Lstat(helper.CoreConfigPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("core configuration is unavailable")
	}
	switch {
	case info.Mode().IsRegular():
		return filepath.Base(helper.CoreConfigPath) + ".bak", nil
	case info.IsDir():
		return filepath.Base(helper.CoreConfigPath) + ".tar.gz", nil
	default:
		return "", errors.New("core configuration has an unsupported type")
	}
}

func (helper *Helper) isBackupPath(backupPath string) bool {
	relativePath, err := filepath.Rel(helper.BackupDir, backupPath)
	return err == nil && relativePath != "." && relativePath != "" &&
		!filepath.IsAbs(relativePath) && relativePath != ".." &&
		!strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)) &&
		filepath.Dir(relativePath) == "."
}

func validateArchiveName(name string) error {
	if name == "" || len(name) > 256 || strings.ContainsRune(name, '\x00') || pathpkg.IsAbs(name) ||
		pathpkg.Clean(name) != name || name == "." || name == ".." ||
		strings.HasPrefix(name, "../") || strings.Count(name, "/") >= maxConfigBackupDepth {
		return errors.New("core configuration archive path is invalid")
	}
	return nil
}

func extractDirectoryBackup(archive *os.File, destinationRoot string) error {
	gzipReader, err := gzip.NewReader(io.LimitReader(archive, maxConfigBackupArchiveBytes+1))
	if err != nil {
		return errors.New("configuration archive is not valid gzip data")
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	type directoryMode struct {
		path string
		mode os.FileMode
	}
	directories := make([]directoryMode, 0)
	entries := 0
	var totalBytes int64
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errors.New("read configuration archive failed")
		}
		entries++
		if entries > maxConfigBackupEntries {
			return errors.New("configuration archive has too many entries")
		}
		archiveName := strings.TrimSuffix(header.Name, "/")
		if err := validateArchiveName(archiveName); err != nil {
			return err
		}
		destinationPath := filepath.Join(destinationRoot, filepath.FromSlash(archiveName))
		relativePath, err := filepath.Rel(destinationRoot, destinationPath)
		if err != nil || relativePath == "." || relativePath == ".." ||
			strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)) {
			return errors.New("configuration archive escapes restore directory")
		}
		mode := os.FileMode(header.Mode) & os.ModePerm
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destinationPath, 0o700); err != nil {
				return fmt.Errorf("create restored configuration directory: %w", err)
			}
			directories = append(directories, directoryMode{path: destinationPath, mode: mode})
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 {
				return errors.New("configuration archive file size is invalid")
			}
			totalBytes += header.Size
			if totalBytes > maxConfigBackupBytes {
				return errors.New("configuration archive exceeds restore size limit")
			}
			if err := os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
				return fmt.Errorf("create restored configuration parent: %w", err)
			}
			file, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return errors.New("configuration archive contains a duplicate or invalid file")
			}
			written, copyErr := io.CopyN(file, tarReader, header.Size)
			syncErr := file.Sync()
			closeErr := file.Close()
			if copyErr != nil || syncErr != nil || closeErr != nil || written != header.Size {
				return errors.New("restore configuration archive file failed")
			}
			if err := os.Chmod(destinationPath, mode); err != nil {
				return fmt.Errorf("restore configuration file mode: %w", err)
			}
		default:
			return errors.New("configuration archive contains an unsupported entry")
		}
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := os.Chmod(directories[index].path, directories[index].mode); err != nil {
			return fmt.Errorf("restore configuration directory mode: %w", err)
		}
	}
	return nil
}

func (helper *Helper) secureBackupFile(file *os.File) error {
	if requiresBackupRootOwner(helper.BackupDir) {
		if err := file.Chown(0, 0); err != nil {
			return fmt.Errorf("secure configuration backup ownership: %w", err)
		}
	}
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("secure configuration backup mode: %w", err)
	}
	info, err := file.Stat()
	if err != nil || !validBackupFileInfo(helper.BackupDir, info) {
		return errors.New("configuration backup metadata could not be verified")
	}
	return nil
}

func (helper *Helper) verifyPublishedBackup(
	path string, expected os.FileInfo, expectedSize int64,
) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil || !validBackupFileInfo(helper.BackupDir, info) ||
		!os.SameFile(expected, info) || !sameFileOwnership(expected, info) ||
		!samePermissions(expected.Mode(), info.Mode()) || info.Size() != expectedSize {
		return nil, errors.New("published configuration backup metadata is invalid")
	}
	return info, nil
}

func (helper *Helper) publishBackup(
	temporaryPath, destinationPath string,
	temporaryInfo os.FileInfo,
	expectedSize int64,
) error {
	if err := os.Link(temporaryPath, destinationPath); err != nil {
		return fmt.Errorf("publish configuration backup: %w", err)
	}
	cleanup := func() {
		info, err := os.Lstat(destinationPath)
		if err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 &&
			os.SameFile(temporaryInfo, info) {
			_ = os.Remove(destinationPath)
			_ = syncDirectory(helper.BackupDir)
		}
	}
	if _, err := helper.verifyPublishedBackup(destinationPath, temporaryInfo, expectedSize); err != nil {
		cleanup()
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		cleanup()
		return fmt.Errorf("finalize configuration backup: %w", err)
	}
	if err := syncDirectory(helper.BackupDir); err != nil {
		cleanup()
		return fmt.Errorf("sync configuration backup directory: %w", err)
	}
	if _, err := helper.verifyPublishedBackup(destinationPath, temporaryInfo, expectedSize); err != nil {
		cleanup()
		return err
	}
	return nil
}

func (helper *Helper) validateBackupDir() error {
	info, err := os.Lstat(helper.BackupDir)
	if err != nil || !validBackupDirectoryInfo(helper.BackupDir, info) {
		return errors.New("backup directory metadata is invalid")
	}
	return nil
}

func (helper *Helper) openVerifiedBackup(path string, limit int64) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil || !validBackupFileInfo(helper.BackupDir, before) ||
		before.Size() < 0 || before.Size() > limit {
		return nil, nil, errors.New("configuration backup metadata is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	opened, statErr := file.Stat()
	after, inspectErr := os.Lstat(path)
	if statErr != nil || inspectErr != nil || !validBackupFileInfo(helper.BackupDir, opened) ||
		!validBackupFileInfo(helper.BackupDir, after) || !os.SameFile(before, opened) ||
		!os.SameFile(opened, after) || !sameFileOwnership(before, opened) ||
		!sameFileOwnership(opened, after) || !samePermissions(before.Mode(), opened.Mode()) ||
		!samePermissions(opened.Mode(), after.Mode()) || before.Size() != opened.Size() ||
		opened.Size() != after.Size() || !before.ModTime().Equal(opened.ModTime()) ||
		!opened.ModTime().Equal(after.ModTime()) {
		_ = file.Close()
		return nil, nil, errors.New("configuration backup changed while opening")
	}
	return file, opened, nil
}

func (helper *Helper) verifyOpenBackup(
	path string, file *os.File, expected os.FileInfo,
) (os.FileInfo, error) {
	opened, statErr := file.Stat()
	after, inspectErr := os.Lstat(path)
	if expected == nil {
		expected = opened
	}
	if statErr != nil || inspectErr != nil || !validBackupFileInfo(helper.BackupDir, opened) ||
		!validBackupFileInfo(helper.BackupDir, after) || !os.SameFile(expected, opened) ||
		!os.SameFile(opened, after) || !sameFileOwnership(expected, opened) ||
		!sameFileOwnership(opened, after) || !samePermissions(expected.Mode(), opened.Mode()) ||
		!samePermissions(opened.Mode(), after.Mode()) || expected.Size() != opened.Size() ||
		opened.Size() != after.Size() || !expected.ModTime().Equal(opened.ModTime()) ||
		!opened.ModTime().Equal(after.ModTime()) {
		return nil, errors.New("configuration backup changed while reading")
	}
	return opened, nil
}

func validBackupDirectoryInfo(path string, info os.FileInfo) bool {
	if info == nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		!exactPermissions(info.Mode(), 0o700) {
		return false
	}
	return !requiresBackupRootOwner(path) || fileOwnedByRoot(info)
}

func validBackupFileInfo(backupDir string, info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		!exactPermissions(info.Mode(), 0o600) {
		return false
	}
	return !requiresBackupRootOwner(backupDir) || fileOwnedByRoot(info)
}

func (helper *Helper) loadResult(operation protocol.NodeOperation) (HelperResponse, bool) {
	if _, err := uuid.Parse(operation.ID); err != nil {
		return HelperResponse{}, false
	}
	path := filepath.Join(helper.LedgerDir, operation.ID+".json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 64*1024 {
		return HelperResponse{}, false
	}
	file, err := os.Open(path)
	if err != nil {
		return HelperResponse{}, false
	}
	defer file.Close()
	var response HelperResponse
	if err := json.NewDecoder(io.LimitReader(file, 64*1024)).Decode(&response); err != nil ||
		response.Sequence != operation.Sequence ||
		(response.Status != "succeeded" && response.Status != "failed") {
		return HelperResponse{}, false
	}
	return response, true
}

func (helper *Helper) saveResult(operation protocol.NodeOperation, response HelperResponse) error {
	if err := os.MkdirAll(helper.LedgerDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(helper.LedgerDir, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(helper.LedgerDir, ".result-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := json.NewEncoder(temporary).Encode(response); err != nil {
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
	return os.Rename(temporaryPath, filepath.Join(helper.LedgerDir, operation.ID+".json"))
}

func (helper *Helper) command(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	runner := helper.RunCommand
	if runner == nil {
		runner = runBoundedCommand
	}
	return runner(ctx, name, arguments...)
}

func (helper *Helper) now() time.Time {
	if helper.Now == nil {
		return time.Now().UTC()
	}
	return helper.Now().UTC()
}

func runBoundedCommand(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	output, err := command.CombinedOutput()
	return []byte(SanitizeOutput(string(output), MaxLogLines, MaxOutputSize)), err
}

func commandErrorMessage(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	return fallback + ": " + err.Error()
}
