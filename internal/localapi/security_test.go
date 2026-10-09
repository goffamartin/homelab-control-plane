package localapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalBoundaryRejectsRemoteBindAndBrowserAccess(t *testing.T) {
	for _, value := range []string{":9444", "0.0.0.0:9444", "localhost:9444", "192.168.1.2:9444"} {
		if ValidateAddress(value) == nil {
			t.Fatalf("accepted bind %q", value)
		}
	}
	for _, value := range []string{"127.0.0.1:9444", "[::1]:9444"} {
		if err := ValidateAddress(value); err != nil {
			t.Fatal(err)
		}
	}
	token := "01234567890123456789012345678901"
	called := 0
	handler := Protect(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called++; w.WriteHeader(204) }))
	for _, scenario := range []struct {
		host, auth, origin string
		status             int
	}{
		{"127.0.0.1:9444", "Bearer " + token, "", 204},
		{"127.0.0.1:9444", "", "", 401},
		{"127.0.0.1:9444", "Bearer wrong", "", 401},
		{"evil.test:9444", "Bearer " + token, "", 403},
		{"127.0.0.1:9444", "Bearer " + token, "https://evil.test", 403},
	} {
		request := httptest.NewRequest("GET", "http://"+scenario.host+"/v1/nodes", nil)
		request.Header.Set("Authorization", scenario.auth)
		request.Header.Set("Origin", scenario.origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != scenario.status {
			t.Fatalf("status=%d want=%d", response.Code, scenario.status)
		}
	}
	if called != 1 {
		t.Fatalf("unauthorized handler calls: %d", called)
	}
}
