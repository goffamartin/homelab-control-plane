package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hlcp/internal/cli"
	"hlcp/internal/localapi"
	"hlcp/internal/protocol"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type logPage struct {
	SchemaVersion   int                  `json:"schema_version"`
	StreamSemantics string               `json:"stream_semantics"`
	CapturedAt      time.Time            `json:"captured_at"`
	SourceSession   string               `json:"source_session"`
	Records         []protocol.LogRecord `json:"records"`
	NextCursor      string               `json:"next_cursor,omitempty"`
	Code            string               `json:"code"`
	Gap             bool                 `json:"gap"`
	Truncated       bool                 `json:"truncated"`
}
type logMessage struct {
	SchemaVersion   int    `json:"schema_version"`
	StreamSemantics string `json:"stream_semantics"`
	protocol.LogEvent
}

func safeLogText(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\t' {
			return '�'
		}
		return r
	}, text)
}

func containerLogsCLI(request cli.Request, api string, out, diagnostic io.Writer) (bool, int) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return runContainerLogsCLI(ctx, request, api, out, diagnostic)
}
func runContainerLogsCLI(ctx context.Context, request cli.Request, api string, out, diagnostic io.Writer) (bool, int) {
	limit := 25 * time.Second
	if request.Logs != nil && request.Logs.Query.Follow {
		limit = 10*time.Minute + 15*time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	fail := func(code, message string) (bool, int) {
		if request.Output == "json" {
			_ = json.NewEncoder(out).Encode(map[string]any{"schema_version": 1, "kind": "error", "code": code, "message": message})
		} else {
			fmt.Fprintln(diagnostic, message, "("+safeLogText(code)+")")
		}
		return true, 4
	}
	if localapi.ValidateURL(api) != nil {
		return fail("INVALID_API", "A loopback controller API is required.")
	}
	token, err := localapi.LoadToken(apiTokenFile)
	if err != nil {
		return fail("CREDENTIAL_UNAVAILABLE", "Cannot read the local API credential.")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 25 * time.Second
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect denied") }}
	get := func(path string) (*http.Response, error) {
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, api+path, nil)
		if err != nil {
			return nil, err
		}
		r.Header.Set("Authorization", "Bearer "+token)
		return client.Do(r)
	}
	base := "/v1/nodes/" + url.PathEscape(request.Node) + "/resources/" + url.PathEscape(request.Resource)
	response, err := get(base)
	if err != nil {
		return fail("API_UNAVAILABLE", "Cannot inspect the selected resource.")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	response.Body.Close()
	if err != nil || len(body) > 2<<20 || response.StatusCode != 200 {
		return fail("RESOURCE_UNAVAILABLE", "Cannot inspect the selected resource or access was denied.")
	}
	var detail struct {
		Type    string   `json:"type"`
		Actions []string `json:"actions"`
	}
	if json.Unmarshal(body, &detail) != nil {
		return fail("INVALID_RESPONSE", "Invalid resource response.")
	}
	// Preserve existing script/service actions named logs without log-only flags.
	if detail.Type != "container" {
		if request.Logs == nil {
			return false, 0
		}
		return fail("NOT_A_CONTAINER", "Native log options require a container resource.")
	}
	if request.CommandID != "" {
		return fail("INVALID_OPTIONS", "Container log reads have no command ID or execution history.")
	}
	enabled := false
	for _, action := range detail.Actions {
		enabled = enabled || action == "logs"
	}
	if !enabled {
		return fail("LOGS_NOT_ENABLED", "Enable the logs action explicitly in the agent YAML.")
	}
	options := request.Logs
	if options == nil {
		options = &cli.LogOptions{Query: protocol.DefaultLogQuery(), Limit: 100}
	}
	if options.Query.Validate() != nil {
		return fail("INVALID_QUERY", "Invalid container log filters.")
	}
	if request.DryRun {
		preview := map[string]any{"schema_version": 1, "status": "preview", "read_only": true, "query": options.Query, "runtime_verified": false}
		if request.Output == "json" {
			_ = json.NewEncoder(out).Encode(preview)
		} else {
			fmt.Fprintln(out, "Read-only log preview. Runtime access was not tested; no logs were requested.")
		}
		return true, 0
	}
	values := url.Values{}
	if options.Cursor != "" {
		values.Set("cursor", options.Cursor)
	} else {
		q := options.Query
		values.Set("tail", strconv.Itoa(q.Tail))
		values.Set("stream", q.Stream)
		if q.Since != "" {
			values.Set("since", q.Since)
		}
		if q.Until != "" {
			values.Set("until", q.Until)
		}
		if q.Contains != "" {
			values.Set("contains", q.Contains)
		}
	}
	path := base + "/logs"
	if options.Query.Follow {
		path += "/stream"
	} else {
		values.Set("limit", strconv.Itoa(options.Limit))
	}
	response, err = get(path + "?" + values.Encode())
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return true, 0
		}
		return fail("API_UNAVAILABLE", "Container log request failed.")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		var result struct {
			Code string `json:"code"`
		}
		data, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
		_ = json.Unmarshal(data, &result)
		if result.Code == "" {
			result.Code = "LOGS_UNAVAILABLE"
		}
		return fail(result.Code, "Container log request did not complete. No container action was executed.")
	}
	if options.Query.Follow {
		return true, consumeLogStream(ctx, response.Body, request.Output == "json", out, diagnostic)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return fail("INVALID_RESPONSE", "Incomplete or oversized log page.")
	}
	var page logPage
	if json.Unmarshal(data, &page) != nil || page.SchemaVersion != 1 || len(page.Records) > 200 || !protocol.LogCode(page.Code) || len(page.NextCursor) > 160 {
		return fail("INVALID_RESPONSE", "Unsupported or invalid log page.")
	}
	for _, record := range page.Records {
		if (protocol.LogEvent{RequestID: "local", Sequence: 2, Kind: "record", Record: &record}).Validate() != nil {
			return fail("INVALID_RESPONSE", "Invalid log record.")
		}
	}
	if request.Output == "json" {
		if json.NewEncoder(out).Encode(page) != nil {
			return true, 4
		}
	} else {
		if len(page.Records) == 0 {
			fmt.Fprintln(out, "No log records in the selected window.")
		}
		for _, record := range page.Records {
			if _, err := fmt.Fprintf(out, "%s [%s] %s\n", record.Timestamp, record.Stream, safeLogText(record.Text)); err != nil {
				return true, 4
			}
		}
		if page.NextCursor != "" {
			fmt.Fprintln(diagnostic, "Next page: --cursor", page.NextCursor)
		}
		if page.Gap || page.Truncated {
			fmt.Fprintln(diagnostic, "Log window or a record was truncated; complete history is not guaranteed.")
		}
	}
	if page.Code != "OK" && page.Code != "BYTE_LIMIT" {
		fmt.Fprintln(diagnostic, "Log read incomplete:", page.Code)
		return true, 5
	}
	return true, 0
}
func consumeLogStream(ctx context.Context, in io.Reader, jsonMode bool, out, diagnostic io.Writer) int {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	used := 0
	id := ""
	seq := uint64(0)
	ended := func(code string) int {
		if errors.Is(ctx.Err(), context.Canceled) {
			if jsonMode {
				_ = json.NewEncoder(out).Encode(logMessage{SchemaVersion: 1, LogEvent: protocol.LogEvent{RequestID: id, Kind: "end", Code: "CANCELED", Gap: true}})
			}
			fmt.Fprintln(diagnostic, "Log stream stopped.")
			return 0
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = "TIME_LIMIT"
		}
		if jsonMode {
			_ = json.NewEncoder(out).Encode(logMessage{SchemaVersion: 1, LogEvent: protocol.LogEvent{RequestID: id, Kind: "end", Code: code, Gap: true}})
		}
		fmt.Fprintln(diagnostic, "Log stream interrupted; continuity is not guaranteed:", code)
		return 5
	}
	for scanner.Scan() {
		used += len(scanner.Bytes())
		if used > 8<<20 {
			return ended("BYTE_LIMIT")
		}
		var message logMessage
		if json.Unmarshal(scanner.Bytes(), &message) != nil || message.SchemaVersion != 1 {
			return ended("INVALID_SEQUENCE")
		}
		if id == "" {
			id = message.RequestID
		}
		if message.RequestID != id {
			return ended("INVALID_SEQUENCE")
		}
		if message.Kind == "keepalive" {
			if (protocol.LogCancel{RequestID: message.RequestID}).Validate() != nil || message.Record != nil || message.Code != "" || message.Sequence != 0 || message.Gap {
				return ended("INVALID_SEQUENCE")
			}
			if jsonMode {
				if json.NewEncoder(out).Encode(message) != nil {
					return 4
				}
			}
			continue
		}
		if message.Sequence == 0 && message.Kind == "end" && message.Gap && protocol.LogCode(message.Code) {
			if jsonMode {
				_ = json.NewEncoder(out).Encode(message)
			}
			fmt.Fprintln(diagnostic, "Log stream ended:", message.Code)
			return 5
		}
		if message.Validate() != nil || message.Sequence != seq+1 || (seq == 0 && message.Kind == "record") {
			return ended("INVALID_SEQUENCE")
		}
		seq = message.Sequence
		if jsonMode {
			if json.NewEncoder(out).Encode(message) != nil {
				return 4
			}
		} else if message.Kind == "start" {
			fmt.Fprintln(diagnostic, "Following container logs; Ctrl+C stops reading.")
		} else if message.Record != nil {
			if _, err := fmt.Fprintf(out, "%s [%s] %s\n", message.Record.Timestamp, message.Record.Stream, safeLogText(message.Record.Text)); err != nil {
				return 4
			}
			if message.Record.Truncated {
				fmt.Fprintln(diagnostic, "A log record was truncated.")
			}
		}
		if message.Kind == "end" {
			if message.Code == "OK" && !message.Gap {
				if !jsonMode {
					fmt.Fprintln(diagnostic, "Log stream ended.")
				}
				return 0
			}
			fmt.Fprintln(diagnostic, "Log stream ended; continuity is not guaranteed:", message.Code)
			return 5
		}
	}
	return ended("SESSION_LOST")
}
