package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hlcp/internal/buildinfo"
	"hlcp/internal/cli"
	"hlcp/internal/doctor"
	"hlcp/internal/localapi"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const defaultAPI = "http://127.0.0.1:9444"

var apiTokenFile = localapi.DefaultTokenFile

var outputMode string
var operationIntent string

func main() {
	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		os.Exit(doctor.CLI(os.Args[2:], "controller", os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "configure" {
		if err := configure(os.Args[2:], os.Stdin, os.Stdout); err != nil {
			exitError(err, 2)
		}
		return
	}
	request, err := cli.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "Use --help for usage.")
		os.Exit(2)
	}
	if request.Help {
		printHelp()
		return
	}
	outputMode = request.Output
	if request.Version {
		if outputMode == "json" {
			output(buildinfo.Current())
		} else {
			fmt.Println(buildinfo.String())
		}
		return
	}
	if err := applyClientConfig(&request); err != nil {
		exitError(err, 2)
	}
	operationIntent = cli.Intent(request)
	if request.DryRun {
		operationIntent = "resolve"
	}
	api := strings.TrimRight(request.API, "/")
	if api == "" {
		api = defaultAPI
	}
	if err := localapi.ValidateURL(api); err != nil {
		exitError(err, 2)
	}
	if request.APITokenFile != "" {
		apiTokenFile = request.APITokenFile
	}
	switch cli.Intent(request) {
	case "result":
		get(api + "/v1/nodes/" + url.PathEscape(request.Node) + "/commands/" + url.PathEscape(request.CommandID))
	case "nodes":
		get(api + "/v1/nodes")
	case "node":
		get(api + "/v1/nodes/" + url.PathEscape(request.Node))
	case "node-status":
		if request.DryRun {
			post(api+"/v1/nodes/"+url.PathEscape(request.Node)+"/resolve", map[string]string{"action": "status"})
		} else {
			get(api + "/v1/nodes/" + url.PathEscape(request.Node))
		}
	case "node-command":
		post(api+"/v1/nodes/"+url.PathEscape(request.Node)+"/resolve", map[string]string{"action": request.Action})
	case "node-actions":
		get(api + "/v1/nodes/" + url.PathEscape(request.Node) + "/actions")
	case "resources":
		get(api + "/v1/nodes/" + url.PathEscape(request.Node) + "/resources")
	case "actions":
		get(api + "/v1/nodes/" + url.PathEscape(request.Node) + "/resources/" + url.PathEscape(request.Resource) + "/actions")
	case "resource":
		get(api + "/v1/nodes/" + url.PathEscape(request.Node) + "/resources/" + url.PathEscape(request.Resource))
	case "command":
		id := request.CommandID
		if id == "" {
			id = randomID()
		}
		command := map[string]string{"command_id": id, "resource_id": request.Resource, "action": request.Action}
		if request.DryRun {
			post(api+"/v1/nodes/"+url.PathEscape(request.Node)+"/resolve", command)
			return
		}
		fmt.Fprintf(os.Stderr, "command_id: %s\n", id)
		fmt.Fprintf(os.Stderr, "Lookup: hlcp --node %q --result %q (use the same API/token options)\n", request.Node, id)
		post(api+"/v1/nodes/"+url.PathEscape(request.Node)+"/commands", command)
	default:
		fmt.Printf("hlcp: %s\n", cli.Intent(request))
	}
}

func get(url string) {
	response, err := authenticatedRequest(http.MethodGet, url, nil)
	if err != nil {
		fail(err)
	}
	defer response.Body.Close()
	outputResponse(response)
}

func post(url string, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		fail(err)
	}
	response, err := authenticatedRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		fail(err)
	}
	defer response.Body.Close()
	outputResponse(response)
}

func authenticatedRequest(method, address string, body io.Reader) (*http.Response, error) {
	token, err := localapi.LoadToken(apiTokenFile)
	if err != nil {
		return nil, fmt.Errorf("%w; run hlcp configure --help or specify --api-token-file", err)
	}
	request, err := http.NewRequest(method, address, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 35 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("local API redirects are not supported") }}
	return client.Do(request)
}

func outputResponse(response *http.Response) {
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil {
		fail(err)
	}
	if len(data) > 2<<20 {
		exitError(fmt.Errorf("invalid or oversized controller response"), 4)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = response.Status
		}
		code := 4
		if response.StatusCode >= 400 && response.StatusCode < 500 {
			code = 2
		}
		var detail struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(data, &detail)
		if response.StatusCode == 504 || detail.Code == "ACK_TIMEOUT" || detail.Code == "RESULT_TIMEOUT" || detail.Code == "COMMAND_ALREADY_TRACKED" || detail.Code == "CANCELED" {
			code = 5
		}
		exitError(fmt.Errorf("controller API: %s", message), code)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		exitError(err, 4)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		exitError(fmt.Errorf("expected exactly one controller response"), 4)
	}
	output(value)
	if code := resultExit(value, operationIntent); code != 0 {
		exitError(fmt.Errorf("command failed or is unresolved; inspect the result before retrying with the same ID"), code)
	}
}

func output(value any) {
	if outputMode != "json" {
		if err := renderText(os.Stdout, value); err != nil {
			fail(err)
		}
		return
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fail(err)
	}
	fmt.Println(string(encoded))
}

func randomID() string {
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err != nil {
		fail(err)
	}
	return hex.EncodeToString(identifier)
}

func fail(err error) {
	code := 4
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		code = 5
	}
	exitError(err, code)
}

func exitError(err error, code int) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(code)
}

func printHelp() {
	fmt.Print(`HLCP — HomeLab Control Plane

Progressive navigation:
  hlcp -n
  hlcp -n debian-01 -a
  hlcp -n debian-01 -a status
  hlcp -n debian-01 -r
  hlcp -n debian-01 -r example-catalog -a
  hlcp -n debian-01 -r example-catalog -a restart
  hlcp -n debian-01 --result <command-id>

Explicit forms:
  --nodes | --node <id>
  --resources | --resource <id>
  --actions | --action <name>

Options:
  -n, -r, -a          navigational selectors; value is optional
  -h, --help          show this help
  --version           print build metadata without contacting the API
  --dry-run           validate without sending a command
  --json              machine-readable output
  --command-id <id>   stable ID for a resource action (otherwise generated)
  --result <id>       read-only result lookup; requires --node / -n <id>
  --api <url>         controller loopback API (default: ` + defaultAPI + `)
  --api-token-file    private bearer token file (default: /etc/hlcp/api.token)
  --config <file>     explicit client JSON configuration (otherwise user hlcp/cli.json)

First-time client setup (run as the actual CLI user, not sudo):
  hlcp configure --api-token-file <private-token-file>
  hlcp configure --token-stdin
  hlcp configure --help
  hlcp doctor --help
User configuration supplies API/token defaults; explicit command-line options win.
Configure does not grant access, change controller policy, or start services.

Selectors cannot be repeated or combined with their singular/plural alternatives.
Resource/action discovery requires a selected node. Long singular forms require values.
Default output is text; --json preserves API JSON on stdout. Diagnostics and the
command ID before submission go to stderr. Dry-run validates a snapshot only.
Action names come from the resource inventory (including custom script actions).
Task actions are not assumed read-only, even when named status.
Timeout does not prove an action did not happen: look up the ID; do not issue a new ID.
Exit codes: 0 success, 2 invalid/rejected request, 3 failed action,
            4 connection/API error, 5 timeout or unresolved operation.
`)
}
