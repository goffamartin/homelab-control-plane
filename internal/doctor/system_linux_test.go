package doctor

import (
	"strings"
	"testing"
)

func TestBoundedNativeProbe(t *testing.T) {
	// Fixed local test utility only; production query entrypoints do not accept
	// executable paths or argv. No scripts or runtime commands are tested here.
	if _, err := runProbe("/usr/bin/printf", []string{strings.Repeat("x", maximumProbeOutput+1)}, []string{"LANG=C"}); err == nil {
		t.Fatal("overflow accepted")
	}
	if _, err := runProbe("/usr/bin/sleep", []string{"5"}, []string{"LANG=C"}); err == nil {
		t.Fatal("timeout accepted")
	}
	if _, err := (NativeHost{}).Properties(Query{Kind: "restart", Unit: "hlcp-agent.service"}); err == nil {
		t.Fatal("mutation query accepted")
	}
	if _, err := (NativeHost{}).Properties(Query{Kind: "unit", Scope: "system", Unit: "SECRET.service"}); err == nil {
		t.Fatal("arbitrary unit accepted")
	}
}
