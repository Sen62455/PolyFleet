//go:build linux

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/nodeops"
)

func TestIncompleteSocketRequestTimesOutWithoutCreatingHelperLock(t *testing.T) {
	server, client, err := socketFilePair()
	if err != nil {
		t.Fatalf("socketFilePair() error = %v", err)
	}
	defer server.Close()
	defer client.Close()

	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatalf("secure state directory: %v", err)
	}
	helper, err := nodeops.NewHelper("hysteria-server.service", "")
	if err != nil {
		t.Fatalf("NewHelper() error = %v", err)
	}
	helper.LedgerDir = stateDir

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	connection, err := openHelperConnection(ctx, server)
	if err != nil {
		t.Fatalf("openHelperConnection() error = %v", err)
	}
	defer connection.Close()
	if _, err := client.Write([]byte(`{"reality_probe":`)); err != nil {
		t.Fatalf("write incomplete request: %v", err)
	}
	err = serveHelper(ctx, helper, connection, connection)
	if err == nil {
		t.Fatal("serveHelper() did not time out on incomplete request")
	}
	if _, statErr := os.Lstat(filepath.Join(stateDir, helperLockName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("incomplete request created helper lock: %v", statErr)
	}
}

func socketFilePair() (*os.File, net.Conn, error) {
	listener, err := net.Listen("unix", "@hyfleet-agentops-test-"+time.Now().Format("150405.000000000"))
	if err != nil {
		return nil, nil, err
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	acceptErrors := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			acceptErrors <- acceptErr
			return
		}
		accepted <- connection
	}()
	client, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		return nil, nil, err
	}
	var server net.Conn
	select {
	case server = <-accepted:
	case err = <-acceptErrors:
		client.Close()
		return nil, nil, err
	}
	unixServer, ok := server.(*net.UnixConn)
	if !ok {
		server.Close()
		client.Close()
		return nil, nil, errors.New("accepted connection is not Unix")
	}
	file, err := unixServer.File()
	server.Close()
	if err != nil {
		client.Close()
		return nil, nil, err
	}
	return file, client, nil
}
