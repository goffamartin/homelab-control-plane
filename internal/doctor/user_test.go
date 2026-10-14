package doctor

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type userFixture struct {
	*fakeHost
	calls   int
	options Options
	err     error
}

func (h *userFixture) DiagnoseUser(options Options, now time.Time) (Report, error) {
	h.calls++
	h.options = options
	return Report{ReadOnly: true, Offline: true, Checks: []Check{{"host.account", "ok", "Selected account checked."}}}, h.err
}

func TestExplicitUserDispatchBeforeAnyFileReads(t *testing.T) {
	host := &userFixture{fakeHost: &fakeHost{}}
	var out bytes.Buffer
	args := []string{"--user", "martin", "--scope", "user", "--config", "/selected/config", "--json"}
	if runCLI(args, "agent", &out, host, testNow) != 0 || host.calls != 1 || len(host.reads) != 0 ||
		host.options.User != "martin" || host.options.Config != "/selected/config" {
		t.Fatalf("%s", out.String())
	}
	host.err = errors.New("SECRET lookup error")
	out.Reset()
	if runCLI(args, "agent", &out, host, testNow) != 1 || strings.Contains(out.String(), "SECRET") {
		t.Fatal("error leaked")
	}
	host.calls = 0
	for _, invalid := range [][]string{{"--user", "martin"}, {"--role", "controller", "--scope", "user", "--user", "martin"}, {"--user", "martin", "--scope", "user", "--help"}} {
		out.Reset()
		runCLI(invalid, "agent", &out, host, testNow)
		if host.calls != 0 || len(host.reads) != 0 {
			t.Fatal("invalid/help dispatched")
		}
	}
}

func TestUserArgumentAllowlist(t *testing.T) {
	options := Options{User: "SECRET", Role: "agent", Scope: "user", Config: "/cfg", SetupDirectory: "/setup", Unit: "hlcp-agent.service", HostChecks: true, Probe: true,
		Key: "SECRET KEY", Certificate: "SECRET CERT", CA: "SECRET CA"}
	args := userArguments(options)
	want := []string{"doctor", "--role", "agent", "--scope", "user", "--config", "/cfg", "--json", "--unit", "hlcp-agent.service", "--setup-dir", "/setup", "--host-checks", "--probe"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("%v", args)
	}
}

func TestUserReportValidation(t *testing.T) {
	good := Report{ReadOnly: true, Offline: true, Checks: []Check{{"agent.config", "ok", "Valid."}}}
	data, _ := json.Marshal(good)
	if _, err := decodeUserReport(data, 0); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		data []byte
		exit int
	}{
		{data, 1}, {data, 2}, {append(append([]byte{}, data...), []byte("{}")...), 0},
		{[]byte("SECRET stderr"), 0}, {bytes.Repeat([]byte("x"), maximumUserReport+1), 0},
		{[]byte(`{"read_only":false,"checks":[]}`), 0},
		{[]byte(`{"read_only":true,"checks":[{"code":"SECRET","state":"ok","message":""}]}`), 0},
	} {
		if _, err := decodeUserReport(row.data, row.exit); err == nil {
			t.Fatal("invalid child report accepted")
		}
	}
}
