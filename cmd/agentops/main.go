package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/Sen62455/PolyFleet/internal/buildinfo"
	"github.com/Sen62455/PolyFleet/internal/config"
	"github.com/Sen62455/PolyFleet/internal/nodeops"
)

const (
	helperRequestTimeout = 40 * time.Second
	helperLockTimeout    = 5 * time.Second
)

func main() {
	configPath := flag.String("config", "", "path to Agent YAML config")
	checkConfig := flag.Bool("check-config", false, "validate configuration and exit")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("hyfleet-agent-ops %s (%s, %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg, err := config.LoadAgent(*configPath)
	if err != nil {
		logger.Error("load configuration failed", "error", err)
		os.Exit(1)
	}
	var helper *nodeops.Helper
	if cfg.AdapterType == "sing_box_vless_reality" {
		helper, err = nodeops.NewRealityHelper(
			cfg.ServiceUnit, cfg.CoreConfigPath, cfg.SingBoxBinaryPath, cfg.RealityIdentityPath,
			cfg.OperationsStateDir, cfg.BackupDir,
		)
	} else {
		helper, err = nodeops.NewHelper(cfg.ServiceUnit, cfg.CoreConfigPath)
	}
	if err != nil {
		logger.Error("initialize operations helper failed", "error", err)
		os.Exit(1)
	}
	if *checkConfig {
		fmt.Println("operations helper configuration is valid")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), helperRequestTimeout)
	defer cancel()
	connection, err := openHelperConnection(ctx, os.Stdin)
	if err != nil {
		logger.Error("initialize operations helper connection failed", "error", err)
		os.Exit(1)
	}
	defer connection.Close()
	if err := serveHelper(ctx, helper, connection, connection); err != nil {
		logger.Error("operations helper request failed", "error", err)
		os.Exit(1)
	}
}

func serveHelper(
	ctx context.Context,
	helper *nodeops.Helper,
	reader io.Reader,
	writer io.Writer,
) (returnErr error) {
	return serveHelperWithLock(ctx, helper, reader, writer, acquireHelperLock)
}

type helperLockAcquirer func(context.Context, string) (func() error, error)

func serveHelperWithLock(
	ctx context.Context,
	helper *nodeops.Helper,
	reader io.Reader,
	writer io.Writer,
	acquireLock helperLockAcquirer,
) (returnErr error) {
	request, err := nodeops.DecodeHelperRequest(reader)
	if err != nil {
		return err
	}
	lockContext, cancelLock := context.WithTimeout(ctx, helperLockTimeout)
	releaseLock, err := acquireLock(lockContext, helper.LedgerDir)
	cancelLock()
	if err != nil {
		return fmt.Errorf("acquire operations helper lock: %w", err)
	}
	defer func() {
		if err := releaseLock(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("release operations helper lock: %w", err))
		}
	}()
	return nodeops.EncodeHelperResponse(writer, helper.Handle(ctx, request))
}
