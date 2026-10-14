package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"hlcp/internal/cli"
	"hlcp/internal/setup"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientCredentialPrivateAndCopied(t *testing.T) {
	input := []byte(strings.Repeat("a", 64))
	c, err := NewClientCredential(input)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = 'b'
	if c.token[0] != 'a' {
		t.Fatal("caller can mutate credential")
	}
	data, _ := json.Marshal(c)
	for _, out := range []string{string(data), fmt.Sprintf("%v", c), fmt.Sprintf("%#v", c)} {
		if strings.Contains(out, strings.Repeat("a", 32)) {
			t.Fatal("credential leaked")
		}
	}
	for _, token := range []string{"short", strings.Repeat("a", 257), strings.Repeat("a", 32) + "\x00"} {
		if _, err := NewClientCredential([]byte(token)); err == nil {
			t.Fatal("invalid bearer accepted")
		}
	}
}
func TestClientIntentsSharedRuntimeAndNoAuthorityChanges(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewClientCredential([]byte(strings.Repeat("a", 64)))
	intents, err := clientIntents(dir, "http://127.0.0.1:9444", c)
	if err != nil || len(intents) != 2 {
		t.Fatal(err)
	}
	for _, intent := range intents {
		if intent.Mode != 0600 || intent.AllowReplace || intent.Validate(intent.Data) != nil {
			t.Fatal("unsafe intent")
		}
	}
	p, err := cli.ParseClientConfig(intents[0].Data, filepath.Join(dir, "cli.json"))
	if err != nil || p.TokenFile != filepath.Join(dir, "cli.token") {
		t.Fatal(p, err)
	}
	for _, api := range []string{"http://192.168.0.15:9444", "https://127.0.0.1:9444", "invalid"} {
		if _, err := clientIntents(dir, api, c); err == nil {
			t.Fatal("unsafe endpoint accepted")
		}
	}
	if _, err := clientIntents(dir, "http://127.0.0.1:9444", ClientCredential{}); err == nil {
		t.Fatal("empty token accepted")
	}
	if _, err := PlanClient(context.Background(), dir, setup.Owner{}, "http://127.0.0.1:9444", c); err == nil {
		t.Fatal("root/unsupported profile allowed")
	}
}
