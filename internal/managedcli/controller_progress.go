package managedcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hlcp/internal/managed"
	"hlcp/internal/onboarding"
	"hlcp/internal/setup"
	"io"
	"os"
	"path/filepath"
)

type controllerRequest struct {
	Version        int                   `json:"version"`
	Config         managed.RuntimeConfig `json:"config"`
	State          string                `json:"state"`
	Backup         string                `json:"backup"`
	CLIUser        string                `json:"cli_user"`
	TrustUserGroup bool                  `json:"trust_user_group"`
	Start          bool                  `json:"start"`
	Enable         bool                  `json:"enable"`
}

// Read-only check: existing private destinations must already be safe. Missing
// directories are not created; only their nearest existing parent is inspected.
func preflightPrivateDirectory(path string, uid, gid int) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return ErrSetup
	}
	if info, err := os.Lstat(path); err == nil {
		if checkDirectory(path, info, uid, gid) != nil {
			return ErrSetup
		}
		return safeParent(filepath.Dir(path), uid)
	} else if !os.IsNotExist(err) {
		return ErrSetup
	}
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		if _, err := os.Lstat(parent); err == nil {
			return safeParent(parent, uid)
		} else if !os.IsNotExist(err) {
			return ErrSetup
		}
		if parent == filepath.Dir(parent) {
			return ErrSetup
		}
	}
}

func loadControllerRequest(directory string) (controllerRequest, bool, error) {
	var r controllerRequest
	data, err := readPrivate(filepath.Join(directory, "managed-setup-request.json"), setup.Owner{})
	if err == nil {
		if managed.DecodeJSON(data, &r, 64<<10) != nil || r.Version != 1 || r.Config.Validate() != nil || !filepath.IsAbs(r.State) || filepath.Clean(r.State) != r.State || !filepath.IsAbs(r.Backup) || filepath.Clean(r.Backup) != r.Backup || (r.Enable && !r.Start) {
			return r, false, ErrSetup
		}
		return r, true, nil
	}
	if !os.IsNotExist(err) {
		return r, false, err
	}
	data, err = readPrivate(filepath.Join(directory, "managed-bootstrap.json"), setup.Owner{})
	if os.IsNotExist(err) {
		return r, false, nil
	}
	var b controllerBootstrap
	if err != nil || managed.DecodeJSON(data, &b, 512<<10) != nil || b.Version != 1 || b.Config.Validate() != nil {
		return r, false, ErrSetup
	}
	return controllerRequest{Version: 1, Config: b.Config, State: controllerState, Backup: b.BackupPath}, false, nil
}

func saveControllerRequest(ctx context.Context, s *managed.Store, r controllerRequest) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err := s.Read("managed-setup-request.json"); os.IsNotExist(err) {
		return s.Create(ctx, "managed-setup-request.json", data)
	} else if err != nil {
		return err
	}
	return s.Update(ctx, "managed-setup-request.json", func([]byte) ([]byte, error) { return data, nil })
}

func explainClientIssue(out io.Writer, r onboarding.ClientAccessReport) {
	fmt.Fprintf(out, "CLI access check: %s", r.Code)
	if r.Path != "" {
		fmt.Fprintf(out, " at %q (group ID %d)", r.Path, r.Group)
	}
	fmt.Fprintln(out)
	switch r.Code {
	case "GROUP_WRITABLE_PARENT":
		fmt.Fprintln(out, "The selected user's primary group can modify this parent. Trusting it accepts that risk; no directory permissions will be changed. HLCP's own directory stays 0700 and files 0600.")
	case "WORLD_WRITABLE_PARENT":
		fmt.Fprintln(out, "Anyone can modify this parent; group trust cannot bypass this. Remove other-write permission only if appropriate, or skip CLI access.")
	case "PRIVATE_PROFILE_PERMISSIONS":
		fmt.Fprintln(out, "The HLCP profile itself must belong to the selected user and primary group with mode 0700. Existing files are never automatically chmod/chowned.")
	case "SYMLINK_OR_NON_DIRECTORY":
		fmt.Fprintln(out, "This path component is a symbolic link or not a directory. Group trust does not bypass this check.")
	case "PROFILE_CONFLICT", "PROFILE_PLAN_REFUSED":
		fmt.Fprintln(out, "Existing CLI files conflict, are unsafe or need transaction recovery. Keep them for inspection; do not delete credentials to retry blindly.")
	default:
		fmt.Fprintln(out, "Check the selected account, path ownership, links and existing profile. No user permissions were changed.")
	}
}

func applyControllerClient(user string, trust bool) onboarding.ClientAccessReport {
	args := []string{"--user", user, "--apply", "--yes", "--json"}
	if trust {
		args = append(args, "--trust-user-group")
	}
	var out bytes.Buffer
	onboarding.ClientGrantCLI(args, &out)
	var report onboarding.ClientAccessReport
	if managed.DecodeJSON(out.Bytes(), &report, 32<<10) != nil {
		return onboarding.ClientAccessReport{Status: "failed", Code: "CLIENT_REPORT_INVALID"}
	}
	return report
}
