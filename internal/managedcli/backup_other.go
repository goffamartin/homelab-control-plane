//go:build !linux

package managedcli

import "io"

func PackageBackup([]string, io.Writer) int { return 1 }
