package doctor

import (
	"bytes"
	"strings"
	"testing"
)

type systemFixture struct {
	*fakeHost
	account    Account
	synced     bool
	properties map[string]map[string]string
	queries    []Query
}

func (h *systemFixture) ClockSynced() (bool, error) { return h.synced, nil }
func (h *systemFixture) Account() (Account, error)  { return h.account, nil }
func (h *systemFixture) Properties(q Query) (map[string]string, error) {
	h.queries = append(h.queries, q)
	p, ok := h.properties[q.Kind]
	if !ok {
		return nil, errProbe
	}
	return p, nil
}
func goodSystem(t *testing.T) *systemFixture {
	return &systemFixture{fakeHost: fixture(t, true, testNow.AddDate(1, 0, 0)),
		account: Account{"martin", "1000", "1000"}, synced: true,
		properties: map[string]map[string]string{
			"unit":    {"LoadState": "loaded", "ActiveState": "active", "UnitFileState": "enabled", "FragmentPath": "/usr/lib/systemd/user/hlcp-agent.service", "DropInPaths": ""},
			"manager": {"ActiveState": "active"}, "linger": {"Linger": "yes"},
		}}
}
func hostReport(h Host, options Options, podman bool) Report {
	r := Report{}
	checkSystem(h, options, podman, func(code, state, message string) { r.Checks = append(r.Checks, Check{code, state, message}) })
	return r
}
func TestUserSystemReadiness(t *testing.T) {
	host := goodSystem(t)
	host.files["/etc/subuid"] = []byte("martin:100000:65536\n")
	host.files["/etc/subgid"] = []byte("1000:100000:65536\n")
	report := hostReport(host, Options{Role: "agent", Scope: "user"}, true)
	if report.Failed() {
		t.Fatalf("%+v", report)
	}
	for _, code := range []string{"host.clock", "host.account", "host.unit.active", "host.linger", "host.user_manager", "host.subuid", "host.subgid"} {
		if stateFor(report, code) != "ok" {
			t.Fatal(code)
		}
	}
	if len(host.queries) != 3 || host.queries[0].Unit != "hlcp-agent.service" {
		t.Fatal("queries")
	}
	host.account.UID = "0"
	host.queries = nil
	if !hostReport(host, Options{Role: "agent", Scope: "user"}, true).Failed() || len(host.queries) != 0 {
		t.Fatal("root user probing")
	}
}
func TestWarningsAndMasks(t *testing.T) {
	host := goodSystem(t)
	host.synced = false
	host.properties["unit"]["LoadState"] = "masked"
	host.properties["unit"]["ActiveState"] = "failed"
	host.properties["unit"]["DropInPaths"] = "/SECRET/drop-in.conf"
	host.properties["linger"]["Linger"] = "no"
	delete(host.properties, "manager")
	report := hostReport(host, Options{Role: "agent", Scope: "user", Unit: "hlcp-agent-podman.service"}, false)
	if !report.Failed() || stateFor(report, "host.clock") != "warn" || stateFor(report, "host.linger") != "warn" ||
		stateFor(report, "host.unit.overrides") != "warn" || stateFor(report, "host.user_manager") != "warn" {
		t.Fatalf("%+v", report)
	}
	for _, check := range report.Checks {
		if strings.Contains(check.Message, "SECRET") {
			t.Fatal("path leaked")
		}
	}
	if len(host.reads) != 0 {
		t.Fatal("generic runtime read subordinate IDs")
	}
}

func TestRuntimeEnablementIsNotPersistent(t *testing.T) {
	host := goodSystem(t)
	host.properties["unit"]["UnitFileState"] = "enabled-runtime"
	if stateFor(hostReport(host, Options{Role: "agent", Scope: "user"}, false), "host.unit.enabled") != "warn" {
		t.Fatal("runtime-only enablement reported as persistent")
	}
}
func TestSubordinateBoundaries(t *testing.T) {
	account := Account{"martin", "1000", "1000"}
	if ValidSubordinateRange([]byte("martin:100000:65536\n"+strings.Repeat("x", MaximumFileSize)), account) {
		t.Fatal("unbounded mapping accepted")
	}
	for _, row := range []struct {
		data string
		good bool
	}{
		{"martin:100000:65536", true}, {"1000:100000:65536", true},
		{"martin:100000:65535", false}, {"martin:4294967295:65536", false},
		{"other:100000:65536", false}, {"martin:0:65536", false},
		{"martin:-1:65536", false}, {"martin:100000:bad", false},
	} {
		if validSubordinateRange([]byte(row.data), account) != row.good {
			t.Fatal(row.data)
		}
	}
}
func TestStrictPropertyParser(t *testing.T) {
	for _, data := range []string{"Linger=yes\nLinger=no", "SECRET=yes", "Linger", strings.Repeat("x", maximumProbeOutput+1), ""} {
		if _, err := parseProperties([]byte(data), []string{"Linger"}); err == nil {
			t.Fatal("accepted malformed output")
		}
	}
	if _, err := parseProperties([]byte("Linger=yes\n"), []string{"Linger"}); err != nil {
		t.Fatal(err)
	}
}
func TestCLIHostOptInAndScope(t *testing.T) {
	host := goodSystem(t)
	host.files["/config"] = []byte(minimalAgent)
	var out bytes.Buffer
	if runCLI([]string{"--config", "/config"}, "agent", &out, host, testNow) != 0 || len(host.queries) != 0 {
		t.Fatal("implicit host queries")
	}
	out.Reset()
	if runCLI([]string{"--config", "/config", "--host-checks", "--scope", "user", "--json"}, "agent", &out, host, testNow) != 0 || len(host.queries) != 3 {
		t.Fatalf("host checks: %s", out.String())
	}
	for _, args := range [][]string{{"--role", "controller", "--scope", "user"}, {"--scope", "SECRET"}, {"--unit", "restart"}} {
		out.Reset()
		if runCLI(args, "agent", &out, host, testNow) != 2 || strings.Contains(out.String(), "SECRET") {
			t.Fatal("invalid options")
		}
	}
}
