package store

import (
	"database/sql"
	"errors"
	"time"
)

var (
	ErrNotFound         = errors.New("not found")
	ErrConflict         = errors.New("conflict")
	ErrExpired          = errors.New("expired")
	ErrPending          = errors.New("pending changes")
	ErrUnauthorized     = errors.New("unauthorized")
	ErrVersionConflict  = errors.New("version conflict")
	ErrUnsupported      = errors.New("unsupported")
	ErrReadOnly         = errors.New("read-only assignment")
	ErrQuotaUnsupported = errors.New("quota unsupported")
	ErrKickUnsupported  = errors.New("kick unsupported")
	ErrNodeEnabled      = errors.New("node must be disabled")
)

func unixTime(value int64) time.Time {
	return time.UnixMilli(value).UTC()
}

func nullableTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	result := unixTime(value.Int64)
	return &result
}
