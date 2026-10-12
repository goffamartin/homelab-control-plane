package controller

import (
	"context"
	"encoding/json"
	"errors"
	"hlcp/internal/localapi"
	"hlcp/internal/protocol"
	"io"
	"mime"
	"net/http"
	"time"
)

// HTTPHandler is intended only for the controller's local loopback API.
func (server *Server) HTTPHandler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/nodes/{node}/resolve", func(writer http.ResponseWriter, request *http.Request) {
		payload, ok := decodeCommand(writer, request)
		if !ok {
			return
		}
		plan, err := server.Resolve(request.PathValue("node"), payload)
		if err != nil {
			localapi.Error(writer, err.Status, err.Code, err.Message)
			return
		}
		writeJSON(writer, plan)
	})
	mux.HandleFunc("GET /v1/nodes/{node}/resources/{resource}", func(writer http.ResponseWriter, request *http.Request) {
		detail, err := server.ResourceDetail(request.PathValue("node"), request.PathValue("resource"))
		if err != nil {
			localapi.Error(writer, err.Status, err.Code, err.Message)
			return
		}
		writeJSON(writer, detail)
	})
	mux.HandleFunc("GET /v1/nodes/{node}/commands/{command}", func(writer http.ResponseWriter, request *http.Request) {
		operation, exists, err := server.sessions.tracker.Operation(request.PathValue("node"), request.PathValue("command"))
		if err != nil {
			localapi.Error(writer, http.StatusServiceUnavailable, "LEDGER_FAILURE", err.Error())
			return
		}
		if !exists {
			localapi.Error(writer, http.StatusNotFound, "COMMAND_NOT_FOUND", "command not found")
			return
		}
		writeJSON(writer, operation)
	})
	mux.HandleFunc("GET /v1/nodes", func(writer http.ResponseWriter, request *http.Request) { writeJSON(writer, server.registry.List()) })
	mux.HandleFunc("GET /v1/nodes/{node}", func(writer http.ResponseWriter, request *http.Request) {
		record, ok := server.registry.Get(request.PathValue("node"))
		if !ok {
			localapi.Error(writer, http.StatusNotFound, "NODE_NOT_FOUND", "node not found")
			return
		}
		writeJSON(writer, record)
	})
	mux.HandleFunc("GET /v1/nodes/{node}/actions", func(writer http.ResponseWriter, request *http.Request) {
		if _, ok := server.registry.Get(request.PathValue("node")); !ok {
			localapi.Error(writer, http.StatusNotFound, "NODE_NOT_FOUND", "node not found")
			return
		}
		writeJSON(writer, []string{"status"})
	})
	mux.HandleFunc("GET /v1/nodes/{node}/resources", func(writer http.ResponseWriter, request *http.Request) {
		record, ok := server.registry.Get(request.PathValue("node"))
		if !ok {
			localapi.Error(writer, http.StatusNotFound, "NODE_NOT_FOUND", "node not found")
			return
		}
		writeJSON(writer, record.Inventory.Resources)
	})
	mux.HandleFunc("GET /v1/nodes/{node}/resources/{resource}/actions", func(writer http.ResponseWriter, request *http.Request) {
		record, ok := server.registry.Get(request.PathValue("node"))
		if !ok {
			localapi.Error(writer, http.StatusNotFound, "NODE_NOT_FOUND", "node not found")
			return
		}
		for _, resource := range record.Inventory.Resources {
			if resource.ID == request.PathValue("resource") {
				writeJSON(writer, resource.Actions)
				return
			}
		}
		localapi.Error(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", "resource not found")
	})
	mux.HandleFunc("POST /v1/nodes/{node}/commands", func(writer http.ResponseWriter, request *http.Request) {
		command, ok := decodeCommand(writer, request)
		if !ok {
			return
		}
		if err := command.Validate(); err != nil {
			localapi.Error(writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
		if _, err := server.Resolve(request.PathValue("node"), command); err != nil {
			localapi.Error(writer, err.Status, err.Code, err.Message)
			return
		}
		requestContext, cancel := contextWithTimeout(request, server.sessions.policy.ResultTimeout+time.Second)
		defer cancel()
		result, err := server.sessions.Execute(requestContext, request.PathValue("node"), command)
		if err != nil {
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, ErrNodeNotConnected):
				status = http.StatusServiceUnavailable
			case errors.Is(err, context.DeadlineExceeded):
				status = http.StatusGatewayTimeout
			case errors.Is(err, ErrACKTimeout), errors.Is(err, ErrResultTimeout):
				status = http.StatusGatewayTimeout
			case errors.Is(err, ErrCommandAlreadyTracked):
				status = http.StatusConflict
			case errors.Is(err, ErrCommandConflict):
				status = http.StatusConflict
			case errors.Is(err, ErrLedgerFailure), errors.Is(err, ErrLedgerFull):
				status = http.StatusServiceUnavailable
			case errors.Is(err, ErrCommandCapacity):
				status = http.StatusTooManyRequests
			case errors.Is(err, context.Canceled):
				status = 499 // Client Closed Request (non-standard but widely understood).
			}
			code := "EXECUTION_ERROR"
			if status == http.StatusGatewayTimeout {
				code = "TIMEOUT"
			}
			if errors.Is(err, ErrACKTimeout) {
				code = "ACK_TIMEOUT"
			}
			if errors.Is(err, ErrResultTimeout) {
				code = "RESULT_TIMEOUT"
			}
			if errors.Is(err, ErrCommandAlreadyTracked) {
				code = "COMMAND_ALREADY_TRACKED"
			}
			if errors.Is(err, ErrCommandCapacity) {
				code = "COMMAND_CAPACITY"
			}
			if errors.Is(err, ErrCommandConflict) {
				code = "COMMAND_CONFLICT"
			}
			if errors.Is(err, ErrLedgerFull) {
				code = "LEDGER_FULL"
			}
			if errors.Is(err, ErrLedgerFailure) {
				code = "LEDGER_FAILURE"
			}
			if status == 499 {
				code = "CANCELED"
			}
			localapi.Error(writer, status, code, err.Error())
			return
		}
		writeJSON(writer, result)
	})
	return localapi.Protect(token, mux)
}

func decodeCommand(writer http.ResponseWriter, request *http.Request) (protocol.CommandPayload, bool) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		localapi.Error(writer, http.StatusUnsupportedMediaType, "INVALID_CONTENT_TYPE", "application/json is required")
		return protocol.CommandPayload{}, false
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 8192)
	var command protocol.CommandPayload
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil {
		localapi.Error(writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid or oversized command")
		return command, false
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		localapi.Error(writer, http.StatusBadRequest, "INVALID_REQUEST", "exactly one JSON command is required")
		return command, false
	}
	return command, true
}
func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}
func contextWithTimeout(request *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(request.Context(), timeout)
}
