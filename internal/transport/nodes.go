package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
)

var nodeIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// LoadAllowedNodes loads an explicit startup allowlist. An empty array denies
// every node; omission of the CLI option retains legacy CA-only trust.
func LoadAllowedNodes(path string) (map[string]bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open node allowlist")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return nil, errors.New("node allowlist must be a regular file of at most 64 KiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil {
		return nil, errors.New("cannot read node allowlist")
	}
	return ParseAllowedNodes(data)
}

// ParseAllowedNodes validates a bounded admission snapshot without file I/O.
func ParseAllowedNodes(data []byte) (map[string]bool, error) {
	if len(data) > 64*1024 {
		return nil, errors.New("node allowlist exceeds 64 KiB")
	}
	var nodes []string
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&nodes); err != nil || nodes == nil || len(nodes) > 1024 {
		return nil, errors.New("node allowlist must be a JSON array of at most 1024 identities")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errors.New("trailing node allowlist data")
	}
	result := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if !nodeIDPattern.MatchString(node) || result[node] {
			return nil, errors.New("invalid or duplicate node allowlist identity")
		}
		result[node] = true
	}
	return result, nil
}
