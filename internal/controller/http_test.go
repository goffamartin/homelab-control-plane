package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hlcp/internal/protocol"
)

func TestHTTPHandlerListsResourcesAndActions(t *testing.T) {
	server := &Server{registry: NewRegistry()}
	server.registry.nodes["debian-01"] = NodeRecord{NodeID: "debian-01", Inventory: protocol.InventorySnapshot{Resources: []protocol.ResourceDescriptor{
		{ID: "example-catalog", Type: "container", Actions: []string{"status", "restart"}},
	}}}

	for _, test := range []struct {
		path       string
		wantStatus int
		wantBody   string
	}{
		{path: "/v1/nodes/debian-01", wantStatus: http.StatusOK, wantBody: `"debian-01"`},
		{path: "/v1/nodes/debian-01/actions", wantStatus: http.StatusOK, wantBody: `"status"`},
		{path: "/v1/nodes/debian-01/resources", wantStatus: http.StatusOK, wantBody: `"example-catalog"`},
		{path: "/v1/nodes/debian-01/resources/example-catalog/actions", wantStatus: http.StatusOK, wantBody: `"restart"`},
		{path: "/v1/nodes/debian-01/resources/missing/actions", wantStatus: http.StatusNotFound, wantBody: "resource not found"},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.HTTPHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if body := response.Body.String(); !strings.Contains(body, test.wantBody) {
				t.Fatalf("body = %q, expected %q", body, test.wantBody)
			}
		})
	}
}

func TestHTTPHandlerRejectsUnknownResourceCommand(t *testing.T) {
	server := &Server{registry: NewRegistry()}
	server.registry.nodes["debian-01"] = NodeRecord{NodeID: "debian-01", Inventory: protocol.InventorySnapshot{Resources: []protocol.ResourceDescriptor{
		{ID: "example-catalog", Type: "container", Actions: []string{"status", "restart"}},
	}}}

	request := httptest.NewRequest(http.MethodPost, "/v1/nodes/debian-01/commands", strings.NewReader(`{"command_id":"cmd-1","resource_id":"missing","action":"restart"}`))
	response := httptest.NewRecorder()
	server.HTTPHandler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "resource not found") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestHTTPHandlerRejectsUnsupportedResourceAction(t *testing.T) {
	server := &Server{registry: NewRegistry()}
	server.registry.nodes["debian-01"] = NodeRecord{NodeID: "debian-01", Inventory: protocol.InventorySnapshot{Resources: []protocol.ResourceDescriptor{
		{ID: "example-catalog", Type: "container", Actions: []string{"status", "restart"}},
	}}}

	request := httptest.NewRequest(http.MethodPost, "/v1/nodes/debian-01/commands", strings.NewReader(`{"command_id":"cmd-1","resource_id":"example-catalog","action":"remove"}`))
	response := httptest.NewRecorder()
	server.HTTPHandler().ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "action is not supported") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}
