package onboarding

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// The transport has no arbitrary unit/command/path arguments. Begin starts only
// the fixed inspector with stdin held closed to request data until unit checks.
type sandboxLaunchTransport interface {
	Unit(context.Context) ([]byte, error)
	Begin(context.Context) (sandboxLaunchProcess, error)
}
type sandboxLaunchProcess interface {
	Exchange(context.Context, []byte) ([]byte, error) // successful return means waited exit 0
	Close()                                           // close stdin/reap client; never stop/reset a service or retry launch
}

const sandboxLaunchProperties = "LoadState,ActiveState,Transient,DropInPaths,FragmentPath,MainPID,Type,User,Group,SupplementaryGroups,NoNewPrivileges,PrivateTmp,ProtectHome,ProtectSystem,StateDirectory,StateDirectoryMode,UMask,RestrictAddressFamilies,SystemCallArchitectures,Restart,RuntimeMaxUSec,TimeoutStartUSec,TimeoutStopUSec,KillMode,SendSIGKILL,CollectMode"

func sandboxLaunchArgs() []string {
	return []string{"--system", "--no-ask-password", "--quiet", "--wait", "--pipe", "--collect", "--service-type=exec", "--unit=" + sandboxProbeUnit,
		"--property=User=hlcp", "--property=Group=hlcp", "--property=NoNewPrivileges=yes", "--property=PrivateTmp=yes", "--property=ProtectHome=yes", "--property=ProtectSystem=strict",
		"--property=StateDirectory=hlcp", "--property=StateDirectoryMode=0700", "--property=UMask=0077", "--property=RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6", "--property=SystemCallArchitectures=native",
		"--property=Restart=no", "--property=RuntimeMaxSec=15s", "--property=TimeoutStartSec=3s", "--property=TimeoutStopSec=2s", "--property=KillMode=control-group", "--property=SendSIGKILL=yes",
		"--property=WorkingDirectory=/", "--property=Environment=PATH=/usr/bin:/bin LANG=C LC_ALL=C",
		"--", "/usr/bin/hlcp-agent", "--internal-sandbox-inspector"}
}
func sandboxUnitProperties(data []byte) (map[string]string, error) {
	if len(data) > 16*1024 {
		return nil, ErrSandboxProbeInvalid
	}
	allowed := map[string]bool{}
	for _, key := range strings.Split(sandboxLaunchProperties, ",") {
		allowed[key] = true
	}
	result := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !allowed[key] {
			return nil, ErrSandboxProbeInvalid
		}
		if _, duplicate := result[key]; duplicate {
			return nil, ErrSandboxProbeInvalid
		}
		result[key] = value
	}
	if len(result) != len(allowed) {
		return nil, ErrSandboxProbeInvalid
	}
	return result, nil
}
func sandboxUnitAbsent(p map[string]string) bool {
	return p["LoadState"] == "not-found" && p["ActiveState"] == "inactive" && p["Transient"] == "no" && p["DropInPaths"] == "" && p["FragmentPath"] == "" && p["MainPID"] == "0"
}
func sandboxUnitReady(p map[string]string) bool {
	pid, err := strconv.ParseUint(p["MainPID"], 10, 32)
	if err != nil || pid == 0 || p["FragmentPath"] != "/run/systemd/transient/"+sandboxProbeUnit || p["DropInPaths"] != "" || p["SupplementaryGroups"] != "" {
		return false
	}
	expected := map[string]string{"LoadState": "loaded", "ActiveState": "active", "Transient": "yes", "Type": "exec", "User": "hlcp", "Group": "hlcp", "NoNewPrivileges": "yes", "PrivateTmp": "yes", "ProtectHome": "yes", "ProtectSystem": "strict", "StateDirectory": "hlcp", "StateDirectoryMode": "0700", "UMask": "0077", "SystemCallArchitectures": "native", "Restart": "no", "RuntimeMaxUSec": "15s", "TimeoutStopUSec": "2s", "KillMode": "control-group", "SendSIGKILL": "yes", "CollectMode": "inactive-or-failed"}
	for key, want := range expected {
		if p[key] != want {
			return false
		}
	}
	if p["TimeoutStartUSec"] != "3s" {
		return false
	}
	families := strings.Fields(p["RestrictAddressFamilies"])
	if len(families) != 3 {
		return false
	}
	seen := map[string]bool{}
	for _, family := range families {
		if seen[family] || (family != "AF_UNIX" && family != "AF_INET" && family != "AF_INET6") {
			return false
		}
		seen[family] = true
	}
	return true
}
func launchSandboxInspector(parent context.Context, transport sandboxLaunchTransport, snapshot SandboxProbeSnapshot, inputs SandboxProbeInputs) (SandboxProbeEvidence, error) {
	data, err := encodeSandboxInspectorRequest(snapshot, inputs)
	if err != nil || transport == nil || parent.Err() != nil {
		return SandboxProbeEvidence{}, ErrSandboxProbeInvalid
	}
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	properties, err := transport.Unit(ctx)
	if err != nil {
		return SandboxProbeEvidence{}, ErrSandboxProbeInvalid
	}
	parsed, err := sandboxUnitProperties(properties)
	if err != nil || !sandboxUnitAbsent(parsed) {
		return SandboxProbeEvidence{}, ErrSandboxProbeInvalid
	}
	process, err := transport.Begin(ctx)
	if err != nil {
		return SandboxProbeEvidence{}, ErrSandboxProbeIncomplete
	}
	if process == nil {
		return SandboxProbeEvidence{}, ErrSandboxProbeIncomplete
	}
	defer process.Close()
	// Poll only the new fixed unit. No request bytes reach its inspector until
	// actual sandbox/lifetime properties are verified; conflicts are not repaired.
	ready := false
	for attempt := 0; attempt < 20; attempt++ {
		properties, err = transport.Unit(ctx)
		if err != nil {
			break
		}
		parsed, err = sandboxUnitProperties(properties)
		if err != nil {
			break
		}
		if sandboxUnitReady(parsed) {
			ready = true
			break
		}
		if !sandboxUnitAbsent(parsed) && !(parsed["LoadState"] == "loaded" && parsed["ActiveState"] == "activating" && parsed["Transient"] == "yes" && parsed["DropInPaths"] == "") {
			break
		}
		if !sandboxLaunchPause(ctx) {
			break
		}
	}
	if !ready || ctx.Err() != nil {
		return SandboxProbeEvidence{}, ErrSandboxProbeIncomplete
	}
	output, err := process.Exchange(ctx, data)
	if err != nil || ctx.Err() != nil {
		return SandboxProbeEvidence{}, ErrSandboxProbeIncomplete
	}
	evidence, err := decodeSandboxInspectorResponse(output, snapshot, inputs)
	if err != nil {
		return SandboxProbeEvidence{}, ErrSandboxProbeIncomplete
	}
	evidence.InspectorExited = true
	// --collect is a request, not proof. Verify absence after client wait/reap.
	for attempt := 0; attempt < 20; attempt++ {
		properties, err = transport.Unit(ctx)
		if err != nil {
			break
		}
		parsed, err = sandboxUnitProperties(properties)
		if err != nil {
			break
		}
		if sandboxUnitAbsent(parsed) && ctx.Err() == nil {
			evidence.UnitCollected = true
			return evidence, nil
		}
		if !sandboxLaunchPause(ctx) {
			break
		}
	}
	return SandboxProbeEvidence{}, ErrSandboxProbeIncomplete
}
func sandboxLaunchPause(ctx context.Context) bool {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
