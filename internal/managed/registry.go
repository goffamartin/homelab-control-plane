package managed

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"hlcp/internal/transport"
	"net/url"
	"sort"
	"strings"
	"time"
)

var ErrAdmission = errors.New("managed admission denied")
var ErrConflict = errors.New("managed identity already exists; explicit replacement required")

const registryFile = "nodes-managed.json"
const MaximumNodes = 1024
const PairingLifetime = 10 * time.Minute
const EnrollmentRetryWindow = 30 * time.Minute

type credential struct {
	Serial  string    `json:"serial"`
	Expires time.Time `json:"expires"`
}
type node struct {
	ID                string       `json:"id"`
	Generation        uint64       `json:"generation"`
	Active            bool         `json:"active"`
	Legacy            bool         `json:"legacy,omitempty"`
	KeyHash           [32]byte     `json:"key_hash"`
	Credentials       []credential `json:"credentials"`
	InvitationHash    [32]byte     `json:"invitation_hash"`
	InvitationExpires time.Time    `json:"invitation_expires"`
	RequestID         string       `json:"request_id"`
	CSRHash           [32]byte     `json:"csr_hash"`
	Certificate       []byte       `json:"certificate"`
	RetryUntil        time.Time    `json:"retry_until"`
	RenewalID         string       `json:"renewal_id"`
	RenewalCSR        [32]byte     `json:"renewal_csr"`
	RenewedAt         time.Time    `json:"renewed_at"`
}
type registryState struct {
	Version int    `json:"version"`
	Nodes   []node `json:"nodes"`
}
type Registry struct {
	store       *Store
	authority   *Authority
	now         func() time.Time
	legacyRoots *x509.CertPool
}

func (*Registry) String() string               { return "managed node registry (redacted)" }
func (r *Registry) GoString() string           { return r.String() }
func (*Registry) MarshalJSON() ([]byte, error) { return []byte(`{"registry":"redacted"}`), nil }

func InitializeRegistry(ctx context.Context, store *Store) error {
	data, _ := json.Marshal(registryState{Version: 1, Nodes: []node{}})
	return store.Create(ctx, registryFile, data)
}
func OpenRegistry(store *Store, a *Authority) (*Registry, error) {
	if store == nil || a == nil {
		return nil, ErrStore
	}
	data, err := store.Read(registryFile)
	if err != nil {
		return nil, ErrStore
	}
	if _, err := parseRegistry(data); err != nil {
		return nil, err
	}
	return &Registry{store: store, authority: a, now: time.Now}, nil
}
func parseRegistry(data []byte) (registryState, error) {
	var state registryState
	if DecodeJSON(data, &state, MaximumStateBytes) != nil || state.Version != 1 || state.Nodes == nil || len(state.Nodes) > MaximumNodes {
		return state, ErrStore
	}
	seen := map[string]bool{}
	for _, n := range state.Nodes {
		if !nodePattern.MatchString(n.ID) || seen[n.ID] || n.Generation == 0 || len(n.Credentials) > 8 || len(n.Certificate) > 16<<10 || (n.Active && !n.Legacy && (n.KeyHash == [32]byte{} || len(n.Credentials) == 0)) || (n.Legacy && (n.KeyHash != [32]byte{} || len(n.Credentials) != 0)) {
			return state, ErrStore
		}
		seen[n.ID] = true
		for _, c := range n.Credentials {
			if c.Serial == "" || len(c.Serial) > 64 || c.Expires.IsZero() {
				return state, ErrStore
			}
		}
	}
	return state, nil
}
func (r *Registry) update(ctx context.Context, fn func(*registryState) error) error {
	return r.store.Update(ctx, registryFile, func(data []byte) ([]byte, error) {
		state, err := parseRegistry(data)
		if err != nil {
			return nil, err
		}
		if err := fn(&state); err != nil {
			return nil, err
		}
		return json.Marshal(state)
	})
}

type Invitation struct {
	Version     int    `json:"version"`
	URL         string `json:"url"`
	Fingerprint string `json:"fingerprint"`
	NodeID      string `json:"node_id"`
	Secret      string `json:"secret"`
}

func (i Invitation) String() string   { return "managed pairing invitation (redacted)" }
func (i Invitation) GoString() string { return i.String() }

func (Invitation) MarshalJSON() ([]byte, error) { return []byte(`{"invitation":"redacted"}`), nil }
func (i Invitation) Encode() string {
	type wire Invitation
	data, _ := json.Marshal(wire(i))
	return "hlcp1." + base64.RawURLEncoding.EncodeToString(data)
}
func ParseInvitation(code string) (Invitation, error) {
	var i Invitation
	if len(code) > 4096 || !strings.HasPrefix(code, "hlcp1.") {
		return i, ErrInput
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(code, "hlcp1."))
	if err != nil || DecodeJSON(data, &i, 4096) != nil || i.Version != 1 || !nodePattern.MatchString(i.NodeID) || !validEnrollmentURL(i.URL) || len(i.Fingerprint) != 64 {
		return Invitation{}, ErrInput
	}
	if fp, err := hex.DecodeString(i.Fingerprint); err != nil || len(fp) != 32 {
		return Invitation{}, ErrInput
	}
	secret, err := base64.RawURLEncoding.DecodeString(i.Secret)
	if err != nil || len(secret) != 32 {
		return Invitation{}, ErrInput
	}
	return i, nil
}
func validEnrollmentURL(address string) bool {
	u, err := url.Parse(address)
	return err == nil && len(address) <= 1024 && u.Scheme == "https" && u.User == nil && u.Hostname() != "" && u.Port() != "" && (u.Path == "" || u.Path == "/") && u.RawQuery == "" && u.Fragment == "" && u.Opaque == ""
}
func (r *Registry) CreateInvitation(ctx context.Context, id, address string, replace bool) (Invitation, error) {
	if !nodePattern.MatchString(id) || !validEnrollmentURL(address) {
		return Invitation{}, ErrInput
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return Invitation{}, ErrStore
	}
	i := Invitation{Version: 1, URL: strings.TrimSuffix(address, "/"), Fingerprint: r.authority.Fingerprint(), NodeID: id, Secret: base64.RawURLEncoding.EncodeToString(secret[:])}
	digest := sha256.Sum256([]byte(i.Secret))
	err := r.update(ctx, func(state *registryState) error {
		index := -1
		generation := uint64(1)
		for n := range state.Nodes {
			if state.Nodes[n].ID == id {
				index = n
				generation = state.Nodes[n].Generation + 1
			}
		}
		if index >= 0 && !replace {
			return ErrConflict
		}
		if generation == 0 {
			return ErrAdmission
		}
		value := node{ID: id, Generation: generation, InvitationHash: digest, InvitationExpires: r.now().Add(PairingLifetime), Credentials: []credential{}}
		if index >= 0 {
			state.Nodes[index] = value
		} else {
			if len(state.Nodes) >= MaximumNodes {
				return ErrAdmission
			}
			state.Nodes = append(state.Nodes, value)
		}
		return nil
	})
	if err != nil {
		return Invitation{}, err
	}
	return i, nil
}

type EnrollmentRequest struct {
	NodeID    string `json:"node_id"`
	Secret    string `json:"secret"`
	RequestID string `json:"request_id"`
	CSR       []byte `json:"csr"`
}
type EnrollmentResult struct {
	Certificate []byte `json:"certificate"`
	CA          []byte `json:"ca"`
	PeerCA      []byte `json:"peer_ca,omitempty"`
	Controller  string `json:"controller"`
	ServerName  string `json:"server_name"`
}

func (r *Registry) Enroll(ctx context.Context, request EnrollmentRequest) ([]byte, error) {
	if !nodePattern.MatchString(request.NodeID) || len(request.Secret) > 128 || len(request.RequestID) != 32 || len(request.CSR) > 16<<10 {
		return nil, ErrAdmission
	}
	if _, err := hex.DecodeString(request.RequestID); err != nil {
		return nil, ErrAdmission
	}
	digest := sha256.Sum256([]byte(request.Secret))
	csrHash := sha256.Sum256(request.CSR)
	var issued []byte
	err := r.update(ctx, func(state *registryState) error {
		for index := range state.Nodes {
			n := &state.Nodes[index]
			if n.ID != request.NodeID {
				continue
			}
			if subtle.ConstantTimeCompare(n.InvitationHash[:], digest[:]) != 1 {
				return ErrAdmission
			}
			if n.RequestID != "" {
				if n.Active && n.RequestID == request.RequestID && n.CSRHash == csrHash && r.now().Before(n.RetryUntil) {
					issued = append([]byte(nil), n.Certificate...)
					return nil
				}
				return ErrAdmission
			}
			if !r.now().Before(n.InvitationExpires) {
				return ErrAdmission
			}
			chain, err := r.authority.IssueAgent(n.ID, request.CSR, r.now())
			if err != nil {
				return ErrAdmission
			}
			b, _ := pem.Decode(chain)
			leaf, _ := x509.ParseCertificate(b.Bytes)
			n.Active = true
			n.KeyHash = sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
			n.Credentials = []credential{{leaf.SerialNumber.Text(16), leaf.NotAfter}}
			n.RequestID = request.RequestID
			n.CSRHash = csrHash
			n.Certificate = chain
			n.RetryUntil = r.now().Add(EnrollmentRetryWindow)
			issued = append([]byte(nil), chain...)
			return nil
		}
		return ErrAdmission
	})
	return issued, err
}
func (r *Registry) Allows(cert *x509.Certificate) bool {
	id, err := transport.AgentNodeID(cert)
	if err != nil || r.now().Before(cert.NotBefore) || !r.now().Before(cert.NotAfter) {
		return false
	}
	data, err := r.store.Read(registryFile)
	if err != nil {
		return false
	}
	state, err := parseRegistry(data)
	if err != nil {
		return false
	}
	hash := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	for _, n := range state.Nodes {
		if n.ID == id && n.Active && n.Legacy && r.legacyRoots != nil {
			_, err := cert.Verify(x509.VerifyOptions{Roots: r.legacyRoots, CurrentTime: r.now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
			return err == nil
		}
		if n.ID == id && n.Active && n.KeyHash == hash {
			for _, c := range n.Credentials {
				if c.Serial == cert.SerialNumber.Text(16) && r.now().Before(c.Expires) {
					return true
				}
			}
		}
	}
	return false
}
func (r *Registry) Revoke(ctx context.Context, id string) error {
	return r.update(ctx, func(state *registryState) error {
		for index := range state.Nodes {
			n := &state.Nodes[index]
			if n.ID == id {
				n.Active = false
				n.InvitationHash = [32]byte{}
				n.Certificate = nil
				n.Credentials = []credential{}
				return nil
			}
		}
		return ErrAdmission
	})
}

type NodeInfo struct {
	ID                 string    `json:"id"`
	Generation         uint64    `json:"generation"`
	Active             bool      `json:"active"`
	Legacy             bool      `json:"legacy,omitempty"`
	InvitationExpires  time.Time `json:"invitation_expires"`
	CertificateExpires time.Time `json:"certificate_expires"`
}

func (r *Registry) List() ([]NodeInfo, error) {
	data, err := r.store.Read(registryFile)
	if err != nil {
		return nil, err
	}
	state, err := parseRegistry(data)
	if err != nil {
		return nil, err
	}
	result := make([]NodeInfo, 0, len(state.Nodes))
	for _, n := range state.Nodes {
		v := NodeInfo{ID: n.ID, Generation: n.Generation, Active: n.Active, Legacy: n.Legacy, InvitationExpires: n.InvitationExpires}
		for _, c := range n.Credentials {
			if c.Expires.After(v.CertificateExpires) {
				v.CertificateExpires = c.Expires
			}
		}
		result = append(result, v)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}
