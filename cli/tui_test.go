package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/tui"
	"github.com/fables-for-robots/great-machine/tui/tuitest"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// tuiApp returns the program with `tui` alone, on the terminal of deps.
func tuiApp(deps tuiDeps, stdout, stderr *bytes.Buffer) *cli.App {
	app := newApp(stdout, stderr)
	app.Commands = []*cli.Command{tuiCommand(deps)}
	return app
}

// A command line of `tui` that is not valid ends with the status 2 and
// one line, and so does a standard input or output that is no terminal.
// The terminal UI is not started.
func TestTUICommandLine(t *testing.T) {
	cleanEnvironment(t)
	term := tuitest.Open(t, 80, 24)
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	for _, tt := range []struct {
		name    string
		in, out *os.File
		args    []string
		want    string // in stderr
	}{
		{"an argument", term.File(), term.File(), []string{"--draal", idA, "web"}, `tui takes no argument, got "web"`},
		{"no --draal", term.File(), term.File(), nil, "--draal is required: give it, or set GREAT_MACHINE_DRAAL"},
		{"a bad --draal", term.File(), term.File(), []string{"--draal", "nonsense"}, `--draal: "nonsense"`},
		{"a flag it does not have", term.File(), term.File(), []string{"--draal", idA, "--follow"}, "flag provided but not defined: -follow"},
		{"a standard input that is no terminal", null, term.File(), []string{"--draal", idA}, "tui needs a terminal as its standard input and its standard output"},
		{"a standard output that is no terminal", term.File(), null, []string{"--draal", idA}, "tui needs a terminal as its standard input and its standard output"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ran := false
			deps := tuiDeps{in: tt.in, out: tt.out, run: func(context.Context, tui.Config) error { ran = true; return nil }}
			var stdout, stderr bytes.Buffer
			args := append([]string{"great-machine", "tui", "--key", filepath.Join(t.TempDir(), "operator.key")}, tt.args...)
			if got := run(context.Background(), tuiApp(deps, &stdout, &stderr), args); got != 2 {
				t.Errorf("the exit status is %d, want 2", got)
			}
			if !strings.HasPrefix(stderr.String(), "great-machine: ") || !strings.Contains(stderr.String(), tt.want) || strings.Count(stderr.String(), "\n") != 1 {
				t.Errorf("stderr is %q, want one line that says %q", stderr.String(), tt.want)
			}
			if ran || stdout.Len() != 0 {
				t.Errorf("the terminal UI was started: %v; stdout is %q", ran, stdout.String())
			}
		})
	}
	if written := term.Written(); written != "" {
		t.Errorf("a command line that is not valid wrote to the terminal: %q", written)
	}
}

// The program names `tui` among its commands, between the other
// operator commands and version.
func TestHelpNamesTUI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run(context.Background(), newApp(&stdout, &stderr), []string{"great-machine", "--help"}); got != 0 {
		t.Fatalf("the exit status is %d", got)
	}
	help := stdout.String()
	shell, ui, version := strings.Index(help, "\n   shell "), strings.Index(help, "\n   tui "), strings.Index(help, "\n   version ")
	if shell < 0 || ui < shell || version < ui {
		t.Errorf("the help does not name tui between shell and version:\n%s", help)
	}
}

// The adapter hands an *operator.Client to the terminal UI as it is,
// but for Shell: a shell that was refused is no session, and one that
// holds a nil pointer would be taken for one.
func TestTUIOperator(t *testing.T) {
	fake := &fakeDraal{fleets: []wire.Fleet{testFleet(statusNow)}}
	var op tui.Operator = tuiOperator{fake.start(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fleets, err := op.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	select {
	case f := <-fleets:
		if len(f.Services) != len(testFleet(statusNow).Services) {
			t.Errorf("the fleet has %d services", len(f.Services))
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for the fleet")
	}
	// The fake answers a shell with the end of the stream.
	sh, err := op.Shell(ctx, wire.ID(idC), wire.ShellRequest{Cols: 80, Rows: 24})
	if err == nil || sh != nil {
		t.Errorf("a shell that was refused is %v with the error %v, want no session and an error", sh, err)
	}
	if err := op.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

var servingInLog = regexp.MustCompile(`msg=serving endpoint=(\S+) addr=(\S+)`)

// draalWithOperator runs a draal on this machine that lets the
// operators in, until the test ends, and returns its endpoint ID and
// where it listens.
func draalWithOperator(t *testing.T, operators ...wire.ID) (wire.ID, netip.AddrPort) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var logged buffer
	cfg := draalForTest(t)
	cfg.admins = operators
	done := make(chan error, 1)
	go func() { done <- runDraal(ctx, cfg, quietLog(&logged)) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("runDraal returned %v, want nil", err)
			}
		case <-time.After(20 * time.Second):
			t.Errorf("runDraal did not return when its context was done\n%s", logged.String())
		}
	})
	var m []string
	waitFor(t, "draal to serve", func() bool {
		m = servingInLog.FindStringSubmatch(logged.String())
		return m != nil
	})
	addr, err := netip.ParseAddrPort(m[2])
	if err != nil {
		t.Fatalf("draal logged the address %q: %v", m[2], err)
	}
	return wire.ID(m[1]), addr
}

// `great-machine tui` against a real draal on this machine, in a
// pseudo-terminal: it shows the fleet and ends with the status 0 on q;
// an operator that draal does not let in ends it with draal's refusal
// and the status 1; an interrupt ends it with 130. Each time the
// terminal is as it was (Review Focus 1).
func TestTUIAgainstDraal(t *testing.T) {
	cleanEnvironment(t)
	t.Setenv("TERM", "xterm-256color")
	dir := t.TempDir()
	keyFile, strangerFile := filepath.Join(dir, "operator.key"), filepath.Join(dir, "stranger.key")
	key, err := node.LoadOrCreateKey(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	draalID, addr := draalWithOperator(t, key.ID())

	// start runs the command in a pseudo-terminal, and returns the
	// terminal, what the command wrote to its standard error, and its
	// exit status when it has ended.
	start := func(t *testing.T, ctx context.Context, keyFile string) (*tuitest.Terminal, *bytes.Buffer, <-chan int) {
		term := tuitest.Open(t, 80, 24)
		deps := tuiDeps{in: term.File(), out: term.File(), addrs: []netip.AddrPort{addr},
			run: func(ctx context.Context, cfg tui.Config) error { return tui.RunDrawn(ctx, cfg, term.Drawn) }}
		var stdout, stderr bytes.Buffer
		status := make(chan int, 1)
		go func() {
			status <- run(ctx, tuiApp(deps, &stdout, &stderr), []string{"great-machine", "tui", "--draal", string(draalID), "--key", keyFile})
		}()
		return term, &stderr, status
	}
	ended := func(t *testing.T, status <-chan int) int {
		t.Helper()
		select {
		case got := <-status:
			return got
		case <-time.After(tuitest.Deadline):
			t.Fatal("timed out waiting for the command to end")
			return 0
		}
	}

	t.Run("q", func(t *testing.T) {
		term, stderr, status := start(t, context.Background(), keyFile)
		term.Wait("draal "+string(draalID[:8])+" · ● live", "draal knows no service yet.")
		term.Type("q")
		if got := ended(t, status); got != 0 || stderr.Len() != 0 {
			t.Errorf("the exit status is %d and stderr %q, want 0 and nothing", got, stderr.String())
		}
		term.Restored()
	})
	t.Run("a key that was granted nothing", func(t *testing.T) {
		term, stderr, status := start(t, context.Background(), strangerFile)
		got := ended(t, status)
		if got != 1 || !strings.HasPrefix(stderr.String(), "great-machine: ") || !strings.Contains(stderr.String(), "unauthorized") || !strings.Contains(stderr.String(), "is no deployer of any service") || strings.Count(stderr.String(), "\n") != 1 {
			t.Errorf("the exit status is %d and stderr %q, want 1 and draal's refusal in one line", got, stderr.String())
		}
		term.Restored()
	})
	t.Run("interrupted", func(t *testing.T) {
		ctx, interrupt := context.WithCancel(context.Background())
		defer interrupt()
		term, stderr, status := start(t, ctx, keyFile)
		term.Wait("● live")
		interrupt()
		if got := ended(t, status); got != 130 || stderr.Len() != 0 {
			t.Errorf("the exit status is %d and stderr %q, want 130 and nothing", got, stderr.String())
		}
		term.Restored()
	})
}

// A key that cannot be read ends the command before the terminal is
// taken.
func TestTUIKeyThatCannotBeRead(t *testing.T) {
	cleanEnvironment(t)
	term := tuitest.Open(t, 80, 24)
	ran := false
	deps := tuiDeps{in: term.File(), out: term.File(), run: func(context.Context, tui.Config) error { ran = true; return nil }}
	var stdout, stderr bytes.Buffer
	// A directory is no file of a key.
	args := []string{"great-machine", "tui", "--draal", idA, "--key", t.TempDir()}
	if got := run(context.Background(), tuiApp(deps, &stdout, &stderr), args); got != 1 || !strings.Contains(stderr.String(), "great-machine: the operator's key: ") {
		t.Errorf("the exit status is %d and stderr %q, want 1 and why the key cannot be read", got, stderr.String())
	}
	if ran || term.Written() != "" {
		t.Errorf("the terminal UI was started: %v; written to the terminal: %q", ran, term.Written())
	}
}

// An interrupt ends the command with the status 130. What Run returned
// then is printed when it says more than that its context ended.
func TestTUIInterruptedSaysWhatRunSaid(t *testing.T) {
	cleanEnvironment(t)
	term := tuitest.Open(t, 80, 24)
	for _, tt := range []struct {
		name   string
		err    func(ctx context.Context) error
		stderr string
	}{
		{"the context's own error", func(ctx context.Context) error { return ctx.Err() }, ""},
		{"an error around it", func(ctx context.Context) error {
			return fmt.Errorf("the terminal UI did not end 5s after it was told to and was cut off, with the terminal restored: %w", ctx.Err())
		}, "great-machine: the terminal UI did not end 5s after it was told to and was cut off, with the terminal restored: context canceled\n"},
		{"another error", func(context.Context) error {
			return errors.New("the terminal UI failed, and the terminal was restored: panic: late")
		},
			"great-machine: the terminal UI failed, and the terminal was restored: panic: late\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, interrupt := context.WithCancel(context.Background())
			deps := tuiDeps{in: term.File(), out: term.File(), run: func(ctx context.Context, _ tui.Config) error {
				interrupt()
				return tt.err(ctx)
			}}
			var stdout, stderr bytes.Buffer
			args := []string{"great-machine", "tui", "--draal", idA, "--key", filepath.Join(t.TempDir(), "operator.key")}
			if got := run(ctx, tuiApp(deps, &stdout, &stderr), args); got != 130 || stderr.String() != tt.stderr {
				t.Errorf("the exit status is %d and stderr %q, want 130 and %q", got, stderr.String(), tt.stderr)
			}
		})
	}
}

// A draal that is not there: nothing answers at its address. The
// terminal UI says that it is connecting, q ends it with the status 0
// and an interrupt with 130, without waiting for the dial, and the
// terminal is as it was.
func TestTUIDraalNotThere(t *testing.T) {
	cleanEnvironment(t)
	t.Setenv("TERM", "xterm-256color")
	dir := t.TempDir()
	other, err := node.LoadOrCreateKey(filepath.Join(dir, "other.key"))
	if err != nil {
		t.Fatal(err)
	}
	// A UDP port that nothing reads.
	nobody, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer nobody.Close()
	addr := netip.MustParseAddrPort(nobody.LocalAddr().String())
	for _, how := range []string{"q", "an interrupt"} {
		t.Run(how, func(t *testing.T) {
			term := tuitest.Open(t, 80, 24)
			deps := tuiDeps{in: term.File(), out: term.File(), addrs: []netip.AddrPort{addr},
				run: func(ctx context.Context, cfg tui.Config) error { return tui.RunDrawn(ctx, cfg, term.Drawn) }}
			var stdout, stderr bytes.Buffer
			ctx, interrupt := context.WithCancel(context.Background())
			defer interrupt()
			status := make(chan int, 1)
			go func() {
				status <- run(ctx, tuiApp(deps, &stdout, &stderr), []string{"great-machine", "tui", "--draal", string(other.ID()), "--key", filepath.Join(dir, "operator.key")})
			}()
			term.Wait("Connecting to draal")
			want := 0
			if how == "q" {
				term.Type("q")
			} else {
				interrupt()
				want = 130
			}
			// The dial is given 30 seconds; the command does not wait for
			// them.
			select {
			case got := <-status:
				if got != want || stderr.Len() != 0 {
					t.Errorf("the exit status is %d and stderr %q, want %d and nothing", got, stderr.String(), want)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("the command did not end while its dial was under way")
			}
			term.Restored()
		})
	}
}
