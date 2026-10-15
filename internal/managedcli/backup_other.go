//go:build !linux

package managedcli

import "io"

func PackageBackup(args []string, out io.Writer) int {
	return resultError(out, has(args, "--json"), "package.backup", "platform", "LINUX_REQUIRED", "Package backup requires Linux. Nothing changed.", "Run on the actual Debian installation owner/host.")
}
