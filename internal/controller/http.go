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
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			localapi.Error(writer, http.StatusUnsupportedMediaType, "INVALID_CONTENT_TYPE", "application/json is required")
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, 8192)
		var command protocol.CommandPayload
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&command); err != nil {
			localapi.Error(writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid or oversized command")
			return
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			localapi.Error(writer, http.StatusBadRequest, "INVALID_REQUEST", "exactly one JSON command is required")
			return
		}
		if err := command.Validate(); err != nil {
			localapi.Error(writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
		if status, message := server.validateResourceCommand(request.PathValue("node"), command); status != 0 {
			code := "RESOURCE_NOT_FOUND"
			if message == "node not found" {
				code = "NODE_NOT_FOUND"
			}
			if status == http.StatusUnprocessableEntity {
				code = "ACTION_NOT_SUPPORTED"
			}
			localapi.Error(writer, status, code, message)
			return
		}
		requestContext, cancel := contextWithTimeout(request, 30*time.Second)
		defer cancel()
		result, err := server.sessions.Execute(requestContext, request.PathValue("node"), command)
		if err != nil {
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, ErrNodeNotConnected):
				status = http.StatusServiceUnavailable
			case errors.Is(err, context.DeadlineExceeded):
				status = http.StatusGatewayTimeout
			case errors.Is(err, context.Canceled):
				status = 499 // Client Closed Request (non-standard but widely understood).
			}
			code := "EXECUTION_ERROR"
			if status == http.StatusGatewayTimeout {
				code = "TIMEOUT"
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

func (server *Server) validateResourceCommand(nodeID string, command protocol.CommandPayload) (int, string) {
	record, ok := server.registry.Get(nodeID)
	if !ok {
		return http.StatusNotFound, "node not found"
	}
	for _, resource := range record.Inventory.Resources {
		if resource.ID != command.ResourceID {
			continue
		}
		for _, action := range resource.Actions {
			if action == command.Action {
				return 0, ""
			}
		}
		return http.StatusUnprocessableEntity, "action is not supported by resource"
	}
	return http.StatusNotFound, "resource not found"
}
func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}
func contextWithTimeout(request *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(request.Context(), timeout)
}
