package tui

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/fables-for-robots/great-machine/human"
	"github.com/fables-for-robots/great-machine/wire"
)

// killAfter is how long the program has to end by itself once its
// context is done, before it is ended whatever it is doing. (It and the
// two times below are variables for the tests alone.)
var killAfter = 5 * time.Second

// cutAfter is how long Run waits for the program once it was ended
// whatever it was doing, and closeAfter how long it waits for what the
// program started: for an Operator whose Dial, Watch or Close does not
// return. Behind them Run returns without what it waited for.
var (
	cutAfter   = time.Second
	closeAfter = 5 * time.Second
)

// killStage is called by the timer that ends a program whatever it is
// doing: with "fired" when the time has come, with "killing" when it has
// decided to end the program and is about to, and with "done" when it
// returns. connectStage is called on the way to the verdict on a dial
// (see bound): with "answered" when Dial, and the watch if Dial gave a
// connection, have returned and the verdict is about to be asked for,
// and by the timer with "fired" when the time has come, then "late"
// when draal had answered and "given up" when the attempt is given up.
// filterForTests, if set, sees every message of the program before it
// is handled, as a filter of Bubble Tea does. All three are for the
// tests alone.
var (
	killStage      = func(stage string) {}
	connectStage   = func(stage string) {}
	filterForTests func(tea.Model, tea.Msg) tea.Msg
)

// tickEvery is how often the times that are drawn move on.
const tickEvery = time.Second

// Run runs the terminal UI until the user quits, which is nil, or ctx is
// done, which is the error of ctx. The terminal is as it was when Run
// returns.
//
// What draal refuses at the first dial, by Dial or by the watch that
// follows it (the code unauthorized), ends Run with that error. Any
// other failure of a dial is tried again, and so is a dial that has not
// had its first fleet ConnectWait after Dial was called.
//
// Run returns within a bounded time of ctx being done, whatever the
// program and the Operator do, but for one thing: a terminal that takes
// no output (a session over a network that has stalled) holds the
// renderer in its write, and with it the giving back of the terminal,
// and Run returns only when the terminal takes output again or goes. It
// is left so, since to return without giving the terminal back would
// leave it raw and on the alternate screen.
//
// A program that has not ended killAfter after ctx was done (an Update
// that does not return) is ended as it is: the terminal is restored,
// and Run returns the error of ctx in a sentence that says so, leaving
// behind the goroutine that is stuck and what it holds. (A program that
// had begun to end by itself by then is not ended a second time: should
// it stay in its last view, Run returns all the same, and says that the
// terminal may not be as it was.) And when something asked of the
// Operator does not return closeAfter after it was told to end (a Dial,
// a Watch or a Close that does not honour its context), Run returns
// what it would have returned, says so in the log of its Config, and
// leaves behind the goroutines that wait for the Operator, and its
// connection.
//
// The text of every error Run returns that has words of draal in it is
// safe to print on a terminal: it went through the function everything
// that is drawn goes through. errors.Is and errors.As see through it;
// the *wire.Error that errors.As finds has its code and its message
// made safe likewise.
func Run(ctx context.Context, cfg Config) error {
	return run(ctx, cfg, nil)
}

// run is Run for the tests: with drawn, if set, called with every view
// as it is handed to the renderer, and with options of Bubble Tea (the
// environment).
func run(ctx context.Context, cfg Config, drawn func(view string), opts ...tea.ProgramOption) error {
	if cfg.Dial == nil || cfg.In == nil || cfg.Out == nil {
		return errors.New("tui: Dial, In and Out are needed")
	}
	cfg = cfg.withDefaults()
	all, end := context.WithCancel(context.WithoutCancel(ctx))
	prog := &program{m: newModel(cfg), r: &runner{cfg: cfg, ctx: all, user: ctx}, drawn: drawn}
	prog.r.crash = func(v any) { prog.recovered(v) }
	// The context is not given to Bubble Tea: it would end by closing
	// its reader of the terminal under the read. It is asked to quit.
	// It handles no signals: they are the caller's, who ends ctx.
	// How the program ends is decided once, under one mutex: by itself,
	// when it takes the message that tells it to quit, or by the timer
	// below, whatever it is doing. Whoever comes second finds it decided
	// and leaves the terminal alone: the terminal is given back by one
	// of them, and nothing is written to it once Run has returned.
	var ending sync.Mutex
	decided := false
	// The filter is the place where the program is seen to take that
	// message; every other message passes as it came. It is the last of
	// the options, so that no other filter takes its place.
	ends := tea.WithFilter(func(m tea.Model, msg tea.Msg) tea.Msg {
		if filterForTests != nil {
			if msg = filterForTests(m, msg); msg == nil {
				return nil
			}
		}
		switch msg.(type) {
		case tea.QuitMsg, tea.InterruptMsg:
			// A kill that is under way is waited for here. When this
			// message is let through after it, the program's context is
			// done: it ends as one that was killed, and draws nothing.
			ending.Lock()
			decided = true
			ending.Unlock()
		}
		return msg
	})
	options := append([]tea.ProgramOption{tea.WithInput(cfg.In), tea.WithOutput(cfg.Out), tea.WithoutSignalHandler()}, opts...)
	p := tea.NewProgram(prog, append(options, ends)...)
	prog.r.send = p.Send
	killed, late := make(chan struct{}), make(chan struct{})
	stopQuit := context.AfterFunc(ctx, func() {
		// The end whatever it is doing is set first: asking the program
		// to quit waits until it takes the message, and a program whose
		// Update does not return, or that has given the terminal to a
		// shell, takes none.
		kill := time.AfterFunc(killAfter, func() {
			killStage("fired")
			defer killStage("done")
			ending.Lock()
			defer ending.Unlock()
			if decided {
				// The program is ending by itself, or has ended: it
				// gives the terminal back, and this must not.
				close(late)
				return
			}
			decided = true
			killStage("killing")
			// The terminal is given back the way it is for a shell,
			// which waits for the reader of the terminal to have ended;
			// the kill alone closes the terminal under its read. Each of
			// the two closes the renderer, so the resets are written
			// twice; the second set changes nothing but for a terminal
			// with a stack of keyboard modes pushed by the program that
			// started this one, which loses one level. Bubble Tea has no
			// way to end a program that is stuck without closing, and
			// this is the path of a failure.
			//
			// One window is left: a program that ends through an error of
			// Bubble Tea's own (not by a message to quit) at the very
			// moment this fires gives the terminal back while this does.
			_ = p.ReleaseTerminal()
			p.Kill()
			close(killed)
		})
		prog.r.mu.Lock()
		prog.r.kill = kill
		prog.r.mu.Unlock()
		go p.Quit()
	})
	ran := make(chan error, 1)
	go func() {
		_, err := p.Run()
		ran <- err
	}()
	var err error
	cut, itself := false, false
	select {
	case err = <-ran:
		// The program has ended and given the terminal back. A timer
		// that fires from here on does nothing, and one that is at work
		// is waited for. One that was at work has cut the program off,
		// which is then why it ended.
		ending.Lock()
		decided = true
		ending.Unlock()
		select {
		case <-killed:
			cut = true
		default:
		}
	case <-killed:
		// The terminal is restored, and the program was cut off, whether
		// it returns now or later. It returns when its Update does; Run
		// does not wait for that.
		cut = true
		select {
		case err = <-ran:
		case <-time.After(cutAfter):
		}
	case <-late:
		// The program took the message to quit and has not ended by the
		// time it would have been killed: it is in its last view, or
		// giving the terminal back. That is its own to finish, and is
		// waited for a moment longer, and no more.
		select {
		case err = <-ran:
		case <-time.After(cutAfter):
			cut, itself = true, true
		}
	}
	stopQuit()
	// Everything the program started is ended, and waited for.
	end()
	if !prog.r.close(closeAfter) {
		cfg.Log.Warn("the terminal UI has ended, and something it asked of draal has not: it is left behind", "after", closeAfter)
	}
	if itself {
		return fmt.Errorf("the terminal UI did not end %v after it was told to, while it was ending by itself, and was left to it: the terminal may not be as it was: %w", killAfter, context.Cause(ctx))
	}
	if cut {
		return fmt.Errorf("the terminal UI did not end %v after it was told to and was cut off, with the terminal restored: %w", killAfter, context.Cause(ctx))
	}

	prog.mu.Lock()
	defer prog.mu.Unlock()
	switch {
	case prog.panicked != nil:
		return fmt.Errorf("the terminal UI failed, and the terminal was restored: panic: %s\n%s", clean(capped(fmt.Sprint(prog.panicked))), prog.stack)
	case errors.Is(err, tea.ErrProgramPanic):
		return fmt.Errorf("the terminal UI failed, and the terminal was restored: %w", err)
	case prog.failed != nil:
		return said{prog.failed}
	case prog.quit:
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case err != nil:
		return fmt.Errorf("the terminal UI: %w", err)
	}
	return nil
}

// said is an error of draal's as Run returns it: with a text that is
// safe to print on a terminal.
type said struct{ err error }

// Error returns the text of the error as it may be drawn.
func (e said) Error() string { return clean(capped(e.err.Error())) }

// Unwrap returns the error as it came, for errors.Is.
func (e said) Unwrap() error { return e.err }

// As gives errors.As a *wire.Error whose code and message are as safe
// as the text: the refusal in the chain, made clean.
func (e said) As(target any) bool {
	t, ok := target.(**wire.Error)
	if !ok {
		return false
	}
	w, ok := refusal(e.err)
	if !ok {
		return false
	}
	*t = &wire.Error{Code: wire.Code(clean(capped(string(w.Code)))), Message: clean(capped(w.Message))}
	return true
}

// program is the model as Bubble Tea runs it: it hands the messages to
// the model, has the runner carry out what the model wants, and draws
// the model on the alternate screen. A panic of the model ends the
// program in the usual way, so that the terminal is restored, and Run
// returns it as an error.
type program struct {
	m     *model
	r     *runner
	drawn func(view string) // for the tests: told every view

	mu       sync.Mutex
	panicked any    // what the model panicked with
	stack    []byte // and where
	quit     bool   // the user has quit
	failed   error  // why the model ended the program
}

// crashedMsg says that a view panicked: Update ends the program.
type crashedMsg struct{}

// recovered takes a panic of the model. It says whether there was one.
func (p *program) recovered(r any) bool {
	if r == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.panicked == nil {
		p.panicked, p.stack = r, debug.Stack()
	}
	return true
}

// crashed says whether the model has panicked.
func (p *program) crashed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.panicked != nil
}

// Init begins the model.
func (p *program) Init() (cmd tea.Cmd) {
	defer func() {
		if p.recovered(recover()) {
			cmd = tea.Quit
		}
	}()
	return p.do(p.m.start())
}

// Update hands a message to the model and carries out what it wants.
func (p *program) Update(msg tea.Msg) (next tea.Model, cmd tea.Cmd) {
	if p.crashed() {
		return p, tea.Quit
	}
	if crash, ok := msg.(shellCrashedMsg); ok {
		// Something failed while a shell had the terminal. Bubble Tea has
		// taken the terminal back, and the program ends as after a panic
		// of its own.
		p.mu.Lock()
		p.panicked, p.stack = crash.what, crash.stack
		p.mu.Unlock()
		return p, tea.Quit
	}
	defer func() {
		if p.recovered(recover()) {
			next, cmd = p, tea.Quit
		}
	}()
	return p, p.do(p.m.update(msg))
}

// View draws the model on the alternate screen, without a cursor and
// without the mouse.
func (p *program) View() (v tea.View) {
	v.AltScreen = true
	if p.crashed() {
		return v
	}
	defer func() {
		if p.recovered(recover()) {
			v.SetContent("")
			p.r.spawn(func() { p.r.send(crashedMsg{}) })
		}
	}()
	content := p.m.view()
	if p.drawn != nil {
		p.drawn(content)
	}
	v.SetContent(content)
	return v
}

// do carries out effects, and returns what Bubble Tea is to do for
// them.
func (p *program) do(effs []effect) tea.Cmd {
	var cmds []tea.Cmd
	for _, e := range effs {
		switch e := e.(type) {
		case effQuit:
			p.mu.Lock()
			p.quit = true
			p.mu.Unlock()
			cmds = append(cmds, tea.Quit)
		case effFail:
			p.mu.Lock()
			p.failed = e.err
			p.mu.Unlock()
			cmds = append(cmds, tea.Quit)
		case effAttach:
			cmds = append(cmds, p.r.shell(e))
		default:
			p.r.do(e)
		}
	}
	return tea.Batch(cmds...)
}

// runner carries out what the model wants against the Operator, and
// tells the program what came of it. Every goroutine it starts ends
// when its connection is hung up or the runner is closed, and close
// waits for them.
//
// It tells the program by send, which waits until the program takes the
// message. The program may not take one for a long time: while a shell
// has the terminal, it takes none at all. Nothing here grows meanwhile,
// and nothing gives up: the fleets and the lines of a log are read as
// they come and the newest are kept (one fleet, logKept lines), and
// every other goroutine is one that waits with its one message.
type runner struct {
	cfg   Config
	ctx   context.Context // done when the program has ended
	user  context.Context // the context of Run: done when the program is to end
	send  func(tea.Msg)
	kill  *time.Timer
	crash func(v any) // takes a panic of a goroutine of the runner

	wg      sync.WaitGroup
	mu      sync.Mutex
	conn    *conn         // the connection that is dialed or there
	tick    bool          // the ticker runs
	pending *pendingShell // the shell that was asked for and has not the terminal
}

// conn is one connection to draal: what Dial returned, and the context
// of everything that is asked of it.
type conn struct {
	epoch  int
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	op       Operator           // nil until Dial has returned
	hungUp   bool               // it is to be closed as soon as it is there
	unfollow context.CancelFunc // ends the log that is followed
}

// spawn runs f as a goroutine that close waits for. A panic in it, which
// may be one of a method of the Operator, ends the program as a panic of
// the model does: the terminal is restored, and Run returns it.
func (r *runner) spawn(f func()) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() {
			v := recover()
			switch {
			case v == nil:
			case r.crash == nil:
				panic(v)
			default:
				r.crash(v)
				r.send(crashedMsg{})
			}
		}()
		f()
	}()
}

// close ends everything the runner started and waits for it, for as
// long as wait at most. It says whether everything has ended.
func (r *runner) close(wait time.Duration) bool {
	r.mu.Lock()
	if r.kill != nil {
		r.kill.Stop()
	}
	c := r.conn
	r.conn = nil
	waiting := r.pending
	r.pending = nil
	r.mu.Unlock()
	r.drop(waiting)
	if c != nil {
		r.spawn(c.hangUp)
	}
	ended := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(ended)
	}()
	select {
	case <-ended:
		return true
	case <-time.After(wait):
		return false
	}
}

// hangUp ends everything that was asked of the connection and closes
// it, now or as soon as Dial has returned it.
func (c *conn) hangUp() {
	c.cancel()
	c.mu.Lock()
	op := c.op
	c.op, c.hungUp = nil, true
	c.mu.Unlock()
	if op != nil {
		op.Close()
	}
}

// current returns the connection of that epoch and its Operator, if it
// is the one there is and it is dialed.
func (r *runner) current(epoch int) (*conn, Operator) {
	r.mu.Lock()
	c := r.conn
	r.mu.Unlock()
	if c == nil || c.epoch != epoch {
		return nil, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.op == nil {
		return nil, nil
	}
	return c, c.op
}

// do carries out one effect.
func (r *runner) do(e effect) {
	switch e := e.(type) {
	case effDial:
		r.dial(e.epoch)
	case effHangUp:
		r.mu.Lock()
		c := r.conn
		r.conn = nil
		r.mu.Unlock()
		if c != nil {
			r.spawn(c.hangUp)
		}
	case effWait:
		r.spawn(func() {
			timer := time.NewTimer(e.pause)
			defer timer.Stop()
			select {
			case <-timer.C:
				r.send(retryMsg{n: e.n})
			case <-r.ctx.Done():
			}
		})
	case effRollouts:
		if c, op := r.current(e.epoch); c != nil {
			r.spawn(func() {
				list, err := op.Rollouts(c.ctx, e.service)
				r.answer(c, err, rolloutsMsg{epoch: e.epoch, n: e.n, service: e.service, rollouts: list, err: err})
			})
		}
	case effHistory:
		if c, op := r.current(e.epoch); c != nil {
			r.spawn(func() {
				samples, err := op.History(c.ctx, e.zathras)
				r.answer(c, err, historyMsg{epoch: e.epoch, n: e.n, zathras: e.zathras, samples: samples, err: err})
			})
		}
	case effFollow:
		if c, op := r.current(e.epoch); c != nil {
			ctx, cancel := context.WithCancel(c.ctx)
			c.mu.Lock()
			old := c.unfollow
			c.unfollow = cancel
			c.mu.Unlock()
			if old != nil {
				old()
			}
			r.spawn(func() { r.follow(ctx, c, op, e) })
		}
	case effShell:
		r.openShell(e)
	case effGiveUp:
		r.giveUp(e.n)
	case effUnfollow:
		r.mu.Lock()
		c := r.conn
		r.mu.Unlock()
		if c != nil {
			c.mu.Lock()
			old := c.unfollow
			c.unfollow = nil
			c.mu.Unlock()
			if old != nil {
				old()
			}
		}
	}
}

// answer tells the program what draal answered a request: the answer,
// or draal's refusal, as msg has it; or, when the request failed with
// anything else, that the connection is lost. Of a connection that was
// hung up meanwhile nothing is told.
func (r *runner) answer(c *conn, err error, msg tea.Msg) {
	if c.ctx.Err() != nil {
		return
	}
	if _, refused := refusal(err); err != nil && !refused {
		r.send(lostMsg{epoch: c.epoch, err: err})
		return
	}
	r.send(msg)
}

// dial begins the connection of an epoch in place of the one there is:
// Dial, then the watch, whose fleets it hands on until it ends. Dial and
// the watch together have ConnectWait to answer, which the watch does
// with the first fleet; an attempt that has not answered by then is
// hung up and reported as a dial that failed, so that it is tried again.
func (r *runner) dial(epoch int) {
	ctx, cancel := context.WithCancel(r.ctx)
	c := &conn{epoch: epoch, ctx: ctx, cancel: cancel}
	r.mu.Lock()
	old := r.conn
	r.conn = c
	ticking := r.tick
	r.tick = true
	r.mu.Unlock()
	if old != nil {
		r.spawn(old.hangUp)
	}
	if !ticking {
		r.spawn(r.ticker)
	}
	r.spawn(func() {
		defer cancel()
		v := r.bound(c)
		op, err := r.cfg.Dial(ctx)
		if err == nil && op == nil {
			err = errors.New("Dial returned no connection")
		}
		var fleets <-chan wire.Fleet
		if err == nil {
			c.mu.Lock()
			if c.hungUp {
				c.mu.Unlock()
				op.Close()
				return
			}
			c.op = op
			c.mu.Unlock()
			if fleets, err = op.Watch(ctx); err != nil {
				err = fmt.Errorf("asking draal for the fleet: %w", err)
			}
		}
		connectStage("answered")
		if !v.decide(true) {
			// The time ran out first, whatever has come since: the
			// attempt is hung up, and nothing of what came is used.
			c.hangUp()
			r.mu.Lock()
			current := r.conn == c
			r.mu.Unlock()
			if current && r.ctx.Err() == nil {
				r.send(dialFailedMsg{epoch: epoch, err: errors.New("no answer within " + human.Took(r.cfg.ConnectWait))})
			}
			return
		}
		if err != nil {
			gone := ctx.Err() != nil
			c.hangUp()
			if !gone {
				r.send(dialFailedMsg{epoch: epoch, err: err})
			}
			return
		}
		r.send(dialedMsg{epoch: epoch})

		// The watch is read as it comes, and the newest fleet is kept
		// for the program, which takes it when it does.
		newest := make(chan wire.Fleet, 1)
		r.spawn(func() {
			defer close(newest)
			for f := range fleets {
				select {
				case newest <- f:
				default:
					select {
					case <-newest:
					default:
					}
					newest <- f
				}
			}
		})
		for f := range newest {
			if ctx.Err() != nil {
				continue
			}
			r.send(fleetMsg{epoch: epoch, fleet: f})
		}
		if ctx.Err() == nil {
			r.send(lostMsg{epoch: epoch})
		}
	})
}

// verdict is whether draal answered a dial in time, decided once: by the
// answer, or by the time that ran out, whichever comes first. The other
// finds it decided and goes by it, so that an attempt is never both
// given up and taken.
type verdict struct {
	mu       sync.Mutex
	inTime   bool
	decided  bool
	answered chan struct{} // closed when the answer came in time
}

// decide decides, if it is not decided yet, that the answer came in time
// (or that the time ran out first), and returns whether it came in time.
func (v *verdict) decide(inTime bool) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.decided {
		v.decided, v.inTime = true, inTime
		if inTime {
			close(v.answered)
		}
	}
	return v.inTime
}

// bound starts the time that a connection has to answer, ConnectWait,
// and returns the verdict on it. When the time runs out before the
// answer has come, the attempt is given up: its context ends, which
// ends Dial or the watch, and the one who dialed hangs it up and says
// so. The timer is a goroutine of the runner: it ends with the answer,
// with the connection, or with its verdict, and stops its time.
func (r *runner) bound(c *conn) *verdict {
	v := &verdict{answered: make(chan struct{})}
	timer := time.NewTimer(r.cfg.ConnectWait)
	r.spawn(func() {
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-v.answered:
			return
		case <-c.ctx.Done():
			return
		}
		connectStage("fired")
		if v.decide(false) {
			connectStage("late")
			return
		}
		c.cancel()
		connectStage("given up")
	})
	return v
}

// ticker tells the program every second that time has passed.
func (r *runner) ticker() {
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			r.send(tickMsg{})
		case <-r.ctx.Done():
			return
		}
	}
}

// follow follows the log of a zathras until ctx is done or the log
// ends. The lines are read as fast as they come; the program is handed
// what has come every logEvery at most, and of a flood the end of it.
//
// draal answers a log with its lines alone, so Logs returns with the
// first line, with the end of the log, or with a refusal: of a log that
// is followed and has no line yet, nothing is known until one comes,
// and the program is told nothing.
func (r *runner) follow(ctx context.Context, c *conn, op Operator, e effFollow) {
	lines, err := op.Logs(ctx, e.zathras, true)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if _, refused := refusal(err); !refused {
			r.send(lostMsg{epoch: e.epoch, err: err})
			return
		}
		r.send(logEndedMsg{epoch: e.epoch, id: e.id, err: err})
		return
	}
	var mu sync.Mutex
	waiting := pending{keep: logKept}
	ended := false
	wake := make(chan struct{}, 1)
	r.spawn(func() {
		for l := range lines {
			mu.Lock()
			waiting.add(l)
			mu.Unlock()
			select {
			case wake <- struct{}{}:
			default:
			}
		}
		mu.Lock()
		ended = true
		mu.Unlock()
		close(wake)
	})
	pause := time.NewTimer(0)
	defer pause.Stop()
	for range wake {
		// The reader is waited for in any case: it ends when the
		// channel of the lines is closed, which ctx sees to.
		if ctx.Err() != nil {
			continue
		}
		select {
		case <-pause.C:
		case <-ctx.Done():
			continue
		}
		mu.Lock()
		batch, done := waiting.take(), ended
		mu.Unlock()
		if len(batch) > 0 {
			r.send(logMsg{epoch: e.epoch, id: e.id, lines: batch})
		}
		if done {
			break
		}
		pause.Reset(logEvery)
	}
	mu.Lock()
	batch := waiting.take()
	mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	if len(batch) > 0 {
		r.send(logMsg{epoch: e.epoch, id: e.id, lines: batch})
	}
	r.send(logEndedMsg{epoch: e.epoch, id: e.id})
}
