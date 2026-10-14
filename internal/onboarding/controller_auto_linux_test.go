package onboarding

import (
	"context"
	"errors"
	"fmt"
	"hlcp/internal/localapi"
	"hlcp/internal/setup"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type countedRandom struct {
	calls int
	fail  bool
	next  byte
}

func (r *countedRandom) Read(data []byte) (int, error) {
	r.calls++
	if r.fail {
		return 0, errors.New("SECRET RNG")
	}
	for i := range data {
		data[i] = r.next
		r.next++
	}
	return len(data), nil
}
func autoFixture(t *testing.T) (string, setup.Owner, ControllerOptions, ImportedTLS) {
	t.Helper()
	return filepath.Join(t.TempDir(), "controller"), setup.Owner{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())},
		ControllerOptions{ListenAddress: "127.0.0.1:9443", ServerName: "controller.test"}, controllerMaterial(t)
}
func TestAutoControllerNoRNGUntilConsentAndPreservedRepeat(t *testing.T) {
	dir, owner, options, material := autoFixture(t)
	options.AdmittedNodes = []string{"debian-01"}
	plan, err := PlanControllerAuto(context.Background(), dir, owner, options, material, testNow)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	rng := &countedRandom{}
	plan.random = rng
	_ = plan.Preview()
	_ = fmt.Sprintf("%+v %#v", plan, *plan)
	if _, err := plan.Apply(context.Background(), false, testNow); !errors.Is(err, setup.ErrConsentRequired) {
		t.Fatal("consent")
	}
	if rng.calls != 0 {
		t.Fatal("preview/cancel generated")
	}
	if _, err := plan.preview.Apply(context.Background(), true); err == nil {
		t.Fatal("stand-in plan applicable")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("preview writes")
	}
	options.AdmittedNodes[0] = "tampered"
	result, err := plan.Apply(context.Background(), true, testNow)
	if err != nil || result.Changed != 9 || rng.calls != 2 {
		t.Fatalf("%+v %v calls=%d", result, err, rng.calls)
	}
	api, _ := localapi.LoadToken(filepath.Join(dir, "api.token"))
	observer, _ := localapi.LoadToken(filepath.Join(dir, "observer.token"))
	if len(api) != 64 || api == observer || api == strings.Repeat("0", 64) || observer == strings.Repeat("1", 64) {
		t.Fatal("placeholder/token generation")
	}
	before, _ := os.ReadFile(filepath.Join(dir, "nodes.json"))
	if string(before) != `["debian-01"]` {
		t.Fatal("caller mutated admission")
	}
	options.AdmittedNodes = nil
	again, err := PlanControllerAuto(context.Background(), dir, owner, options, material, testNow)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	failRNG := &countedRandom{fail: true}
	again.random = failRNG
	if result, err := again.Apply(context.Background(), true, testNow); err != nil || result.Changed != 0 || failRNG.calls != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	nowAPI, _ := localapi.LoadToken(filepath.Join(dir, "api.token"))
	after, _ := os.ReadFile(filepath.Join(dir, "nodes.json"))
	if nowAPI != api || string(before) != string(after) {
		t.Fatal("credentials/admission rotated")
	}
}
func TestAutoControllerRNGFailureAndRacedDirectoryHaveNoTokenWrites(t *testing.T) {
	dir, owner, options, material := autoFixture(t)
	plan, err := PlanControllerAuto(context.Background(), dir, owner, options, material, testNow)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	plan.random = &countedRandom{fail: true}
	if _, err := plan.Apply(context.Background(), true, testNow); err == nil {
		t.Fatal("RNG failure")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("RNG failure mutated directory")
	}
	plan.random = &countedRandom{}
	os.Mkdir(dir, 0700)
	os.WriteFile(filepath.Join(dir, "manual"), []byte("PRIVATE"), 0600)
	if _, err := plan.Apply(context.Background(), true, testNow); !errors.Is(err, setup.ErrStalePlan) {
		t.Fatalf("%v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "manual"))
	if string(data) != "PRIVATE" {
		t.Fatal("racer changed")
	}
	if _, err := os.Stat(filepath.Join(dir, "api.token")); !os.IsNotExist(err) {
		t.Fatal("raced directory adopted")
	}
}
func TestAutoControllerPartialCredentialsRefused(t *testing.T) {
	dir, owner, options, material := autoFixture(t)
	os.Mkdir(dir, 0700)
	os.WriteFile(filepath.Join(dir, "api.token"), []byte(strings.Repeat("a", 64)+"\n"), 0600)
	if _, err := PlanControllerAuto(context.Background(), dir, owner, options, material, testNow); !errors.Is(err, setup.ErrRecoveryRequired) {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "observer.token")); !os.IsNotExist(err) {
		t.Fatal("partial credentials repaired")
	}
}
