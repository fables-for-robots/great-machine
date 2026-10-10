### Task 10: The terminal UI against a real draal, in a pseudo-terminal

The "terminal UI" half of the spec's section 14: `tui.Run` in a pseudo-terminal, with a `Dial` that dials the draal of the harness of `e2e` as its operator, over go-iroh on loopback, with real zathrases, real services and real shells. Nothing is faked but the terminal.

**Review Focus pinned here, with nothing faked:**
- **1**: every test ends its terminal UI and checks the terminal (`Restored`): after `q` (`TestTUIShowsTheFleetARolloutAndAZathras`, `TestTUIZathrasGoesWhileItsShellIsOpen`), after the context ended (`TestTUIDraalLostAndBack`), and after the context ended with a shell open (`TestTUIShell`). No goroutine of a terminal UI is left (the cleanup of `tuitest.Open`), no goroutine of a shell session in draal or in a zathras (`noSessionLeft`), and no process of a shell (`alive`).
- **3**: `TestTUIDraalLostAndBack`.
- **4**: `TestTUIZathrasGoesWhileItsShellIsOpen`.

**Files:**
- Modify: `e2e/harness_test.go` (a zathras can be kept away from draal for a while)
- Test: `e2e/tui_test.go`

**Interfaces:**
- Consumes: `tui.RunDrawn`, `tui.Config`, `tui.Operator`, `tui.Shell`; `tuitest.Open`, `(*tuitest.Terminal)` (Task 8); the line `great-machine: a shell on NAME. Ctrl-] leaves it.` and the sentences of Task 8; what the views of Task 6 say; of the harness: `start(t, options{zathrases})`, `h.dial(ctx, key)`, `h.opKey`, `h.draalKey`, `h.pods`, `h.rollout(args...)`, `h.await(info, state)`, `h.stopDraal()`, `h.startDraal()`, `(*pod).cut()`, `eventually`, `alive(pid)`, `deadline`, and `shellStacks()` of `e2e/shell_blocked_test.go`; `human.ShortID`; `operator.New`.
- Produces: `(*pod).keepAway()` and `(*pod).comeBack()`; the four tests.

**How they are made, and why:**
- **The screen is read from the program, the bytes from the pseudo-terminal** (Task 7 says why, and Task 8 has the package). Words of a view are waited for on the screen that was drawn last (`Wait`, `WaitFor`); what a shell wrote lands on the terminal as it came, and is looked for in the bytes (`WaitWritten`), behind the place where the shell's line was written. **No terminal emulator is needed, and none was added to the module**: no test here needs to know what the renderer's bytes add up to.
- **Nothing is asserted of a screen in the middle of a change.** Every expectation is awaited, with `tuitest.Deadline` (60 seconds), and a condition that has several parts is awaited as one (`WaitFor`), so that it is judged on one screen.
- **How draal is taken away from the operator**: it is stopped (`h.stopDraal()`), and another is started on the same database and with the same key (`h.startDraal()`), which listens on another port. The terminal UI's `Dial` asks the harness where draal listens each time it dials, as the zathrases of the harness do, and gives a dial up after 2 seconds, so that a dial to a draal that is gone does not outlast its absence.
- **How a zathras is taken away**: `keepAway` closes its connection and makes its `Dial` fail until `comeBack`. `cut` alone would not do: the zathras dials again within 50 ms, and whether a view ever says that it is gone would be luck.
- **The process of a shell** is found by having the shell print `$$`; the echo of what was typed has `$$` where the output has the number.
- **These tests are not parallel**, and say so: each looks at the goroutines of the whole process. The tests of the package that are parallel wait until those that are not have ended.
- **`TERM`** is set by the tests: Bubble Tea reads it, and CI has no terminal of its own. They need no terminal: `go test ./...` in CI's `test` step runs them, on Linux, as they are.

- [ ] **Step 1: Let the harness keep a zathras away**

In `e2e/harness_test.go`, the struct `pod` gets a field (and the imports get `"errors"`):

```go
	mu        sync.Mutex
	conn      node.Conn // the latest connection to draal
	away      bool      // it does not dial draal: see keepAway
	preloaded []string  // the bodies the preload endpoint was sent
```

In `startPod`, the `Dial` of the agent becomes:

```go
		Dial: func(ctx context.Context) (node.Conn, error) {
			p.mu.Lock()
			away := p.away
			p.mu.Unlock()
			if away {
				return nil, errors.New("the test keeps this zathras away from draal")
			}
			conn, err := h.dial(ctx, key)
			if err == nil {
				p.mu.Lock()
				p.conn = conn
				p.mu.Unlock()
			}
			return conn, err
		},
```

and behind `cut`, add:

```go
// keepAway closes the connection the zathras has to draal, and keeps it
// from dialing draal again until comeBack.
func (p *pod) keepAway() {
	p.mu.Lock()
	p.away = true
	p.mu.Unlock()
	p.cut()
}
```

```go
// comeBack lets the zathras dial draal again.
func (p *pod) comeBack() {
	p.mu.Lock()
	p.away = false
	p.mu.Unlock()
}
```

- [ ] **Step 2: Write the tests**

Create `e2e/tui_test.go`:

```go
package e2e

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/human"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/tui"
	"github.com/fables-for-robots/great-machine/tui/tuitest"
	"github.com/fables-for-robots/great-machine/wire"
)

// The terminal UI against a real draal with real zathrases, in a
// pseudo-terminal. Nothing is faked but the terminal: tui.Run dials the
// harness's draal as the operator, over go-iroh on loopback.
//
// None of these tests is parallel: each looks at the goroutines of the
// whole process when it ends, for what a terminal UI may have left.

// hello is what the service of these tests writes at its start, and
// inLog what the log of its zathras then says: the line with the sign of
// its stream before it. The words alone are on the screen a second time,
// among the arguments the zathras runs its service with, and where that
// line is broken depends on how long the test's directory is.
const (
	hello = "hello from the service"
	inLog = " o " + hello
)

// tuiOperator is a client as the terminal UI asks for one: see the
// adapter of the same name in cli.
type tuiOperator struct{ *operator.Client }

func (o tuiOperator) Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (tui.Shell, error) {
	sh, err := o.Client.Shell(ctx, zathras, req)
	if err != nil {
		return nil, err
	}
	return sh, nil
}

// ui is a terminal UI that runs against the draal of a harness.
type ui struct {
	*tuitest.Terminal
	t     *testing.T
	stop  context.CancelFunc // ends the context of Run
	ended chan error         // what Run returned
}

// startUI runs the terminal UI in a pseudo-terminal of 100 columns and
// 30 rows, as the operator of the harness. It dials draal where draal
// listens at that time, each time it dials. It is ended with the test.
func (h *harness) startUI() *ui {
	h.t.Helper()
	h.t.Setenv("TERM", "xterm-256color")
	term := tuitest.Open(h.t, 100, 30)
	ctx, stop := context.WithCancel(context.Background())
	u := &ui{Terminal: term, t: h.t, stop: stop, ended: make(chan error, 1)}
	cfg := tui.Config{
		Dial: func(ctx context.Context) (tui.Operator, error) {
			// A draal that is not there does not answer: the dial is given
			// up, and tried again where draal is then.
			ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			conn, err := h.dial(ctx, h.opKey)
			if err != nil {
				return nil, err
			}
			return tuiOperator{operator.New(conn)}, nil
		},
		In:         term.File(),
		Out:        term.File(),
		Draal:      human.ShortID(h.draalKey.ID()),
		RetryFirst: 50 * time.Millisecond,
		RetryMax:   200 * time.Millisecond,
	}
	go func() { u.ended <- tui.RunDrawn(ctx, cfg, term.Drawn) }()
	// Registered after the terminal, and so done before the terminal
	// looks for goroutines that are left.
	h.t.Cleanup(func() {
		stop()
		select {
		case err := <-u.ended:
			u.ended <- err
		case <-time.After(tuitest.Deadline):
			h.t.Error("timed out waiting for the terminal UI to end")
		}
	})
	return u
}

// end waits for Run to return, and checks that the terminal is as it
// was: Review Focus 1.
func (u *ui) end() error {
	u.t.Helper()
	select {
	case err := <-u.ended:
		u.ended <- err
		u.Restored()
		return err
	case <-time.After(tuitest.Deadline):
		u.t.Fatal("timed out waiting for the terminal UI to end")
		return nil
	}
}

// openZathras goes from the services to the view of the zathras of that
// name, and waits until its log is followed.
func (u *ui) openZathras(name string) {
	u.t.Helper()
	u.Wait("● live", "▸ web")
	u.Type("l")
	u.Wait("NAME", "PHASE", name)
	for range 8 {
		if strings.Contains(u.Screen(), "▸ "+name) {
			break
		}
		u.Type("j")
		// The key is taken before the next: what is chosen has moved, or
		// was the last and stays.
		time.Sleep(50 * time.Millisecond)
	}
	u.Wait("▸ " + name)
	u.Type("l")
	u.Wait("great-machine › web › "+name, "Log · followed live")
}

// shellPID types a command into the shell that is open which prints the
// process ID of the shell, and returns it. from is a place in what was
// written to the terminal before the shell was opened.
func (u *ui) shellPID(from int) int {
	u.t.Helper()
	// What is typed is echoed with "$$" where the output has the number.
	u.Type("echo shell=$$.\r")
	re := regexp.MustCompile(`shell=(\d+)\.`)
	var pid int
	eventually(u.t, "the shell to print its process ID", func() bool {
		m := re.FindStringSubmatch(u.Written()[from:])
		if m == nil {
			return false
		}
		pid, _ = strconv.Atoi(m[1])
		return true
	})
	return pid
}

// noSessionLeft waits until neither draal nor a zathras has a goroutine
// of a shell session.
func noSessionLeft(t *testing.T) {
	t.Helper()
	end := time.Now().Add(deadline)
	for stacks := shellStacks(); stacks != nil; stacks = shellStacks() {
		if time.Now().After(end) {
			t.Fatalf("goroutines of a shell session are left:\n%s", stacks)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The fleet is drawn with every zathras; a rollout is followed to its
// end on the view of the service; the view of a zathras shows its state
// and what the service wrote; q ends it with the terminal as it was.
func TestTUIShowsTheFleetARolloutAndAZathras(t *testing.T) {
	h := start(t, options{zathrases: 3})
	u := h.startUI()
	u.Wait("draal "+human.ShortID(h.draalKey.ID())+" · ● live", "1 service, 3 zathrases connected", "▸ web", "no stable generation", "3 idle", "nothing was rolled out yet")

	// The service: its three zathrases, and no rollout yet.
	u.Type("\r")
	u.Wait("great-machine › web", "web-0", "web-1", "web-2", "○ idle")
	info := h.rollout("--stdout", hello)
	u.Wait("web · stable generation 1", "● 3 running", "#"+strconv.FormatUint(info.ID, 10)+" ✔ completed")
	if screen := u.Screen(); strings.Contains(screen, "○ idle") {
		t.Errorf("after the rollout a zathras is still drawn as idle:\n%s", screen)
	}

	// A zathras: what it runs, and the line the service wrote.
	u.Type("\r")
	screen := u.Wait("great-machine › web › web-", "● running for", "generation 1, the stable one of web", "testservice", "Log · followed live", inLog)
	if !strings.Contains(screen, string(h.pods[0].id)) && !strings.Contains(screen, string(h.pods[1].id)) && !strings.Contains(screen, string(h.pods[2].id)) {
		t.Errorf("the view of a zathras does not say its endpoint ID:\n%s", screen)
	}

	u.Type("q")
	if err := u.end(); err != nil {
		t.Errorf("Run = %v, want nil when the user quits", err)
	}
}

// A shell from the terminal UI, in the real thing: a command is typed
// and its output seen; exit 3 is said as the status 3; another shell is
// left with Ctrl-], and its process and what it ran are gone. Then the
// context ends while a third shell is open: that shell is gone as well,
// and the terminal is as it was (Review Focus 1).
func TestTUIShell(t *testing.T) {
	h := start(t, options{zathrases: 1})
	h.await(h.rollout("--stdout", hello), wire.RolloutCompleted)
	u := h.startUI()
	u.openZathras("web-0")

	// The first shell ends by itself.
	mark := len(u.Written())
	u.Type("s")
	mark = u.WaitWritten(mark, "great-machine: a shell on web-0. Ctrl-] leaves it.")
	if u.OnAlternateScreen() {
		t.Error("the shell is not on the terminal's own screen")
	}
	first := u.shellPID(mark)
	u.Type("echo answer-$((6*7))\r")
	u.WaitWritten(mark, "answer-42")
	if !alive(first) {
		t.Fatalf("the shell %d does not run", first)
	}
	u.Type("exit 3\r")
	u.Wait("The shell ended with status 3.", "Log · followed live")
	// It is back on the alternate screen: what the shell wrote stays on
	// the terminal's own.
	u.WaitWritten(mark, "\x1b[?1049h")
	eventually(t, "the first shell to be gone", func() bool { return !alive(first) })

	// The second is left while a program runs in it.
	mark = len(u.Written())
	u.Type("s")
	mark = u.WaitWritten(mark, "great-machine: a shell on web-0. Ctrl-] leaves it.")
	second := u.shellPID(mark)
	u.Type("sh -c 'echo program=$$.; exec sleep 1000'\r")
	re := regexp.MustCompile(`program=(\d+)\.`)
	var program int
	eventually(t, "the program to print its process ID", func() bool {
		m := re.FindStringSubmatch(u.Written()[mark:])
		if m == nil {
			return false
		}
		program, _ = strconv.Atoi(m[1])
		return true
	})
	if !alive(second) || !alive(program) {
		t.Fatalf("the shell %d or its program %d does not run", second, program)
	}
	u.Type("\x1d")
	u.Wait("The shell was left with Ctrl-], which hung it up.", "Log · followed live")
	eventually(t, "the shell that was left, and its program, to be gone", func() bool { return !alive(second) && !alive(program) })
	noSessionLeft(t)

	// A key of the terminal UI is the terminal UI's again.
	u.Type("?")
	u.Wait("on a zathras: open a shell")
	u.Type("?")

	// The third is open when the context ends.
	mark = len(u.Written())
	u.Type("s")
	mark = u.WaitWritten(mark, "great-machine: a shell on web-0. Ctrl-] leaves it.")
	third := u.shellPID(mark)
	u.stop()
	if err := u.end(); !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want the error of its context", err)
	}
	eventually(t, "the shell that was open at the end to be gone", func() bool { return !alive(third) })
	noSessionLeft(t)
}

// Review Focus 4, with nothing faked: the connection of a zathras is cut
// while its view and its shell are open. The shell ends with a sentence
// and its process is gone, the view says that the zathras is gone, and
// when it has registered again the view is alive again.
func TestTUIZathrasGoesWhileItsShellIsOpen(t *testing.T) {
	h := start(t, options{zathrases: 1})
	h.await(h.rollout("--stdout", hello), wire.RolloutCompleted)
	u := h.startUI()
	u.openZathras("web-0")
	u.Wait("● running for", inLog)

	mark := len(u.Written())
	u.Type("s")
	mark = u.WaitWritten(mark, "great-machine: a shell on web-0. Ctrl-] leaves it.")
	pid := u.shellPID(mark)

	h.pods[0].keepAway()
	screen := u.Wait("The shell ended: ", "This zathras is not connected to draal any more")
	t.Logf("the shell of a zathras that went ended with:\n%s", lineWith(screen, "The shell ended: "))
	eventually(t, "the shell of the zathras that went to be gone", func() bool { return !alive(pid) })
	// It has no shell now.
	u.Type("s")
	u.Wait("This zathras is not connected: there is no container to open a shell in.")

	h.pods[0].comeBack()
	u.WaitFor("show the zathras connected again, with its log followed", func(screen string) bool {
		return !strings.Contains(screen, "not connected to draal any more") && strings.Contains(screen, "● running for") &&
			strings.Contains(screen, "Log · followed live") && strings.Count(screen, inLog) == 1
	})
	// And a shell again.
	mark = len(u.Written())
	u.Type("s")
	mark = u.WaitWritten(mark, "great-machine: a shell on web-0. Ctrl-] leaves it.")
	u.Type("exit\r")
	u.Wait("The shell ended.")
	u.Type("q")
	if err := u.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
	noSessionLeft(t)
}

// lineWith returns the line of a screen that has text in it, and the
// lines behind it that go on with it.
func lineWith(screen, text string) string {
	if i := strings.Index(screen, text); i >= 0 {
		rest := strings.SplitN(screen[i:], "\n", 4)
		return strings.Join(rest[:min(len(rest), 3)], "\n")
	}
	return ""
}

// Review Focus 3, with nothing faked: draal is stopped while the view of
// a zathras is open, and another draal is started on the same database
// and with the same key, where it listens then. The terminal UI says
// that the connection is lost and keeps what it showed; when draal is
// back it is on the same view, alive, with the log followed again and
// no line of it twice. The context ends it, with the terminal as it was.
func TestTUIDraalLostAndBack(t *testing.T) {
	h := start(t, options{zathrases: 2})
	h.await(h.rollout("--stdout", hello), wire.RolloutCompleted)
	u := h.startUI()
	u.openZathras("web-1")
	u.Wait("● running for", inLog)

	h.stopDraal()
	// What the log says of its end is one of two things: draal ends what
	// it sends when it stops, and which the terminal UI sees first, the
	// end of the log or the end of the connection, is not fixed.
	u.WaitFor("say that the connection is lost, with what it showed", func(screen string) bool {
		return strings.Contains(screen, "✖ lost for") && strings.Contains(screen, "The connection to draal is lost since") &&
			strings.Contains(screen, "great-machine › web › web-1") && strings.Count(screen, inLog) == 1 &&
			(strings.Contains(screen, "The connection to draal was lost.") || strings.Contains(screen, "The log has ended."))
	})
	// It stays lost for as long as draal is away, and says how long.
	u.WaitFor("say for how many seconds the connection is lost", func(screen string) bool {
		return regexp.MustCompile(`✖ lost for [2-9]s`).MatchString(screen)
	})

	h.startDraal()
	t.Cleanup(func() { h.stopDraal() })
	u.WaitFor("show the same zathras over the new connection", func(screen string) bool {
		return strings.Contains(screen, "● live") && !strings.Contains(screen, "The connection to draal is lost") &&
			strings.Contains(screen, "great-machine › web › web-1") && strings.Contains(screen, "● running for") &&
			strings.Contains(screen, "Log · followed live") && strings.Count(screen, inLog) == 1
	})
	// The way back is the same: the service, with both zathrases.
	u.Type("h")
	u.Wait("great-machine › web", "web-0", "▸ web-1", "● 2 running")

	u.stop()
	if err := u.end(); !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want the error of its context", err)
	}
}
```

- [ ] **Step 3: Run the tests**

Run: `nix develop .#bare --command go test ./e2e/ -run TestTUI -v`
Expected: PASS, in some five seconds: `TestTUIShowsTheFleetARolloutAndAZathras`, `TestTUIShell`, `TestTUIZathrasGoesWhileItsShellIsOpen` (which logs the sentence the shell ended with, as draal says it: `The shell ended: the connection of zathras <ID> failed: …`), `TestTUIDraalLostAndBack`.

These tests are of what Tasks 1 to 9 built, and pass at once when that is right. If one fails, the defect is in what it tests: read what it printed (the screen it waited on is in the message) before anything is changed in the test.

Run: `nix develop .#bare --command go test -race -count=50 -timeout 60m ./e2e/ -run TestTUI`
Expected: `ok` (about five minutes)

Run: `nix develop .#bare --command go test ./e2e/`
Expected: `ok`: the tests of stages 1 and 2 with them

Run: `nix develop .#bare --command gofmt -l e2e`
Expected: no output

- [ ] **Step 4: See one of them fail for the right reason**

In `tui/shell.go`, delete the line `p.sess.Close()` in the deferred function of `attach`: a shell that was left with `Ctrl-]` then stays in its container.

Run: `nix develop .#bare --command go test ./e2e/ -run 'TestTUIShell$'`
Expected: FAIL, after 30 seconds:

```
--- FAIL: TestTUIShell (30.60s)
    tui_test.go:249: timed out waiting for the shell that was left, and its program, to be gone
FAIL
FAIL	github.com/fables-for-robots/great-machine/e2e	30.972s
FAIL
```

Put the line back (`git checkout tui/shell.go`), and run the test once more: `ok`.

- [ ] **Step 5: Commit**

```bash
git add e2e/
git commit -m "E2E: the terminal UI in a pseudo-terminal against a real draal: the fleet, a rollout, a shell, a zathras that goes, and draal lost and back"
```
