package onboarding

import (
	"context"
	"hlcp/internal/agent"
	"io"
	"os"
	"strconv"
	"strings"
)

type nativeSandboxInspectorHost struct{}

func (nativeSandboxInspectorHost) Identity(ctx context.Context, uid, gid uint32) error {
	if ctx.Err() != nil || uid == 0 || gid == 0 || uint32(os.Getuid()) != uid || uint32(os.Geteuid()) != uid || uint32(os.Getgid()) != gid || uint32(os.Getegid()) != gid {
		return ErrSandboxProbeInvalid
	}
	account, err := serviceActivationAccount("hlcp")
	if err != nil {
		return ErrSandboxProbeInvalid
	}
	fields := strings.Split(account, ":")
	if len(fields) != 7 || fields[2] != strconv.FormatUint(uint64(uid), 10) || fields[3] != strconv.FormatUint(uint64(gid), 10) {
		return ErrSandboxProbeInvalid
	}
	file, err := os.Open("/proc/self/status")
	if err != nil {
		return ErrSandboxProbeInvalid
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil || len(data) > 64*1024 || !inspectorProcessStatus(data, uid, gid) {
		return ErrSandboxProbeInvalid
	}
	return nil
}
func (nativeSandboxInspectorHost) ScriptFiles(ctx context.Context, command agent.ScriptCommandConfig) ([32]byte, error) {
	return (nativeResourceHost{}).ScriptFiles(ctx, command)
}

// Identity/no-new-privileges verification only, not mount sandbox attestation.
// ProtectHome/PrivateTmp/ProtectSystem still need launcher/unit verification.
func inspectorProcessStatus(data []byte, uid, gid uint32) bool {
	if len(data) > 64*1024 || uid == 0 || gid == 0 {
		return false
	}
	selected := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch key {
		case "Uid", "Gid", "Groups", "NoNewPrivs", "CapInh", "CapPrm", "CapEff", "CapAmb":
			if _, duplicate := selected[key]; duplicate {
				return false
			}
			selected[key] = strings.TrimSpace(value)
		}
	}
	if len(selected) != 8 || selected["NoNewPrivs"] != "1" {
		return false
	}
	for _, key := range []string{"CapInh", "CapPrm", "CapEff", "CapAmb"} {
		if selected[key] != "0000000000000000" {
			return false
		}
	}
	for key, want := range map[string]uint32{"Uid": uid, "Gid": gid} {
		fields := strings.Fields(selected[key])
		if len(fields) != 4 {
			return false
		}
		for _, field := range fields {
			if field != strconv.FormatUint(uint64(want), 10) {
				return false
			}
		}
	}
	groups := strings.Fields(selected["Groups"])
	if len(groups) > 1 || (len(groups) == 1 && groups[0] != strconv.FormatUint(uint64(gid), 10)) {
		return false
	}
	return true
}
