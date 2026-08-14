package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/Sen62455/PolyFleet/internal/nodeops"
)

const maxHelperResponseBytes int64 = 64 * 1024

type helperExchangeStage uint8

const (
	helperExchangeDial helperExchangeStage = iota + 1
	helperExchangeWrite
	helperExchangeRead
)

type helperExchangeError struct {
	stage helperExchangeStage
	err   error
}

func (exchangeErr *helperExchangeError) Error() string { return exchangeErr.err.Error() }
func (exchangeErr *helperExchangeError) Unwrap() error { return exchangeErr.err }

func exchangeHelper(
	ctx context.Context,
	socketPath string,
	timeout time.Duration,
	request nodeops.HelperRequest,
) (nodeops.HelperResponse, error) {
	dialer := net.Dialer{Timeout: 3 * time.Second}
	connection, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nodeops.HelperResponse{}, &helperExchangeError{stage: helperExchangeDial, err: err}
	}
	defer connection.Close()

	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = connection.SetDeadline(deadline)

	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return nodeops.HelperResponse{}, &helperExchangeError{stage: helperExchangeWrite, err: err}
	}
	writeCloser, ok := connection.(interface{ CloseWrite() error })
	if !ok {
		return nodeops.HelperResponse{}, &helperExchangeError{
			stage: helperExchangeWrite,
			err:   errors.New("helper connection does not support write half-close"),
		}
	}
	if err := writeCloser.CloseWrite(); err != nil {
		return nodeops.HelperResponse{}, &helperExchangeError{
			stage: helperExchangeWrite,
			err:   fmt.Errorf("finish helper request: %w", err),
		}
	}

	response, err := decodeHelperResponse(connection)
	if err != nil {
		return nodeops.HelperResponse{}, &helperExchangeError{stage: helperExchangeRead, err: err}
	}
	return response, nil
}

func decodeHelperResponse(reader io.Reader) (nodeops.HelperResponse, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxHelperResponseBytes+1))
	if err != nil {
		return nodeops.HelperResponse{}, fmt.Errorf("read helper response: %w", err)
	}
	if int64(len(data)) > maxHelperResponseBytes {
		return nodeops.HelperResponse{}, fmt.Errorf(
			"helper response exceeds %d byte limit", maxHelperResponseBytes,
		)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var response nodeops.HelperResponse
	if err := decoder.Decode(&response); err != nil {
		return nodeops.HelperResponse{}, fmt.Errorf("decode helper response: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nodeops.HelperResponse{}, errors.New("helper response contains multiple JSON values")
		}
		return nodeops.HelperResponse{}, fmt.Errorf("helper response has trailing data: %w", err)
	}
	return response, nil
}

func helperExchangeErrorStage(err error) helperExchangeStage {
	var exchangeErr *helperExchangeError
	if errors.As(err, &exchangeErr) {
		return exchangeErr.stage
	}
	return 0
}
