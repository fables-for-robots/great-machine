package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

// The program is its commands and nothing before them. exec-in-place, by
// which a zathras had the program replace itself with a service or a
// shell up to stage 4, is no command of it: a zathras starts what it
// starts itself.
func TestTheProgramIsItsCommands(t *testing.T) {
	gm := program(t)

	out, _, status := run(t, gm, "version")
	if status != 0 || strings.TrimSpace(out) == "" {
		t.Errorf("version wrote %q and ended with %d", out, status)
	}

	out, errOut, status := run(t, gm, "exec-in-place", "", "/bin/sh", "sh", "-c", "echo the shell ran")
	if status != 2 || strings.Contains(out+errOut, "the shell ran") || !strings.Contains(errOut, `unknown command "exec-in-place"`) {
		t.Errorf("exec-in-place wrote %q and %q and ended with %d; want no shell, the refusal of an unknown command and 2", out, errOut, status)
	}
}
