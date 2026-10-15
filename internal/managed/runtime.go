package managed

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"time"
)

type RuntimeConfig struct {
	Version          int    `json:"version"`
	Listen           string `json:"listen"`
	ServerName       string `json:"server_name"`
	EnrollmentListen string `json:"enrollment_listen"`
	EnrollmentURL    string `json:"enrollment_url"`
}

func (c RuntimeConfig) Validate() error {
	if c.Version != 1 || !validEnrollmentURL(c.EnrollmentURL) || (!validDNS(c.ServerName) && net.ParseIP(c.ServerName) == nil) {
		return ErrInput
	}
	for _, address := range []string{c.Listen, c.EnrollmentListen} {
		host, port, err := net.SplitHostPort(address)
		p, e := strconv.Atoi(port)
		if err != nil || e != nil || net.ParseIP(host) == nil || p < 1 || p > 65535 {
			return ErrInput
		}
	}
	return nil
}
func CreateRuntimeConfig(ctx context.Context, s *Store, config RuntimeConfig) error {
	if config.Validate() != nil {
		return ErrInput
	}
	data, _ := json.Marshal(config)
	return s.Create(ctx, "runtime.json", data)
}

type Runtime struct {
	Store     *Store
	Config    RuntimeConfig
	Authority *Authority
	Registry  *Registry
	Identity  *ServerIdentity
}

func (*Runtime) String() string     { return "managed controller runtime (redacted)" }
func (r *Runtime) GoString() string { return r.String() }
func OpenRuntime(path string) (*Runtime, error) {
	s, err := OpenStore(path)
	if err != nil {
		return nil, err
	}
	fail := func() (*Runtime, error) { s.Close(); return nil, ErrStore }
	data, err := s.Read("runtime.json")
	if err != nil {
		return fail()
	}
	var config RuntimeConfig
	if DecodeJSON(data, &config, 16<<10) != nil || config.Validate() != nil {
		return fail()
	}
	a, err := LoadStoredAuthority(s, time.Now())
	if err != nil {
		return fail()
	}
	r, err := OpenRegistry(s, a)
	if err != nil {
		return fail()
	}
	i, err := OpenServerIdentity(s, a, config.ServerName)
	if err != nil {
		return fail()
	}
	return &Runtime{Store: s, Config: config, Authority: a, Registry: r, Identity: i}, nil
}
