package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Sen62455/PolyFleet/internal/nodeops"
)

func TestServeHelperDecodesAndValidatesBeforeAcquiringLock(t *testing.T) {
	helper, err := nodeops.NewHelper("hysteria-server.service", "")
	if err != nil {
		t.Fatalf("NewHelper() error = %v", err)
	}
	lockCalls := 0
	acquireLock := func(context.Context, string) (func() error, error) {
		lockCalls++
		return func() error { return nil }, nil
	}
	for name, input := range map[string]string{
		"incomplete JSON": `{"reality_probe":`,
		"missing action":  `{}`,
		"unknown field":   `{"reality_probe":{},"unexpected":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			lockCalls = 0
			err := serveHelperWithLock(
				t.Context(), helper, strings.NewReader(input), &bytes.Buffer{}, acquireLock,
			)
			if err == nil {
				t.Fatal("serveHelperWithLock() accepted invalid request")
			}
			if lockCalls != 0 {
				t.Fatalf("lock acquired %d times before request validation", lockCalls)
			}
		})
	}
}

func TestServeHelperAcquiresLockOnlyForHandleAndEncode(t *testing.T) {
	helper, err := nodeops.NewHelper("hysteria-server.service", "")
	if err != nil {
		t.Fatalf("NewHelper() error = %v", err)
	}
	helper.RunCommand = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("active\n"), nil
	}
	locked := false
	released := false
	writer := &lockCheckingWriter{t: t, locked: &locked, released: &released}
	acquireLock := func(context.Context, string) (func() error, error) {
		if locked || released {
			t.Fatal("unexpected lock state before acquisition")
		}
		locked = true
		return func() error {
			locked = false
			released = true
			return nil
		}, nil
	}
	input := `{"operation":{"id":"5e0a77b4-75aa-4223-b4c8-73ce84864df7","sequence":1,"type":"probe_core","attempt":1}}`
	if err := serveHelperWithLock(t.Context(), helper, strings.NewReader(input), writer, acquireLock); err != nil {
		t.Fatalf("serveHelperWithLock() error = %v", err)
	}
	if locked || !released {
		t.Fatalf("lock state after request: locked=%t released=%t", locked, released)
	}
}

type lockCheckingWriter struct {
	t        *testing.T
	locked   *bool
	released *bool
}

func (writer *lockCheckingWriter) Write(data []byte) (int, error) {
	writer.t.Helper()
	if !*writer.locked || *writer.released {
		writer.t.Fatal("helper response was written outside the lock")
	}
	return len(data), nil
}

func TestServeHelperJoinsReleaseError(t *testing.T) {
	helper, err := nodeops.NewHelper("hysteria-server.service", "")
	if err != nil {
		t.Fatalf("NewHelper() error = %v", err)
	}
	helper.RunCommand = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("active\n"), nil
	}
	releaseErr := errors.New("release failed")
	acquireLock := func(context.Context, string) (func() error, error) {
		return func() error { return releaseErr }, nil
	}
	input := `{"operation":{"id":"5e0a77b4-75aa-4223-b4c8-73ce84864df7","sequence":1,"type":"probe_core","attempt":1}}`
	err = serveHelperWithLock(t.Context(), helper, strings.NewReader(input), &bytes.Buffer{}, acquireLock)
	if !errors.Is(err, releaseErr) {
		t.Fatalf("serveHelperWithLock() error = %v", err)
	}
}
