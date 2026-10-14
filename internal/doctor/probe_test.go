package doctor

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"hlcp/internal/controller"
	"hlcp/internal/liveness"
	"hlcp/internal/session"
	"math/big"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

type probeFixture struct {
	*fakeHost
	calls int
	err   error
}

func (h *probeFixture) ProbeTLS(ctx context.Context, endpoint string, config *tls.Config) error {
	h.calls++
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > probeTimeout || time.Until(deadline) < probeTimeout-time.Second ||
		config.InsecureSkipVerify || config.MinVersion != tls.VersionTLS13 || config.ServerName != "controller.test" ||
		len(config.NextProtos) != 1 || config.NextProtos[0] != "hlcp/1" || config.VerifyConnection == nil ||
		config.RootCAs == nil || len(config.Certificates) != 1 {
		return errors.New("SECRET invalid probe contract")
	}
	return h.err
}

func TestProbeOptInPreflightAndRedaction(t *testing.T) {
	h := &probeFixture{fakeHost: fixture(t, true, testNow.AddDate(1, 0, 0))}
	h.files["/config"] = []byte(minimalAgent)
	var out bytes.Buffer
	if runCLI([]string{"--config", "/config", "--json"}, "agent", &out, h, testNow) != 0 || h.calls != 0 {
		t.Fatal("implicit probe")
	}
	out.Reset()
	if runCLI([]string{"--config", "/config", "--probe", "--json"}, "agent", &out, h, testNow) != 0 || h.calls != 1 ||
		!strings.Contains(out.String(), `"offline":false`) {
		t.Fatalf("probe: %s", out.String())
	}
	h.err = errors.New("SECRET remote error")
	out.Reset()
	if runCLI([]string{"--config", "/config", "--probe", "--json"}, "agent", &out, h, testNow) != 1 ||
		strings.Contains(out.String(), "SECRET") {
		t.Fatal("error leak/exit")
	}
	h.calls = 0
	h.files["/key"] = []byte("SECRET invalid key")
	report := Run(h, Options{Role: "agent", Config: "/config", Probe: true}, testNow)
	if h.calls != 0 || !report.Offline || stateFor(report, "tls.peer") != "skip" {
		t.Fatal("failed preflight dialed")
	}
	out.Reset()
	if runCLI([]string{"--probe"}, "controller", &out, h, testNow) != 2 {
		t.Fatal("controller probe accepted")
	}
	out.Reset()
	if runCLI([]string{"--probe", "--help"}, "agent", &out, h, testNow) != 0 || h.calls != 0 {
		t.Fatal("help dialed")
	}
}

func serverSnapshot(t *testing.T, host *fakeHost, name string, eku []x509.ExtKeyUsage, notAfter time.Time) tls.Certificate {
	t.Helper()
	caBlock, _ := pem.Decode(host.files["/ca"])
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	keyBlock, _ := pem.Decode(host.files["/key"])
	key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	client, err := tls.X509KeyPair(host.files["/cert"], host.files["/key"])
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(client.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "SECRET server"},
		NotBefore: testNow.Add(-time.Hour), NotAfter: notAfter, DNSNames: []string{name}, ExtKeyUsage: eku}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, leaf.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), host.files["/key"])
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

func TestNativeTLSProbeChecksAndNoHLCPData(t *testing.T) {
	for _, row := range []struct {
		name       string
		host       string
		eku        []x509.ExtKeyUsage
		expiry     time.Time
		alpn       []string
		maxVersion uint16
		want       string
	}{
		{"valid", "controller.test", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, testNow.AddDate(1, 0, 0), []string{"hlcp/1"}, 0, "ok"},
		{"untrusted", "controller.test", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, testNow.AddDate(1, 0, 0), []string{"hlcp/1"}, 0, "fail"},
		{"wrong-name", "other.test", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, testNow.AddDate(1, 0, 0), []string{"hlcp/1"}, 0, "fail"},
		{"expired", "controller.test", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, testNow.Add(-time.Second), []string{"hlcp/1"}, 0, "fail"},
		{"implicit-usage", "controller.test", nil, testNow.AddDate(1, 0, 0), []string{"hlcp/1"}, 0, "fail"},
		{"missing-alpn", "controller.test", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, testNow.AddDate(1, 0, 0), nil, 0, "fail"},
		{"tls12", "controller.test", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, testNow.AddDate(1, 0, 0), []string{"hlcp/1"}, tls.VersionTLS12, "fail"},
	} {
		t.Run(row.name, func(t *testing.T) {
			host := fixture(t, true, testNow.AddDate(1, 0, 0))
			cert := serverSnapshot(t, host, row.host, row.eku, row.expiry)
			pool := x509.NewCertPool()
			pool.AppendCertsFromPEM(host.files["/ca"])
			if row.name == "untrusted" {
				host.files["/ca"] = fixture(t, true, testNow.AddDate(1, 0, 0)).files["/ca"]
			}
			config := &tls.Config{Certificates: []tls.Certificate{cert}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert,
				NextProtos: row.alpn, MaxVersion: row.maxVersion, Time: func() time.Time { return testNow }}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan int, 1)
			go func() {
				raw, err := listener.Accept()
				if err != nil {
					done <- -1
					return
				}
				defer raw.Close()
				raw.SetDeadline(time.Now().Add(4 * time.Second))
				server := tls.Server(raw, config)
				if err := server.Handshake(); err != nil {
					done <- -1
					return
				}
				data := make([]byte, 1)
				n, _ := server.Read(data)
				done <- n
			}()
			report := Report{}
			checkPeer(NativeHost{}, listener.Addr().String(), "controller.test", host.files["/cert"], host.files["/key"], host.files["/ca"], testNow,
				func(code, state, message string) { report.Checks = append(report.Checks, Check{code, state, message}) })
			if stateFor(report, "tls.peer") != row.want {
				t.Fatalf("%+v", report)
			}
			select {
			case n := <-done:
				if n > 0 || (row.want == "ok" && n != 0) {
					t.Fatalf("application bytes: %d", n)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("server did not terminate")
			}
		})
	}
}

func TestNativeProbeDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		// Read handshake bytes but never respond; avoid an unbounded server goroutine.
		raw.SetDeadline(time.Now().Add(time.Second))
		data := make([]byte, 4096)
		for {
			if _, err := raw.Read(data); err != nil {
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = (NativeHost{}).ProbeTLS(ctx, listener.Addr().String(), &tls.Config{MinVersion: tls.VersionTLS13, ServerName: "controller.test", RootCAs: x509.NewCertPool()})
	if !errors.Is(err, errPeerTimeout) {
		t.Fatalf("%v", err)
	}
	<-done
}

func TestProbeDoesNotRegisterWithController(t *testing.T) {
	host := fixture(t, true, testNow.AddDate(1, 0, 0))
	cert := serverSnapshot(t, host, "controller.test", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, testNow.AddDate(1, 0, 0))
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(host.files["/ca"])
	server, err := controller.Listen("127.0.0.1:0", controller.Config{
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert,
			MinVersion: tls.VersionTLS13, NextProtos: []string{"hlcp/1"}, Time: func() time.Time { return testNow }},
		LivenessPolicy: liveness.Policy{HeartbeatInterval: time.Second, DegradedAfter: 2, OfflineAfter: 3},
		AllowedNodeIDs: map[string]bool{"debian-01": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	server.Registry().Register(session.Registration{NodeID: "debian-01", SessionID: "existing"}, session.Synchronization{}, testNow)
	before := server.Registry().List()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(ctx) }()
	report := Report{}
	checkPeer(NativeHost{}, server.Address().String(), "controller.test", host.files["/cert"], host.files["/key"], host.files["/ca"], testNow,
		func(code, state, message string) { report.Checks = append(report.Checks, Check{code, state, message}) })
	if stateFor(report, "tls.peer") != "ok" || stateFor(report, "tls.admission") != "skip" {
		t.Fatalf("%+v", report)
	}
	_ = server.Close()
	<-done
	if !reflect.DeepEqual(before, server.Registry().List()) {
		t.Fatal("probe replaced or registered a node")
	}
}
