//go:build !linux

package managedcli

import (
	"io"
	"os"
)

func isTerminal(io.Reader) bool                          { return false }
func hideEcho(io.Reader) (func(), error)                 { return func() {}, nil }
func checkDirectory(string, os.FileInfo, int, int) error { return ErrSetup }
func safeParent(string, int) error                       { return ErrSetup }
