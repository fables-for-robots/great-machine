package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/creack/pty"
	xterm "golang.org/x/term"

	"github.com/fables-for-robots/great-machine/wire"
)

// The line above a shell names its zathras in one line of the
// terminal's own screen, however long the name: one of 4 KiB is cut in
// its middle, as the views cut names, and an ordinary one is whole.
func TestTheLineAboveAShellIsOneLine(t *testing.T) {
	long := named(strings.Repeat("web-7d9f8b6c4-", 292) + "2xkqp")
	if len(long) < fieldCap-5 || len(long) > fieldCap {
		t.Fatalf("the name is %d bytes, want one of the limit", len(long))
	}
	line := aboveShell(long)
	// width counts the line's end as one cell.
	if w := width(line); w > 60+len("great-machine: a shell on . Ctrl-] leaves it. ") || !strings.Contains(line, "…") || !strings.Contains(line, "2xkqp. Ctrl-] leaves it.") {
		t.Errorf("a name of %d bytes gives a line of %d cells: %q", len(long), w, line)
	}
	if got, want := aboveShell("web-7d9f8b6c4-2xkqp"), shellLine+"\r\n"; got != want {
		t.Errorf("an ordinary name gives %q, want %q", got, want)
	}
}

// The line that says how a shell ended, for every way it can end.
func TestShellSentence(t *testing.T) {
	refused := wire.Errorf(wire.CodeUnavailable, "zathras 20ab12cd is not connected")
	for _, tt := range []struct {
		name string
		msg  shellEndedMsg
		want string
	}{
		{"it ended well", shellEndedMsg{opened: true}, "The shell ended."},
		{"it ended with a status", shellEndedMsg{opened: true, exit: 3}, "The shell ended with status 3."},
		{"a signal ended it", shellEndedMsg{opened: true, exit: -1}, "The shell was ended by a signal."},
		{"it was left", shellEndedMsg{opened: true, detached: true}, "The shell was left with Ctrl-], which hung it up."},
		{"draal refused it", shellEndedMsg{err: fmt.Errorf("operator: shell: %w", refused)}, "No shell was opened: zathras 20ab12cd is not connected"},
		{"draal could not be asked", shellEndedMsg{err: errors.New("operator: shell: the connection ended")}, "No shell was opened: operator: shell: the connection ended"},
		{"its zathras went", shellEndedMsg{opened: true, err: fmt.Errorf("term: reading from the shell: %w", wire.Errorf(wire.CodeUnavailable, "the zathras has disconnected"))},
			"The shell ended: the zathras has disconnected"},
		{"the session failed", shellEndedMsg{opened: true, err: errors.New("term: reading from the shell: the stream was reset")},
			"The shell ended: term: reading from the shell: the stream was reset"},
		// Review Focus 2: what a refusal says is drawn as text.
		{"a refusal with a title in it", shellEndedMsg{err: wire.Errorf(wire.CodeUnavailable, "no \x1b]0;pwned\x07shell\x1b[2J here")}, "No shell was opened: no shell here"},
	} {
		if got := shellSentence(tt.msg); got != tt.want {
			t.Errorf("%s: the line is %q, want %q", tt.name, got, tt.want)
		}
	}
}

// A shell is opened in two steps. The key asks draal for a session and
// says so; draal's answer asks for the terminal to be given to it, by
// the name the zathras is drawn with; the message that the shell has
// ended is said in one line, which the next key takes away, and what the
// view holds is asked for anew. From the key until the end no key is
// acted on but Esc, which gives up a shell that draal has not opened.
func TestShellKeyAndItsEnd(t *testing.T) {
	m, _ := onZathras(80, 24, "web", 0)
	m.seen.Name = "web-\x1b[2J7d9f"
	id := m.logID
	wantEffects(t, "s", press(m, "s"), effShell{epoch: 1, zathras: idOf(0x20), n: 1})
	opening := "Opening a shell on web-7d9f … esc gives it up"
	if m.notice != opening || !strings.Contains(plain(m.view()), " "+opening) {
		t.Errorf("while draal is asked the line is %q, and the view:\n%s", m.notice, plain(m.view()))
	}
	// The line of the keys names the one key that does something now.
	if rows := strings.Split(plain(m.view()), "\n"); strings.TrimSpace(rows[len(rows)-1]) != "esc give the shell up" {
		t.Errorf("while draal is asked the keys are said as %q", rows[len(rows)-1])
	}
	// Keys meanwhile: none is acted on, none takes the line away, and q
	// does not quit.
	at := m.logTop
	wantEffects(t, "keys while the shell is opened", press(m, "s", "q", "ctrl+c", "?", "k", "left", "enter", "G"))
	if m.notice != opening || m.screen != screenZathras || m.help || m.logTop != at || m.sh.n != 1 {
		t.Errorf("keys were acted on while a shell was opened: the line is %q, on %v, help %v", m.notice, m.screen, m.help)
	}
	// An answer to another request is not read.
	wantEffects(t, "the answer to a shell nobody asked for", m.update(shellOpenedMsg{n: 7}))
	wantEffects(t, "draal has opened the session", m.update(shellOpenedMsg{n: 1}), effAttach{n: 1, name: "web-7d9f"})
	if m.notice != "" {
		t.Errorf("when the terminal is given away the line is %q", m.notice)
	}
	// Keys that come before the model is told of the shell's end (one
	// that Bubble Tea's reader held when it gave the terminal away) are
	// dropped as well, Esc among them.
	wantEffects(t, "keys while the shell is open", press(m, "q", "esc", "s"))
	wantEffects(t, "the shell has ended", m.update(shellEndedMsg{opened: true, exit: 3}),
		effHistory{epoch: 1, zathras: idOf(0x20)}, effFollow{epoch: 1, zathras: idOf(0x20), id: id + 1})
	if m.notice != "The shell ended with status 3." {
		t.Errorf("after the shell the line is %q", m.notice)
	}
	if !strings.Contains(plain(m.view()), " The shell ended with status 3.") {
		t.Errorf("the line is not drawn:\n%s", plain(m.view()))
	}
	press(m, "j")
	if m.notice != "" {
		t.Errorf("after the next key the line is still %q", m.notice)
	}
	// And the keys are the model's again: the next s is the second shell.
	wantEffects(t, "s again", press(m, "s"), effShell{epoch: 1, zathras: idOf(0x20), n: 2})

	// Esc gives the opening up: the program is told, the line says so,
	// the view stays, and draal's answer, when it comes, is not read.
	wantEffects(t, "esc while the shell is opened", press(m, "esc"), effGiveUp{n: 2})
	if m.notice != "The shell was given up before draal had opened it." || m.screen != screenZathras {
		t.Errorf("after esc the line is %q, on %v", m.notice, m.screen)
	}
	wantEffects(t, "the answer to a shell that was given up", m.update(shellOpenedMsg{n: 2}))
	wantEffects(t, "the keys are the model's again", press(m, "esc"), effUnfollow{})

	// draal refuses: its sentence, and no terminal is asked for.
	m, _ = onZathras(80, 24, "web", 0)
	press(m, "s")
	wantEffects(t, "draal refuses", m.update(shellOpenedMsg{n: 1, err: fmt.Errorf("operator: shell: %w", wire.Errorf(wire.CodeUnavailable, "zathras 20ab12cd is not connected"))}))
	if m.notice != "No shell was opened: zathras 20ab12cd is not connected" || m.sh.stage != shellNone {
		t.Errorf("after a refusal the line is %q", m.notice)
	}

	// The connection is lost while draal is asked: the shell is given
	// up with the connection, and says so.
	m, _ = onZathras(80, 24, "web", 0)
	press(m, "s")
	effs := m.update(lostMsg{epoch: 1})
	if len(effs) == 0 || effs[0] != (effGiveUp{n: 1}) || m.notice != "No shell was opened: the connection to draal was lost." || m.sh.stage != shellNone {
		t.Errorf("the connection lost while a shell is opened: the model wants %+v and says %q", effs, m.notice)
	}

	// The connection was lost while the shell was open, and the model
	// was told first: the line is said all the same, and nothing is
	// asked of a connection that is gone.
	m, _ = onZathras(80, 24, "web", 0)
	press(m, "s")
	m.update(shellOpenedMsg{n: 1})
	m.update(lostMsg{epoch: 1})
	wantEffects(t, "the shell has ended without a connection", m.update(shellEndedMsg{opened: true, err: errors.New("the stream was reset")}))
	if m.notice != "The shell ended: the stream was reset" || m.conn != connLost {
		t.Errorf("the line is %q and the connection %v", m.notice, m.conn)
	}

	// What a shell failed with may be long: it is said in three lines at
	// most, and the view keeps to the size of the terminal.
	m.update(shellEndedMsg{opened: true, err: errors.New(strings.Repeat("the stream was reset by the peer; ", 12))})
	if got := len(m.noticeLines()); got != 3 {
		t.Errorf("a long error is said in %d lines, want 3", got)
	}
	if rows := strings.Split(plain(m.view()), "\n"); len(rows) != 24 || !strings.HasSuffix(strings.TrimSpace(rows[22]), "…") {
		t.Errorf("with a long error the view has %d rows, and its last line of it is %q", len(rows), rows[min(22, len(rows)-1)])
	}
}

// fakeShell is a session the test plays the shell of.
type fakeShell struct {
	out  chan []byte   // what the shell writes
	end  chan struct{} // closed by finish: the shell has ended
	echo bool          // what is typed is written back

	// How it ends, set before finish.
	exit   int
	fail   error // what Read returns in place of the end
	panics bool  // Read panics

	// hold, when set, is waited for by Close: the test is then between
	// term.Attach, which has returned, and Bubble Tea, which has not yet
	// taken the terminal back.
	hold chan struct{}
	// stuck, when set, is waited for by Resize, whatever else happens: a
	// session that does not return.
	stuck   chan struct{}
	closing chan struct{} // closed when Close is called
	closed  chan struct{} // closed when Close returns

	mu    sync.Mutex
	input []byte
	sizes [][2]uint16
	once  sync.Once
	ended sync.Once
}

func newFakeShell() *fakeShell {
	return &fakeShell{out: make(chan []byte, 64), end: make(chan struct{}), closing: make(chan struct{}), closed: make(chan struct{})}
}

func (f *fakeShell) Read(p []byte) (int, error) {
	// What was written before the end is read before it.
	select {
	case b := <-f.out:
		return copy(p, b), nil
	default:
	}
	select {
	case b := <-f.out:
		return copy(p, b), nil
	case <-f.end:
		switch {
		case f.panics:
			panic("the session went wrong")
		case f.fail != nil:
			return 0, f.fail
		}
		return 0, io.EOF
	case <-f.closed:
		return 0, errors.New("the session is closed")
	}
}

func (f *fakeShell) Write(p []byte) (int, error) {
	f.mu.Lock()
	f.input = append(f.input, p...)
	f.mu.Unlock()
	if f.echo {
		select {
		case f.out <- append([]byte(nil), p...):
		case <-f.closed:
		}
	}
	return len(p), nil
}

func (f *fakeShell) Resize(cols, rows uint16) error {
	f.mu.Lock()
	f.sizes = append(f.sizes, [2]uint16{cols, rows})
	f.mu.Unlock()
	if f.stuck != nil {
		<-f.stuck
	}
	return nil
}

func (f *fakeShell) Wait(ctx context.Context) (int, error) {
	select {
	case <-f.end:
		return f.exit, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (f *fakeShell) Close() error {
	f.once.Do(func() {
		close(f.closing)
		if f.hold != nil {
			<-f.hold
		}
		close(f.closed)
	})
	return nil
}

// finish ends the shell.
func (f *fakeShell) finish() { f.ended.Do(func() { close(f.end) }) }

// got returns what was typed into the shell.
func (f *fakeShell) got() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.input)
}

// attached waits until the terminal is joined to the shell: term.Attach
// has made it raw and has sent its size.
func (f *fakeShell) attached(t *testing.T) [2]uint16 {
	t.Helper()
	var size [2]uint16
	await(t, "the terminal to be joined to the shell", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.sizes) == 0 {
			return false
		}
		size = f.sizes[0]
		return true
	})
	return size
}

// isClosed waits until the session is closed.
func (f *fakeShell) isClosed(t *testing.T) {
	t.Helper()
	select {
	case <-f.closed:
	case <-time.After(deadline):
		t.Fatal("timed out waiting for the session to be closed")
	}
}

// shellsOf makes op answer Shell with the sessions, one after the other,
// and returns where the requests are noted.
func shellsOf(op *fakeOperator, sessions ...*fakeShell) *[]wire.ShellRequest {
	var mu sync.Mutex
	reqs := new([]wire.ShellRequest)
	op.shell = func(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (Shell, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(sessions) == 0 {
			return nil, wire.Errorf(wire.CodeUnavailable, "the test has no shell left")
		}
		*reqs = append(*reqs, req)
		sh := sessions[0]
		sessions = sessions[1:]
		return sh, nil
	}
	return reqs
}

// onZathrasOfRun brings a terminal UI that runs to the view of the first
// zathras of web, with its log asked for, and returns the channel its
// log is fed with.
func onZathrasOfRun(t *testing.T, term *terminal, op *fakeOperator) chan wire.LogLine {
	t.Helper()
	op.fleets <- fixtureFleet()
	term.wait("4 services")
	term.typeKeys("jjll")
	term.wait("20ab12cd34ab12cd")
	return op.linesOf(t, idOf(0x20))
}

// shellLine is what is written above a shell of the first zathras of
// web.
const shellLine = "great-machine: a shell on web-7d9f8b6c4-2xkqp. Ctrl-] leaves it."

// mode returns the mode the terminal is in.
func (term *terminal) mode() *xterm.State {
	term.t.Helper()
	state, err := xterm.GetState(int(term.slave.Fd()))
	if err != nil {
		term.t.Fatalf("reading the mode of the pseudo-terminal: %v", err)
	}
	return state
}

// waitWritten waits until text was written to the terminal behind the
// place from, and returns where it ends.
func (term *terminal) waitWritten(from int, text string) int {
	term.t.Helper()
	at := -1
	await(term.t, fmt.Sprintf("%q to be written to the terminal", text), func() bool {
		at = strings.Index(term.written()[from:], text)
		return at >= 0
	})
	return from + at + len(text)
}

// nextReader checks that what is typed after the terminal UI has ended
// is read by whoever reads the terminal next: Review Focus 1.
func (term *terminal) nextReader() {
	term.t.Helper()
	term.typeKeys("next\n")
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 5)
		n, _ := io.ReadFull(term.slave, buf)
		got <- string(buf[:n])
	}()
	select {
	case s := <-got:
		if s != "next\n" {
			term.t.Errorf("the next reader of the terminal read %q, want what was typed", s)
		}
	case <-time.After(deadline):
		term.t.Error("what was typed after the terminal UI ended did not reach the next reader of the terminal")
	}
}

// Review Focus 1, a shell from its key to its end: the terminal is the
// shell's while it is open, and the terminal UI's again afterwards. No
// key is lost on either side: what is typed during the shell reaches the
// session, all of it and nothing of it the program, and what is typed
// after it reaches the program, in order. Meanwhile the fleet and the
// log are read, and afterwards the view shows the newest fleet and no
// line twice.
func TestShellHasTheTerminalAndGivesItBack(t *testing.T) {
	t.Setenv("TERM", "xterm-test")
	op := newFakeOperator()
	sh := newFakeShell()
	sh.exit = 3
	reqs := shellsOf(op, sh)
	term := startRun(t, oneOperator(op), nil)
	lines := onZathrasOfRun(t, term, op)
	at := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Second) }
	lines <- wire.LogLine{At: at(1), Stream: "stdout", Text: "said before the shell"}
	term.wait("said before the shell")

	mark := len(term.written())
	term.typeKeys("s")
	if size := sh.attached(t); size != [2]uint16{80, 24} {
		t.Errorf("the session was told the size %v, want 80 by 24", size)
	}
	if want := (wire.ShellRequest{Zathras: idOf(0x20), Cols: 80, Rows: 24, Term: "xterm-test"}); len(*reqs) != 1 || (*reqs)[0] != want {
		t.Errorf("draal was asked for %+v, want %+v", *reqs, want)
	}
	if reflect.DeepEqual(term.mode(), term.before) {
		t.Error("the terminal is in its usual mode while a shell is joined to it")
	}
	// The line that says whose shell this is stands on the terminal's own
	// screen: the alternate screen was left before it.
	begun := term.waitWritten(mark, shellLine)
	if out := term.written()[:begun]; strings.LastIndex(out, "\x1b[?1049l") < mark || strings.LastIndex(out, "\x1b[?1049h") > strings.LastIndex(out, "\x1b[?1049l") {
		t.Errorf("the alternate screen was not left before the shell: %q", out[mark:])
	}

	// 900 bytes are typed, among them the keys of the terminal UI, Ctrl-C
	// and sequences of keys: all of them reach the session.
	typed := strings.Repeat("xyzq\x03\x1b[A", 100)
	term.typeKeys(typed)
	await(t, "what was typed to reach the session", func() bool { return sh.got() == typed })
	sh.out <- []byte("OUTPUT-OF-THE-SHELL\r\n")
	term.waitWritten(begun, "OUTPUT-OF-THE-SHELL\r\n")

	// Meanwhile the program takes nothing, and the fleet and the log are
	// read all the same.
	for restarts := range 3 {
		f := fixtureFleet()
		f.Services[2].Zathrases[0].State.Restarts = uint64(restarts + 1)
		select {
		case op.fleets <- f:
		case <-time.After(deadline):
			t.Fatal("a fleet was not read while the shell was open")
		}
	}
	await(t, "the fleets to be read", func() bool { return len(op.fleets) == 0 })
	select {
	case lines <- wire.LogLine{At: at(2), Stream: "stdout", Text: "said during the shell"}:
	case <-time.After(deadline):
		t.Fatal("a line of the log was not read while the shell was open")
	}

	// The shell ends. Until then nothing but the shell wrote to the
	// terminal: no key was echoed, and the terminal UI drew nothing.
	sh.finish()
	term.wait("The shell ended with status 3.")
	sh.isClosed(t)
	back := term.waitWritten(begun, "\x1b[?1049h") - len("\x1b[?1049h")
	out := term.written()
	if got := strings.NewReplacer("\r", "", "\n", "").Replace(out[begun:back]); !strings.HasPrefix(got, "OUTPUT-OF-THE-SHELL") || strings.ContainsAny(strings.TrimPrefix(got, "OUTPUT-OF-THE-SHELL"), "xyzq") {
		t.Errorf("while the shell was open, this was written to the terminal behind its line: %q", out[begun:back])
	}
	if got := sh.got(); got != typed {
		t.Errorf("the session got %d bytes, and %d were typed during the shell", len(got), len(typed))
	}

	// Back in the terminal UI: the newest fleet, and what the view holds
	// asked for again. The log comes again from its start, as draal sends
	// it, and no line of it is drawn twice: not the one from before the
	// shell, and not the one that was read while the shell was open,
	// whether the program took it afterwards or not.
	term.wait("3 restarts since this zathras started")
	var again chan wire.LogLine
	await(t, "the log to be asked for again", func() bool {
		op.mu.Lock()
		defer op.mu.Unlock()
		again = op.lines[idOf(0x20)]
		return again != lines
	})
	for n, text := range []string{"said before the shell", "said during the shell", "said after the shell"} {
		again <- wire.LogLine{At: at(n + 1), Stream: "stdout", Text: text}
	}
	term.wait("said after the shell")
	for _, text := range []string{"said before the shell", "said during the shell", "said after the shell"} {
		if n := strings.Count(term.shown(), text); n != 1 {
			t.Errorf("after the shell the log says %q %d times:\n%s", text, n, term.shown())
		}
	}
	op.mu.Lock()
	asked := strings.Join(op.asked, ", ")
	op.mu.Unlock()
	if strings.Count(asked, "history 20") != 2 || strings.Count(asked, "logs 20") != 2 {
		t.Errorf("draal was asked for: %s; want the samples and the log twice, before the shell and after it", asked)
	}

	// Keys again, in order: back, down, open is the second zathras of
	// web. In any other order, or with one of them lost, it is not.
	term.typeKeys("hjl")
	term.wait("21ab12cd34ab12cd")
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
	term.nextReader()
}

// Review Focus 1, the moment between the shell and the terminal UI:
// term.Attach has returned and Bubble Tea has not yet taken the terminal
// back. The terminal is in its usual mode then. What is typed then is
// not the shell's, and it is read by the terminal UI, which acts on it
// when it has been told that the shell has ended, and drops it when the
// key comes before that: the two are sent to the program at about the
// same time, and which comes first is not fixed. So the test types keys
// that do nothing either way, and holds what is fixed: the program takes
// them, the session does not, and the keys after them are acted on in
// order.
func TestKeysTypedWhileTheTerminalChangesHands(t *testing.T) {
	seen := keysSeen(t)
	op := newFakeOperator()
	sh := newFakeShell()
	sh.hold = make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(sh.hold) }) }
	defer release()
	shellsOf(op, sh)
	term := startRun(t, oneOperator(op), nil)
	onZathrasOfRun(t, term, op)
	before := seen()
	term.typeKeys("s")
	sh.attached(t)
	term.typeKeys("in")
	await(t, "what was typed to reach the session", func() bool { return sh.got() == "in" })
	sh.finish()
	select {
	case <-sh.closing:
	case <-time.After(deadline):
		t.Fatal("timed out waiting for the session to be closed")
	}
	if !reflect.DeepEqual(term.mode(), term.before) {
		t.Error("between the shell and the terminal UI the terminal is not in its usual mode")
	}
	term.typeKeys("xyz")
	release()
	await(t, "the keys to be taken by the program", func() bool { return seen() == before+4 })
	if got := sh.got(); got != "in" {
		t.Errorf("the session got %q, want what was typed during the shell alone", got)
	}
	// Back, down, open is the second zathras of web. In any other order,
	// or with one of them lost, it is not.
	term.typeKeys("hjl")
	term.wait("21ab12cd34ab12cd")
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
	term.nextReader()
}

// Ctrl-] leaves a shell that runs on: the session is closed, which hangs
// the shell up, and the line says so. Then two more shells from the same
// view, the last of them left as well.
func TestShellLeftWithCtrlRightBracket(t *testing.T) {
	op := newFakeOperator()
	first, second, third := newFakeShell(), newFakeShell(), newFakeShell()
	for _, sh := range []*fakeShell{first, second, third} {
		sh.echo = true
	}
	shellsOf(op, first, second, third)
	term := startRun(t, oneOperator(op), nil)
	onZathrasOfRun(t, term, op)

	mark := len(term.written())
	term.typeKeys("s")
	first.attached(t)
	term.typeKeys("ls")
	mark = term.waitWritten(term.waitWritten(mark, shellLine), "ls")
	term.typeKeys("\x1d")
	term.wait("The shell was left with Ctrl-], which hung it up.")
	first.isClosed(t)
	if got := first.got(); got != "ls" {
		t.Errorf("the session got %q, want what was typed before Ctrl-]", got)
	}

	term.typeKeys("s")
	second.attached(t)
	term.typeKeys("exit")
	mark = term.waitWritten(term.waitWritten(mark, shellLine), "exit")
	second.finish()
	term.wait("The shell ended.")
	second.isClosed(t)

	term.typeKeys("s")
	third.attached(t)
	term.typeKeys("q")
	term.waitWritten(term.waitWritten(mark, shellLine), "q")
	term.typeKeys("\x1d")
	term.wait("The shell was left with Ctrl-], which hung it up.")
	third.isClosed(t)

	term.typeKeys("?")
	term.wait("on a zathras: open a shell")
	term.typeKeys("?q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
	term.nextReader()
}

// A shell that draal refuses is said as draal's sentence, and nothing is
// written above a shell that is none. Review Focus 4: a zathras that
// goes while its shell is open ends the shell with what the session
// says, and its view says that it is gone.
func TestShellRefusedAndShellOfAZathrasThatGoes(t *testing.T) {
	op := newFakeOperator()
	sh := newFakeShell()
	sh.fail = fmt.Errorf("operator: the shell session failed: %w", wire.Errorf(wire.CodeUnavailable, "zathras web-7d9f8b6c4-2xkqp has disconnected"))
	refuse := true
	var mu sync.Mutex
	op.shell = func(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (Shell, error) {
		mu.Lock()
		defer mu.Unlock()
		if refuse {
			refuse = false
			return nil, fmt.Errorf("operator: shell: %w", wire.Errorf(wire.CodeUnavailable, "zathras 20ab12cd is not connected"))
		}
		return sh, nil
	}
	term := startRun(t, oneOperator(op), nil)
	onZathrasOfRun(t, term, op)

	// The refusal is said in the terminal UI, which never left its
	// screen for it.
	mark := len(term.written())
	term.typeKeys("s")
	term.wait("No shell was opened: zathras 20ab12cd is not connected")
	term.typeKeys("?")
	term.wait("on a zathras: open a shell")
	term.typeKeys("?")
	term.wait("Log ·")
	if out := term.written()[mark:]; strings.Contains(out, "great-machine: a shell on") || strings.Contains(out, "\x1b[?1049l") {
		t.Errorf("for a shell that was refused the terminal UI left its screen: %q", out)
	}

	mark = len(term.written())
	term.typeKeys("s")
	sh.attached(t)
	term.waitWritten(mark, shellLine)
	// The zathras goes: draal says so in the fleet and ends the session.
	gone := fixtureFleet()
	gone.Services[2].Zathrases = gone.Services[2].Zathrases[1:]
	op.fleets <- gone
	sh.finish()
	term.wait("The shell ended: zathras web-7d9f8b6c4-2xkqp has disconnected", "This zathras is not connected to draal any more")
	sh.isClosed(t)
	// It has no shell now, and the key says so.
	term.typeKeys("s")
	term.wait("This zathras is not connected: there is no container to open a shell in.")
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// Review Focus 3, with a shell open: the connection to draal is lost.
// The session ends with it, and back in the terminal UI the header says
// that the connection is lost.
func TestConnectionLostWhileAShellIsOpen(t *testing.T) {
	op := newFakeOperator()
	sh := newFakeShell()
	sh.fail = errors.New("operator: the shell session failed: the connection was closed")
	shellsOf(op, sh)
	term := startRun(t, oneOperator(op), func(cfg *Config) { cfg.RetryFirst, cfg.RetryMax = time.Hour, time.Hour })
	onZathrasOfRun(t, term, op)
	term.typeKeys("s")
	sh.attached(t)
	close(op.fleets)
	sh.finish()
	term.wait("The shell ended: term: reading from the shell: operator: the shell session failed: the connection was closed",
		"✖ lost for", "The connection to draal is lost since")
	sh.isClosed(t)
	select {
	case <-op.closed:
	case <-time.After(deadline):
		t.Fatal("the connection that was lost was not closed")
	}
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// Review Focus 1: the context ends while a shell that never ends has the
// terminal. The shell ends with it, the session is closed, Run returns
// the error of the context, and the terminal is as it was.
func TestShellEndsWithTheContext(t *testing.T) {
	// Nothing but the context ends this shell: the program is not ended
	// by force, and the shell is not left behind.
	grace, kill := shellGrace, killAfter
	shellGrace, killAfter = time.Hour, time.Hour
	t.Cleanup(func() { shellGrace, killAfter = grace, kill })
	op := newFakeOperator()
	sh := newFakeShell()
	shellsOf(op, sh)
	term := startRun(t, oneOperator(op), nil)
	onZathrasOfRun(t, term, op)
	term.typeKeys("s")
	sh.attached(t)
	term.typeKeys("sleep 1000\r")
	await(t, "what was typed to reach the session", func() bool { return sh.got() == "sleep 1000\r" })
	term.stop()
	if err := term.end(); !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want the error of its context", err)
	}
	sh.isClosed(t)
	select {
	case <-op.closed:
	case <-time.After(deadline):
		t.Error("the connection was not closed")
	}
	term.nextReader()
}

// Review Focus 1: the context ends, and the shell does not end with it:
// the session does not return from being told the size, at the start of
// term.Attach, with the terminal already in the mode of a shell. Run
// does not wait for it: it takes the terminal back after shellGrace,
// gives up what does not return after closeAfter, returns the error of
// its context, and the terminal is as it was.
//
// When the session comes loose, after Run has returned, nothing more is
// written to the terminal, though the shell has written something that
// waits to be copied to it then. That is what this test holds of that
// moment: the bytes. The mode of the terminal is set once more then, to the
// usual one, by term.Attach as it returns: see shellGrace.
func TestShellThatDoesNotEndWithTheContext(t *testing.T) {
	grace, closing := shellGrace, closeAfter
	shellGrace, closeAfter = 100*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { shellGrace, closeAfter = grace, closing })
	op := newFakeOperator()
	stuck := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(stuck) }) }
	sh := newFakeShell()
	sh.stuck = stuck
	shellsOf(op, sh)
	term := startRun(t, oneOperator(op), nil)
	// Registered behind the terminal's own, and so done before it looks
	// for goroutines that are left.
	t.Cleanup(release)
	onZathrasOfRun(t, term, op)
	term.typeKeys("s")
	sh.attached(t)
	stopped := time.Now()
	term.stop()
	// The program has ended by itself, with the terminal its own again,
	// before it would have been ended by force: what Run returns is the
	// error of its context and nothing around it.
	if err := term.end(); err != context.Canceled {
		t.Errorf("Run = %v, want the error of its context", err)
	}
	if took := time.Since(stopped); took >= killAfter {
		t.Errorf("Run returned %v after its context was done; want it after shellGrace and closeAfter, and not ended by force", took)
	}
	if _, err := term.slave.WriteString(returnedMark); err != nil {
		t.Fatalf("writing to the pseudo-terminal: %v", err)
	}
	sh.out <- []byte("WRITTEN-AFTER-RUN\r\n")
	release()
	sh.isClosed(t)
	// The line was read from the session, and so was to be written: it
	// is thrown away, and the terminal is quiet.
	await(t, "the session's line to be read", func() bool { return len(sh.out) == 0 })
	term.quietBehindTheMark()
	term.nextReader()
}

// What the goroutine of a shell writes once it is left behind is thrown
// away and reported written, so that term.Attach has no error of it to
// end on: a shell that is left behind ends by its context alone.
func TestShutOutThrowsAwayWhatIsWrittenOnceShut(t *testing.T) {
	var b strings.Builder
	out := &shutOut{w: &b}
	if n, err := out.Write([]byte("before")); n != 6 || err != nil {
		t.Errorf("Write before the shut = %d, %v; want 6, nil", n, err)
	}
	out.shut()
	if n, err := out.Write([]byte("after")); n != 5 || err != nil {
		t.Errorf("Write once shut = %d, %v; want 5, nil", n, err)
	}
	if b.String() != "before" {
		t.Errorf("the terminal has %q, want what came before the shut alone", b.String())
	}
}

// The context ends while draal has not answered the request for a
// shell, and the request does not honour its context. The terminal was
// never given away: Run ends as it does without a shell, gives up what
// does not return after closeAfter, and a session that comes after that
// is closed, with nothing written to the terminal.
func TestContextEndsWhileDraalIsAskedForAShell(t *testing.T) {
	closing := closeAfter
	closeAfter = 300 * time.Millisecond
	t.Cleanup(func() { closeAfter = closing })
	op := newFakeOperator()
	answer := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(answer) }) }
	sh := newFakeShell()
	op.shell = func(context.Context, wire.ID, wire.ShellRequest) (Shell, error) {
		<-answer
		return sh, nil
	}
	term := startRun(t, oneOperator(op), nil)
	t.Cleanup(release)
	onZathrasOfRun(t, term, op)
	mark := len(term.written())
	term.typeKeys("s")
	term.wait("Opening a shell on web-7d9f8b6c4-2xkqp … esc gives it up")
	term.stop()
	if err := term.end(); err != context.Canceled {
		t.Errorf("Run = %v, want the error of its context", err)
	}
	if _, err := term.slave.WriteString(returnedMark); err != nil {
		t.Fatalf("writing to the pseudo-terminal: %v", err)
	}
	release()
	sh.isClosed(t)
	term.quietBehindTheMark()
	if out := term.written()[mark:]; strings.Contains(out, "great-machine: a shell on") {
		t.Errorf("a shell that nobody waited for was announced on the terminal: %q", out)
	}
	if n := len(sh.sizes); n != 0 {
		t.Errorf("the terminal was joined to a session that came after the end")
	}
	term.nextReader()
}

// A shell is left behind well before the program would be ended by
// force, whatever the two times are set to: a kill must not come while
// a shell has the terminal.
func TestShellIsLeftBeforeTheKill(t *testing.T) {
	if shellGrace >= killAfter {
		t.Errorf("shellGrace is %v and killAfter %v: a shell has to be left before the kill", shellGrace, killAfter)
	}
	grace, kill := shellGrace, killAfter
	t.Cleanup(func() { shellGrace, killAfter = grace, kill })
	for _, tt := range []struct{ grace, kill, want time.Duration }{
		{2 * time.Second, 5 * time.Second, 2 * time.Second},
		{10 * time.Second, 5 * time.Second, 2500 * time.Millisecond},
		{time.Hour, time.Hour, 30 * time.Minute},
	} {
		shellGrace, killAfter = tt.grace, tt.kill
		if got := graceOfShell(); got != tt.want || got >= killAfter {
			t.Errorf("with shellGrace %v and killAfter %v a shell is left after %v, want %v", tt.grace, tt.kill, got, tt.want)
		}
	}
}

// Review Focus 1: a panic while a shell has the terminal. term.Attach
// restores the terminal and panics on; the terminal UI takes the
// terminal back, ends as after any panic, and Run returns it as an
// error with the terminal as it was. The session is closed.
func TestShellPanics(t *testing.T) {
	op := newFakeOperator()
	sh := newFakeShell()
	sh.panics = true
	shellsOf(op, sh)
	term := startRun(t, oneOperator(op), nil)
	onZathrasOfRun(t, term, op)
	term.typeKeys("s")
	sh.attached(t)
	sh.finish()
	err := term.end()
	if err == nil || !strings.Contains(err.Error(), "the session went wrong") || !strings.Contains(err.Error(), "the terminal was restored") {
		t.Errorf("Run = %v, want the panic as an error", err)
	}
	sh.isClosed(t)
	term.nextReader()
}

// Review Focus 5: the terminal is resized while a shell is open. Back in
// the terminal UI, the view is drawn for the size the terminal has now.
//
// The size is changed without the signal that a terminal sends with it:
// that the session is told of a resize is the test of term, and a
// SIGWINCH in the middle of a shell makes Bubble Tea write the size from
// two of its goroutines, which the race detector reports.
func TestTerminalResizedWhileAShellIsOpen(t *testing.T) {
	op := newFakeOperator()
	sh := newFakeShell()
	shellsOf(op, sh)
	term := startRun(t, oneOperator(op), nil)
	onZathrasOfRun(t, term, op)
	if rows := strings.Count(term.shown(), "\n") + 1; rows != 24 {
		t.Fatalf("before the shell the view has %d rows, want 24", rows)
	}
	term.typeKeys("s")
	sh.attached(t)
	if err := pty.Setsize(term.master, &pty.Winsize{Cols: 120, Rows: 40}); err != nil {
		t.Fatalf("sizing the pseudo-terminal: %v", err)
	}
	sh.finish()
	term.wait("The shell ended.")
	await(t, "the view to be drawn for 120 columns and 40 rows", func() bool {
		rows := strings.Split(term.shown(), "\n")
		return len(rows) == 40 && width(rows[0]) == 120
	})
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// A zathras that is not connected has no shell: the key says so, and
// draal is not asked.
func TestNoShellOnAZathrasThatIsGone(t *testing.T) {
	op := newFakeOperator()
	term := startRun(t, oneOperator(op), nil)
	onZathrasOfRun(t, term, op)
	gone := fixtureFleet()
	gone.Services[2].Zathrases = gone.Services[2].Zathrases[1:]
	op.fleets <- gone
	term.wait("This zathras is not connected to draal any more")
	term.typeKeys("s")
	term.wait("This zathras is not connected: there is no container to open a shell in.")
	if _, asked := op.was("shell 20"); asked {
		t.Error("draal was asked for a shell on a zathras that is gone")
	}
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// keysSeen has the program count the keys it takes, and returns how
// many it has taken: a test waits on it for keys that the model drops,
// which show in nothing else.
func keysSeen(t *testing.T) func() int {
	t.Helper()
	var mu sync.Mutex
	n := 0
	filterForTests = func(_ tea.Model, msg tea.Msg) tea.Msg {
		if _, key := msg.(tea.KeyPressMsg); key {
			mu.Lock()
			n++
			mu.Unlock()
		}
		return msg
	}
	t.Cleanup(func() { filterForTests = nil })
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

// shellsAsked returns how many shells draal was asked for.
func (f *fakeOperator) shellsAsked() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Count(strings.Join(f.asked, ","), "shell ")
}

// Review Focus 1, while draal has not answered the request for a shell:
// the terminal is the terminal UI's, on its own screen and in its own
// mode, and the view says that a shell is being opened. What is typed
// then is nobody's: it is not echoed, the terminal UI does not act on it
// (q does not quit, s asks for no second shell), and it does not reach
// the shell when the shell is there.
func TestKeysTypedWhileAShellIsOpened(t *testing.T) {
	seen := keysSeen(t)
	op := newFakeOperator()
	sh := newFakeShell()
	answer := make(chan struct{})
	op.shell = func(context.Context, wire.ID, wire.ShellRequest) (Shell, error) {
		<-answer
		return sh, nil
	}
	term := startRun(t, oneOperator(op), nil)
	onZathrasOfRun(t, term, op)
	before := seen()
	mark := len(term.written())
	term.typeKeys("s")
	term.wait("Opening a shell on web-7d9f8b6c4-2xkqp … esc gives it up")
	term.typeKeys("sq?x\x1d")
	await(t, "the keys to be taken by the program", func() bool { return seen() == before+6 })
	if screen := term.shown(); !strings.Contains(screen, "Opening a shell on") || !strings.Contains(screen, "20ab12cd34ab12cd") {
		t.Errorf("a key typed while the shell was opened was acted on:\n%s", screen)
	}
	if out := term.written()[mark:]; strings.Contains(out, "\x1b[?1049l") || strings.Contains(out, "great-machine: a shell on") || reflect.DeepEqual(term.mode(), term.before) {
		t.Errorf("the terminal was given away before there was a session: %q", out)
	}
	close(answer)
	sh.attached(t)
	term.waitWritten(mark, shellLine)
	term.typeKeys("in")
	await(t, "what was typed to reach the session", func() bool { return sh.got() != "" })
	sh.finish()
	term.wait("The shell ended.")
	if got := sh.got(); got != "in" {
		t.Errorf("the session got %q, want what was typed when it had the terminal alone", got)
	}
	// The keys are the terminal UI's again, and one shell was asked for.
	term.typeKeys("?")
	term.wait("on a zathras: open a shell")
	if n := op.shellsAsked(); n != 1 {
		t.Errorf("draal was asked for %d shells, want 1", n)
	}
	term.typeKeys("?q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
	term.nextReader()
}

// s typed twice, and s with q behind it, in one go: one shell is asked
// for, and the key behind s is dropped, whether the program takes it
// while draal is asked or after the terminal was given away.
func TestKeyTypedRightBehindS(t *testing.T) {
	for _, keys := range []string{"ss", "sq"} {
		t.Run(keys, func(t *testing.T) {
			seen := keysSeen(t)
			op := newFakeOperator()
			sh := newFakeShell()
			shellsOf(op, sh)
			term := startRun(t, oneOperator(op), nil)
			onZathrasOfRun(t, term, op)
			before := seen()
			term.typeKeys(keys)
			sh.attached(t)
			sh.finish()
			term.wait("The shell ended.")
			// The second key was read by Bubble Tea before the shell had
			// the terminal, or it would be the shell's. The program has
			// taken it by now, and dropped it.
			await(t, "both keys to be taken by the program", func() bool { return seen() == before+2 })
			if got := sh.got(); got != "" {
				t.Errorf("the session got %q, which was typed before it had the terminal", got)
			}
			term.typeKeys("?")
			term.wait("on a zathras: open a shell")
			if n := op.shellsAsked(); n != 1 {
				t.Errorf("draal was asked for %d shells, want 1", n)
			}
			term.typeKeys("?q")
			if err := term.end(); err != nil {
				t.Errorf("Run = %v, want nil", err)
			}
		})
	}
}

// A key that Bubble Tea's reader holds when the terminal is given away:
// read, and not yet handed to the program, which is busy. It is handed
// over when the program takes messages again, behind the shell, and
// before the message that the shell has ended: the model drops it.
//
// The order stands on this: the reader has waited to send the key since
// before the shell, the message of the shell's end is sent after it,
// and a Go channel serves the senders that wait in the order they came.
func TestKeyHeldWhenTheTerminalIsGivenAway(t *testing.T) {
	held := make(chan struct{})  // closed when the program is at the answer of draal
	letGo := make(chan struct{}) // closed to let it go on
	var keys []string
	var mu sync.Mutex
	filterForTests = func(_ tea.Model, msg tea.Msg) tea.Msg {
		switch msg := msg.(type) {
		case shellOpenedMsg:
			close(held)
			<-letGo
		case tea.KeyPressMsg:
			mu.Lock()
			keys = append(keys, msg.String())
			mu.Unlock()
		case shellEndedMsg:
			mu.Lock()
			keys = append(keys, "(the shell has ended)")
			mu.Unlock()
		}
		return msg
	}
	t.Cleanup(func() { filterForTests = nil })
	var once sync.Once
	release := func() { once.Do(func() { close(letGo) }) }
	op := newFakeOperator()
	sh := newFakeShell()
	shellsOf(op, sh)
	term := startRun(t, oneOperator(op), nil)
	t.Cleanup(release)
	onZathrasOfRun(t, term, op)
	term.typeKeys("s")
	waitFor(t, "the program to be at draal's answer", held)
	// The program takes nothing now. q is read by Bubble Tea's reader,
	// which then waits to hand it over.
	term.typeKeys("q")
	await(t, "Bubble Tea's reader to hold the key", func() bool {
		buf := make([]byte, 4<<20)
		for _, g := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
			if strings.Contains(g, "[chan send") && strings.Contains(g, "TerminalReader).sendEvents") {
				return true
			}
		}
		return false
	})
	release()
	sh.attached(t)
	sh.finish()
	term.wait("The shell ended.")
	term.typeKeys("?")
	term.wait("on a zathras: open a shell")
	mu.Lock()
	order := strings.Join(keys, " ")
	mu.Unlock()
	if !strings.HasSuffix(order, "s q (the shell has ended) ?") {
		t.Errorf("the program took %q; want the key that was held before the end of the shell", order)
	}
	if got := sh.got(); got != "" {
		t.Errorf("the session got %q", got)
	}
	term.typeKeys("?q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil: the q that was held is not acted on", err)
	}
}

// Esc gives up a shell that draal has not opened: the request is ended,
// the keys are the terminal UI's again, and a session that draal opens
// all the same is closed and never has the terminal.
func TestEscGivesUpAShellThatIsOpened(t *testing.T) {
	op := newFakeOperator()
	late, second := newFakeShell(), newFakeShell()
	answer := make(chan struct{})
	ended := make(chan struct{})
	n := 0
	var mu sync.Mutex
	op.shell = func(ctx context.Context, _ wire.ID, _ wire.ShellRequest) (Shell, error) {
		mu.Lock()
		n++
		first := n == 1
		mu.Unlock()
		if !first {
			return second, nil
		}
		// The request is ended by its context, and draal answers it all
		// the same, later.
		<-ctx.Done()
		close(ended)
		<-answer
		return late, nil
	}
	term := startRun(t, oneOperator(op), nil)
	onZathrasOfRun(t, term, op)
	mark := len(term.written())
	term.typeKeys("s")
	term.wait("Opening a shell on web-7d9f8b6c4-2xkqp … esc gives it up")
	term.typeKeys("\x1b")
	term.wait("The shell was given up before draal had opened it.", "20ab12cd34ab12cd")
	waitFor(t, "the request for the shell to be ended", ended)
	close(answer)
	late.isClosed(t)
	// Another shell, which has the terminal as any other.
	term.typeKeys("s")
	second.attached(t)
	second.finish()
	term.wait("The shell ended.")
	if n := len(late.sizes); n != 0 {
		t.Error("the terminal was joined to a session that was given up")
	}
	if n := strings.Count(term.written()[mark:], "great-machine: a shell on"); n != 1 {
		t.Errorf("%d shells were announced on the terminal, want the one that was opened", n)
	}
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// Review Focus 3, while draal is asked for a shell: the connection is
// lost. The shell is given up with it, the view says so, and the
// terminal was never given away.
func TestConnectionLostWhileAShellIsOpened(t *testing.T) {
	op := newFakeOperator()
	op.shell = func(ctx context.Context, _ wire.ID, _ wire.ShellRequest) (Shell, error) {
		<-ctx.Done()
		return nil, fmt.Errorf("operator: shell: %w", ctx.Err())
	}
	term := startRun(t, oneOperator(op), func(cfg *Config) { cfg.RetryFirst, cfg.RetryMax = time.Hour, time.Hour })
	onZathrasOfRun(t, term, op)
	mark := len(term.written())
	term.typeKeys("s")
	term.wait("Opening a shell on")
	close(op.fleets)
	term.wait("No shell was opened: the connection to draal was lost.", "✖ lost for")
	if out := term.written()[mark:]; strings.Contains(out, "\x1b[?1049l") {
		t.Errorf("the terminal UI left its screen for a shell that was never opened: %q", out)
	}
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// The watch and the log are read whatever the program does: 2,000
// fleets and 20,000 lines come while a shell has the terminal and the
// program takes no message, and every one of them is taken off its
// stream. A shell needs that: it is a stream of the same connection, and
// two streams of a go-iroh connection that nobody reads stall every
// other stream of it, the shell's among them.
func TestWatchAndLogAreReadWhileAShellIsOpen(t *testing.T) {
	op := newFakeOperator()
	sh := newFakeShell()
	shellsOf(op, sh)
	term := startRun(t, oneOperator(op), nil)
	lines := onZathrasOfRun(t, term, op)
	lines <- wire.LogLine{At: t0, Stream: "stdout", Text: "the first line"}
	term.wait("the first line")
	term.typeKeys("s")
	sh.attached(t)
	done := make(chan string, 2)
	go func() {
		for i := range 2000 {
			f := fixtureFleet()
			f.Services[0].Stable = uint64(i + 1)
			select {
			case op.fleets <- f:
			case <-time.After(deadline):
				done <- fmt.Sprintf("fleet %d was not read while the shell was open", i)
				return
			}
		}
		done <- ""
	}()
	go func() {
		for i := range 20000 {
			select {
			case lines <- wire.LogLine{At: t0.Add(time.Duration(i+1) * time.Millisecond), Stream: "stdout", Text: fmt.Sprintf("flood %d", i)}:
			case <-time.After(deadline):
				done <- fmt.Sprintf("line %d was not read while the shell was open", i)
				return
			}
		}
		done <- ""
	}()
	for range 2 {
		if msg := <-done; msg != "" {
			t.Error(msg)
		}
	}
	await(t, "the last fleet to be taken", func() bool { return len(op.fleets) == 0 })
	select {
	case <-sh.closing:
		t.Fatal("the shell ended during the flood")
	default:
	}
	sh.finish()
	term.wait("The shell ended.")
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// answerNotTaken makes the program lose draal's answer to the request
// for a shell, as when the program ends, or the shell is given up,
// before it takes the answer. The session is then held by the runner
// and nobody has the terminal joined to it. The channel is closed when
// the answer has come.
func answerNotTaken(t *testing.T) <-chan struct{} {
	t.Helper()
	came := make(chan struct{})
	var once sync.Once
	filterForTests = func(_ tea.Model, msg tea.Msg) tea.Msg {
		if _, ok := msg.(shellOpenedMsg); ok {
			once.Do(func() { close(came) })
			return nil
		}
		return msg
	}
	t.Cleanup(func() { filterForTests = nil })
	return came
}

// Run ends while the runner holds a session that draal has opened and
// the program has not taken: the session is closed. And when its Close
// does not return, Run does not wait for it longer than closeAfter.
func TestSessionThatIsHeldWhenRunEnds(t *testing.T) {
	closing := closeAfter
	closeAfter = 300 * time.Millisecond
	t.Cleanup(func() { closeAfter = closing })
	for _, how := range []string{"its Close returns", "its Close does not return"} {
		t.Run(how, func(t *testing.T) {
			came := answerNotTaken(t)
			op := newFakeOperator()
			sh := newFakeShell()
			var once sync.Once
			release := func() {}
			if how == "its Close does not return" {
				sh.hold = make(chan struct{})
				release = func() { once.Do(func() { close(sh.hold) }) }
			}
			shellsOf(op, sh)
			term := startRun(t, oneOperator(op), nil)
			t.Cleanup(release)
			onZathrasOfRun(t, term, op)
			term.typeKeys("s")
			waitFor(t, "draal's answer", came)
			term.stop()
			if err := term.end(); err != context.Canceled {
				t.Errorf("Run = %v, want the error of its context", err)
			}
			waitFor(t, "the session that was held to be closed", sh.closing)
			release()
			sh.isClosed(t)
		})
	}
}

// Esc gives up a shell whose session the runner holds already, and the
// Close of that session does not return. The terminal UI is not held up
// by it: it takes the next key.
func TestEscWithASessionWhoseCloseDoesNotReturn(t *testing.T) {
	came := answerNotTaken(t)
	op := newFakeOperator()
	sh := newFakeShell()
	sh.hold = make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(sh.hold) }) }
	shellsOf(op, sh)
	term := startRun(t, oneOperator(op), nil)
	t.Cleanup(release)
	onZathrasOfRun(t, term, op)
	term.typeKeys("s")
	term.wait("Opening a shell on")
	waitFor(t, "draal's answer", came)
	term.typeKeys("\x1b")
	term.wait("The shell was given up before draal had opened it.")
	waitFor(t, "the session that was held to be closed", sh.closing)
	term.typeKeys("?")
	term.wait("on a zathras: open a shell")
	release()
	sh.isClosed(t)
	term.typeKeys("?q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// Review Focus 3, between draal's answer and the terminal changing
// hands: the connection is lost, and the program is told before it
// gives the terminal away. The session of a connection that is gone is
// not given the terminal: nothing is written for it, it is closed, and
// the line says that the connection was lost.
func TestConnectionLostBetweenTheAnswerAndTheShell(t *testing.T) {
	op := newFakeOperator()
	sh := newFakeShell()
	shellsOf(op, sh)
	var once sync.Once
	filterForTests = func(_ tea.Model, msg tea.Msg) tea.Msg {
		if _, ok := msg.(shellOpenedMsg); ok {
			once.Do(func() {
				// The watch ends, and the program is at draal's answer
				// until the message of that waits to be sent: it is then
				// taken before the one that gives the terminal away, which
				// is sent later, as a channel serves those who wait in
				// their order.
				close(op.fleets)
				await(t, "the end of the watch to wait for the program", func() bool {
					buf := make([]byte, 4<<20)
					for _, g := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
						if strings.Contains(g, "(*Program).Send") && strings.Contains(g, "tui.(*runner).dial.func") && !strings.Contains(g, "dial.func1.1") {
							return true
						}
					}
					return false
				})
			})
		}
		return msg
	}
	t.Cleanup(func() { filterForTests = nil })
	term := startRun(t, oneOperator(op), func(cfg *Config) { cfg.RetryFirst, cfg.RetryMax = time.Hour, time.Hour })
	onZathrasOfRun(t, term, op)
	mark := len(term.written())
	term.typeKeys("s")
	term.wait("✖ lost for", "No shell was opened: the connection to draal was lost.")
	sh.isClosed(t)
	if out := term.written()[mark:]; strings.Contains(out, "great-machine: a shell on") || len(sh.sizes) != 0 {
		t.Errorf("the session of a connection that was lost was given the terminal: %q", out)
	}
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}
