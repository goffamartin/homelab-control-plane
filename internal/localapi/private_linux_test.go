package localapi

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func readACLFixture(uid uint32) []byte {
	data := make([]byte, 44)
	binary.LittleEndian.PutUint32(data, 2)
	for index, entry := range []struct {
		tag, perm uint16
		id        uint32
	}{
		{1, 4, 0xffffffff}, {2, 4, uid}, {4, 0, 0xffffffff},
		{16, 4, 0xffffffff}, {32, 0, 0xffffffff},
	} {
		raw := data[4+index*8 : 12+index*8]
		binary.LittleEndian.PutUint16(raw, entry.tag)
		binary.LittleEndian.PutUint16(raw[2:], entry.perm)
		binary.LittleEndian.PutUint32(raw[4:], entry.id)
	}
	return data
}

func TestSystemdACLStrictShape(t *testing.T) {
	valid := readACLFixture(1001)
	if !systemdReadACL(valid, 1001) {
		t.Fatal("rejected canonical systemd ACL")
	}
	for _, uid := range []uint32{0, 1002} {
		if systemdReadACL(valid, uid) {
			t.Fatal("accepted foreign UID")
		}
	}
	for index := range valid {
		corrupt := append([]byte(nil), valid...)
		corrupt[index] ^= 1
		if systemdReadACL(corrupt, 1001) {
			t.Fatalf("accepted modified byte %d", index)
		}
	}
	if systemdReadACL(valid[:43], 1001) || systemdReadACL(append(valid, make([]byte, 8)...), 1001) {
		t.Fatal("accepted truncated/extra ACL")
	}
}

func TestGroupReadableCredentialStillRejected(t *testing.T) {
	t.Setenv("CREDENTIALS_DIRECTORY", t.TempDir())
	path := filepath.Join(os.Getenv("CREDENTIALS_DIRECTORY"), "api.token")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0440); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadToken(path); err == nil {
		t.Fatal("accepted ordinary group-readable file / spoofed credential directory")
	}
}

func TestCredentialACLChild(t *testing.T) {
	path := os.Getenv("HLCP_ACL_TEST_POLICY")
	if path == "" {
		t.Skip("subprocess-only fixture")
	}
	_, err := LoadPolicy(path)
	if os.Getenv("HLCP_ACL_TEST_EXPECT_REJECT") == "1" {
		if err == nil {
			t.Fatal("accepted unsafe credential ACL")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestActualRootOwnedReadACLPolicy(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root required to reproduce systemd's owner/UID ACL")
	}
	dir := t.TempDir()
	for _, path := range []string{filepath.Dir(dir), dir} {
		if err := os.Chmod(path, 0711); err != nil {
			t.Fatal(err)
		}
	}
	policy := filepath.Join(dir, "api-policy.json")
	for name, contents := range map[string]string{
		"api-policy.json": `{"version":1,"principals":[{"id":"operator","role":"operator","token_file":"api.token","scope":{"all_nodes":true}},{"id":"observer","role":"observer","token_file":"observer.token","scope":{"all_nodes":true}}]}`,
		"api.token":       strings.Repeat("a", 64), "observer.token": strings.Repeat("b", 64),
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(contents), 0400); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Setxattr(path, "system.posix_acl_access", readACLFixture(1001), 0); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0440 {
			t.Fatalf("not real ACL-masked mode 0440: %v", err)
		}
	}
	// The test executable may be under a
	// root-private Go temp tree, so copy it to our explicitly traversable fixture.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(dir, "acl-test")
	if err := os.WriteFile(helper, data, 0755); err != nil {
		t.Fatal(err)
	}
	runChild := func(t *testing.T, reject bool) {
		t.Helper()
		child := exec.Command(helper, "-test.run=^TestCredentialACLChild$")
		child.Env = append(os.Environ(), "HLCP_ACL_TEST_POLICY="+policy, "HLCP_ACL_TEST_EXPECT_REJECT=0")
		if reject {
			child.Env[len(child.Env)-1] = "HLCP_ACL_TEST_EXPECT_REJECT=1"
		}
		child.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 1001, Gid: 1001}}
		if output, err := child.CombinedOutput(); err != nil {
			t.Fatalf("real service UID policy/token ACL loading: %v %s", err, output)
		}
	}
	runChild(t, false)
	for _, name := range []string{"api-policy.json", "api.token", "observer.token"} {
		for _, variant := range []string{"group-readable", "writable-mask", "extra-user", "non-root-owner"} {
			t.Run(name+"/"+variant, func(t *testing.T) {
				path := filepath.Join(dir, name)
				acl := readACLFixture(1001)
				switch variant {
				case "group-readable":
					binary.LittleEndian.PutUint16(acl[22:24], 4)
				case "writable-mask":
					binary.LittleEndian.PutUint16(acl[14:16], 6)
					binary.LittleEndian.PutUint16(acl[30:32], 6)
				case "extra-user":
					extra := make([]byte, 8)
					binary.LittleEndian.PutUint16(extra, 2)
					binary.LittleEndian.PutUint16(extra[2:], 4)
					binary.LittleEndian.PutUint32(extra[4:], 1002)
					acl = append(append(acl[:20:20], extra...), acl[20:]...)
				case "non-root-owner":
					if err := os.Chown(path, 1002, 1002); err != nil {
						t.Fatal(err)
					}
				}
				if err := syscall.Setxattr(path, "system.posix_acl_access", acl, 0); err != nil {
					t.Fatal(err)
				}
				runChild(t, true)
				if err := os.Chown(path, 0, 0); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Setxattr(path, "system.posix_acl_access", readACLFixture(1001), 0); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
