package build_test

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/fables-for-robots/great-machine/testservice/build"
)

// run runs the program to its end and returns what it wrote and its exit
// status.
func run(t *testing.T, bin string, args ...string) (stdout, stderr string, status int) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		status = exit.ExitCode()
	default:
		t.Fatalf("running testservice %q: %v", args, err)
	}
	return out.String(), errOut.String(), status
}

func TestBinaryWritesAndExits(t *testing.T) {
	bin := build.Binary(t)
	if filepath.Dir(filepath.Dir(bin)) != filepath.Dir(t.TempDir()) {
		t.Errorf("the binary is at %s, outside the test's temporary directory", bin)
	}
	tests := []struct {
		name           string
		args           []string
		stdout, stderr string
		status         int
	}{
		{"exits with 0", []string{"--exit-after", "0"}, "", "", 0},
		{"exit code", []string{"--exit-after", "10ms", "--exit-code", "3"}, "", "", 3},
		{"stdout", []string{"--stdout", "hello there", "--exit-after", "0"}, "hello there\n", "", 0},
		{"stderr", []string{"--stderr", "oh no", "--exit-after", "0", "--exit-code", "1"}, "", "oh no\n", 1},
		{"an empty text is a line", []string{"--stdout", "", "--stderr", "x", "--exit-after", "0"}, "\n", "x\n", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, status := run(t, bin, tt.args...)
			if stdout != tt.stdout || stderr != tt.stderr || status != tt.status {
				t.Errorf("got stdout %q, stderr %q, status %d; want %q, %q, %d",
					stdout, stderr, status, tt.stdout, tt.stderr, tt.status)
			}
		})
	}
}

// started starts the program and returns it when it has written the line
// "ready", with the lines it wrote before.
func started(t *testing.T, bin string, args ...string) (*exec.Cmd, []string) {
	t.Helper()
	cmd := exec.Command(bin, append(args, "--stdout", "ready")...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	var lines []string
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		if sc.Text() == "ready" {
			return cmd, lines
		}
		lines = append(lines, sc.Text())
	}
	t.Fatalf("testservice %q ended before it was ready", args)
	return nil, nil
}

func TestBinaryEndsOnSIGTERM(t *testing.T) {
	bin := build.Binary(t)
	cmd, _ := started(t, bin)
	cmd.Process.Signal(syscall.SIGTERM)
	state, err := cmd.Process.Wait()
	if err != nil || state.ExitCode() != 0 {
		t.Errorf("after SIGTERM: %v, %v; want exit status 0", state, err)
	}
}

func TestBinaryIgnoresSIGTERM(t *testing.T) {
	bin := build.Binary(t)
	cmd, _ := started(t, bin, "--ignore-term", "--exit-after", "300ms", "--exit-code", "5")
	cmd.Process.Signal(syscall.SIGTERM)
	state, err := cmd.Process.Wait()
	if err != nil || state.ExitCode() != 5 {
		t.Errorf("after SIGTERM: %v, %v; want it ignored and exit status 5", state, err)
	}
}

func TestBinarySpawnsAChildInItsGroup(t *testing.T) {
	bin := build.Binary(t)
	cmd, lines := started(t, bin, "--spawn")
	var child int
	if len(lines) != 1 {
		t.Fatalf("got the lines %q before ready, want one: child <pid>", lines)
	}
	if _, err := fmt.Sscanf(lines[0], "child %d", &child); err != nil {
		t.Fatalf("got the line %q, want child <pid>", lines[0])
	}
	t.Cleanup(func() { syscall.Kill(child, syscall.SIGKILL) })

	pgid, err := syscall.Getpgid(child)
	if err != nil {
		t.Fatalf("the child %d: %v", child, err)
	}
	want, _ := syscall.Getpgid(cmd.Process.Pid)
	if pgid != want {
		t.Errorf("the child is in process group %d, want %d, that of testservice", pgid, want)
	}

	// The child ignores SIGTERM: it is there when testservice has ended
	// by one.
	syscall.Kill(child, syscall.SIGTERM)
	cmd.Process.Signal(syscall.SIGTERM)
	if state, err := cmd.Process.Wait(); err != nil || state.ExitCode() != 0 {
		t.Errorf("after SIGTERM: %v, %v; want exit status 0", state, err)
	}
	if err := syscall.Kill(child, 0); err != nil {
		t.Errorf("the child is gone after SIGTERM: %v", err)
	}
}
