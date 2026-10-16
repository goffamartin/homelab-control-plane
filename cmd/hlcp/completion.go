package main

import (
	"context"
	"encoding/json"
	"fmt"
	"hlcp/internal/cli"
	"hlcp/internal/localapi"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func completeHLCP(args []string, out io.Writer) {
	request, ok := cli.CompletionRequest(args)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	lookup := func(kind, node, resource string) []string {
		if err := applyClientConfig(&request); err != nil {
			return nil
		}
		api := strings.TrimRight(request.API, "/")
		if api == "" {
			api = defaultAPI
		}
		if localapi.ValidateURL(api) != nil {
			return nil
		}
		tokenFile := request.APITokenFile
		if tokenFile == "" {
			tokenFile = localapi.DefaultTokenFile
		}
		token, err := localapi.LoadToken(tokenFile)
		if err != nil {
			return nil
		}
		path := "/v1/nodes"
		switch kind {
		case "resources":
			if !cli.CompletionWord(node) {
				return nil
			}
			path += "/" + url.PathEscape(node) + "/resources"
		case "actions":
			if !cli.CompletionWord(node) || !cli.CompletionWord(resource) {
				return nil
			}
			path += "/" + url.PathEscape(node) + "/resources/" + url.PathEscape(resource) + "/actions"
		case "nodes":
		default:
			return nil
		}
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, api+path, nil)
		if err != nil {
			return nil
		}
		r.Header.Set("Authorization", "Bearer "+token)
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		defer transport.CloseIdleConnections()
		client := http.Client{Transport: transport, Timeout: 500 * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := client.Do(r)
		if err != nil {
			return nil
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return nil
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
		if err != nil || len(data) > 256<<10 {
			return nil
		}
		var values []string
		if kind == "actions" {
			if json.Unmarshal(data, &values) != nil {
				return nil
			}
		} else {
			var rows []struct {
				Node string `json:"node_id"`
				ID   string `json:"id"`
			}
			if json.Unmarshal(data, &rows) != nil {
				return nil
			}
			for _, row := range rows {
				if kind == "nodes" {
					values = append(values, row.Node)
				} else {
					values = append(values, row.ID)
				}
			}
		}
		// Filter again at the trust boundary; output is consumed as plain words.
		safe := []string{}
		for _, v := range values {
			if cli.CompletionWord(v) {
				safe = append(safe, v)
				if len(safe) == 256 {
					break
				}
			}
		}
		return safe
	}
	for _, candidate := range cli.CompletionCandidates("hlcp", args, lookup) {
		fmt.Fprintln(out, candidate)
	}
}
