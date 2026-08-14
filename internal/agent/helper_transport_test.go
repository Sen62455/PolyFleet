package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Sen62455/PolyFleet/internal/nodeops"
	"github.com/Sen62455/PolyFleet/internal/protocol"
)

func TestExchangeHelperHalfClosesRequestBeforeReadingResponse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows AF_UNIX does not reliably propagate EOF after peer close")
	}
	socketPath := filepath.Join(os.TempDir(), "hyfleet-"+uuid.NewString()+".sock")
	t.Cleanup(func() { _ = os.Remove(socketPath) })
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatalf("net.ListenUnix() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	operation := protocol.NodeOperation{
		ID: uuid.NewString(), Sequence: 17, Type: "probe_core",
		Attempt: 1, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Minute),
	}
	serverErr := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(2 * time.Second))

		requestBytes, readErr := io.ReadAll(connection)
		if readErr != nil {
			serverErr <- readErr
			return
		}
		var request nodeops.HelperRequest
		if decodeErr := json.Unmarshal(requestBytes, &request); decodeErr != nil {
			serverErr <- decodeErr
			return
		}
		if request.Operation == nil || request.Operation.ID != operation.ID ||
			request.Operation.Sequence != operation.Sequence {
			serverErr <- fmt.Errorf("helper request = %#v", request)
			return
		}
		response := nodeops.HelperResponse{
			Sequence: operation.Sequence, Status: "succeeded", CompletedAt: time.Now().UTC(),
		}
		if encodeErr := json.NewEncoder(connection).Encode(response); encodeErr != nil {
			serverErr <- encodeErr
			return
		}
		if closeErr := connection.Close(); closeErr != nil {
			serverErr <- closeErr
			return
		}
		serverErr <- nil
	}()

	response, err := exchangeHelper(
		context.Background(), socketPath, 2*time.Second,
		nodeops.HelperRequest{Operation: &operation},
	)
	if err != nil {
		t.Fatalf("exchangeHelper() error = %v", err)
	}
	if response.Sequence != operation.Sequence || response.Status != "succeeded" {
		t.Fatalf("exchangeHelper() response = %#v", response)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("helper server error = %v", err)
	}
}

func TestDecodeHelperResponseRejectsInvalidFraming(t *testing.T) {
	tests := map[string]struct {
		response string
		want     string
	}{
		"unknown field": {
			response: `{"sequence":17,"status":"succeeded","unexpected":true}`,
			want:     "unknown field",
		},
		"trailing data": {
			response: `{"sequence":17,"status":"succeeded"} trailing`,
			want:     "trailing data",
		},
		"second JSON value": {
			response: `{"sequence":17,"status":"succeeded"}{"sequence":18,"status":"failed"}`,
			want:     "multiple JSON values",
		},
		"oversized response": {
			response: `{"status":"succeeded","output":"` + strings.Repeat("a", int(maxHelperResponseBytes)) + `"}`,
			want:     "exceeds 65536 byte limit",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeHelperResponse(strings.NewReader(test.response)); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("decodeHelperResponse() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestDecodeHelperResponseAcceptsValidResponseAtByteLimit(t *testing.T) {
	prefix := `{"sequence":17,"status":"succeeded","output":"`
	suffix := `"}`
	outputLength := int(maxHelperResponseBytes) - len(prefix) - len(suffix)
	encoded := prefix + strings.Repeat("a", outputLength) + suffix
	if len(encoded) != int(maxHelperResponseBytes) {
		t.Fatalf("test response size = %d, want %d", len(encoded), maxHelperResponseBytes)
	}

	response, err := decodeHelperResponse(strings.NewReader(encoded))
	if err != nil {
		t.Fatalf("decodeHelperResponse() error = %v", err)
	}
	if response.Sequence != 17 || response.Status != "succeeded" || len(response.Output) != outputLength {
		t.Fatalf("decodeHelperResponse() response = %#v", response)
	}
}
