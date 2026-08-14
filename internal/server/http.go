package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Sen62455/PolyFleet/internal/protocol"
)

func decodeJSON(response http.ResponseWriter, request *http.Request, destination any, limit int64) error {
	request.Body = http.MaxBytesReader(response, request.Body, limit)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON object")
	}
	return nil
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func (a *App) writeError(
	response http.ResponseWriter,
	request *http.Request,
	status int,
	code, message string,
) {
	writeJSON(response, status, protocol.ErrorResponse{Error: protocol.APIError{
		Code:      code,
		Message:   message,
		RequestID: requestIDFromContext(request.Context()),
	}})
}
