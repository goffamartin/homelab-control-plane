package main

import (
	"errors"
	"flag"
	"fmt"
	"hlcp/deploy/install"
	"hlcp/internal/releaseauth"
	"io"
)

func verifyReleaseCLI(args []string, out io.Writer) int {
	f := flag.NewFlagSet("verify-release", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	directory := f.String("directory", "", "private staging directory")
	version := f.String("version", "", "exact release version")
	role := f.String("role", "", "controller or agent")
	arch := f.String("arch", "", "amd64 or arm64")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(out, "Usage: hlcp verify-release --directory /private/staging --version vX.Y.Z --role controller|agent --arch amd64|arm64\nVerifies pinned Ed25519 manifest signature, metadata, compatibility and artifact digests.")
			return 0
		}
		return 2
	}
	if f.NArg() != 0 {
		return 2
	}
	name, err := releaseauth.VerifyDirectory(*directory, *version, *role, *arch, install.ReleasePublicKey)
	if err != nil {
		fmt.Fprintln(out, "Release verification refused: signature, version, architecture, compatibility or artifact mismatch.")
		return 1
	}
	fmt.Fprintln(out, name)
	return 0
}
