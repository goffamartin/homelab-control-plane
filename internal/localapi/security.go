// Package localapi defines the access boundary shared by the controller and CLI.
package localapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
)

const DefaultTokenFile = "/etc/hlcp/api.token"

func ValidateAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return errors.New("local API requires an explicit loopback IP and port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("local API must bind to a literal loopback IP")
	}
	return nil
}

func ValidateURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("API URL must be plain loopback HTTP without credentials, path or query")
	}
	return ValidateAddress(parsed.Host)
}

func LoadToken(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("cannot open local API token file")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 257 {
		return "", errors.New("invalid local API token file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("API token file must be private (mode 0600)")
	}
	data, err := io.ReadAll(io.LimitReader(file, 258))
	if err != nil {
		return "", errors.New("cannot read local API token file")
	}
	token := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if len(token) < 32 || len(token) > 256 {
		return "", errors.New("API token must contain 32 to 256 characters")
	}
	for _, character := range token {
		if character < 0x21 || character > 0x7e {
			return "", errors.New("API token contains invalid characters")
		}
	}
	return token, nil
}

func Error(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{code, message})
}

func Protect(token string, handler http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		host := request.Host
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			Error(writer, http.StatusForbidden, "INVALID_HOST", "loopback Host is required")
			return
		}
		if request.Header.Get("Origin") != "" || request.Header.Get("Sec-Fetch-Site") == "cross-site" {
			Error(writer, http.StatusForbidden, "BROWSER_REQUEST_DENIED", "browser-origin requests are not supported")
			return
		}
		credentials := request.Header.Get("Authorization")
		if len(token) < 32 || subtle.ConstantTimeCompare([]byte(credentials), []byte("Bearer "+token)) != 1 {
			writer.Header().Set("WWW-Authenticate", "Bearer")
			Error(writer, http.StatusUnauthorized, "UNAUTHORIZED", "valid local API credentials are required")
			return
		}
		writer.Header().Set("Cache-Control", "no-store")
		handler.ServeHTTP(writer, request)
	})
}
