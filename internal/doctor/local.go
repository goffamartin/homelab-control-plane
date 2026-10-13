package doctor

import (
	"errors"
	"hlcp/internal/setup"
)

type ScriptStatus struct {
	Executable bool
	Directory  bool
	Format     string // binary, interpreter, indirect, unknown
}

// LocalHost deliberately offers inspection, never execution or recovery.
type LocalHost interface {
	Script(executable, directory string) ScriptStatus
	InspectSetup(directory string) (setup.TransactionStatus, error)
}

func checkScript(host Host, executable, directory string, add func(string, string, string)) {
	local, ok := host.(LocalHost)
	if !ok {
		add("script.readiness", "skip", "Script filesystem inspection is unavailable.")
		return
	}
	status := local.Script(executable, directory)
	if status.Executable {
		add("script.executable", "ok", "Executable passed local safety and access checks; it was not executed.")
	} else {
		add("script.executable", "fail", "Configured executable is unavailable, inaccessible or unsafe.")
	}
	if status.Directory {
		add("script.directory", "ok", "Working directory passed local safety and search-access checks.")
	} else {
		add("script.directory", "fail", "Working directory is missing, inaccessible or unsafe.")
	}
	if status.Executable {
		switch status.Format {
		case "binary":
			add("script.format", "skip", "ELF header recognized; architecture, loader and linked libraries were not tested.")
		case "interpreter":
			add("script.interpreter", "ok", "Absolute shebang interpreter passed file and access checks; it was not executed.")
		case "indirect":
			add("script.interpreter", "warn", "Indirect env interpreter requires runtime resolution; it was not tested.")
		default:
			add("script.interpreter", "fail", "No supported ELF header or usable absolute shebang interpreter was found.")
		}
	}
	add("script.execution", "skip", "Arguments, environment dependencies, mounts and actual script behavior were not tested.")
}

func checkTransaction(host Host, directory string, add func(string, string, string)) {
	local, ok := host.(LocalHost)
	if !ok {
		add("setup.transaction", "skip", "Setup inspection is unavailable.")
		return
	}
	status, err := local.InspectSetup(directory)
	if err != nil {
		switch {
		case errors.Is(err, setup.ErrMissingDirectory):
			add("setup.transaction", "warn", "Setup directory does not exist; it was not created.")
		case errors.Is(err, setup.ErrUnsupportedPlatform):
			add("setup.transaction", "skip", "Native setup inspection requires Linux.")
		default:
			add("setup.transaction", "fail", "Setup state is unsafe, unreadable, conflicting or invalid; it was preserved.")
		}
		return
	}
	switch status.State {
	case "none":
		add("setup.transaction", "ok", "No setup transaction or retained backup was found.")
	case "committed":
		add("setup.transaction", "warn", "A committed setup backup is retained; a later change requires separate explicit backup handling.")
	case "pending", "discarding":
		add("setup.transaction", "fail", "Setup has unfinished work; explicit recovery is required and was not performed.")
	default:
		add("setup.transaction", "fail", "Setup state was not recognized; it was preserved.")
	}
}
