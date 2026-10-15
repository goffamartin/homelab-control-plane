// Package doctor performs diagnostics, offline unless a TLS probe is selected.
// Its base host interface deliberately
// offers no writes, arbitrary command execution, service control or network operations.
// Optional SystemHost diagnostics expose only predefined local property queries.
// Optional ProbeHost performs an explicitly selected TLS-only connection.
package doctor

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"hlcp/internal/agent"
	"hlcp/internal/transport"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

const MaximumFileSize = 1 << 20

type Host interface {
	ReadFile(path string, private bool) ([]byte, error)
	Executable(name string) bool
	Platform() (string, string)
}

type Options struct {
	Role           string
	Config         string
	Certificate    string
	Key            string
	CA             string
	ServerName     string
	HostChecks     bool
	Scope          string
	Unit           string
	SetupDirectory string
	Probe          bool
	User           string
}

type Check struct {
	Code    string `json:"code"`
	State   string `json:"state"`
	Message string `json:"message"`
}

type Report struct {
	ReadOnly bool    `json:"read_only"`
	Offline  bool    `json:"offline"`
	Checks   []Check `json:"checks"`
}

func (r Report) Failed() bool {
	for _, check := range r.Checks {
		if check.State == "fail" {
			return true
		}
	}
	return false
}

// Run never returns parser, OS or certificate errors: those can contain secrets.
// The caller supplies the clock so boundary conditions are deterministic.
func Run(host Host, options Options, now time.Time) Report {
	r := Report{ReadOnly: true, Offline: true, Checks: []Check{}}
	add := func(code, state, message string) { r.Checks = append(r.Checks, Check{code, state, message}) }
	osName, arch := host.Platform()
	if osName == "linux" && (arch == "amd64" || arch == "arm64") {
		add("platform", "ok", "Supported deployment platform.")
	} else {
		add("platform", "warn", "Deployment diagnostics target Linux amd64/arm64.")
	}
	if options.Role != "agent" && options.Role != "controller" {
		add("role", "fail", "Select agent or controller diagnostics.")
		return r
	}
	if options.SetupDirectory != "" {
		checkTransaction(host, options.SetupDirectory, add)
	}
	nodeID := ""
	endpoint := ""
	podman := false
	if options.Role == "agent" {
		data, err := host.ReadFile(options.Config, true)
		if err != nil || len(data) > MaximumFileSize {
			add("agent.config", "fail", "Configuration is missing, unreadable, oversized or unsafe.")
			return r
		}
		config, err := agent.Parse(data)
		if err != nil {
			add("agent.config", "fail", "Configuration does not satisfy the agent schema.")
			return r
		}
		add("agent.config", "ok", "Agent configuration is valid.")
		options.Certificate, options.Key, options.CA = config.CertificateFile, config.PrivateKeyFile, config.CAFile
		options.ServerName, nodeID = config.ServerName, config.NodeID
		endpoint = config.ControllerAddress
		hostName, port, err := net.SplitHostPort(config.ControllerAddress)
		portNumber, portErr := strconv.Atoi(port)
		if err != nil || hostName == "" || portErr != nil || portNumber < 1 || portNumber > 65535 {
			add("agent.endpoint", "fail", "Controller endpoint requires host and port.")
		} else {
			add("agent.endpoint", "ok", "Controller endpoint syntax is valid; connectivity was not tested.")
		}
		required := map[string]bool{}
		for _, resource := range config.Resources {
			if resource.Adapter == "podman" {
				podman = true
			}
			switch resource.Adapter {
			case "docker", "podman":
				required[resource.Adapter] = true
			case "systemd":
				required["systemctl"] = true
			}
			actions := resource.ScriptActions()
			sort.Strings(actions)
			for _, action := range actions {
				spec := resource.Commands[action]
				checkScript(host, spec.Executable, spec.WorkingDirectory, add)
			}
		}
		names := make([]string, 0, len(required))
		for name := range required {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			// Do not expose configured paths, resource IDs, arguments or environment.
			code := "agent.executable"
			if name == "docker" || name == "podman" || name == "systemctl" {
				code += "." + name
			}
			if host.Executable(name) {
				add(code, "ok", "Configured executable is available; it was not executed.")
			} else {
				add(code, "fail", "A configured executable is unavailable or unsafe.")
			}
		}
		add("agent.state", "skip", "Journal and resource state were not opened.")
	}
	if options.HostChecks {
		checkSystem(host, options, podman, add)
	}
	read := func(path, code string, private bool) []byte {
		data, err := host.ReadFile(path, private)
		if err != nil || len(data) > MaximumFileSize {
			add(code, "fail", "File is missing, unreadable, oversized or unsafe.")
			return nil
		}
		add(code, "ok", "File is readable and passed filesystem safety checks.")
		return data
	}
	certData := read(options.Certificate, "tls.certificate.file", false)
	keyData := read(options.Key, "tls.key.file", true)
	caData := read(options.CA, "tls.trust.file", false)
	if certData != nil && keyData != nil {
		pair, err := tls.X509KeyPair(certData, keyData)
		if err != nil {
			add("tls.key_match", "fail", "Certificate and private key are invalid or do not match.")
		} else {
			add("tls.key_match", "ok", "Certificate matches the private key.")
			leaf := pair.Leaf
			if leaf == nil {
				leaf, _ = x509.ParseCertificate(pair.Certificate[0])
			}
			if leaf == nil {
				add("tls.leaf", "fail", "Leaf certificate cannot be parsed.")
			} else {
				if leaf.IsCA {
					add("tls.leaf", "fail", "A CA certificate cannot serve as a deployment leaf.")
				}
				checkValidity(leaf, now, "tls.leaf.validity", add)
				usage := x509.ExtKeyUsageServerAuth
				if options.Role == "agent" {
					usage = x509.ExtKeyUsageClientAuth
					certNodeID, identityErr := transport.AgentNodeID(leaf)
					matching := identityErr == nil && certNodeID == nodeID
					if matching {
						add("tls.node_identity", "ok", "Certificate URI matches the configured node.")
					} else {
						add("tls.node_identity", "fail", "Certificate must contain one unambiguous matching HLCP node URI and explicit client-auth EKU.")
					}
				} else if options.ServerName != "" {
					if leaf.VerifyHostname(options.ServerName) != nil {
						add("tls.server_name", "fail", "Certificate does not cover the selected server name.")
					} else {
						add("tls.server_name", "ok", "Certificate covers the selected server name.")
					}
				} else {
					add("tls.server_name", "skip", "No server name selected; hostname coverage was not checked.")
				}
				validUsage := false
				for _, eku := range leaf.ExtKeyUsage {
					if eku == usage {
						validUsage = true
					}
				}
				if validUsage {
					add("tls.usage", "ok", "Leaf permits the required TLS role.")
				} else {
					add("tls.usage", "fail", "Leaf must explicitly permit the required TLS role.")
				}
			}
		}
	}
	if caData != nil {
		certificates, ok := parseBundle(caData)
		if !ok {
			add("tls.trust", "fail", "Trust bundle must contain only valid CA certificate PEM blocks.")
		} else {
			add("tls.trust", "ok", "Trust bundle contains CA certificates.")
			for _, ca := range certificates {
				checkValidity(ca, now, "tls.trust.validity", add)
			}
		}
	}
	if options.Probe {
		if options.Role != "agent" {
			add("tls.peer", "fail", "TLS probe is available only for the agent role.")
		} else if r.Failed() {
			add("tls.peer", "skip", "TLS probe was not attempted because local diagnostics failed.")
		} else {
			probe, ok := host.(ProbeHost)
			if !ok {
				add("tls.peer", "fail", "TLS probe is unavailable on this host.")
			} else {
				r.Offline = false
				checkPeer(probe, endpoint, options.ServerName, certData, keyData, caData, now, add)
			}
		}
	} else {
		add("tls.peer", "skip", "Peer handshake and remote chain trust were not tested.")
	}
	if !options.HostChecks {
		add("host.integration", "skip", "Host checks were not selected; use --host-checks with an explicit service scope.")
	}
	add("runtime.access", "skip", "Runtime sockets, containers, script execution and HLCP registration were not tested.")
	return r
}

func checkValidity(cert *x509.Certificate, now time.Time, code string, add func(string, string, string)) {
	switch {
	case now.Before(cert.NotBefore):
		add(code, "fail", "Certificate is not valid yet; check the system clock.")
	case !now.Before(cert.NotAfter):
		add(code, "fail", "Certificate has expired.")
	case cert.NotAfter.Sub(now) <= 30*24*time.Hour:
		add(code, "warn", "Certificate expires within 30 days; arrange renewal.")
	default:
		add(code, "ok", "Certificate validity exceeds 30 days.")
	}
}

func parseBundle(data []byte) ([]*x509.Certificate, bool) {
	var certificates []*x509.Certificate
	for len(strings.TrimSpace(string(data))) > 0 {
		// Reject skipped prose/private blocks rather than silently accepting them.
		if !strings.HasPrefix(strings.TrimSpace(string(data)), "-----BEGIN CERTIFICATE-----") {
			return nil, false
		}
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, false
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA || !cert.BasicConstraintsValid {
			return nil, false
		}
		certificates = append(certificates, cert)
		data = rest
	}
	return certificates, len(certificates) > 0
}
