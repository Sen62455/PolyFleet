package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

func BackupDatabase(ctx context.Context, sourcePath, destinationPath string) error {
	if sourcePath == "" || destinationPath == "" || sourcePath == ":memory:" ||
		strings.HasPrefix(sourcePath, "file:") || strings.HasPrefix(destinationPath, "file:") {
		return errors.New("database backup requires filesystem source and destination paths")
	}
	sourcePath = filepath.Clean(sourcePath)
	destinationPath = filepath.Clean(destinationPath)
	if sourcePath == destinationPath {
		return errors.New("database backup destination must differ from source")
	}
	sourceInfo, err := os.Lstat(sourcePath)
	if err != nil || !sourceInfo.Mode().IsRegular() || sourceInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("database backup source must be a regular file")
	}
	if _, err := os.Lstat(destinationPath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return errors.New("database backup destination already exists")
		}
		return fmt.Errorf("inspect database backup destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
		return fmt.Errorf("create database backup directory: %w", err)
	}
	database, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		return fmt.Errorf("open database backup source: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	defer database.Close()
	if _, err := database.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return fmt.Errorf("configure database backup source: %w", err)
	}
	if _, err := database.ExecContext(ctx, "VACUUM INTO ?", destinationPath); err != nil {
		_ = os.Remove(destinationPath)
		return fmt.Errorf("create consistent database backup: %w", err)
	}
	if err := os.Chmod(destinationPath, 0o600); err != nil {
		_ = os.Remove(destinationPath)
		return fmt.Errorf("secure database backup: %w", err)
	}
	if err := CheckDatabase(ctx, destinationPath); err != nil {
		_ = os.Remove(destinationPath)
		return fmt.Errorf("validate database backup: %w", err)
	}
	return nil
}

func CheckDatabase(ctx context.Context, path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("database must be a regular file")
	}
	database, err := sql.Open("sqlite", filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("open database for validation: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	defer database.Close()
	rows, err := database.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return fmt.Errorf("run database integrity check: %w", err)
	}
	integrityOK := false
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read database integrity result: %w", err)
		}
		if result == "ok" {
			integrityOK = true
			continue
		}
		_ = rows.Close()
		return fmt.Errorf("database integrity check failed: %s", result)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate database integrity results: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close database integrity results: %w", err)
	}
	if !integrityOK {
		return errors.New("database integrity check did not return ok")
	}
	foreignKeys, err := database.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("run database foreign key check: %w", err)
	}
	if foreignKeys.Next() {
		_ = foreignKeys.Close()
		return errors.New("database foreign key check found a violation")
	}
	if err := foreignKeys.Err(); err != nil {
		_ = foreignKeys.Close()
		return fmt.Errorf("iterate database foreign key results: %w", err)
	}
	if err := foreignKeys.Close(); err != nil {
		return fmt.Errorf("close database foreign key results: %w", err)
	}
	var migrationTable string
	if err := database.QueryRowContext(
		ctx,
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'",
	).Scan(&migrationTable); err != nil || migrationTable != "schema_migrations" {
		return errors.New("database does not contain the PolyFleet/HyFleet migration table")
	}
	return nil
}
