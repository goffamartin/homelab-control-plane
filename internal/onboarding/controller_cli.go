package onboarding

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hlcp/internal/setup"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type controllerPlan interface {
	Preview() setup.Preview
	Apply(context.Context, bool, time.Time) (setup.ApplyResult, error)
	Close() error
}
type controllerSetupHost interface {
	ReadFile(string, bool) ([]byte, error)
	Owner() (setup.Owner, error)
	ControllerPlan(context.Context, string, setup.Owner, ControllerOptions, ImportedTLS, time.Time) (controllerPlan, error)
}

func (nativeSetupHost) ControllerPlan(ctx context.Context, directory string, owner setup.Owner, options ControllerOptions, material ImportedTLS, now time.Time) (controllerPlan, error) {
	return PlanControllerAuto(ctx, directory, owner, options, material, now)
}

type admittedFlags []string

func (v *admittedFlags) String() string { return "explicit node identities" }
func (v *admittedFlags) Set(value string) error {
	if len(*v) >= 1024 {
		return ErrControllerOptions
	}
	*v = append(*v, value)
	return nil
}

func ControllerCLI(args []string, input io.Reader, out io.Writer) int {
	return runControllerCLI(args, input, out, nativeSetupHost{}, terminalInput(input), time.Now)
}
func runControllerCLI(args []string, input io.Reader, out io.Writer, host controllerSetupHost, tty bool, clock func() time.Time) int {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var directory, cert, key, peer, issuer string
	var options ControllerOptions
	var admitted admittedFlags
	flags.StringVar(&directory, "directory", "/etc/hlcp/controller", "private config directory; safe parent must exist")
	flags.StringVar(&options.ListenAddress, "listen", "", "explicit management IP:port")
	flags.StringVar(&options.ServerName, "server-name", "", "controller certificate DNS/IP")
	flags.StringVar(&cert, "cert", "", "absolute server chain PEM path")
	flags.StringVar(&key, "key", "", "absolute private server key PEM path")
	flags.StringVar(&peer, "peer-ca", "", "absolute agent trust PEM path")
	flags.StringVar(&issuer, "issuer-ca", "", "absolute server issuer trust PEM path")
	flags.Var(&admitted, "admit", "explicit admitted node (repeat); omitted retains existing admission")
	apply := flags.Bool("apply", false, "write confirmed setup")
	yes := flags.Bool("yes", false, "explicit write consent; requires apply")
	interactive := flags.Bool("interactive", false, "TTY metadata/path prompts and confirmation")
	jsonOutput := flags.Bool("json", false, "JSON report")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err = fmt.Fprintln(out, "Usage: hlcp-controller setup --listen IP:port --server-name DNS/IP\n  --cert /server-chain.pem --key /private/server.key\n  --peer-ca /agent-trust.pem --issuer-ca /server-issuer-trust.pem\n  [--directory /etc/hlcp/controller] [--admit node (repeat)]\n  [--json] [--apply --yes] or --interactive\nRequires root. Default: preview, no token generation or writes.\nFresh tokens are generated only after consent; existing tokens/admission are preserved.\nNo service/account/firewall activation, ledger changes or user CLI provisioning.")
			if err != nil {
				return 2
			}
			return 0
		}
		fmt.Fprintln(out, "Invalid controller setup options. Use setup --help.")
		return 2
	}
	fail := func(code string, readOnly bool) int {
		if *jsonOutput {
			_ = json.NewEncoder(out).Encode(setupReport{Status: "failed", Code: code, ReadOnly: readOnly})
		} else {
			fmt.Fprintf(out, "Controller setup failed (%s); preserve state for inspection.\n", code)
		}
		return 1
	}
	if flags.NArg() != 0 || (*yes && !*apply) || (*apply && !*yes) || (*interactive && (*apply || *yes || *jsonOutput)) {
		if *jsonOutput {
			_ = json.NewEncoder(out).Encode(setupReport{Status: "failed", Code: "INVALID_OPTIONS", ReadOnly: true})
		} else {
			fmt.Fprintln(out, "Invalid controller setup options. Use setup --help.")
		}
		return 2
	}
	if *interactive && !tty {
		return fail("TTY_REQUIRED", true)
	}
	owner, err := host.Owner()
	if err != nil || owner.UID != 0 {
		return fail("ROOT_REQUIRED", true)
	}
	if admitted != nil {
		options.AdmittedNodes = append([]string{}, admitted...)
	}
	var reader *bufio.Reader
	if *interactive {
		reader = bufio.NewReaderSize(input, 4096)
		for _, field := range []struct {
			label string
			value *string
		}{
			{"Management IP:port", &options.ListenAddress}, {"Controller certificate DNS/IP", &options.ServerName},
			{"Config directory", &directory}, {"Server cert-chain PEM path", &cert}, {"Private server key PEM path", &key},
			{"Agent trust PEM path", &peer}, {"Server issuer trust PEM path", &issuer},
		} {
			if *field.value != "" {
				continue
			}
			if _, err := fmt.Fprintf(out, "%s: ", field.label); err != nil {
				return 2
			}
			answer, err := readAnswer(reader)
			if err != nil || answer == "" {
				return fail("INVALID_INPUT", true)
			}
			*field.value = answer
		}
	}
	for _, value := range []string{directory, options.ListenAddress, options.ServerName, cert, key, peer, issuer} {
		if value == "" {
			return fail("MISSING_OPTIONS", true)
		}
	}
	var blobs [][]byte
	for _, spec := range []struct {
		path    string
		private bool
	}{{cert, false}, {key, true}, {peer, false}, {issuer, false}} {
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
	plan, err := host.ControllerPlan(context.Background(), directory, owner, options, material, clock())
	if err != nil {
		code := "INVALID_PLAN"
		switch {
		case errors.Is(err, ErrImportedTLS):
			code = "INVALID_TLS"
		case errors.Is(err, setup.ErrRecoveryRequired):
			code = "RECOVERY_REQUIRED"
		case errors.Is(err, setup.ErrExistingConflict):
			code = "EXISTING_CONFLICT"
		case errors.Is(err, setup.ErrMissingParent):
			code = "PARENT_DIRECTORY_REQUIRED"
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
		if _, err := fmt.Fprintln(out, "Validated controller plan (tokens generated only after consent; no service activation):"); err != nil {
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
		if _, err := fmt.Fprint(out, "Type apply to generate missing tokens and write this plan; anything else cancels: "); err != nil {
			return 2
		}
		answer, err := readAnswer(reader)
		if err != nil {
			return fail("INVALID_INPUT", true)
		}
		confirmed = answer == "apply"
	}
	if !confirmed {
		fmt.Fprintln(out, "No files written or tokens generated; use --apply --yes after review.")
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, err := plan.Apply(ctx, true, clock())
	if err != nil {
		return fail("TRANSACTION_INCOMPLETE", false)
	}
	if *jsonOutput {
		if json.NewEncoder(out).Encode(setupReport{Status: "applied", ReadOnly: false, Preview: &preview, Result: &result}) != nil {
			return 2
		}
	} else {
		if _, err := fmt.Fprintf(out, "Controller setup applied (%d files changed). Service/account/user CLI were not activated.\n", result.Changed); err != nil {
			return 2
		}
	}
	return 0
}
