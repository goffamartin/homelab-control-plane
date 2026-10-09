package transport

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/url"
	"testing"
	"time"
)

func TestAgentIdentityRejectsMissingAmbiguousAndUnsafeSAN(t *testing.T) {
	for _, identities := range [][]string{nil, {"urn:hlcp:node:a", "urn:hlcp:node:b"}, {"urn:hlcp:node:a/b"}, {"urn:hlcp:node:"}} {
		leaf := &x509.Certificate{ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
		for _, value := range identities {
			uri, err := url.Parse(value)
			if err != nil {
				t.Fatal(err)
			}
			leaf.URIs = append(leaf.URIs, uri)
		}
		if _, err := AgentNodeID(leaf); err == nil {
			t.Fatalf("accepted identities %v", identities)
		}
	}
}

func TestTLSRejectsInvalidPeerProfiles(t *testing.T) {
	for _, scenario := range []string{"missing-alpn", "missing-identity", "expired", "wrong-eku", "foreign-ca", "wrong-server-name"} {
		t.Run(scenario, func(t *testing.T) {
			serverFiles, clientFiles := writeTestCertificates(t)
			serverConfig, err := ServerTLSConfig(serverFiles)
			if err != nil {
				t.Fatal(err)
			}
			clientConfig, err := ClientTLSConfig(clientFiles)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing-alpn":
				clientConfig.NextProtos = nil
			case "wrong-server-name":
				clientConfig.ServerName = "other.test"
			case "foreign-ca":
				clientConfig.RootCAs = x509.NewCertPool()
			default:
				ca, key := createCA(t)
				serverConfig.ClientCAs = x509.NewCertPool()
				serverConfig.ClientCAs.AddCert(ca)
				leaf, leafKey := createLeaf(t, ca, key, "agent.test", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
				switch scenario {
				case "missing-identity":
					leaf.URIs = nil
				case "expired":
					leaf.NotAfter = time.Now().Add(-time.Minute)
					leaf.NotBefore = time.Now().Add(-time.Hour)
				case "wrong-eku":
					leaf.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
				}
				encoded, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, key)
				if err != nil {
					t.Fatal(err)
				}
				clientConfig.Certificates = []tls.Certificate{{Certificate: [][]byte{encoded}, PrivateKey: leafKey}}
			}
			rawServer, rawClient := net.Pipe()
			defer rawServer.Close()
			defer rawClient.Close()
			rawServer.SetDeadline(time.Now().Add(2 * time.Second))
			rawClient.SetDeadline(time.Now().Add(2 * time.Second))
			server := tls.Server(rawServer, serverConfig)
			client := tls.Client(rawClient, clientConfig)
			done := make(chan error, 1)
			go func() { err := server.Handshake(); rawServer.Close(); done <- err }()
			clientErr := client.Handshake()
			serverErr := <-done
			if clientErr == nil && serverErr == nil {
				t.Fatal("invalid TLS peer was accepted")
			}
		})
	}
}
