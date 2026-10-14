package onboarding

import (
	"context"
	"errors"
	"fmt"
	"hlcp/internal/localapi"
	"hlcp/internal/setup"
	"hlcp/internal/transport"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControllerPlanConsentRepeatAndNoAdmissionDefaults(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "controller")
	owner := setup.Owner{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}
	options := ControllerOptions{ListenAddress: "127.0.0.1:9443", ServerName: "controller.test"}
	material := controllerMaterial(t)
	credentials := controllerCredentials(t)
	plan, err := PlanController(context.Background(), directory, owner, options, material, credentials, testNow)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("preview writes")
	}
	if len(plan.Preview().Changes) != 10 {
		t.Fatal("directory + nine file preview")
	}
	if _, err := plan.Apply(context.Background(), false); !errors.Is(err, setup.ErrConsentRequired) {
		t.Fatal("consent")
	}
	result, err := plan.Apply(context.Background(), true)
	if err != nil || result.Changed != 9 {
		t.Fatalf("%+v %v", result, err)
	}
	policy, err := localapi.LoadPolicy(filepath.Join(directory, "api-policy.json"))
	if err != nil || policy == nil {
		t.Fatalf("runtime policy: %v", err)
	}
	nodes, err := transport.LoadAllowedNodes(filepath.Join(directory, "nodes.json"))
	if err != nil || len(nodes) != 0 {
		t.Fatal("automatic admission")
	}
	api, err := localapi.LoadToken(filepath.Join(directory, "api.token"))
	if err != nil || api != strings.TrimSpace(string(credentials.operator)) {
		t.Fatal("operator token")
	}
	again, err := PlanController(context.Background(), directory, owner, options, material, credentials, testNow)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if result, err := again.Apply(context.Background(), true); err != nil || result.Changed != 0 {
		t.Fatal("repeat")
	}
	original, _ := os.ReadFile(filepath.Join(directory, "api.token"))
	changed := controllerCredentials(t)
	changed.operator = []byte(strings.Repeat("c", 64) + "\n")
	if _, err := PlanController(context.Background(), directory, owner, options, material, changed, testNow); !errors.Is(err, setup.ErrExistingConflict) {
		t.Fatalf("silent rotation: %v", err)
	}
	actual, _ := os.ReadFile(filepath.Join(directory, "api.token"))
	if string(actual) != string(original) {
		t.Fatal("token changed")
	}
	for _, file := range []string{"controller.crt", "controller.key", "ca.crt", "issuer-ca.crt", "controller.env", "api-policy.json", "api.token", "observer.token", "nodes.json"} {
		info, _ := os.Stat(filepath.Join(directory, file))
		if info.Mode().Perm() != 0600 {
			t.Fatal("private modes")
		}
	}
	for _, file := range []string{"controller-operations.json", "ledger.json", "cli.json"} {
		if _, err := os.Stat(filepath.Join(directory, file)); !os.IsNotExist(err) {
			t.Fatal("unplanned file")
		}
	}
	report := fmt.Sprintf("%+v %#v", plan, plan)
	if strings.Contains(report, "BEGIN") || strings.Contains(report, strings.Repeat("a", 32)) {
		t.Fatal("private plan leaked")
	}
}

func TestControllerAdmissionMustBeExplicitAndStable(t *testing.T) {
	directory := t.TempDir()
	os.Chmod(directory, 0700)
	owner := setup.Owner{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}
	options := ControllerOptions{ListenAddress: "127.0.0.1:9443", ServerName: "controller.test", AdmittedNodes: []string{"z-node", "a-node"}}
	material := controllerMaterial(t)
	credentials := controllerCredentials(t)
	plan, err := PlanController(context.Background(), directory, owner, options, material, credentials, testNow)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	if _, err := plan.Apply(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(directory, "nodes.json"))
	if string(data) != `["a-node","z-node"]` {
		t.Fatalf("%s", data)
	}
	options.AdmittedNodes = []string{"a-node", "a-node"}
	if _, err := PlanController(context.Background(), directory, owner, options, material, credentials, testNow); !errors.Is(err, ErrControllerOptions) {
		t.Fatal("duplicates accepted")
	}
}
