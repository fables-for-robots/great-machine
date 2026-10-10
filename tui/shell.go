package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	xterm "golang.org/x/term"

	"github.com/fables-for-robots/great-machine/term"
	"github.com/fables-for-robots/great-machine/wire"
)

// The shell of a zathras, which is opened in two steps, so that the
// terminal is given away only for a session that exists.
//
// First the model asks for a session (effShell), and the program asks
// draal for one while the terminal UI keeps the terminal and its screen:
// the view says that a shell is being opened, and Esc gives it up. When
// draal has opened the session the program holds it and tells the model
// (shellOpenedMsg), and only then the model asks for the terminal to be
// given to it (effAttach): Bubble Tea gives the terminal away, the
// session has it until the shell has ended or was left, and a message
// tells the model how it ended (shellEndedMsg). A session that draal
// refuses, or that comes when nobody waits for it any more, never has
// the terminal.
//
// From the key s until the shell has ended, the model acts on no key
// but Esc, and on that only while the shell is being opened. What is
// typed while the terminal UI still has the terminal is dropped: it is
// not the terminal UI's, since a shell was asked for, and not the
// shell's, which was not there. What is typed while the shell has the
// terminal is the shell's, but for Ctrl-], and what the shell writes is
// on the terminal's own screen, where it stays when the terminal UI is
// back. While the shell has the terminal the program does nothing else:
// it reads no key, draws nothing and takes no message.

// shellStage is how far a shell is that was asked for.
type shellStage int

const (
	// shellNone: no shell was asked for, or the last one has ended.
	shellNone shellStage = iota
	// shellOpening: draal was asked for a session and has not answered.
	shellOpening
	// shellOpen: the session is there, and the terminal is given to it,
	// or about to be, or was and the model has not been told of its end.
	shellOpen
)

// shellState is what the model holds of a shell: no session, but the
// number of the request for one.
type shellState struct {
	stage shellStage
	n     int    // the number of the shell that was asked for last
	name  string // how its zathras is named; clean
}

// effShell asks the program to ask draal for a shell session on the
// zathras, with the terminal's size. It answers with shellOpenedMsg of
// the same number, and holds the session until effAttach or effGiveUp.
type effShell struct {
	epoch   int
	zathras wire.ID
	n       int
}

// effGiveUp tells the program that nobody waits for the shell of this
// number any more: the request is ended, and a session that is there or
// comes yet is closed.
type effGiveUp struct{ n int }

// effAttach asks the program to give the terminal to the session of
// this number, and to take it back when the shell has ended or was
// left. It answers with shellEndedMsg.
type effAttach struct {
	n    int
	name string // how the line above the shell names the zathras; clean
}

// shellOpenedMsg says that draal has opened the session of this number,
// which the program holds, or with err why there is none.
type shellOpenedMsg struct {
	n   int
	err error
}

// shellEndedMsg says how a shell ended: with an exit status, left with
// Ctrl-], or with an error. A shell that was never opened has ended with
// the error that kept it from opening.
type shellEndedMsg struct {
	opened   bool // draal has opened the session
	exit     int
	detached bool
	err      error
}

// shellCrashedMsg says that the terminal could not be given away or
// taken back, or that something panicked while the shell had it. It is
// the program's to take, and ends it.
type shellCrashedMsg struct {
	what  any
	stack []byte
}

// openShell is the key s on a zathras that is connected, with a
// connection to draal that is live: draal is asked for the session, and
// the view says so.
func (m *model) openShell() []effect {
	m.sh.n++
	m.sh.stage, m.sh.name = shellOpening, named(m.seen.Name)
	m.notice = "Opening a shell on " + m.sh.name + " … esc gives it up"
	return []effect{effShell{epoch: m.epoch, zathras: m.zathras, n: m.sh.n}}
}

// shellTakesKey says whether a key is not the model's to act on because
// a shell was asked for and has not ended, and acts on the one that is
// then: Esc, which gives up a shell that draal has not opened yet.
//
// What this holds, and what it does not. A key typed while the shell
// has the terminal never reaches the terminal UI, and a key typed
// before s never reaches the shell. Within a few milliseconds of the
// terminal changing hands, in either direction, a key may be dropped:
// the model is told of the shell's end by a message, and a key that
// Bubble Tea hands over before that message is one of those it drops.
// A key typed in the instant before the shell takes the terminal has
// one of three ends: Bubble Tea has read it and the model drops it; or
// Bubble Tea has not read it and it is the shell's; or, rarely, Bubble
// Tea has read it behind another key that it could not hand over
// before the terminal went, and it is acted on by the terminal UI when
// the shell has ended. Nothing in Bubble Tea tells such a key from one
// that is typed after the shell.
func (m *model) shellTakesKey(name string) (effs []effect, taken bool) {
	switch {
	case m.sh.stage == shellNone:
		return nil, false
	case m.sh.stage == shellOpening && name == "esc":
		return m.giveUpShell("The shell was given up before draal had opened it."), true
	}
	return nil, true
}

// giveUpShell ends the waiting for a shell that is being opened, and
// says why.
func (m *model) giveUpShell(why string) []effect {
	if m.sh.stage != shellOpening {
		return nil
	}
	m.sh.stage, m.notice = shellNone, why
	return []effect{effGiveUp{n: m.sh.n}}
}

// shellOpened takes draal's answer to the request for a session. The
// answer to a request that was given up is not read: the program has
// closed that session, or closes it when it comes.
func (m *model) shellOpened(msg shellOpenedMsg) []effect {
	if m.sh.stage != shellOpening || msg.n != m.sh.n {
		return nil
	}
	if msg.err != nil {
		m.sh.stage, m.notice = shellNone, shellSentence(shellEndedMsg{err: msg.err})
		return nil
	}
	m.sh.stage, m.notice = shellOpen, ""
	return []effect{effAttach{n: msg.n, name: m.sh.name}}
}

// shellEnded takes the message that a shell has ended, and says in one
// line how. While the shell had the terminal the model was told nothing,
// for as long as that was: what the view holds is asked for anew.
func (m *model) shellEnded(msg shellEndedMsg) []effect {
	m.sh.stage, m.notice = shellNone, shellSentence(msg)
	return m.again()
}

// shellSentence is the one line that says how a shell ended.
func shellSentence(msg shellEndedMsg) string {
	why := ""
	if msg.err != nil {
		// What draal or the zathras refused is said as its sentence; of
		// anything else, the whole error. Either is kept to the limit of a
		// field, as every other word of theirs is.
		why = clean(capped(msg.err.Error()))
		if e, ok := refusal(msg.err); ok {
			why = clean(capped(e.Message))
		}
	}
	switch {
	case !msg.opened:
		return "No shell was opened: " + why
	case msg.detached:
		return "The shell was left with Ctrl-], which hung it up."
	case msg.err != nil:
		return "The shell ended: " + why
	case msg.exit < 0:
		return "The shell was ended by a signal."
	case msg.exit > 0:
		return fmt.Sprintf("The shell ended with status %d.", msg.exit)
	}
	return "The shell ended."
}

// shellGrace is how long a shell has to give the terminal back once the
// context of Run is done. term.Attach returns at once then; what does
// not return is a session that does not return from being told the size
// at the start of term.Attach, with the terminal already in the mode of
// a shell. Behind the grace the terminal is taken back without waiting,
// so that the program ends as it does without a shell.
//
// A shell that is left behind so sets the mode of the terminal once
// more when it comes loose, which may be after Run has returned: that is
// term.Attach putting back the mode it found, the terminal's usual one.
// A Shell of the client cannot wait there, since nothing was sent on its
// stream before; it takes an Operator that misbehaves.
//
// The grace has to be over well before killAfter, which is when a
// program that has not ended is ended by force: a kill that came while a
// shell had the terminal would draw over the shell and change the
// terminal's mode under term.Attach. graceOfShell sees to it whatever
// the two are set to.
var shellGrace = 2 * time.Second

// graceOfShell is shellGrace, and at most half of killAfter.
func graceOfShell() time.Duration {
	return min(shellGrace, killAfter/2)
}

// pendingShell is the shell that draal was asked for last and that has
// not been given the terminal: the request, and the session once it is
// there.
type pendingShell struct {
	n      int
	ctx    context.Context // done when the connection it was asked over is hung up
	cancel context.CancelFunc
	sess   Shell // nil until draal has opened it
}

// drop ends a shell that nobody waits for: the request is ended at
// once, and the session, if it is there, is closed in a goroutine of
// the runner. A Close that does not return so holds up neither Update,
// which gives a shell up, nor the end of Run, which waits closeAfter for
// what the runner started and no more.
func (r *runner) drop(p *pendingShell) {
	if p == nil {
		return
	}
	p.cancel()
	if sess := p.sess; sess != nil {
		r.spawn(func() { sess.Close() })
	}
}

// openShell carries out effShell: draal is asked for the session in a
// goroutine, and the program is told what came of it. The terminal is
// the terminal UI's meanwhile.
func (r *runner) openShell(e effShell) {
	c, op := r.current(e.epoch)
	if c == nil {
		r.spawn(func() { r.send(shellOpenedMsg{n: e.n, err: errors.New("the connection to draal is gone")}) })
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	p := &pendingShell{n: e.n, ctx: ctx, cancel: cancel}
	r.mu.Lock()
	old := r.pending
	r.pending = p
	r.mu.Unlock()
	r.drop(old)
	r.spawn(func() {
		cols, rows := term.Size(r.cfg.In)
		sh, err := op.Shell(ctx, e.zathras, wire.ShellRequest{Zathras: e.zathras, Cols: cols, Rows: rows, Term: os.Getenv("TERM")})
		// Whoever gave the shell up meanwhile has taken it out of the
		// runner: the session is then nobody's, and closed here.
		r.mu.Lock()
		waited := r.pending == p
		if waited && err == nil {
			p.sess = sh
		}
		r.mu.Unlock()
		switch {
		case !waited:
			cancel()
			if err == nil {
				sh.Close()
			}
			return
		case err != nil:
			r.giveUp(e.n)
		}
		r.send(shellOpenedMsg{n: e.n, err: err})
	})
}

// giveUp carries out effGiveUp: the shell of that number, if it is the
// one that waits, is ended.
func (r *runner) giveUp(n int) {
	r.mu.Lock()
	p := r.pending
	if p == nil || p.n != n {
		r.mu.Unlock()
		return
	}
	r.pending = nil
	r.mu.Unlock()
	r.drop(p)
}

// take returns the shell of that number for the terminal to be given
// to, if it waits with its session, and nil otherwise.
func (r *runner) take(n int) *pendingShell {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.pending
	if p == nil || p.n != n || p.sess == nil {
		return nil
	}
	r.pending = nil
	return p
}

// shell carries out effAttach. It is called from Update, and what it
// returns is run by Bubble Tea: tea.Exec gives the terminal away for as
// long as its command runs, and takes it back. The terminal is then in
// its usual mode and on its own screen, as term.Attach expects it and
// leaves it.
//
// Until Bubble Tea has given the terminal away it reads it. A key that
// its reader holds at that moment, read and not yet handed to the
// program, is handed over when the program takes messages again, behind
// the shell: before the message that the shell has ended, since it has
// waited longer to be sent and a channel serves those who wait in their
// order. The model still has the shell as open then, and drops it. (A
// second key of the same read would come behind that message, and be
// the terminal UI's: it takes two keys within the instant in which the
// terminal changes hands.)
func (r *runner) shell(e effAttach) tea.Cmd {
	c := &shellExec{r: r, e: e}
	return tea.Exec(c, func(err error) tea.Msg {
		switch {
		case c.done.panicked != nil:
			return shellCrashedMsg{what: c.done.panicked, stack: c.done.stack}
		case err != nil:
			// Run returns no error: this one is Bubble Tea's, which could
			// not give the terminal away or take it back.
			return shellCrashedMsg{what: fmt.Errorf("handing the terminal over: %w", err), stack: debug.Stack()}
		}
		return c.done.ended
	})
}

// shellExec is one shell, as Bubble Tea runs it between giving the
// terminal away and taking it back.
type shellExec struct {
	r    *runner
	e    effAttach
	done shellDone
}

// shellDone is what came of a shell.
type shellDone struct {
	ended    shellEndedMsg // how it ended
	panicked any           // or what it panicked with
	stack    []byte        // and where
}

// SetStdin is of tea.ExecCommand. The shell is joined to the terminal of
// the Config, which is what Bubble Tea would hand over here.
func (c *shellExec) SetStdin(io.Reader) {}

// SetStdout is of tea.ExecCommand.
func (c *shellExec) SetStdout(io.Writer) {}

// SetStderr is of tea.ExecCommand.
func (c *shellExec) SetStderr(io.Writer) {}

// Run joins the terminal to the session until the shell has ended or
// was left, and closes the session, which hangs the shell up. It always
// returns nil: how the shell ended is the message.
//
// The shell is a goroutine of its own, which Run waits for, so that Run
// can return without it when the context of Run is done and the shell
// does not end with it: the terminal is put back in the mode it was
// given away in, Bubble Tea takes it back, and the program ends in its
// usual way, with the terminal as it was. What is left behind then is
// the goroutine of the shell, which the end of Run waits closeAfter for.
// A panic, of the session or of term.Attach behind its own restoring of
// the terminal, is kept and not passed on, so that the terminal is
// taken back first and the program ends as it does after any other
// panic.
func (c *shellExec) Run() error {
	r := c.r
	p := r.take(c.e.n)
	if p == nil {
		c.done = shellDone{ended: shellEndedMsg{err: errors.New("its session is gone")}}
		return nil
	}
	// The connection may have been lost, or Run told to end, between
	// draal's answer and this moment: the session is then not given the
	// terminal, and nothing is written. (The terminal has left the
	// terminal UI's screen for it all the same, and comes back at once.)
	switch {
	case p.ctx.Err() != nil:
		r.drop(p)
		c.done = shellDone{ended: shellEndedMsg{err: errors.New("the connection to draal was lost.")}}
		return nil
	case r.user.Err() != nil:
		r.drop(p)
		c.done = shellDone{ended: shellEndedMsg{err: context.Cause(r.user)}}
		return nil
	}
	// The shell ends with the connection it was asked over, and with the
	// context of Run: the program takes no message while the shell has
	// the terminal, so nothing else of the program could end it.
	ctx, cancel := context.WithCancel(p.ctx)
	defer cancel()
	stop := context.AfterFunc(r.user, cancel)
	defer stop()

	// The mode the terminal is given away in, which is its usual one.
	fd := int(r.cfg.In.Fd())
	usual, _ := xterm.GetState(fd)

	// Everything the goroutine of the shell writes to the terminal goes
	// through out, which is shut however Run returns: nothing of the
	// shell is written once Bubble Tea has the terminal back. On the
	// ways out but one the goroutine has written its last before it
	// tells how the shell ended; the one is the shell left behind.
	out := &shutOut{w: r.cfg.Out}
	defer out.shut()
	done := make(chan shellDone, 1)
	r.spawn(func() {
		defer func() {
			if v := recover(); v != nil {
				done <- shellDone{panicked: v, stack: debug.Stack()}
			}
		}()
		done <- shellDone{ended: r.attach(ctx, p, c.e.name, out)}
	})
	select {
	case c.done = <-done:
		return nil
	case <-r.user.Done():
	}
	grace := time.NewTimer(graceOfShell())
	defer grace.Stop()
	select {
	case c.done = <-done:
	case <-grace.C:
		// The shell is left behind. Nothing of it reaches the terminal
		// from here on, but for a write that is under way, which is not
		// waited for: a terminal that is held holds it. If the shell has
		// the terminal in the mode of a shell, the terminal is put back:
		// Bubble Tea takes the mode it finds now for the one to restore at
		// the end. Out is shut first, before the terminal is put back.
		out.shut()
		if usual != nil {
			xterm.Restore(fd, usual)
		}
		c.done = shellDone{ended: shellEndedMsg{opened: true, err: fmt.Errorf("the shell did not end with the terminal UI: %w", context.Cause(r.user))}}
	}
	return nil
}

// attach joins the terminal to a session that is open until the shell
// has ended or was left, or ctx is done, and closes the session. It
// writes to the terminal through out alone.
func (r *runner) attach(ctx context.Context, p *pendingShell, name string, out io.Writer) shellEndedMsg {
	// However this is left, the session is closed: a shell that nobody
	// is joined to is hung up. It is closed here, in the goroutine of the
	// shell, before the terminal is taken back.
	defer func() {
		p.cancel()
		p.sess.Close()
	}()

	fmt.Fprint(out, aboveShell(name))
	exit, detached, err := term.Attach(ctx, r.cfg.In, out, p.sess)
	if detached {
		// The prompt of the shell is left as it is, on a line of its own.
		fmt.Fprint(out, "\r\n")
	}
	return shellEndedMsg{opened: true, exit: exit, detached: detached, err: err}
}

// aboveShell is the line on the terminal's own screen above what the
// shell writes: it says where the prompt below is from, and the way out.
// Of the name it keeps nameAboveShell cells at most, cut in the middle as
// the views cut a name that is too long: the line stays on the screen
// when the terminal UI is back, and a name of kilobytes would fill it.
func aboveShell(name string) string {
	return fmt.Sprintf("great-machine: a shell on %s. Ctrl-] leaves it.\r\n", middle(name, nameAboveShell))
}

// nameAboveShell is how many cells of a name the line above a shell
// keeps.
const nameAboveShell = 60

// shutOut is the terminal as the goroutine of a shell writes to it: it
// passes every write on as it comes, one at a time, until it is shut.
// From then on a write reaches nothing and is reported written. An error
// would give term.Attach of a shell that is left behind an end other
// than its context, and on that end term.Attach waits for the session
// and for its reading of the terminal, which may never come, and the
// session would never be closed; so the shell ends by its context
// alone. Shut does not wait for the write that is under way, if one is:
// a terminal that is held (Ctrl-S) holds that write, and must not hold
// Run. That one piece may still arrive after Run has returned, and
// nothing follows it, as term.Attach has it for the end of its context.
type shutOut struct {
	w      io.Writer
	mu     sync.Mutex // held by the write that is under way
	closed atomic.Bool
}

// Write writes p to the terminal, unless out is shut: then it throws p
// away.
func (o *shutOut) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed.Load() {
		return len(p), nil
	}
	return o.w.Write(p)
}

// shut lets no write begin from here on, and returns at once.
func (o *shutOut) shut() { o.closed.Store(true) }
