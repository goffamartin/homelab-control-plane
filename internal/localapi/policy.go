package localapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var ErrForbidden = errors.New("local API permission denied")

const maxPolicyBytes = 64 << 10

type nodeScope struct {
	AllResources bool     `json:"all_resources"`
	Resources    []string `json:"resources"`
}
type scope struct {
	AllNodes bool                 `json:"all_nodes"`
	Nodes    map[string]nodeScope `json:"nodes"`
}
type principalSpec struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	TokenFile string `json:"token_file"`
	Scope     scope  `json:"scope"`
}
type principal struct {
	id, role string
	digest   [32]byte
	scope    scope
}

// Policy is an immutable, fully validated snapshot. It never stores bearer text.
type Policy struct{ principals []principal }

// RoleForToken inspects this validated immutable snapshot without granting new
// permissions or retaining plaintext. Used before explicitly exporting access.
func (p *Policy) RoleForToken(token string) (string, error) {
	if p == nil || !validToken(token) {
		return "", ErrForbidden
	}
	digest := sha256.Sum256([]byte(token))
	for _, principal := range p.principals {
		if subtle.ConstantTimeCompare(principal.digest[:], digest[:]) == 1 {
			return principal.role, nil
		}
	}
	return "", ErrForbidden
}

type Authorizer struct {
	mutex  sync.RWMutex
	policy *Policy
}
type authKey struct{}
type authentication struct {
	owner     *Authorizer
	policy    *Policy
	principal *principal
}

func NewAuthorizer(policy *Policy) *Authorizer { return &Authorizer{policy: policy} }
func LegacyAuthorizer(token string) *Authorizer {
	p := &Policy{}
	if validToken(token) {
		p.principals = []principal{{id: "local-api", role: "operator", digest: sha256.Sum256([]byte(token)), scope: scope{AllNodes: true}}}
	}
	return NewAuthorizer(p)
}

func validToken(token string) bool {
	if len(token) < 32 || len(token) > 256 {
		return false
	}
	for _, ch := range token {
		if ch < 0x21 || ch > 0x7e {
			return false
		}
	}
	return true
}
func identifier(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, ch := range id {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || strings.ContainsRune("._-:", ch) {
			continue
		}
		return false
	}
	return true
}

func LoadPolicy(path string) (*Policy, error) {
	invalid := errors.New("invalid local API policy or credential files")
	file, err := os.Open(path)
	if err != nil {
		return nil, invalid
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxPolicyBytes || !privateCredential(file, info) {
		return nil, invalid
	}
	data, err := io.ReadAll(io.LimitReader(file, maxPolicyBytes+1))
	if err != nil || len(data) > maxPolicyBytes {
		return nil, invalid
	}
	return ParsePolicy(data, func(credential string) (string, error) {
		if !filepath.IsAbs(credential) {
			credential = filepath.Join(filepath.Dir(path), credential)
		}
		return LoadToken(credential)
	})
}

// ParsePolicy validates the same policy schema using a caller-supplied bounded
// token snapshot resolver. It never opens files itself or retains bearer text.
func ParsePolicy(data []byte, readToken func(string) (string, error)) (*Policy, error) {
	invalid := errors.New("invalid local API policy or credential files")
	if len(data) > maxPolicyBytes || readToken == nil {
		return nil, invalid
	}
	// encoding/json normally accepts duplicate keys; policy must not.
	if uniqueJSON(json.NewDecoder(strings.NewReader(string(data))), 0) != nil {
		return nil, invalid
	}
	var document struct {
		Version    int             `json:"version"`
		Principals []principalSpec `json:"principals"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&document) != nil || decoder.Decode(new(any)) != io.EOF || document.Version != 1 || len(document.Principals) == 0 || len(document.Principals) > 32 {
		return nil, invalid
	}
	p := &Policy{}
	ids := map[string]bool{}
	digests := map[[32]byte]bool{}
	for _, spec := range document.Principals {
		if !identifier(spec.ID) || ids[spec.ID] || (spec.Role != "observer" && spec.Role != "operator") || spec.TokenFile == "" || len(spec.Scope.Nodes) > 128 || (spec.Scope.AllNodes && len(spec.Scope.Nodes) > 0) {
			return nil, invalid
		}
		ids[spec.ID] = true
		for node, allowed := range spec.Scope.Nodes {
			if !identifier(node) || len(allowed.Resources) > 256 || (allowed.AllResources && len(allowed.Resources) > 0) {
				return nil, invalid
			}
			seen := map[string]bool{}
			for _, resource := range allowed.Resources {
				if !identifier(resource) || seen[resource] {
					return nil, invalid
				}
				seen[resource] = true
			}
		}
		token, err := readToken(spec.TokenFile)
		if err != nil || !validToken(token) {
			return nil, invalid
		}
		digest := sha256.Sum256([]byte(token))
		if digests[digest] {
			return nil, invalid
		}
		digests[digest] = true
		p.principals = append(p.principals, principal{id: spec.ID, role: spec.Role, digest: digest, scope: spec.Scope})
	}
	return p, nil
}

func uniqueJSON(decoder *json.Decoder, depth int) error {
	if depth > 8 {
		return ErrForbidden
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for decoder.More() {
		if delim == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return ErrForbidden
			}
			allowed := ""
			switch depth {
			case 0:
				allowed = "|version|principals|"
			case 2:
				allowed = "|id|role|token_file|scope|"
			case 3:
				allowed = "|all_nodes|nodes|"
			case 5:
				allowed = "|all_resources|resources|"
			}
			if allowed != "" && !strings.Contains(allowed, "|"+name+"|") {
				return ErrForbidden
			}
			seen[name] = true
		}
		if err := uniqueJSON(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

// Reload loads every file first. Failure leaves the entire active policy intact.
// Replacement serializes against admission, not the external execution/result wait.
func (a *Authorizer) Reload(path string) error {
	p, err := LoadPolicy(path)
	if err != nil {
		return err
	}
	a.mutex.Lock()
	a.policy = p
	a.mutex.Unlock()
	return nil
}
func (a *Authorizer) authenticate(request *http.Request) *authentication {
	if a == nil || len(request.Header.Values("Authorization")) != 1 {
		return nil
	}
	header := request.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") || !validToken(strings.TrimPrefix(header, "Bearer ")) {
		return nil
	}
	digest := sha256.Sum256([]byte(strings.TrimPrefix(header, "Bearer ")))
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	if a.policy == nil {
		return nil
	}
	var found *principal
	for i := range a.policy.principals {
		p := &a.policy.principals[i]
		if subtle.ConstantTimeCompare(digest[:], p.digest[:]) == 1 {
			found = p
		}
	}
	if found == nil {
		return nil
	}
	return &authentication{a, a.policy, found}
}
func Identity(ctx context.Context) string {
	if auth, ok := ctx.Value(authKey{}).(*authentication); ok {
		return auth.principal.id
	}
	return "anonymous"
}
func (p *principal) allows(node, resource string, execute bool) bool {
	if execute && p.role != "operator" {
		return false
	}
	if p.scope.AllNodes {
		return true
	}
	s, ok := p.scope.Nodes[node]
	if !ok {
		return false
	}
	if resource == "" || s.AllResources {
		return true
	}
	for _, id := range s.Resources {
		if id == resource {
			return true
		}
	}
	return false
}
func Allowed(ctx context.Context, node, resource string, execute bool) bool {
	auth, ok := ctx.Value(authKey{}).(*authentication)
	return ok && auth.principal.allows(node, resource, execute)
}

// Rechecked at every streamed page/chunk, not only when a long-lived request
// was authenticated. Any policy replacement revokes the old request snapshot.
func CurrentRead(ctx context.Context, node, resource string) bool {
	auth, ok := ctx.Value(authKey{}).(*authentication)
	if !ok {
		return false
	}
	auth.owner.mutex.RLock()
	defer auth.owner.mutex.RUnlock()
	return auth.owner.policy == auth.policy && auth.principal.allows(node, resource, false)
}

func GlobalRead(ctx context.Context) bool {
	auth, ok := ctx.Value(authKey{}).(*authentication)
	return ok && auth.principal.scope.AllNodes
}

// DiagnosticBinding is not a public fingerprint. It is used only as encrypted
// cursor associated data; token rotation or a scope/identity change invalidates it.
func DiagnosticBinding(ctx context.Context) [32]byte {
	auth, ok := ctx.Value(authKey{}).(*authentication)
	if !ok {
		return [32]byte{}
	}
	p := auth.principal
	data, _ := json.Marshal(struct {
		ID, Role string
		Scope    scope
		Digest   [32]byte
	}{p.id, p.role, p.scope, p.digest})
	return sha256.Sum256(data)
}

// GuardAdmission gives internal trusted callers their original behavior, while
// authenticated API callers are rechecked at the durable admission boundary.
// Every policy reload invalidates older, not-yet-admitted request snapshots.
func GuardAdmission(ctx context.Context, node, resource string, admit func() error) error {
	auth, ok := ctx.Value(authKey{}).(*authentication)
	if !ok {
		return admit()
	}
	auth.owner.mutex.RLock()
	defer auth.owner.mutex.RUnlock()
	if auth.owner.policy != auth.policy || !auth.principal.allows(node, resource, true) {
		return ErrForbidden
	}
	return admit()
}
