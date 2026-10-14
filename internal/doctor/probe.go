package doctor

import (
	"context"
	"crypto/tls"
	"errors"
	"hlcp/internal/transport"
	"net"
	"time"
)

const probeTimeout = 3 * time.Second

// ProbeHost has no application-level exchange, registry or operation API.
type ProbeHost interface {
	ProbeTLS(context.Context, string, *tls.Config) error
}

var (
	errPeerTimeout = errors.New("TLS probe timed out")
	errPeerDial    = errors.New("TLS probe could not connect")
	errPeerTLS     = errors.New("TLS peer verification failed")
)

func checkPeer(host ProbeHost, endpoint, serverName string, cert, key, ca []byte, now time.Time, add func(string, string, string)) {
	config, err := transport.ClientTLSConfigFromPEM(cert, key, ca, serverName)
	if err != nil {
		add("tls.peer", "fail", "TLS snapshot could not be prepared; no connection was attempted.")
		return
	}
	config.Time = func() time.Time { return now }
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	err = host.ProbeTLS(ctx, endpoint, config)
	switch {
	case err == nil:
		add("tls.peer", "ok", "Server certificate chain, name, explicit server-auth EKU and HLCP ALPN were verified.")
	case errors.Is(err, errPeerTimeout):
		add("tls.peer", "fail", "TLS probe exceeded its deadline.")
	case errors.Is(err, errPeerDial):
		add("tls.peer", "fail", "Controller endpoint could not be reached; check addressing, DNS and firewall.")
	default:
		add("tls.peer", "fail", "TLS handshake or peer verification failed; check trust, name, validity, TLS version and HLCP ALPN.")
	}
	add("tls.admission", "skip", "Server acceptance of the client certificate, node allowlist and HLCP registration were not confirmed.")
}

func (NativeHost) ProbeTLS(ctx context.Context, endpoint string, config *tls.Config) error {
	if config == nil {
		return errPeerTLS
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", endpoint)
	if err != nil {
		if ctx.Err() != nil {
			return errPeerTimeout
		}
		return errPeerDial
	}
	defer connection.Close() // No TLS application writes or HLCP frames.
	if deadline, ok := ctx.Deadline(); ok {
		if connection.SetDeadline(deadline) != nil {
			return errPeerTLS
		}
	}
	client := tls.Client(connection, config)
	if err := client.HandshakeContext(ctx); err != nil {
		var timeout net.Error
		if ctx.Err() != nil || (errors.As(err, &timeout) && timeout.Timeout()) {
			return errPeerTimeout
		}
		return errPeerTLS
	}
	return nil
}
