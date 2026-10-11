package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fables-for-robots/great-machine/launcher"
)

// program builds great-machine into a directory of the test and returns
// its path.
func program(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "great-machine")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building great-machine: %v\n%s", err, output)
	}
	return out
}

// run runs the program with args, and returns what it wrote to standard
// output and to standard error, and its exit status.
func run(t *testing.T, program string, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := exec.Command(program, args...)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	status := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("running %s %q: %v", program, args, err)
		}
		status = exit.ExitCode()
	}
	return out.String(), errOut.String(), status
}

// A zathras starts the service and the shells as the program itself with
// exec-in-place, and the program then becomes what was meant: before it
// reads a command, a flag or the environment.
func TestTheProgramReplacesItself(t *testing.T) {
	gm := program(t)

	// The first argument of the program that was meant is the one given,
	// and its exit status is the program's.
	out, errOut, status := run(t, gm, launcher.Arg, "/bin/sh", "the-shell", "-c", `echo "$0"; exit 7`)
	if out != "the-shell\n" || errOut != "" || status != 7 {
		t.Errorf("as /bin/sh the program wrote %q and %q and ended with %d; want the shell's name, nothing and 7", out, errOut, status)
	}
	// What follows is the other program's: no flag of it is read here.
	out, errOut, status = run(t, gm, launcher.Arg, "/bin/sh", "sh", "-c", `echo "$1 $2"`, "zero", "--data", "--help")
	if out != "--data --help\n" || errOut != "" || status != 0 {
		t.Errorf("with flags for /bin/sh the program wrote %q and %q and ended with %d; want the flags, nothing and 0", out, errOut, status)
	}

	nothing := filepath.Join(t.TempDir(), "nothing")
	out, errOut, status = run(t, gm, launcher.Arg, nothing, "nothing")
	if status != 127 || out != "" || !strings.Contains(errOut, "executing "+nothing) || !strings.Contains(errOut, "no such file") {
		t.Errorf("as a program that is not there it wrote %q and %q and ended with %d; want the reason on standard error and 127", out, errOut, status)
	}

	// Without the argument it is the program it always was.
	out, _, status = run(t, gm, "version")
	if status != 0 || strings.TrimSpace(out) == "" {
		t.Errorf("version wrote %q and ended with %d", out, status)
	}
}
