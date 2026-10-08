package main

import (
	"context"
	"flag"
	"fmt"
	"hlcp/internal/controller"
	"hlcp/internal/liveness"
	"hlcp/internal/transport"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	listen := flag.String("listen", ":9443", "agent TCP address")
	certificate := flag.String("cert", "", "controller certificate PEM")
	privateKey := flag.String("key", "", "controller private key PEM")
	ca := flag.String("ca", "", "trusted agent CA bundle PEM")
	interval := flag.Duration("heartbeat-interval", 5*time.Second, "agent heartbeat interval")
	degraded := flag.Uint("degraded-after", 1, "missed intervals before degraded")
	offline := flag.Uint("offline-after", 3, "missed intervals before offline")
	flag.Parse()
	if *degraded > 255 || *offline > 255 {
		fmt.Fprintln(os.Stderr, "thresholds must not exceed 255")
		os.Exit(2)
	}
	tlsConfig, err := transport.ServerTLSConfig(transport.TLSFiles{CertificateFile: *certificate, PrivateKeyFile: *privateKey, CAFile: *ca})
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid TLS configuration: %v\n", err)
		os.Exit(2)
	}
	server, err := controller.Listen(*listen, controller.Config{TLSConfig: tlsConfig, LivenessPolicy: liveness.Policy{HeartbeatInterval: *interval, DegradedAfter: uint8(*degraded), OfflineAfter: uint8(*offline)}})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot start controller: %v\n", err)
		os.Exit(1)
	}
	defer server.Close()
	context, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("HLCP controller listening on %s (%s)\n", server.Address(), transport.ALPN)
	if err := server.Serve(context); err != nil {
		fmt.Fprintf(os.Stderr, "controller failed: %v\n", err)
		os.Exit(1)
	}
}
