package controller

import (
	"context"
	"encoding/json"
	"errors"
	"hlcp/internal/localapi"
	"hlcp/internal/protocol"
	"hlcp/internal/telemetry"
	"io"
	"mime"
	"net/http"
	"time"
)

// HTTPHandler is intended only for the controller's local loopback API.
func (server *Server) HTTPHandler(token string) http.Handler {
	return server.HTTPHandlerWithAuthorizer(localapi.LegacyAuthorizer(token))
}

func (server *Server) HTTPHandlerWithAuthorizer(authorizer *localapi.Authorizer) http.Handler {
	mux := http.NewServeMux()
	handle := func(pattern string, handler http.HandlerFunc) {
		mux.HandleFunc(pattern, func(writer http.ResponseWriter, request *http.Request) {
			if node := request.PathValue("node"); node != "" && !localapi.Allowed(request.Context(), node, request.PathValue("resource"), false) {
				forbidden(writer)
				return
			}
			handler(writer, request)
		})
	}
	handle("POST /v1/nodes/{node}/resolve", func(writer http.ResponseWriter, request *http.Request) {
		payload, ok := decodeCommand(writer, request)
		if !ok {
			return
		}
		if !localapi.Allowed(request.Context(), request.PathValue("node"), payload.ResourceID, false) {
			forbidden(writer)
			return
		}
		if !server.visibleCommand(writer, request, payload.CommandID) {
			return
		}
		plan, err := server.Resolve(request.PathValue("node"), payload)
		if err != nil {
			localapi.Error(writer, err.Status, err.Code, err.Message)
			return
		}
		writeJSON(writer, plan)
	})
	handle("GET /v1/nodes/{node}/resources/{resource}", func(writer http.ResponseWriter, request *http.Request) {
		detail, err := server.ResourceDetail(request.PathValue("node"), request.PathValue("resource"))
		if err != nil {
			localapi.Error(writer, err.Status, err.Code, err.Message)
			return
		}
		writeJSON(writer, detail)
	})
	handle("GET /v1/nodes/{node}/commands/{command}", func(writer http.ResponseWriter, request *http.Request) {
		operation, exists, err := server.sessions.tracker.Operation(request.PathValue("node"), request.PathValue("command"))
		if err != nil {
			localapi.Error(writer, http.StatusServiceUnavailable, "LEDGER_FAILURE", err.Error())
			return
		}
		if !exists || !localapi.Allowed(request.Context(), request.PathValue("node"), operation.Command.ResourceID, false) {
			localapi.Error(writer, http.StatusNotFound, "COMMAND_NOT_FOUND", "command not found")
			return
		}
		writeJSON(writer, operation)
	})
	handle("GET /v1/nodes", func(writer http.ResponseWriter, request *http.Request) {
		records := make([]NodeRecord, 0)
		for _, record := range server.registry.List() {
			if localapi.Allowed(request.Context(), record.NodeID, "", false) {
				records = append(records, scopedRecord(request.Context(), record))
			}
		}
		writeJSON(writer, records)
	})
	handle("GET /v1/nodes/{node}", func(writer http.ResponseWriter, request *http.Request) {
		record, ok := server.registry.Get(request.PathValue("node"))
		if !ok {
			localapi.Error(writer, http.StatusNotFound, "NODE_NOT_FOUND", "node not found")
			return
		}
		writeJSON(writer, scopedRecord(request.Context(), record))
	})
	handle("GET /v1/nodes/{node}/actions", func(writer http.ResponseWriter, request *http.Request) {
		if _, ok := server.registry.Get(request.PathValue("node")); !ok {
			localapi.Error(writer, http.StatusNotFound, "NODE_NOT_FOUND", "node not found")
			return
		}
		writeJSON(writer, []string{"status"})
	})
	handle("GET /v1/nodes/{node}/resources", func(writer http.ResponseWriter, request *http.Request) {
		record, ok := server.registry.Get(request.PathValue("node"))
		if !ok {
			localapi.Error(writer, http.StatusNotFound, "NODE_NOT_FOUND", "node not found")
			return
		}
		writeJSON(writer, scopedRecord(request.Context(), record).Inventory.Resources)
	})
	handle("GET /v1/nodes/{node}/resources/{resource}/actions", func(writer http.ResponseWriter, request *http.Request) {
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
	handle("POST /v1/nodes/{node}/commands", func(writer http.ResponseWriter, request *http.Request) {
		if !localapi.Allowed(request.Context(), request.PathValue("node"), "", true) {
			forbidden(writer)
			return
		}
		command, ok := decodeCommand(writer, request)
		if !ok {
			return
		}
		if err := command.Validate(); err != nil {
			localapi.Error(writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
		if !localapi.Allowed(request.Context(), request.PathValue("node"), command.ResourceID, true) {
			forbidden(writer)
			return
		}
		if !server.visibleCommand(writer, request, command.CommandID) {
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
			case errors.Is(err, localapi.ErrForbidden):
				status = http.StatusForbidden
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
			if errors.Is(err, localapi.ErrForbidden) {
				code = "FORBIDDEN"
			}
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
	audited := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		response := &auditResponse{ResponseWriter: writer}
		actor := localapi.Identity(request.Context())
		mux.ServeHTTP(response, request.WithContext(telemetry.WithActor(request.Context(), actor)))
		if response.status >= 400 {
			server.config.Telemetry.Record(telemetry.Event{Kind: telemetry.APIRejected, ActorID: actor, Code: "REQUEST_REJECTED"})
		}
	})
	return localapi.ProtectPolicy(authorizer, audited, func(code, actor string, allowed bool) {
		kind := telemetry.APIRejected
		if allowed {
			kind = telemetry.Authenticated
		}
		server.config.Telemetry.Record(telemetry.Event{Kind: kind, ActorID: actor, Code: code})
	})
}

func forbidden(writer http.ResponseWriter) {
	localapi.Error(writer, http.StatusForbidden, "FORBIDDEN", "local API permission denied")
}

func scopedRecord(ctx context.Context, record NodeRecord) NodeRecord {
	resources := make([]protocol.ResourceDescriptor, 0)
	states := make([]protocol.ResourceState, 0)
	for _, resource := range record.Inventory.Resources {
		if localapi.Allowed(ctx, record.NodeID, resource.ID, false) {
			resources = append(resources, resource)
		}
	}
	for _, state := range record.Resources.Resources {
		if localapi.Allowed(ctx, record.NodeID, state.ResourceID, false) {
			states = append(states, state)
		}
	}
	record.Inventory.Resources, record.Resources.Resources = resources, states
	return record
}

// Prevent a permitted resource payload from probing another resource's operation
// through the shared same-ID resolver. Missing and hidden IDs share one response.
func (server *Server) visibleCommand(writer http.ResponseWriter, request *http.Request, id string) bool {
	if id == "" || server.sessions == nil {
		return true
	}
	operation, exists, err := server.sessions.tracker.Operation(request.PathValue("node"), id)
	if err == nil && exists && !localapi.Allowed(request.Context(), request.PathValue("node"), operation.Command.ResourceID, false) {
		localapi.Error(writer, http.StatusNotFound, "COMMAND_NOT_FOUND", "command not found")
		return false
	}
	return true
}

type auditResponse struct {
	http.ResponseWriter
	status int
}

func (writer *auditResponse) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
	writer.ResponseWriter.WriteHeader(status)
}
func (writer *auditResponse) Write(data []byte) (int, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	return writer.ResponseWriter.Write(data)
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
