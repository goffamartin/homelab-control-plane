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
	server.registry.nodes["debian-01"] = NodeRecord{Inventory: protocol.InventorySnapshot{Resources: []protocol.ResourceDescriptor{
		{ID: "example-catalog", Type: "container", Actions: []string{"status", "restart"}},
	}}}

	for _, test := range []struct {
		path       string
		wantStatus int
		wantBody   string
	}{
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
