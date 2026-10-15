package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGroupTrustNeverAcceptsRootOrAnotherGroup(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("fixture chown only")
	}
	parent := filepath.Join(t.TempDir(), "parent")
	if err := os.Mkdir(parent, 0771); err != nil {
		t.Fatal(err)
	}
	owner := Owner{UID: 12345, GID: 12345}
	for _, fixture := range []struct {
		uid, gid int
		mode     os.FileMode
		want     bool
	}{
		{12345, 12345, 0771, true}, {12345, 54321, 0771, false}, {0, 12345, 0771, false}, {12345, 12345, 0777, false}, {54321, 12345, 0771, false},
	} {
		if os.Chown(parent, fixture.uid, fixture.gid) != nil || os.Chmod(parent, fixture.mode) != nil {
			t.Fatal("fixture permissions")
		}
		f, err := os.Open(parent)
		if err != nil {
			t.Fatal(err)
		}
		got := safeDirectoryParentWithTrust(int(f.Fd()), owner, true)
		f.Close()
		if got != fixture.want {
			t.Fatal("trust escaped primary user/group boundary", fixture)
		}
	}
	if _, err := OpenPlannerTrustingOwnerGroup(parent, Owner{}); err == nil {
		t.Fatal("root credentials gained group exception")
	}
	if _, err := PlanDirectoryTrustingOwnerGroup(filepath.Join(parent, "private"), Owner{}); err == nil {
		t.Fatal("root directory gained group exception")
	}
}
