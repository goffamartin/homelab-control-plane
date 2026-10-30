package controller

import (
	"encoding/json"
	"hlcp/internal/localapi"
	"hlcp/internal/protocol"
	"hlcp/internal/session"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTagAPIANDCatalogAndValidation(t *testing.T) {
	s := &Server{registry: NewRegistry()}
	s.registry.nodes["n"] = NodeRecord{NodeID: "n", Inventory: protocol.InventorySnapshot{Revision: 1, Resources: []protocol.ResourceDescriptor{
		{ID: "front", Type: "container", Actions: []string{"status"}, Tags: []string{"kostka", "production", "frontend"}},
		{ID: "back", Type: "container", Actions: []string{"status"}, Tags: []string{"kostka", "backend"}},
		{ID: "other", Type: "container", Actions: []string{"status"}, Tags: []string{"medaile"}},
		{ID: "untagged", Type: "container", Actions: []string{"status"}},
	}}}
	h := s.HTTPHandler(testAPIToken)
	for _, row := range []struct {
		path string
		code int
		want []string
	}{
		{"/resources", 200, []string{"front", "back", "other", "untagged"}},
		{"/resources?tag=kostka", 200, []string{"front", "back"}},
		{"/resources?tag=kostka&tag=production", 200, []string{"front"}},
		{"/resources?tag=missing", 200, []string{}},
		{"/tags", 200, []string{"backend", "frontend", "kostka", "medaile", "production"}},
		{"/tags?tag=production", 200, []string{"frontend", "kostka", "production"}},
		{"/resources?tag=", 400, nil}, {"/resources?tag=a&tag=a", 400, nil},
		{"/resources?tag=Production", 400, nil}, {"/resources?tag=a%20b", 400, nil},
		{"/resources?tag=%ZZ", 400, nil}, {"/resources?unknown=x", 400, nil},
		{"/tags?tag=" + strings.Repeat("a", 65), 400, nil},
		{"/tags?" + strings.Repeat("tag=a&", 400), 400, nil},
	} {
		t.Run(row.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, apiRequest("GET", "/v1/nodes/n"+row.path, ""))
			if w.Code != row.code {
				t.Fatal(w.Code, w.Body.String())
			}
			if row.code != 200 {
				if !strings.Contains(w.Body.String(), "INVALID_QUERY") {
					t.Fatal(w.Body.String())
				}
				return
			}
			got := []string{}
			if strings.HasPrefix(row.path, "/resources") {
				if w.Header().Get(localapi.ResourceTagsHeader) != "1" {
					t.Fatal("missing filter acknowledgement")
				}
				var resources []protocol.ResourceDescriptor
				if err := json.Unmarshal(w.Body.Bytes(), &resources); err != nil || resources == nil {
					t.Fatal("expected array", err)
				}
				for _, resource := range resources {
					got = append(got, resource.ID)
				}
			} else if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, row.want) {
				t.Fatal(got, row.want)
			}
		})
	}
}

func TestRegistryTagsAreDeepCopied(t *testing.T) {
	r := NewRegistry()
	sync := session.Synchronization{Inventory: protocol.InventorySnapshot{Revision: 1, Resources: []protocol.ResourceDescriptor{{ID: "app", Type: "container", Actions: []string{"status"}, Tags: []string{"production"}}}}}
	registered := r.Register(session.Registration{NodeID: "n", SessionID: "s"}, sync, time.Now())
	sync.Inventory.Resources[0].Tags[0] = "source-change"
	registered.Inventory.Resources[0].Tags[0] = "return-change"
	get, _ := r.Get("n")
	get.Inventory.Resources[0].Tags[0] = "get-change"
	list := r.List()
	list[0].Inventory.Resources[0].Tags[0] = "list-change"
	final, _ := r.Get("n")
	if final.Inventory.Resources[0].Tags[0] != "production" {
		t.Fatal("registry inventory tag alias")
	}
}
