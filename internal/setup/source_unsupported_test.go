//go:build !linux

package setup

import (
	"errors"
	"testing"
)

func TestNativePlanningUnsupportedPlatformFailsClosed(t *testing.T) {
	if _, err := OpenPlanner(t.TempDir(), Owner{}); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("err=%v", err)
	}
}
