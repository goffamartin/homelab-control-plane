package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"hlcp/internal/localapi"
	"io"
	"path/filepath"
)

// ClientConfig is the shared version-1 persisted CLI schema. Parsing does not
// read token files, grant access or infer a user's home/environment.
type ClientConfig struct {
	Version   int    `json:"version"`
	API       string `json:"api"`
	TokenFile string `json:"api_token_file"`
}

var ErrClientConfig = errors.New("invalid client configuration")

func ParseClientConfig(data []byte, path string) (ClientConfig, error) {
	var config ClientConfig
	if len(data) > 8192 {
		return config, ErrClientConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return config, ErrClientConfig
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return ClientConfig{}, ErrClientConfig
		}
		seen[name] = true
		switch name {
		case "version":
			err = decoder.Decode(&config.Version)
		case "api":
			err = decoder.Decode(&config.API)
		case "api_token_file":
			err = decoder.Decode(&config.TokenFile)
		default:
			return ClientConfig{}, ErrClientConfig
		}
		if err != nil {
			return ClientConfig{}, ErrClientConfig
		}
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
		return ClientConfig{}, ErrClientConfig
	}
	if decoder.Decode(new(any)) != io.EOF || config.Version != 1 || config.TokenFile == "" || localapi.ValidateURL(config.API) != nil {
		return ClientConfig{}, ErrClientConfig
	}
	if !filepath.IsAbs(config.TokenFile) {
		config.TokenFile = filepath.Join(filepath.Dir(path), config.TokenFile)
	}
	return config, nil
}
