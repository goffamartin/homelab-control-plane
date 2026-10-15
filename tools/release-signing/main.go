// Explicit local signing-key provisioning. Never generates keys during builds.
package main

import (
	"flag"
	"fmt"
	"hlcp/internal/releaseauth"
	"os"
)

func main() {
	private := flag.String("private", "", "new private key path; never overwrite")
	public := flag.String("public", "", "new public key path; never overwrite")
	flag.Parse()
	if flag.NArg() != 0 || *private == "" || *public == "" || *private == *public {
		fmt.Fprintln(os.Stderr, "Usage: release-signing -private /private/signing.key -public /path/release.pub")
		os.Exit(2)
	}
	priv, pub, err := releaseauth.Generate()
	if err != nil {
		os.Exit(1)
	}
	defer clear(priv)
	for _, file := range []struct {
		path string
		data []byte
	}{{*private, priv}, {*public, pub}} {
		f, e := os.OpenFile(file.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			fmt.Fprintln(os.Stderr, "Refusing existing or unavailable key destination; inspect partial provisioning.")
			os.Exit(1)
		}
		_, e = f.Write(file.data)
		closeErr := f.Close()
		if e != nil || closeErr != nil {
			os.Exit(1)
		}
	}
	fmt.Println("Release signing key provisioned. Back it up securely; never commit or upload the private key.")
}
