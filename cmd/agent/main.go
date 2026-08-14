package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Sen62455/PolyFleet/internal/agent"
	"github.com/Sen62455/PolyFleet/internal/buildinfo"
	"github.com/Sen62455/PolyFleet/internal/config"
	"github.com/Sen62455/PolyFleet/internal/hy2migration"
)

func main() {
	configPath := flag.String("config", "/etc/hyfleet/agent.yaml", "path to Agent YAML config")
	checkConfig := flag.Bool("check-config", false, "validate configuration and exit")
	showVersion := flag.Bool("version", false, "print version and exit")
	migrateHysteria := flag.Bool("migrate-hysteria", false, "migrate /etc/hysteria/config.yaml to Agent HTTP authentication")
	configureHysteriaStats := flag.Bool("configure-hysteria-stats", false, "configure Hysteria HTTP authentication and loopback traffic stats")
	statsEnvFile := flag.String("stats-env-file", "/etc/hyfleet/hy2-stats.env", "path to the Hysteria traffic stats environment file")
	rollbackHysteria := flag.String("rollback-hysteria", "", "restore the Hysteria server YAML from this absolute backup path")
	migrationTimeout := flag.Duration("migration-timeout", 12*time.Second, "timeout for Hysteria restart and stability checks")
	flag.Parse()
	if *showVersion {
		fmt.Printf("hyfleet-agent %s (%s, %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.LoadAgent(*configPath)
	if err != nil {
		logger.Error("load configuration failed", "error", err)
		os.Exit(1)
	}
	if *checkConfig {
		fmt.Println("Agent configuration is valid")
		return
	}
	selectedOperations := 0
	for _, selected := range []bool{*migrateHysteria, *configureHysteriaStats, *rollbackHysteria != ""} {
		if selected {
			selectedOperations++
		}
	}
	if selectedOperations > 1 {
		logger.Error("only one Hysteria migration, stats configuration, or rollback operation can be used")
		os.Exit(1)
	}
	if *migrateHysteria || *configureHysteriaStats || *rollbackHysteria != "" {
		if cfg.AdapterType != "native_hysteria2" {
			logger.Error("Hysteria migration requires a native_hysteria2 Agent configuration")
			os.Exit(1)
		}
		ctx, cancel := context.WithTimeout(context.Background(), *migrationTimeout*3)
		defer cancel()
		manager := hy2migration.SystemdManager{}
		if *migrateHysteria || *configureHysteriaStats {
			authURL := "http://" + cfg.AuthListen + cfg.AuthPath
			statsListen := ""
			statsSecret := ""
			if *configureHysteriaStats {
				statsListen = strings.TrimPrefix(cfg.TrafficStatsURL, "http://")
				statsSecret = cfg.TrafficStatsSecret
				if statsSecret == "" {
					statsSecret, err = readEnvironmentValue(*statsEnvFile, "HYFLEET_HY2_STATS_SECRET")
					if err != nil {
						logger.Error("load Hysteria traffic stats secret failed", "error", err)
						os.Exit(1)
					}
				}
			}
			result, err := hy2migration.Apply(ctx, hy2migration.Options{
				ConfigPath:  "/etc/hysteria/config.yaml",
				AuthURL:     authURL,
				StatsListen: statsListen,
				StatsSecret: statsSecret,
				Service:     cfg.ServiceUnit,
				Timeout:     *migrationTimeout,
			}, manager)
			if err != nil {
				logger.Error("Hysteria HTTP authentication migration failed", "error", err, "backup_path", result.BackupPath)
				os.Exit(1)
			}
			if !result.Changed {
				fmt.Println("Hysteria integration is already configured and reachable; no restart was needed")
				return
			}
			fmt.Printf("Hysteria integration configuration completed\nBackup: %s\n", result.BackupPath)
			return
		}

		result, err := hy2migration.Rollback(
			ctx, "/etc/hysteria/config.yaml", *rollbackHysteria,
			cfg.ServiceUnit, *migrationTimeout, manager,
		)
		if err != nil {
			logger.Error("Hysteria configuration rollback failed", "error", err, "safeguard_path", result.BackupPath)
			os.Exit(1)
		}
		if !result.Changed {
			fmt.Println("The selected backup already matches the active Hysteria config")
			return
		}
		fmt.Printf("Hysteria configuration rollback completed\nPre-rollback safeguard: %s\n", result.BackupPath)
		return
	}
	runner, err := agent.New(cfg, logger)
	if err != nil {
		logger.Error("initialize Agent failed", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runner.Run(ctx); err != nil {
		logger.Error("Agent stopped", "error", err)
		os.Exit(1)
	}
}

func readEnvironmentValue(path, key string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return "", fmt.Errorf("%s must be a small regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, found := strings.Cut(line, "=")
		if found && name == key && value != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("%s does not define %s", path, key)
}
