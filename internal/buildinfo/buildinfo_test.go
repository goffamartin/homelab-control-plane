package buildinfo

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildInfo(t *testing.T) {
	info := Current()
	if info.Version != Version || info.Revision != Revision || !strings.Contains(String(), info.GoVersion) || !strings.Contains(String(), info.Platform) {
		t.Fatalf("info=%+v", info)
	}
	data, err := json.Marshal(info)
	if err != nil || !json.Valid(data) {
		t.Fatal("invalid JSON build metadata")
	}
}
