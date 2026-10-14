package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientProfileSharedSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.json")
	data := []byte(`{"version":1,"api":"http://127.0.0.1:9444","api_token_file":"cli.token"}`)
	p, err := ParseClientConfig(data, path)
	if err != nil || p.TokenFile != filepath.Join(filepath.Dir(path), "cli.token") || p.Version != 1 {
		t.Fatal(p, err)
	}
	absolute := filepath.Join(filepath.Dir(path), "existing.token")
	data, _ = json.Marshal(ClientConfig{1, "http://127.0.0.1:19444", absolute})
	p, err = ParseClientConfig(data, path)
	if err != nil || p.TokenFile != absolute {
		t.Fatal(p, err)
	}
}
func TestClientProfileStrictAndBounded(t *testing.T) {
	valid := `{"version":1,"api":"http://127.0.0.1:9444","api_token_file":"cli.token"}`
	for _, data := range []string{
		valid + ` {}`, valid + `{"SECRET":true}`, strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(valid, `"version":1`, `"Version":1`, 1), strings.Replace(valid, `"version":1`, `"version":2`, 1),
		strings.Replace(valid, `"cli.token"`, `null`, 1), strings.Replace(valid, `"cli.token"`, `""`, 1),
		strings.Replace(valid, `127.0.0.1`, `192.168.0.15`, 1), strings.Repeat("x", 8193), `[]`, `null`,
	} {
		if _, err := ParseClientConfig([]byte(data), "cli.json"); err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatal("invalid profile accepted or echoed")
		}
	}
}
