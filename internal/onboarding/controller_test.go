package onboarding

import (
	"crypto/x509"
	"encoding/json"
	"fmt"
	"hlcp/internal/localapi"
	"strings"
	"testing"
)

func controllerMaterial(t *testing.T) ImportedTLS {
	t.Helper()
	return leafMaterial(t, testCA(t, "server issuer"), testCA(t, "client trust"),
		x509.Certificate{DNSNames: []string{"controller.test"}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
}
func controllerCredentials(t *testing.T) ControllerCredentials {
	t.Helper()
	result, err := NewControllerCredentials([]byte(strings.Repeat("a", 64)), []byte(strings.Repeat("b", 64)))
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestControllerCredentialsAndEnvBoundaries(t *testing.T) {
	for _, row := range []struct{ a, b string }{
		{"short", strings.Repeat("b", 64)},
		{strings.Repeat("a", 64), strings.Repeat("a", 64) + "\n"},
		{strings.Repeat("a", 64), "bad token with spaces"},
	} {
		if _, err := NewControllerCredentials([]byte(row.a), []byte(row.b)); err == nil {
			t.Fatal("invalid tokens accepted")
		}
	}
	credentials := controllerCredentials(t)
	data, _ := json.Marshal(credentials)
	for _, report := range []string{string(data), fmt.Sprintf("%+v %#v", credentials, &credentials)} {
		if strings.Contains(report, strings.Repeat("a", 32)) || strings.Contains(report, strings.Repeat("b", 32)) {
			t.Fatal("token leaked")
		}
	}
	for _, good := range []string{"HLCP_AGENT_LISTEN=192.168.0.15:9443\n", "# comment\nHLCP_AGENT_LISTEN=[::1]:9443\n"} {
		if validateControllerEnv([]byte(good)) != nil {
			t.Fatal(good)
		}
	}
	for _, bad := range []string{"HLCP_AGENT_LISTEN=0.0.0.0:9443", "HLCP_AGENT_LISTEN=:9443",
		"HLCP_AGENT_LISTEN=192.168.0.15:22", "HLCP_AGENT_LISTEN=127.0.0.1:99999",
		"HLCP_AGENT_LISTEN=127.0.0.1:9443\nLD_PRELOAD=SECRET",
		"HLCP_AGENT_LISTEN=127.0.0.1:9443\nHLCP_AGENT_LISTEN=127.0.0.1:9444"} {
		if validateControllerEnv([]byte(bad)) == nil {
			t.Fatal("invalid env accepted")
		}
	}
	policy, err := localapi.ParsePolicy([]byte(controllerPolicy), func(name string) (string, error) {
		if name == "api.token" {
			return localapi.ParseToken(credentials.operator)
		}
		return localapi.ParseToken(credentials.observer)
	})
	if err != nil || policy == nil {
		t.Fatal(err)
	}
}
