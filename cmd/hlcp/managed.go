package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hlcp/internal/cli"
	"hlcp/internal/localapi"
	"hlcp/internal/managed"
	"hlcp/internal/managedcli"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func managedArguments(args []string) ([]string, bool) {
	for index := 0; index < len(args); index++ {
		value := args[index]
		if value == "pairing" || value == "certificates" {
			result := append([]string{}, args[index:]...)
			return append(result, args[:index]...), true
		}
		name, _, assigned := strings.Cut(value, "=")
		switch name {
		case "--api", "--api-token-file", "--config":
			if !assigned {
				index++
				if index >= len(args) {
					return nil, false
				}
			}
		case "--json":
		default:
			return nil, false
		}
	}
	return nil, false
}

func managedCLI(args []string, out io.Writer) int {
	if len(args) == 0 {
		return 2
	}
	command := args[0]
	action := ""
	if command == "pairing" {
		if len(args) < 2 {
			fmt.Fprintln(out, "Usage: hlcp pairing create|list|revoke --node ID [--replace --yes] [--output-file PRIVATE_FILE]")
			return 2
		}
		action = args[1]
		args = args[2:]
	} else {
		args = args[1:]
	}
	f := flag.NewFlagSet("hlcp "+command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	node := f.String("node", "", "node identity")
	replace := f.Bool("replace", false, "explicitly revoke old generation and re-pair")
	yes := f.Bool("yes", false, "explicit replacement/revocation consent")
	outputFile := f.String("output-file", "", "new private pairing-code file; refuses overwrite")
	api := f.String("api", "", "loopback API URL")
	tokenFile := f.String("api-token-file", "", "private authorized token path")
	configFile := f.String("config", "", "private client profile path")
	jsonOutput := f.Bool("json", false, "JSON result")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(out, "Usage: hlcp pairing create --node ID [--replace --yes] [--output-file PRIVATE_FILE]\n       hlcp pairing list\n       hlcp pairing revoke --node ID --yes\n       hlcp certificates\nAll use the existing private CLI profile or explicit --api/--api-token-file.\nCreate prints a short-lived secret pairing code exactly once. Do not log or pass it as an agent argument.")
			return 0
		}
		fmt.Fprintln(out, "Invalid managed CLI options.")
		return 2
	}
	if f.NArg() != 0 || (command != "pairing" && command != "certificates") || (command == "pairing" && action != "create" && action != "list" && action != "revoke") || ((action == "create" || action == "revoke") && *node == "") || (*replace && (action != "create" || !*yes)) || (action == "revoke" && !*yes) || (*outputFile != "" && action != "create") {
		fmt.Fprintln(out, "Invalid managed CLI options or required consent missing.")
		return 2
	}
	requestConfig := cli.Request{API: *api, APITokenFile: *tokenFile, ConfigFile: *configFile}
	if applyClientConfig(&requestConfig) != nil {
		fmt.Fprintln(out, "Cannot read private CLI configuration.")
		return 1
	}
	if requestConfig.API == "" {
		requestConfig.API = defaultAPI
	}
	if requestConfig.APITokenFile == "" {
		requestConfig.APITokenFile = localapi.DefaultTokenFile
	}
	if localapi.ValidateURL(requestConfig.API) != nil {
		fmt.Fprintln(out, "Managed administration requires loopback API.")
		return 2
	}
	token, err := localapi.LoadToken(requestConfig.APITokenFile)
	if err != nil {
		fmt.Fprintln(out, "Cannot read authorized API token.")
		return 1
	}
	method, path, body := http.MethodGet, "/v1/certificates", []byte(nil)
	if command == "pairing" {
		path = "/v1/pairing"
		switch action {
		case "create":
			method = http.MethodPost
			body, _ = json.Marshal(map[string]any{"node": *node, "replace": *replace})
		case "revoke":
			method = http.MethodDelete
			path += "/" + url.PathEscape(*node)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(requestConfig.API, "/")+path, bytes.NewReader(body))
	if err != nil {
		return 2
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 16 << 10}}
	response, err := client.Do(request)
	if err != nil {
		fmt.Fprintln(out, "Managed API request failed; inspect state before repeating a mutation.")
		return 1
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (512<<10)+1))
	if err != nil || len(data) > 512<<10 || response.StatusCode != http.StatusOK {
		fmt.Fprintf(out, "Managed API refused (%d); inspect state before retry.\n", response.StatusCode)
		return 1
	}
	if action == "create" {
		var result struct {
			Code string `json:"pairing_code"`
		}
		if managed.DecodeJSON(data, &result, 8192) != nil {
			fmt.Fprintln(out, "Invalid pairing response.")
			return 1
		}
		if _, err := managed.ParseInvitation(result.Code); err != nil {
			return 1
		}
		if *outputFile != "" {
			if managedcli.WritePrivateFile(ctx, *outputFile, []byte(result.Code+"\n")) != nil {
				fmt.Fprintln(out, "Cannot save private code; invitation may already exist. Do not retry blindly.")
				return 1
			}
			fmt.Fprintln(out, "Pairing invitation saved privately; expires after ten minutes.")
			return 0
		}
		if *jsonOutput {
			_, err = out.Write(data)
		} else {
			_, err = fmt.Fprintln(out, result.Code)
		}
		if err != nil {
			return 2
		}
		return 0
	}
	if _, err := out.Write(data); err != nil {
		return 2
	}
	return 0
}
