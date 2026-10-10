### Task 9: `great-machine tui`

The command: the flags of every operator command and no argument, a terminal as its standard input and output, and `tui.Run` with a `Dial` that dials draal as the other operator commands do.

**Review Focus pinned here:** 1, for the command: `TestTUIAgainstDraal` runs it against a real draal in a pseudo-terminal and checks the terminal after `q`, after draal's refusal and after an interrupt.

**Files:**
- Create: `cli/tui.go`
- Modify: `cli/operator.go` (`operatorSettings` can dial with a key it was given, and at addresses a test names)
- Modify: `cli/app.go` (`commands` has `tui`, between `shell` and `version`)
- Test: `cli/tui_test.go`
- Modify: `cli/commands_test.go` (`TestHelpNamesEveryCommand` names `tui`)

**Interfaces:**
- Consumes: `tui.Run`, `tui.RunDrawn`, `tui.Config`, `tui.Operator`, `tui.Shell` (the contract; Task 7 and 8); `tuitest` (Task 8); `(*operator.Client).Shell(ctx, zathras, req) (*operator.Shell, error)`; `node.LoadOrCreateKey`, `node.Dial`, `node.DialConfig{Key, Server, Addrs}`; `human.ShortID`; of `cli`: `operatorFlags`, `readOperator`, `noArguments`, `usageError`, `interrupted`, `run(ctx, app, args) int`, `newApp`, `dialTimeout`, and in the tests `fakeDraal`, `net.ListenPacket` for an address where nothing answers, `draalForTest`, `runDraal`, `quietLog`, `buffer`, `waitFor`, `cleanEnvironment`, `testFleet`, `statusNow`, `idA`, `idC`.
- Produces:
  - `great-machine tui [--draal ID] [--key FILE]`, as the contract's section `cli` has it: status 2 and one line for a command line that is not valid and for a standard input or output that is no terminal; 0 when the user quits; 130 when its context ended, with nothing on standard error unless `Run` said more than that; 1 with `great-machine: <error>` otherwise, which for an operator that is not on the allowlist is `great-machine: asking draal for the fleet: unauthorized: <ID> is no operator and may not watch`.
  - `type tuiDeps struct{ in, out *os.File; run func(ctx context.Context, cfg tui.Config) error; addrs []netip.AddrPort }` and `func tuiCommand(deps tuiDeps) *cli.Command`: the tests give the command a terminal, a `Run` and a draal of their own.
  - `type tuiOperator struct{ *operator.Client }`, the adapter: a `tui.Operator`.
  - `func (s operatorSettings) dial(ctx context.Context, k node.Key) (*operator.Client, error)`, and the field `addrs []netip.AddrPort` of `operatorSettings`, which no flag sets.

**How it is made, and why:**
- **The adapter.** `tui.Operator` asks for `Shell(...) (tui.Shell, error)`, an interface, so that the tests of `tui` and its demo can give it sessions of their own; `*operator.Client` returns `*operator.Shell`. Go has no covariant results, so one method is wrapped. It returns a nil interface for a shell that was refused: a `tui.Shell` that holds a nil `*operator.Shell` would not be nil.
- **The key is read once, before the terminal is taken.** A key file that cannot be read ends the command with the status 1 and its reason; inside `Dial` it would be tried again for ever, as a draal that does not answer is.
- **`Dial` dials anew each time it is called**, with the 30 seconds every operator command gives a dial. `tui.Run` closes what it returned.
- **The errors of `Run` are printed as `Run` gives them**: their text is safe for a terminal (Task 7), and `run` of `cli` puts `great-machine:` before it. When the context of the command is done, the status is that of an interrupt, 130, whatever `Run` returned; and what `Run` returned is printed when it is more than the error of the context itself: that the terminal UI was cut off, that the terminal may not be as it was, or a panic that came with the end.
- **The terminal is checked after the command line.** A flag that is wrong is said before a terminal that is missing: both are mistakes of usage.
- **`TERM`.** Bubble Tea reads the environment for what the terminal can draw. The tests set `TERM=xterm-256color`, as CI has no terminal of its own and may have no `TERM`.

- [ ] **Step 1: Write the failing tests**

Create `cli/tui_test.go`:

```go
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
	cfg := draalForTest(t, "")
	cfg.operators = operators
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
	t.Run("an operator that is not on the allowlist", func(t *testing.T) {
		term, stderr, status := start(t, context.Background(), strangerFile)
		got := ended(t, status)
		if got != 1 || !strings.HasPrefix(stderr.String(), "great-machine: ") || !strings.Contains(stderr.String(), "unauthorized") || !strings.Contains(stderr.String(), "is no operator") || strings.Count(stderr.String(), "\n") != 1 {
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
```

In `cli/commands_test.go`, in `TestHelpNamesEveryCommand`, the list of names gets `"tui"`:

```go
	for _, name := range []string{"draal", "zathras", "id", "rollout", "rollback", "status", "logs", "shell", "tui", "version"} {
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `nix develop .#bare --command go test ./cli/`
Expected: FAIL, the package does not build:

```
# github.com/fables-for-robots/great-machine/cli [github.com/fables-for-robots/great-machine/cli.test]
cli/tui_test.go:25:18: undefined: tuiDeps
cli/tui_test.go:27:32: undefined: tuiCommand
cli/tui_test.go:57:12: undefined: tuiDeps
cli/tui_test.go:95:24: undefined: tuiOperator
cli/tui_test.go:177:11: undefined: tuiDeps
cli/tui_test.go:233:10: undefined: tuiDeps
cli/tui_test.go:266:12: undefined: tuiDeps
cli/tui_test.go:301:12: undefined: tuiDeps
FAIL	github.com/fables-for-robots/great-machine/cli [build failed]
FAIL
```

- [ ] **Step 3: Write the command**

Create `cli/tui.go`:

```go
package cli

import (
	"context"
	"fmt"
	"net/netip"
	"os"

	"github.com/fables-for-robots/great-machine/human"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/tui"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
	xterm "golang.org/x/term"
)

// tuiDeps is what `great-machine tui` works with. The tests give it a
// terminal and a draal of their own.
type tuiDeps struct {
	in, out *os.File                                        // the terminal
	run     func(ctx context.Context, cfg tui.Config) error // tui.Run
	// addrs is for tests: where draal is, as node.DialConfig has it. No
	// flag sets it.
	addrs []netip.AddrPort
}

// tuiCommand is `great-machine tui`.
func tuiCommand(deps tuiDeps) *cli.Command {
	return &cli.Command{
		Name:  "tui",
		Usage: "the fleet in this terminal: services, zathrases, figures, logs, and a shell",
		Description: "Shows what the web page shows, and follows it: every service, the\n" +
			"zathrases of one and how far a rollout is, and of a zathras its state, its\n" +
			"figures and its log. It rolls nothing out. The key ? says the keys; s on a\n" +
			"zathras opens a shell in its container, which Ctrl-] leaves; q quits.\n" +
			"While a shell is being opened Esc gives it up, and nothing else that is\n" +
			"typed then is acted on or passed on to the shell.\n\n" +
			"It needs a terminal as its standard input and its standard output. When\n" +
			"the connection to draal is lost it says so, keeps what it showed, and\n" +
			"asks again by itself.",
		Flags: operatorFlags(),
		Action: func(c *cli.Context) error {
			if err := noArguments(c); err != nil {
				return err
			}
			s, err := readOperator(c)
			if err != nil {
				return err
			}
			if !xterm.IsTerminal(int(deps.in.Fd())) || !xterm.IsTerminal(int(deps.out.Fd())) {
				return usageError("tui needs a terminal as its standard input and its standard output; status and logs print the same without one")
			}
			s.addrs = deps.addrs
			return runTUI(c.Context, s, deps)
		},
	}
}

// runTUI runs the terminal UI on the terminal of deps, as the operator
// the settings name, until the user quits. What draal refuses at the
// first dial is returned, with the terminal as it was; a context that
// is done makes the status 130, with what Run had to say beyond that.
func runTUI(ctx context.Context, s operatorSettings, deps tuiDeps) error {
	// The key is read once, and before the terminal is taken: a file
	// that cannot be read is no reason to dial again and again.
	k, err := node.LoadOrCreateKey(s.key)
	if err != nil {
		return fmt.Errorf("the operator's key: %w", err)
	}
	err = deps.run(ctx, tui.Config{
		Dial: func(ctx context.Context) (tui.Operator, error) {
			client, err := s.dial(ctx, k)
			if err != nil {
				return nil, err
			}
			return tuiOperator{client}, nil
		},
		In:    deps.in,
		Out:   deps.out,
		Draal: human.ShortID(s.draal),
	})
	if err != nil && ctx.Err() != nil {
		// The status is that of an interrupt. What Run says beyond the
		// end of its context is said: that the terminal UI had to be cut
		// off, that the terminal may not be as it was, or a failure that
		// came with the end.
		if err == ctx.Err() || err == context.Cause(ctx) {
			return interrupted("")
		}
		return interrupted("%v", err)
	}
	return err
}

// tuiOperator is an *operator.Client as the terminal UI asks for one.
// The two differ in one thing: the client's Shell returns its own type,
// and the terminal UI asks for the interface, so that its tests and its
// demo can give it sessions of their own.
type tuiOperator struct{ *operator.Client }

// A tuiOperator is a tui.Operator.
var _ tui.Operator = tuiOperator{}

// Shell asks for a shell in the container of a zathras. A shell that
// was refused is no session at all: not one that holds a nil pointer.
func (o tuiOperator) Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (tui.Shell, error) {
	sh, err := o.Client.Shell(ctx, zathras, req)
	if err != nil {
		return nil, err
	}
	return sh, nil
}
```

In `cli/operator.go`, `operatorSettings` gets a field, and `connect` is split so that a key that was read can be dialed with (the imports get `"net/netip"`):

```go
// operatorSettings is what the flags of every operator command say.
type operatorSettings struct {
	draal wire.ID
	key   string // the file of the operator's key
	// addrs is for tests: draal is dialed at these addresses, without a
	// relay and without a lookup. No flag sets it.
	addrs []netip.AddrPort
}
```

```go
// connect dials draal as the operator the settings name, and returns the
// client and the operator's key. The caller closes the client.
func (s operatorSettings) connect(ctx context.Context) (*operator.Client, node.Key, error) {
	k, err := node.LoadOrCreateKey(s.key)
	if err != nil {
		return nil, node.Key{}, fmt.Errorf("the operator's key: %w", err)
	}
	client, err := s.dial(ctx, k)
	if err != nil {
		return nil, node.Key{}, err
	}
	return client, k, nil
}
```

```go
// dial dials draal with the key given. The caller closes the client.
func (s operatorSettings) dial(ctx context.Context, k node.Key) (*operator.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, err := node.Dial(ctx, node.DialConfig{Key: k, Server: s.draal, Addrs: s.addrs})
	if err != nil {
		return nil, fmt.Errorf("connecting to draal %s: %w", s.draal, err)
	}
	return operator.New(conn), nil
}
```

In `cli/app.go`, `commands` gets the command behind `shellCommand()` (the imports get `"github.com/fables-for-robots/great-machine/tui"`):

```go
// commands returns every command of the program.
func commands() []*cli.Command {
	return []*cli.Command{
		draalCommand(),
		zathrasCommand(),
		idCommand(),
		rolloutCommand(),
		rollbackCommand(),
		statusCommand(),
		logsCommand(),
		shellCommand(),
		tuiCommand(tuiDeps{in: os.Stdin, out: os.Stdout, run: tui.Run}),
		versionCommand(),
	}
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `nix develop .#bare --command go test ./cli/ -run 'TUI|Help' -v`
Expected: PASS: `TestTUICommandLine` (six), `TestHelpNamesTUI`, `TestTUIOperator`, `TestTUIAgainstDraal` (`q`, `an operator that is not on the allowlist`, `interrupted`), `TestTUIKeyThatCannotBeRead`, `TestTUIInterruptedSaysWhatRunSaid` (three), `TestTUIDraalNotThere` (`q`, `an interrupt`), `TestHelpNamesEveryCommand`.

Run: `nix develop .#bare --command go test -race -count=50 ./cli/ -run TUI`
Expected: `ok`

Run: `nix develop .#bare --command go test ./cli/`
Expected: `ok`

Run: `nix develop .#bare --command gofmt -l cli`
Expected: no output

- [ ] **Step 5: Look at the command**

Run: `nix develop .#bare --command go run ./cmd/great-machine --help`
Expected: the commands, with `tui` between `shell` and `version`:

```
   shell     a shell in the container of a zathras
   tui       the fleet in this terminal: services, zathrases, figures, logs, and a shell
   version   print the version of this program
```

Run: `nix develop .#bare --command go run ./cmd/great-machine tui --draal $(printf 'a1%.0s' $(seq 32)) < /dev/null`
Expected: one line, and `exit status 2` from `go run`:

```
great-machine: tui needs a terminal as its standard input and its standard output; status and logs print the same without one
exit status 2
```

- [ ] **Step 6: Commit**

```bash
git add cli/
git commit -m "CLI: great-machine tui: the terminal UI as an operator command, on a terminal alone"
```
