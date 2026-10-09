package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hlcp/internal/cli"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const defaultAPI = "http://127.0.0.1:9444"

func main() {
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
	api := strings.TrimRight(request.API, "/")
	if api == "" {
		api = defaultAPI
	}
	switch cli.Intent(request) {
	case "nodes":
		get(api + "/v1/nodes")
	case "node", "node-status":
		get(api + "/v1/nodes/" + url.PathEscape(request.Node))
	case "node-actions":
		get(api + "/v1/nodes/" + url.PathEscape(request.Node) + "/actions")
	case "resources":
		get(api + "/v1/nodes/" + url.PathEscape(request.Node) + "/resources")
	case "actions":
		get(api + "/v1/nodes/" + url.PathEscape(request.Node) + "/resources/" + url.PathEscape(request.Resource) + "/actions")
	case "command":
		command := map[string]string{"command_id": randomID(), "resource_id": request.Resource, "action": request.Action}
		if request.DryRun {
			output(command)
			return
		}
		post(api+"/v1/nodes/"+url.PathEscape(request.Node)+"/commands", command)
	default:
		fmt.Printf("hlcp: %s\n", cli.Intent(request))
	}
}

func get(url string) {
	response, err := client().Get(url)
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
	response, err := client().Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		fail(err)
	}
	defer response.Body.Close()
	outputResponse(response)
}

func client() *http.Client { return &http.Client{Timeout: 35 * time.Second} }

func outputResponse(response *http.Response) {
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		messageBytes, _ := io.ReadAll(response.Body)
		message := strings.TrimSpace(string(messageBytes))
		if message == "" {
			message = response.Status
		}
		fail(fmt.Errorf("controller API: %s", message))
	}
	var value any
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		fail(err)
	}
	output(value)
}

func output(value any) {
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
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
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

Explicit forms:
  --nodes | --node <id>
  --resources | --resource <id>
  --actions | --action <name>

Options:
  -n, -r, -a          navigational selectors; value is optional
  -h, --help          show this help
  --dry-run           validate without sending a command
  --json              machine-readable output
  --api <url>         controller loopback API (default: ` + defaultAPI + `)
`)
}
