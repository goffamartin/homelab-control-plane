package managedcli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"strconv"
	"strings"
)

func checkLinger(ctxContext context.Context) error {
	data, err := fixedProcess(ctxContext, "/usr/bin/loginctl", []string{"show-user", strconv.Itoa(os.Geteuid()), "--property=Linger", "--value"}, "")
	if err != nil || strings.TrimSpace(string(data)) != "yes" {
		return ErrSetup
	}
	return nil
}
func PrepareUser(args []string, out io.Writer) int {
	f := flag.NewFlagSet("prepare-user", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	name := f.String("user", "", "explicit local non-root user")
	apply := f.Bool("apply", false, "enable linger")
	yes := f.Bool("yes", false, "confirm")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(out, "Usage: sudo hlcp-agent prepare-user --user ACCOUNT [--apply --yes]\nEnables the existing user's systemd manager at boot. Does not create keys, containers or impersonate the user.")
			return 0
		}
		return 2
	}
	if f.NArg() != 0 || *apply != *yes || os.Geteuid() != 0 || *name == "" {
		return 2
	}
	u, err := user.Lookup(*name)
	if err != nil || u.Uid == "0" || u.HomeDir == "/nonexistent" || u.Username != *name {
		return 1
	}
	if !*apply {
		fmt.Fprintln(out, "Read-only preview: enable linger for", *name)
		return 0
	}
	ctx, cancel := boundedContext()
	defer cancel()
	if _, err = fixedProcess(ctx, "/usr/bin/loginctl", []string{"enable-linger", *name}, ""); err != nil {
		fmt.Fprintln(out, "Linger provisioning failed; inspect loginctl.")
		return 1
	}
	data, err := fixedProcess(ctx, "/usr/bin/loginctl", []string{"show-user", *name, "--property=Linger", "--value"}, "")
	if err != nil || strings.TrimSpace(string(data)) != "yes" {
		return 1
	}
	fmt.Fprintln(out, "User manager enabled at boot:", *name)
	return 0
}
