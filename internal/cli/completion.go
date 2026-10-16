package cli

import "strings"

type CompletionLookup func(kind, node, resource string) []string

func CompletionWord(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return value[0] != '-'
}

// Incomplete shell input is not parsed as a runnable request. Only known
// selectors and transport values are extracted, never actions dispatched.
func CompletionRequest(args []string) (Request, bool) {
	r := Request{}
	if len(args) == 0 || len(args) > 64 {
		return r, false
	}
	total := 0
	for _, a := range args {
		total += len(a)
		if len(a) > 512 || total > 8192 || strings.ContainsAny(a, "\x00\n\r") {
			return r, false
		}
	}
	for i := 0; i < len(args)-1; i++ {
		var target *string
		switch args[i] {
		case "-n", "--node":
			target = &r.Node
		case "-r", "--resource":
			target = &r.Resource
		case "-a", "--action":
			target = &r.Action
		case "--api":
			target = &r.API
		case "--api-token-file":
			target = &r.APITokenFile
		case "--config":
			target = &r.ConfigFile
		case "--tail", "--since", "--until", "--contains", "--stream", "--cursor", "--limit", "--command-id", "--result", "--scope", "--unit", "--adapter", "--output-file":
			if i+1 < len(args)-1 {
				i++
			}
			continue
		}
		if target != nil && i+1 < len(args)-1 {
			i++
			*target = args[i]
		}
	}
	return r, true
}

func CompletionCandidates(command string, args []string, lookup CompletionLookup) []string {
	r, ok := CompletionRequest(args)
	if !ok {
		return nil
	}
	prefix := args[len(args)-1]
	words := args[:len(args)-1]
	prev := ""
	for i := 0; i < len(words); i++ {
		prev = words[i]
		switch words[i] {
		case "-n", "--node", "-r", "--resource", "-a", "--action", "--api", "--api-token-file", "--config", "--tail", "--since", "--until", "--contains", "--stream", "--cursor", "--limit", "--command-id", "--result", "--scope", "--unit", "--adapter", "--output-file":
			if i+1 < len(words) {
				i++
				prev = ""
			}
		}
	}
	candidates := []string{}
	add := func(text string) { candidates = append(candidates, strings.Fields(text)...) }
	dynamic := func(kind string) {
		if lookup != nil {
			candidates = append(candidates, lookup(kind, r.Node, r.Resource)...)
		}
	}
	switch prev {
	case "--scope":
		add("user system")
	case "--unit":
		if command == "hlcp-agent" {
			add("hlcp-agent.service hlcp-agent-podman.service")
		}
	case "--adapter":
		add("docker podman")
	case "--stream":
		add("both stdout stderr")
	case "completion":
		add("bash")
	case "--api", "--config", "--api-token-file", "--tail", "--since", "--until", "--contains", "--cursor", "--limit", "--command-id", "--result", "--output-file":
		return nil
	case "-n", "--node":
		if command == "hlcp" {
			dynamic("nodes")
		}
	case "-r", "--resource":
		if command == "hlcp" && CompletionWord(r.Node) {
			dynamic("resources")
		}
	case "-a", "--action":
		if command == "hlcp" && CompletionWord(r.Node) {
			if CompletionWord(r.Resource) {
				dynamic("actions")
			} else {
				add("status")
			}
		}
	default:
		if command == "hlcp" {
			if len(words) == 0 {
				add("configure pairing certificates doctor verify-release package-backup completion")
			}
			if len(words) == 1 && words[0] == "pairing" {
				add("create list revoke")
			}
			admin := false
			if len(words) > 0 {
				switch words[0] {
				case "configure":
					add("--help --api --api-token-file --token-stdin --config")
					admin = true
				case "pairing":
					add("--help --api --api-token-file --config --json --node --replace --yes --output-file")
					admin = true
				case "certificates":
					add("--help --api --api-token-file --config --json")
					admin = true
				case "doctor":
					add("--help --json --config --scope --unit --host-checks --probe")
					admin = true
				case "verify-release":
					add("--help --directory --version --role --arch")
					admin = true
				case "package-backup":
					add("--help --json --role --scope --output --stopped-confirmed")
					admin = true
				case "completion":
					admin = true
				}
			}
			if !admin {
				add("-n -r -a --node --nodes --resource --resources --action --actions --result --command-id --api --api-token-file --config --json --dry-run --help --version --tail --since --until --contains --stream --cursor --limit --follow")
			}
		} else if command == "hlcp-agent" {
			if len(words) == 0 {
				add("join configure check-config resources certificates prepare-user setup start restart stop status probe-resources doctor completion")
			}
			if len(words) == 1 && words[0] == "resources" {
				add("list discover")
			}
			add("--help")
			if len(words) > 0 {
				switch words[0] {
				case "start":
					add("--json --scope --unit --enable --dry-run")
				case "restart", "stop":
					add("--json --scope --unit --dry-run")
				case "status":
					add("--json --scope --unit")
				case "configure", "check-config":
					add("--json --scope")
				case "resources":
					add("--json --scope --adapter")
				case "join":
					add("--json --scope --unit --pairing-file --pairing-stdin --resume --re-pair --offline-stopped --start --enable --apply --yes")
				case "certificates":
					add("--json --scope --apply --yes")
				case "probe-resources":
					add("--json --scope --unit --start --enable --apply --yes")
				case "prepare-user":
					add("--json --apply --yes")
				case "doctor":
					add("--json --config --scope --unit --host-checks --probe")
				}
			} else {
				add("--version --config --check-config")
			}
		} else if command == "hlcp-controller" {
			if len(words) == 0 {
				add("setup pki configure-client activate doctor completion --version")
			}
			if len(words) == 1 && words[0] == "pki" {
				add("renew-issuer finish-migration")
			}
			add("--help")
			if len(words) > 0 {
				switch words[0] {
				case "setup":
					add("--json --apply --yes --imported --user --start --enable")
				case "activate":
					add("--json --apply --yes --enable --server-name")
				case "pki":
					add("--json --apply --yes --backup-file --password-stdin")
				case "configure-client":
					add("--json --apply --yes --user")
				case "doctor":
					add("--json --config --host-checks --probe")
				}
			}
		}
	}
	result := []string{}
	seen := map[string]bool{}
	for _, c := range candidates {
		safe := CompletionWord(c) || strings.HasPrefix(c, "-") && safeCompletionFlag(c)
		if safe && strings.HasPrefix(c, prefix) && !seen[c] {
			result = append(result, c)
			seen[c] = true
			if len(result) == 256 {
				break
			}
		}
	}
	return result
}
func safeCompletionFlag(value string) bool {
	if len(value) < 2 || len(value) > 64 {
		return false
	}
	return CompletionWord(strings.TrimLeft(value, "-"))
}
