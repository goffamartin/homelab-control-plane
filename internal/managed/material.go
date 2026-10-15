package managed

import "time"

// IdentityMaterial validates a bounded, already permission-checked private
// snapshot. It never opens a store lock or falls back to stale imported PEMs.
func IdentityMaterial(data []byte, id string, now time.Time) (chain, key, ca []byte, err error) {
	var d identityDisk
	if DecodeJSON(data, &d, 128<<10) != nil || d.Version != 1 || d.NodeID != id || !validEnrollmentURL(d.EnrollmentURL) || VerifyAgentMaterial(id, d.Certificate, d.Key, d.CA, now) != nil {
		return nil, nil, nil, ErrPKI
	}
	return d.Certificate, d.Key, d.CA, nil
}
