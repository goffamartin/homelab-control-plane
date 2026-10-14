package localapi

import (
	"errors"
	"strings"
	"testing"
)

func TestPolicySnapshotRejectsDuplicateAndInvalidCredential(t *testing.T) {
	good := `{"version":1,"principals":[{"id":"operator","role":"operator","token_file":"api.token","scope":{"all_nodes":true}}]}`
	resolve := func(name string) (string, error) {
		if name != "api.token" {
			return "", errors.New("SECRET")
		}
		return strings.Repeat("a", 64), nil
	}
	if _, err := ParsePolicy([]byte(good), resolve); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(good, `"version":1`, `"version":1,"version":1`, 1), good + "{}", strings.Repeat("x", maxPolicyBytes+1)} {
		if _, err := ParsePolicy([]byte(bad), resolve); err == nil {
			t.Fatal("invalid snapshot")
		}
	}
	for _, resolve := range []func(string) (string, error){nil, func(string) (string, error) { return "short", nil }, func(string) (string, error) { return "", errors.New("SECRET") }} {
		if _, err := ParsePolicy([]byte(good), resolve); err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatal("credential validation/leak")
		}
	}
	for _, bad := range []string{"short", strings.Repeat("x", 258), strings.Repeat("x", 32) + " extra", strings.Repeat("x", 32) + "\n\n"} {
		if _, err := ParseToken([]byte(bad)); err == nil {
			t.Fatal("invalid token")
		}
	}
}
