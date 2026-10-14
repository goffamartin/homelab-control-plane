package onboarding

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hlcp/internal/buildinfo"
	"hlcp/internal/doctor"
	"hlcp/internal/setup"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type agentPlan interface {
	Preview() setup.Preview
	Apply(context.Context, bool) (setup.ApplyResult, error)
	Close() error
}
type setupHost interface {
	ReadFile(string, bool) ([]byte, error)
	Owner() (setup.Owner, error)
	Plan(context.Context, string, setup.Owner, AgentOptions, ImportedTLS, time.Time) (agentPlan, error)
}
type nativeSetupHost struct{ doctor.NativeHost }

func (nativeSetupHost) Plan(ctx context.Context, directory string, owner setup.Owner, options AgentOptions, material ImportedTLS, now time.Time) (agentPlan, error) {
	return PlanAgent(ctx, directory, owner, options, material, now)
}

type setupReport struct {
	Status   string             `json:"status"`
	Code     string             `json:"code,omitempty"`
	ReadOnly bool               `json:"read_only"`
	Preview  *setup.Preview     `json:"preview,omitempty"`
	Result   *setup.ApplyResult `json:"result,omitempty"`
}

// AgentCLI defaults to preview. Interactive mode requires a real TTY and asks
// only for metadata/file paths, never key contents or passwords.
func AgentCLI(args []string, input io.Reader, out io.Writer) int {
	return runAgentCLIClock(args, input, out, nativeSetupHost{}, terminalInput(input), time.Now)
}

func runAgentCLI(args []string, input io.Reader, out io.Writer, host setupHost, tty bool, now time.Time) int {
	return runAgentCLIClock(args, input, out, host, tty, func() time.Time { return now })
}

func runAgentCLIClock(args []string, input io.Reader, out io.Writer, host setupHost, tty bool, clock func() time.Time) int {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var options AgentOptions
	var directory, cert, key, peerCA, issuerCA, scope string
	flags.StringVar(&options.NodeID, "node", "", "explicit node identity")
	flags.StringVar(&options.ControllerAddress, "controller", "", "controller host:port")
	flags.StringVar(&options.ServerName, "server-name", "", "expected controller DNS/IP")
	flags.StringVar(&options.JournalFile, "journal", "", "absolute journal path outside config directory")
	flags.StringVar(&directory, "directory", "", "private configuration directory; safe parent must exist")
	flags.StringVar(&cert, "cert", "", "absolute client cert-chain PEM path")
	flags.StringVar(&key, "key", "", "absolute private client key PEM path")
	flags.StringVar(&peerCA, "peer-ca", "", "absolute controller trust PEM path")
	flags.StringVar(&issuerCA, "issuer-ca", "", "absolute own leaf issuer trust PEM path")
	flags.StringVar(&scope, "scope", "user", "user (non-root) or system (root)")
	apply := flags.Bool("apply", false, "write validated plan")
	yes := flags.Bool("yes", false, "explicit noninteractive write consent; requires apply")
	interactive := flags.Bool("interactive", false, "TTY metadata/path prompts and explicit write confirmation")
	jsonOutput := flags.Bool("json", false, "JSON report")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err = fmt.Fprintln(out, "Usage: hlcp-agent setup --node ID --controller host:port --server-name DNS/IP\n  --directory /private/config --journal /separate/state/journal.json\n  --cert /client-chain.pem --key /private/client.key\n  --peer-ca /controller-trust.pem --issuer-ca /client-issuer-trust.pem\n  [--scope user|system] [--json] [--apply --yes]\n  Or: hlcp-agent setup --interactive [--scope user|system]\nDefault is read-only preview. A missing final directory is created only after consent; its safe parent must exist.\nExisting target requires mode 0700; permissions are never repaired.\nNo service activation, resources, admission, journal writes or certificate generation.")
			if err != nil {
				return 2
			}
			return 0
		}
		fmt.Fprintln(out, "Invalid setup options. Use setup --help.")
		return 2
	}
	fail := func(code string, readOnly bool) int {
		if *jsonOutput {
			_ = json.NewEncoder(out).Encode(setupReport{Status: "failed", Code: code, ReadOnly: readOnly})
		} else {
			fmt.Fprintf(out, "Setup failed (%s). Existing configuration/state is preserved for inspection.\n", code)
		}
		return 1
	}
	if flags.NArg() != 0 || (scope != "user" && scope != "system") || (*yes && !*apply) ||
		(*apply && !*yes && !*interactive) || (*interactive && (*apply || *yes || *jsonOutput)) {
		if *jsonOutput {
			_ = json.NewEncoder(out).Encode(setupReport{Status: "failed", Code: "INVALID_OPTIONS", ReadOnly: true})
		} else {
			fmt.Fprintln(out, "Invalid setup options. Use setup --help.")
		}
		return 2
	}
	if *interactive && !tty {
		return fail("TTY_REQUIRED", true)
	}
	owner, err := host.Owner()
	if err != nil || (scope == "user" && (owner.UID == 0 || owner.GID == 0)) || (scope == "system" && (owner.UID != 0 || owner.GID != 0)) {
		return fail("ACCOUNT_SCOPE", true)
	}
	var reader *bufio.Reader
	if *interactive {
		reader = bufio.NewReaderSize(input, 4096)
		for _, field := range []struct {
			label string
			value *string
		}{
			{"Node ID", &options.NodeID}, {"Controller host:port", &options.ControllerAddress},
			{"Controller DNS/IP", &options.ServerName}, {"Config directory (safe parent must exist)", &directory},
			{"Journal path outside config directory", &options.JournalFile}, {"Client cert-chain PEM path", &cert},
			{"Private client key PEM path", &key}, {"Controller trust PEM path", &peerCA}, {"Client issuer trust PEM path", &issuerCA},
		} {
			if *field.value != "" {
				continue
			}
			if _, err := fmt.Fprintf(out, "%s: ", field.label); err != nil {
				return 2
			}
			value, err := readAnswer(reader)
			if err != nil || value == "" {
				return fail("INVALID_INPUT", true)
			}
			*field.value = value
		}
	}
	options.AgentVersion = buildinfo.Current().Version
	options.Scope = scope
	for _, value := range []string{options.NodeID, options.ControllerAddress, options.ServerName, options.JournalFile, directory, cert, key, peerCA, issuerCA} {
		if value == "" {
			return fail("MISSING_OPTIONS", true)
		}
	}
	var blobs [][]byte
	for _, spec := range []struct {
		path    string
		private bool
	}{{cert, false}, {key, true}, {peerCA, false}, {issuerCA, false}} {
		data, err := host.ReadFile(spec.path, spec.private)
		if err != nil || len(data) > setup.MaximumFileBytes {
			return fail("UNSAFE_TLS_INPUT", true)
		}
		blobs = append(blobs, data)
	}
	material, err := NewImportedTLS(blobs[0], blobs[1], blobs[2], blobs[3])
	if err != nil {
		return fail("INVALID_TLS", true)
	}
	plan, err := host.Plan(context.Background(), directory, owner, options, material, clock())
	if err != nil {
		code := "INVALID_PLAN"
		switch {
		case errors.Is(err, ErrImportedTLS):
			code = "INVALID_TLS"
		case errors.Is(err, setup.ErrMissingDirectory):
			code = "DIRECTORY_REQUIRED"
		case errors.Is(err, setup.ErrMissingParent):
			code = "PARENT_DIRECTORY_REQUIRED"
		case errors.Is(err, setup.ErrRecoveryRequired):
			code = "DIRECTORY_RECOVERY_REQUIRED"
		case errors.Is(err, setup.ErrExistingConflict):
			code = "EXISTING_CONFLICT"
		case errors.Is(err, setup.ErrInvalidExisting):
			code = "INVALID_EXISTING"
		case errors.Is(err, setup.ErrUnsafePath):
			code = "UNSAFE_TARGET"
		}
		return fail(code, true)
	}
	defer plan.Close()
	preview := plan.Preview()
	if *jsonOutput && !*apply {
		if json.NewEncoder(out).Encode(setupReport{Status: "preview", ReadOnly: true, Preview: &preview}) != nil {
			return 2
		}
		return 0
	}
	if !*jsonOutput {
		if _, err := fmt.Fprintln(out, "Validated agent setup plan (no service activation):"); err != nil {
			return 2
		}
		for _, change := range preview.Changes {
			if _, err := fmt.Fprintf(out, "%s %s %04o\n", change.Action, change.Name, change.Mode); err != nil {
				return 2
			}
		}
	}
	confirmed := *apply && *yes
	if *interactive {
		if _, err := fmt.Fprint(out, "Type apply to write this plan; anything else cancels: "); err != nil {
			return 2
		}
		answer, err := readAnswer(reader)
		if err != nil {
			return fail("INVALID_INPUT", true)
		}
		confirmed = answer == "apply"
		if !confirmed {
			fmt.Fprintln(out, "Cancelled; no files written.")
			return 0
		}
	}
	if !confirmed {
		fmt.Fprintln(out, "Preview only. Use --apply --yes to write after review.")
		return 0
	}
	if _, err := material.Validate(Identity{Role: "agent", NodeID: options.NodeID}, clock()); err != nil {
		return fail("TLS_RECHECK_FAILED", true)
	}
	applyContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, err := plan.Apply(applyContext, true)
	if err != nil {
		return fail("TRANSACTION_INCOMPLETE", false)
	}
	if *jsonOutput {
		if json.NewEncoder(out).Encode(setupReport{Status: "applied", ReadOnly: false, Preview: &preview, Result: &result}) != nil {
			return 2
		}
	} else {
		if _, err := fmt.Fprintf(out, "Setup applied (%d files changed). Service and node admission were not activated.\n", result.Changed); err != nil {
			return 2
		}
	}
	return 0
}

func readAnswer(reader *bufio.Reader) (string, error) {
	data, err := reader.ReadSlice('\n')
	if err != nil || len(data) > 4096 {
		return "", errors.New("invalid input")
	}
	return strings.TrimSpace(string(data)), nil
}
