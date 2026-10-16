package completion

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBashArrayCompletionHasNoEvalOrActionDispatch(t *testing.T) {
	for _, name := range []string{"hlcp", "hlcp-agent", "hlcp-controller"} {
		script := Bash(name)
		if strings.Contains(script, "eval ") {
			t.Fatal("eval in completion")
		}
		cmd := exec.Command("/bin/bash", "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "hlcp-agent")
	body := "#!/bin/bash\n[[ $1 == __complete ]] || exit 3\nif [[ $# == 2 ]]; then [[ $2 == rest ]] || exit 4; else [[ $# == 4 && $2 == --api && $3 == http://127.0.0.1:9444 && $4 == rest ]] || exit 5; fi\nprintf '%s\\n' restart\n"
	if os.WriteFile(fake, []byte(body), 0700) != nil {
		t.Fatal("fake")
	}
	script := Bash("hlcp-agent") + "\nCOMP_WORDS=(hlcp-agent rest); COMP_CWORD=1; _hlcp_complete; printf '%s\\n' \"${COMPREPLY[@]}\"\nCOMP_WORDS=(hlcp-agent --api http : //127.0.0.1 : 9444 rest); COMP_CWORD=7; _hlcp_complete; printf '%s\\n' \"${COMPREPLY[@]}\"\n"
	cmd := exec.Command("/bin/bash", "--noprofile", "--norc")
	cmd.Env = append(os.Environ(), "PATH="+dir+":/usr/bin:/bin")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "restart\nrestart\n" {
		t.Fatal(string(out), err)
	}
}
