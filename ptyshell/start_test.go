package ptyshell

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fables-for-robots/great-machine/launcher"
)

// Start, if set, is what starts the shell: it is given the command of the
// shell and what starts it on its terminal.
func TestStartIsGivenTheCommandAndWhatStartsIt(t *testing.T) {
	var paths []string
	var args [][]string
	var term bool
	s, out := start(t, Config{Term: "vt100", Start: func(cmd *exec.Cmd, begin func() error) error {
		paths, args = append(paths, cmd.Path), append(args, slices.Clone(cmd.Args))
		term = slices.Contains(cmd.Env, "TERM=vt100")
		return begin()
	}})
	typeIn(t, s, "echo hello-$((6*7))")
	out.waitFor(t, "hello-42")
	if len(paths) != 1 || paths[0] != "/bin/sh" || !slices.Equal(args[0], []string{"/bin/sh"}) || !term {
		t.Errorf("Start was given the programs %q with the arguments %q, with TERM: %v; want once /bin/sh, as its own first argument, with TERM", paths, args, term)
	}
}

// What Start returns is what came of the start.
func TestWhatStartReturnsIsTheErrorOfTheStart(t *testing.T) {
	failed := errors.New("the launcher would not")
	s, err := Start(Config{Start: func(*exec.Cmd, func() error) error { return failed }})
	if err == nil {
		s.Close()
		t.Fatal("Start() = nil, want the error of the start")
	}
	if !errors.Is(err, failed) || !strings.Contains(err.Error(), "/bin/sh") {
		t.Errorf("Start() = %v, want one that names the shell and has the error of the start in its chain", err)
	}
}

// throughALauncher returns cfg with a real launcher as its Start.
func throughALauncher(t *testing.T, cfg Config) Config {
	t.Helper()
	l, err := launcher.New(launcher.Config{Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("launcher.New: %v", err)
	}
	cfg.Start = l.Start
	return cfg
}

// Through a launcher the shell runs as it does without one: on the
// terminal, in its session, with its environment, its directory and its
// exit status, and ended as before.
func TestThroughALauncherTheShellRunsAsBefore(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, out := start(t, throughALauncher(t, Config{Term: "vt100", Dir: dir, Cols: 132, Rows: 43}))
	typeIn(t, s, "echo term=$TERM. in=$(pwd -P). size=$(stty size).")
	out.waitFor(t, "term=vt100. in="+dir+". size=43 132.")
	// The terminal is the shell's controlling terminal: it has a name.
	typeIn(t, s, "echo tty=$(tty).")
	out.waitFor(t, "tty=/dev/")
	if err := s.Resize(101, 37); err != nil {
		t.Fatalf("Resize() = %v", err)
	}
	typeIn(t, s, "stty size")
	out.waitFor(t, "37 101")
	typeIn(t, s, "exit 3")
	if got := wait(t, s); got != 3 {
		t.Fatalf("Wait() = %d, want 3", got)
	}
	out.waitEOF(t)
}

// Close ends a shell that was started through a launcher as any other:
// the hangup reaches it, and what it runs.
func TestThroughALauncherCloseEndsTheShell(t *testing.T) {
	s, out := start(t, throughALauncher(t, Config{}))
	typeIn(t, s, "echo ready-$((6*7)); sleep 1000")
	out.waitFor(t, "ready-42")
	if err := s.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	wait(t, s)
	out.waitEOF(t)
}

// A shell that is not there is a start that failed, and so an error of
// Start, with a launcher as without: whoever asked for a shell is told.
func TestThroughALauncherAShellThatIsNotThere(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-shell")
	s, err := Start(throughALauncher(t, Config{Shell: missing}))
	if err == nil {
		s.Close()
		t.Fatal("Start() = nil, want an error")
	}
	if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), missing) {
		t.Fatalf("Start() = %v, want one that names the shell and has os.ErrNotExist in its chain", err)
	}
	noProgram := filepath.Join(t.TempDir(), "no-execute-bit")
	if err := os.WriteFile(noProgram, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err = Start(throughALauncher(t, Config{Shell: noProgram}))
	if err == nil {
		s.Close()
		t.Fatal("Start() of a file without an execute bit = nil, want an error")
	}
	if !strings.Contains(err.Error(), noProgram) {
		t.Fatalf("Start() = %v, want one that names the shell", err)
	}
}
