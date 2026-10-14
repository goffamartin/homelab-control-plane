package transport

import (
	"crypto/tls"
	"os"
	"testing"
)

func TestClientSnapshotPolicyMatchesRuntime(t *testing.T) {
	_, files := writeTestCertificates(t)
	cert, err := os.ReadFile(files.CertificateFile)
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(files.PrivateKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := os.ReadFile(files.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	config, err := ClientTLSConfigFromPEM(cert, key, ca, files.ServerName)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := ClientTLSConfig(files)
	if err != nil {
		t.Fatal(err)
	}
	if config.MinVersion != tls.VersionTLS13 || config.MinVersion != runtime.MinVersion || config.ServerName != runtime.ServerName ||
		config.InsecureSkipVerify || config.VerifyConnection == nil || config.RootCAs == nil ||
		len(config.Certificates) != 1 || len(config.NextProtos) != 1 || config.NextProtos[0] != ALPN {
		t.Fatal("snapshot policy mismatch")
	}
	for _, row := range []struct {
		cert, key, ca []byte
		name          string
	}{
		{cert, key, ca, ""}, {[]byte("SECRET"), key, ca, files.ServerName},
		{cert, []byte("SECRET"), ca, files.ServerName}, {cert, key, []byte("SECRET"), files.ServerName},
	} {
		if _, err := ClientTLSConfigFromPEM(row.cert, row.key, row.ca, row.name); err == nil {
			t.Fatal("invalid snapshot accepted")
		}
	}
}
