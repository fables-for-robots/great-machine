// Package supervisor runs the service of a zathras: it starts the binary
// it is told to, stops it for the next one, and starts it again when it
// has ended, after a delay that grows while the service ends soon.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/wire"
)

// Config is what a Supervisor is made of.
type Config struct {
	Ring           *logring.Ring // required: the service's lines and the supervisor's own
	Stdout, Stderr io.Writer     // copies of the service's output; nil for none
	Env            []string      // nil: os.Environ()
	Dir            string        // "": the directory of this process
	StopTimeout    time.Duration // 0: 2 minutes
	QuickExit      time.Duration // 0: 30 seconds
	BackoffMin     time.Duration // 0: 1 second
	BackoffMax     time.Duration // 0: 2 minutes
	Log            *slog.Logger  // nil: slog.Default()
	// Start, if set, starts a process in the place of cmd.Start: it is
	// given the command and what starts it, and returns what that
	// returned. It is (*launcher.Launcher).Start. nil: the process is
	// started as it is.
	//
	// Its error is that of a binary that could not be started, as the
	// error of cmd.Start is: a service that ended at once.
	Start func(cmd *exec.Cmd, start func() error) error
}

// drainTimeout is how long the output of a service that has ended is
// waited for. Its process group was killed by then, so the wait ends at
// once unless a process of the service left the group and kept the
// output open.
var drainTimeout = time.Second

// ErrStopped is what Activate returns once Run has ended.
var ErrStopped = errors.New("the supervisor has stopped")

// Supervisor runs one service. Its methods may be called from any
// goroutine.
type Supervisor struct {
	cfg     Config
	acts    chan *activation // from Activate to Run
	quit    chan struct{}    // closed when Run begins to stop
	changes chan struct{}

	mu        sync.Mutex
	state     wire.State  // without RunningFor
	startedAt time.Time   // of the service, while it runs
	began     bool        // Run was called
	first     *activation // of an Activate before Run
}

// activation is one call of Activate.
type activation struct {
	generation uint64
	binary     string
	args       []string
	taken      chan struct{} // closed when Run has taken it
}

// New returns a supervisor that is idle. Run makes it work.
func New(cfg Config) *Supervisor {
	if cfg.StopTimeout == 0 {
		cfg.StopTimeout = 2 * time.Minute
	}
	if cfg.QuickExit == 0 {
		cfg.QuickExit = 30 * time.Second
	}
	if cfg.BackoffMin == 0 {
		cfg.BackoffMin = time.Second
	}
	if cfg.BackoffMax == 0 {
		cfg.BackoffMax = 2 * time.Minute
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Supervisor{
		cfg:     cfg,
		acts:    make(chan *activation),
		quit:    make(chan struct{}),
		changes: make(chan struct{}, 1),
		state:   wire.State{Phase: wire.PhaseIdle},
	}
}

// Activate checks the binary, and refuses with ErrNotExecutable leaving
// what runs alone. Otherwise it takes the generation as its own and
// returns; the service that runs is stopped and binary is started after
// that, by Run.
//
// When Activate returns nil, State shows the generation, and a phase that
// is true of it: running when nothing had to be stopped, stopping while
// the service of the generation before has not ended. An Activate before
// Run is kept for Run to begin with, and the phase stays idle until then.
func (s *Supervisor) Activate(generation uint64, binary string, args []string) error {
	select {
	case <-s.quit:
		return ErrStopped
	default:
	}
	if err := CheckExecutable(binary); err != nil {
		return err
	}
	a := &activation{
		generation: generation,
		binary:     binary,
		args:       slices.Clone(args),
		taken:      make(chan struct{}),
	}

	s.mu.Lock()
	if !s.began {
		s.first = a
		s.state.Generation, s.state.Binary, s.state.Args = a.generation, a.binary, a.args
		s.mu.Unlock()
		s.changed()
		return nil
	}
	s.mu.Unlock()

	select {
	case s.acts <- a:
		<-a.taken
		return nil
	case <-s.quit:
		return ErrStopped
	}
}

// State returns Generation, Binary, Args, Phase, PID, RunningFor,
// RestartAt, LastExit and Restarts. The other fields are the zero value.
func (s *Supervisor) State() wire.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state
	st.Args = slices.Clone(st.Args)
	if st.LastExit != nil {
		exit := *st.LastExit
		st.LastExit = &exit
	}
	if st.Phase == wire.PhaseRunning {
		st.RunningFor = time.Since(s.startedAt)
	}
	return st
}

// Changes has room for one value and gets one after every change of what
// State returns but RunningFor.
func (s *Supervisor) Changes() <-chan struct{} {
	return s.changes
}

// update changes the state and tells Changes.
func (s *Supervisor) update(change func(st *wire.State)) {
	s.mu.Lock()
	change(&s.state)
	s.mu.Unlock()
	s.changed()
}

func (s *Supervisor) changed() {
	select {
	case s.changes <- struct{}{}:
	default:
	}
}

// say adds a line of the supervisor's own to the ring, and logs it.
func (s *Supervisor) say(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	s.cfg.Ring.Add(logring.Zathras, text)
	s.cfg.Log.Info(text)
}

// Run does the work until ctx is done, then stops the service as the
// spec's 7.1 says and returns nil. Call it once.
//
// When Run has returned the phase is stopping, for good, if there was a
// generation, and idle otherwise.
func (s *Supervisor) Run(ctx context.Context) error {
	r := &runner{s: s}
	s.mu.Lock()
	s.began = true
	r.target, s.first = s.first, nil
	s.mu.Unlock()

	if r.target != nil {
		r.start(nil)
	}
	for {
		var ended chan ending
		if r.proc != nil {
			ended = r.proc.ended
		}
		select {
		case <-ctx.Done():
			r.shutdown()
			return nil
		case a := <-s.acts:
			r.activate(a)
		case e := <-ended:
			r.exited(e)
		case <-r.restartC:
			r.restartC = nil
			r.start(func(st *wire.State) { st.Restarts++ })
		case <-r.killC:
			r.killC = nil
			r.signal(syscall.SIGKILL)
		}
	}
}

// runner is what Run knows and nobody else: only Run's goroutine touches
// it.
type runner struct {
	s        *Supervisor
	target   *activation   // what is to run; nil before the first Activate
	proc     *process      // the service, from its start until Run has heard of its end
	stopping bool          // proc was sent SIGTERM
	delay    time.Duration // the delay of the backoff before; 0 when the next is the first
	restartC <-chan time.Time
	restartT *time.Timer
	killC    <-chan time.Time
	killT    *time.Timer
}

// process is a service that was started.
type process struct {
	pid        int
	generation uint64
	started    time.Time
	ended      chan ending // gets one value, when the service has ended and its output was read
}

// ending is what Wait said of a service, and when.
type ending struct {
	err error
	at  time.Time
}

// activate takes a as the target: it stops the service if one runs, and
// starts the target at once if none does. Any backoff is forgotten.
func (r *runner) activate(a *activation) {
	defer close(a.taken)
	r.target = a
	r.delay = 0
	r.cancelRestart()
	take := func(st *wire.State) {
		st.Generation, st.Binary, st.Args = a.generation, a.binary, a.args
	}
	if r.proc == nil {
		r.start(take)
		return
	}
	r.stop()
	r.s.update(func(st *wire.State) {
		take(st)
		st.Phase = wire.PhaseStopping
		st.PID = 0
	})
}

// stop sends the service SIGTERM, and SIGKILL after StopTimeout, unless
// it was sent SIGTERM already.
func (r *runner) stop() {
	if r.stopping {
		return
	}
	r.stopping = true
	r.signal(syscall.SIGTERM)
	r.killT = time.NewTimer(r.s.cfg.StopTimeout)
	r.killC = r.killT.C
}

// signal sends sig to the process group of the service.
func (r *runner) signal(sig syscall.Signal) {
	if r.proc == nil {
		return
	}
	if err := syscall.Kill(-r.proc.pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		r.s.cfg.Log.Warn("signalling the service", "pid", r.proc.pid, "signal", signalName(sig), "error", err)
	}
}

func (r *runner) cancelRestart() {
	if r.restartT != nil {
		r.restartT.Stop()
	}
	r.restartT, r.restartC = nil, nil
}

func (r *runner) cancelKill() {
	if r.killT != nil {
		r.killT.Stop()
	}
	r.killT, r.killC = nil, nil
}

// start starts the target. The state changes once: before to the state of
// a service that runs, or that waits to be started again when the binary
// could not be started, with the changes of before applied first.
func (r *runner) start(before func(st *wire.State)) {
	if before == nil {
		before = func(*wire.State) {}
	}
	s, t := r.s, r.target
	p, err := s.spawn(t)
	if err != nil {
		// A binary that cannot be started counts as a service that
		// ended at once.
		r.backoff(wire.Exit{Generation: t.generation, Status: -1, Error: err.Error(), At: time.Now()}, before)
		return
	}
	r.proc = p
	s.update(func(st *wire.State) {
		before(st)
		st.Phase = wire.PhaseRunning
		st.PID = p.pid
		st.RestartAt = time.Time{}
		s.startedAt = p.started
	})
	s.say("started generation %d: %s (pid %d)", t.generation, t.binary, p.pid)
}

// exited is called when the service has ended.
func (r *runner) exited(e ending) {
	exit := r.proc.exit(e)
	r.proc = nil
	r.cancelKill()
	if r.stopping {
		// It was stopped for the target, which starts at once.
		r.stopping = false
		r.s.say("%s", describe(exit))
		r.start(func(st *wire.State) { st.LastExit = &exit })
		return
	}
	if exit.Ran >= r.s.cfg.QuickExit {
		r.delay = 0
		r.s.say("%s; starting it again", describe(exit))
		r.start(func(st *wire.State) {
			st.LastExit = &exit
			st.Restarts++
		})
		return
	}
	r.backoff(exit, nil)
}

// backoff records an end that came within QuickExit of the start, and
// waits before the next start: BackoffMin, then twice as long each time,
// up to BackoffMax.
func (r *runner) backoff(exit wire.Exit, before func(st *wire.State)) {
	s := r.s
	switch {
	case r.delay == 0:
		r.delay = s.cfg.BackoffMin
	default:
		r.delay = min(2*r.delay, s.cfg.BackoffMax)
	}
	at := exit.At.Add(r.delay)
	s.say("%s; starting it again in %s", describe(exit), r.delay)
	s.update(func(st *wire.State) {
		if before != nil {
			before(st)
		}
		st.Phase = wire.PhaseBackoff
		st.PID = 0
		st.LastExit = &exit
		st.RestartAt = at
	})
	r.restartT = time.NewTimer(time.Until(at))
	r.restartC = r.restartT.C
}

// shutdown stops the service and waits for its end. Nothing is started.
func (r *runner) shutdown() {
	s := r.s
	close(s.quit)
	r.cancelRestart()
	var last *wire.Exit
	if r.proc != nil {
		r.stop()
		s.update(func(st *wire.State) {
			st.Phase = wire.PhaseStopping
			st.PID = 0
		})
		var e ending
		select {
		case e = <-r.proc.ended:
		case <-r.killC:
			r.signal(syscall.SIGKILL)
			e = <-r.proc.ended
		}
		exit := r.proc.exit(e)
		last = &exit
		r.proc = nil
		r.cancelKill()
		s.say("%s", describe(exit))
	}
	s.update(func(st *wire.State) {
		if last != nil {
			st.LastExit = last
		}
		if st.Generation != 0 {
			st.Phase = wire.PhaseStopping
		}
		st.PID = 0
		st.RestartAt = time.Time{}
	})
}

// spawn starts the binary of t with no standard input, in a process group
// of its own, its output going to the ring and to the copies.
func (s *Supervisor) spawn(t *activation) (*process, error) {
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return nil, err
	}

	cmd := exec.Command(t.binary, t.args...)
	cmd.Env = s.cfg.Env
	cmd.Dir = s.cfg.Dir
	// The service writes to pipes of this package's own, not to pipes
	// that os/exec makes: Wait then returns when the service has ended,
	// and not only when the last process that holds its output has.
	cmd.Stdout = outW
	cmd.Stderr = errW
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if s.cfg.Start != nil {
		err = s.cfg.Start(cmd, cmd.Start)
	} else {
		err = cmd.Start()
	}
	started := time.Now()
	outW.Close()
	errW.Close()
	if err != nil {
		outR.Close()
		errR.Close()
		return nil, err
	}

	p := &process{
		pid:        cmd.Process.Pid,
		generation: t.generation,
		started:    started,
		ended:      make(chan ending, 1),
	}
	outLines, errLines := s.cfg.Ring.Writer(logring.Stdout), s.cfg.Ring.Writer(logring.Stderr)
	var copies sync.WaitGroup
	copies.Go(func() { io.Copy(tee{outLines, s.cfg.Stdout}, outR) })
	copies.Go(func() { io.Copy(tee{errLines, s.cfg.Stderr}, errR) })
	go func() {
		err := cmd.Wait()
		at := time.Now()
		// Whatever is left of the process group goes with the service.
		// That closes the output as well, unless a process left the
		// group; then the output is closed here.
		syscall.Kill(-p.pid, syscall.SIGKILL)
		read := make(chan struct{})
		go func() {
			copies.Wait()
			close(read)
		}()
		select {
		case <-read:
		case <-time.After(drainTimeout):
			outR.Close()
			errR.Close()
			<-read
		}
		outR.Close()
		errR.Close()
		// A last line without a line feed.
		outLines.Flush()
		errLines.Flush()
		p.ended <- ending{err: err, at: at}
	}()
	return p, nil
}

// tee writes to the ring and to the copy, if there is one. It never
// fails: a copy that cannot be written to does not stop the service's
// output from being read.
type tee struct {
	lines *logring.Writer
	copy  io.Writer
}

func (t tee) Write(p []byte) (int, error) {
	t.lines.Write(p)
	if t.copy != nil {
		t.copy.Write(p)
	}
	return len(p), nil
}

// exit makes of what Wait said how the service ended.
func (p *process) exit(e ending) wire.Exit {
	exit := wire.Exit{Generation: p.generation, At: e.at, Ran: e.at.Sub(p.started)}
	var exitErr *exec.ExitError
	switch {
	case e.err == nil:
	case errors.As(e.err, &exitErr):
		exit.Status = exitErr.ExitCode()
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			exit.Status = -1
			exit.Signal = signalName(ws.Signal())
		}
	default:
		// Wait itself failed: nothing is known of the end.
		exit.Status = -1
	}
	return exit
}

// describe says how a service ended, as the ring has it.
func describe(exit wire.Exit) string {
	switch {
	case exit.Error != "":
		return fmt.Sprintf("generation %d could not be started: %s", exit.Generation, exit.Error)
	case exit.Signal != "":
		return fmt.Sprintf("generation %d was ended by %s after %s", exit.Generation, exit.Signal, short(exit.Ran))
	default:
		return fmt.Sprintf("generation %d exited with status %d after %s", exit.Generation, exit.Status, short(exit.Ran))
	}
}

// short rounds d for a person: 3.2s, 417ms.
func short(d time.Duration) time.Duration {
	if d < time.Second {
		return d.Round(time.Millisecond)
	}
	return d.Round(100 * time.Millisecond)
}

var signalNames = map[syscall.Signal]string{
	syscall.SIGABRT: "SIGABRT",
	syscall.SIGALRM: "SIGALRM",
	syscall.SIGBUS:  "SIGBUS",
	syscall.SIGFPE:  "SIGFPE",
	syscall.SIGHUP:  "SIGHUP",
	syscall.SIGILL:  "SIGILL",
	syscall.SIGINT:  "SIGINT",
	syscall.SIGKILL: "SIGKILL",
	syscall.SIGPIPE: "SIGPIPE",
	syscall.SIGQUIT: "SIGQUIT",
	syscall.SIGSEGV: "SIGSEGV",
	syscall.SIGSYS:  "SIGSYS",
	syscall.SIGTERM: "SIGTERM",
	syscall.SIGTRAP: "SIGTRAP",
	syscall.SIGUSR1: "SIGUSR1",
	syscall.SIGUSR2: "SIGUSR2",
	syscall.SIGXCPU: "SIGXCPU",
	syscall.SIGXFSZ: "SIGXFSZ",
}

// signalName returns the name of sig, such as SIGKILL, or "signal 42" for
// one that has none here.
func signalName(sig syscall.Signal) string {
	if name, ok := signalNames[sig]; ok {
		return name
	}
	return fmt.Sprintf("signal %d", int(sig))
}
