package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/nodeops"
	"github.com/Sen62455/PolyFleet/internal/protocol"
)

func (agent *Agent) runOperationCycle(ctx context.Context) error {
	if agent.localStore == nil || agent.state.NodeCredential == "" {
		return nil
	}
	if err := agent.flushOperationResults(ctx); err != nil {
		return err
	}
	after, err := agent.localStore.lastOperationSequence(ctx)
	if err != nil {
		return err
	}
	var response protocol.NodeOperationsResponse
	status, err := agent.doJSON(
		ctx, http.MethodGet, "/agent/v1/operations?after="+strconv.FormatInt(after, 10),
		nil, cryptoutil.NewID(), true, &response,
	)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("operations poll returned status %d", status)
	}
	for _, operation := range response.Operations {
		if operation.Sequence <= after || operation.ID == "" {
			return errors.New("operation sequence is invalid or out of order")
		}
		result := agent.executeNodeOperation(ctx, operation)
		result.Sequence = operation.Sequence
		result.Output = nodeops.SanitizeOutput(
			result.Output, nodeops.MaxLogLines, nodeops.MaxOutputSize,
		)
		result.ErrorMessage = nodeops.SanitizeMessage(result.ErrorMessage, 512)
		if result.CompletedAt.IsZero() {
			result.CompletedAt = time.Now().UTC()
		}
		if err := agent.localStore.recordOperationResult(
			ctx, operation, result, time.Now().UTC(),
		); err != nil {
			return err
		}
		after = operation.Sequence
		if err := agent.flushOperationResults(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (agent *Agent) flushOperationResults(ctx context.Context) error {
	results, err := agent.localStore.listPendingOperationResults(ctx, 20)
	if err != nil {
		return err
	}
	for _, pending := range results {
		status, err := agent.doJSON(
			ctx, http.MethodPost,
			"/agent/v1/operations/"+pending.OperationID+"/result",
			pending.Result, cryptoutil.NewID(), true, nil,
		)
		if err != nil {
			_ = agent.localStore.recordOperationResultFailure(
				ctx, pending.OperationID, "transport_error", time.Now().UTC(),
			)
			return err
		}
		if status != http.StatusNoContent {
			_ = agent.localStore.recordOperationResultFailure(
				ctx, pending.OperationID, "invalid_response", time.Now().UTC(),
			)
			return fmt.Errorf("operation result returned status %d", status)
		}
		if err := agent.localStore.markOperationResultReported(
			ctx, pending.OperationID, time.Now().UTC(),
		); err != nil {
			return err
		}
	}
	return nil
}

func (agent *Agent) executeNodeOperation(
	ctx context.Context,
	operation protocol.NodeOperation,
) protocol.OperationResultRequest {
	agent.dataPlaneMu.Lock()
	defer agent.dataPlaneMu.Unlock()
	if agent.config.AdapterType == "sing_box_vless_reality" {
		agent.dataPlaneRevision++
		if operation.Type == "restart_core" {
			if _, _, err := agent.sampleRealityUsage(ctx, false); err != nil {
				agent.logger.Warn("pre-restart Reality usage sample failed", "error", err)
			}
		}
	}
	if agent.operationExecutor != nil {
		return agent.operationExecutor(ctx, operation)
	}
	return agent.executeNodeOperationWithHelper(ctx, operation)
}

func (agent *Agent) executeNodeOperationWithHelper(
	ctx context.Context,
	operation protocol.NodeOperation,
) protocol.OperationResultRequest {
	response, err := exchangeHelper(
		ctx,
		agent.config.OperationsSocketPath,
		45*time.Second,
		nodeops.HelperRequest{Operation: &operation},
	)
	if err != nil {
		errorCode := "operations_helper_unavailable"
		switch helperExchangeErrorStage(err) {
		case helperExchangeWrite:
			errorCode = "operations_helper_write_failed"
		case helperExchangeRead:
			errorCode = "operations_helper_read_failed"
		}
		return failedOperationResult(operation.Sequence, errorCode, err)
	}
	if response.Sequence != operation.Sequence ||
		(response.Status != "succeeded" && response.Status != "failed") ||
		response.CompletedAt.IsZero() {
		return failedOperationResult(
			operation.Sequence, "operations_helper_invalid_response",
			errors.New("helper returned invalid result fields"),
		)
	}
	return response.ProtocolResult()
}

func failedOperationResult(
	sequence int64,
	errorCode string,
	err error,
) protocol.OperationResultRequest {
	message := "operation failed"
	if err != nil {
		message = err.Error()
	}
	return protocol.OperationResultRequest{
		Sequence: sequence, Status: "failed", ErrorCode: errorCode,
		ErrorMessage: nodeops.SanitizeMessage(message, 512), CompletedAt: time.Now().UTC(),
	}
}
