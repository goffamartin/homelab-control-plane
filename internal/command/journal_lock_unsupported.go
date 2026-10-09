//go:build !linux && !windows

package command

import (
	"errors"
	"os"
)

func acquireJournalLock(string) (*os.File, error) {
	return nil, errors.New("journal platform is unsupported")
}
func syncJournalDirectory(string) error { return errors.New("journal platform is unsupported") }
