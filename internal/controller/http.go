package controller

import (
	"context"
	"encoding/json"
	"errors"
	"hlcp/internal/protocol"
	"net/http"
	"time"
)

// HTTPHandler is intended only for the controller's local loopback API.
func (server *Server) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/nodes", func(writer http.ResponseWriter, request *http.Request) { writeJSON(writer, server.registry.List()) })
	mux.HandleFunc("GET /v1/nodes/{node}/resources", func(writer http.ResponseWriter, request *http.Request) {
		record, ok := server.registry.Get(request.PathValue("node"))
		if !ok {
			http.Error(writer, "node not found", http.StatusNotFound)
			return
		}
		writeJSON(writer, record.Inventory.Resources)
	})
	mux.HandleFunc("GET /v1/nodes/{node}/resources/{resource}/actions", func(writer http.ResponseWriter, request *http.Request) {
		record, ok := server.registry.Get(request.PathValue("node"))
		if !ok {
			http.Error(writer, "node not found", http.StatusNotFound)
			return
		}
		for _, resource := range record.Inventory.Resources {
			if resource.ID == request.PathValue("resource") {
				writeJSON(writer, resource.Actions)
				return
			}
		}
		http.Error(writer, "resource not found", http.StatusNotFound)
	})
	mux.HandleFunc("POST /v1/nodes/{node}/commands", func(writer http.ResponseWriter, request *http.Request) {
		var command protocol.CommandPayload
		if err := json.NewDecoder(request.Body).Decode(&command); err != nil {
			http.Error(writer, "invalid command", http.StatusBadRequest)
			return
		}
		if err := command.Validate(); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
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
			http.Error(writer, err.Error(), status)
			return
		}
		writeJSON(writer, result)
	})
	return mux
}
func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}
func contextWithTimeout(request *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(request.Context(), timeout)
}
