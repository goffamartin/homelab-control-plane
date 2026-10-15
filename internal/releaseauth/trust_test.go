package releaseauth

import (
	"bytes"
	"hlcp/deploy/install"
	"os"
	"testing"
)

func TestInstallerAndBinaryPinSamePublicTrust(t *testing.T) {
	data, err := os.ReadFile("../../deploy/install/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	normalize := func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }
	if !bytes.Contains(normalize(data), normalize(install.ReleasePublicKey)) {
		t.Fatal("installer trust differs from binary")
	}
	if _, err := publicKey(install.ReleasePublicKey); err != nil {
		t.Fatal(err)
	}
}
