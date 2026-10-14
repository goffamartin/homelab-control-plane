package localapi

import (
	"strings"
	"testing"
)

func TestValidatedPolicyRoleForExport(t *testing.T) {
	operator := strings.Repeat("a", 64)
	observer := strings.Repeat("b", 64)
	p, err := ParsePolicy([]byte(`{"version":1,"principals":[{"id":"owner","role":"operator","token_file":"op","scope":{"all_nodes":true}},{"id":"reader","role":"observer","token_file":"obs","scope":{"all_nodes":true}}]}`), func(name string) (string, error) {
		if name == "op" {
			return operator, nil
		}
		return observer, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for token, role := range map[string]string{operator: "operator", observer: "observer"} {
		if actual, err := p.RoleForToken(token); err != nil || actual != role {
			t.Fatal(actual, err)
		}
	}
	for _, token := range []string{"short", strings.Repeat("c", 64)} {
		if _, err := p.RoleForToken(token); err == nil {
			t.Fatal("unauthorized export")
		}
	}
	var empty *Policy
	if _, err := empty.RoleForToken(operator); err == nil {
		t.Fatal("nil policy authorized")
	}
}
