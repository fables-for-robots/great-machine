// Package zathras is the agent of an app container: it keeps a connection
// to draal, registers there, reports the state of its service, and answers
// what draal asks of it: prepare, activate, logs and shell.
//
// The service itself is run by a supervisor that the caller starts. The
// agent tells it what to run and reads its state, and nothing the
// connection does stops the service: a draal that cannot be reached is
// dialed again, for ever.
package zathras

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/preload"
	"github.com/fables-for-robots/great-machine/ptyshell"
	"github.com/fables-for-robots/great-machine/stats"
	"github.com/fables-for-robots/great-machine/supervisor"
	"github.com/fables-for-robots/great-machine/wire"
)

// Config is what an agent is made of.
type Config struct {
	Service string // the service this zathras belongs to
	Name    string // what the operator sees
	Version string // of the program
	// Dial dials draal. Its context bounds the dial alone.
	Dial func(ctx context.Context) (node.Conn, error)
	// Supervisor runs the service. The caller runs it; the agent calls
	// Activate, State and Changes.
	Supervisor *supervisor.Supervisor
	// Ring holds the lines that logs answers with.
	Ring *logring.Ring
	// Stats reads the samples. nil: no samples.
	Stats *stats.Reader
	// Preload is the client of the sidecar, for prepare.
	Preload preload.Client
	// Shell is what a shell session is started with. Term, Cols and Rows
	// come with the request.
	Shell ptyshell.Config
	// ReportEvery is the longest time between two reports, and the time
	// between two samples. 0: 2 seconds.
	ReportEvery time.Duration
	// RedialMin is how long after the end of a connection draal is dialed
	// again. 0: 1 second.
	RedialMin time.Duration
	// RedialMax is the longest time between two dials, which is reached by
	// doubling. A dial, and a registration, are given as long. 0: 30
	// seconds.
	RedialMax time.Duration
	// Log is where the agent logs. nil: slog.Default().
	Log *slog.Logger
}

// Agent is the side of a zathras that talks to draal.
type Agent struct {
	cfg Config
	log *slog.Logger

	mu        sync.Mutex
	prepared  []uint64     // the generations that were prepared, ascending
	preparing []uint64     // the generations being prepared, oldest first
	sample    *wire.Sample // the latest one

	// changed has room for one value, and gets one when the state has
	// changed by something the supervisor does not tell: a prepare.
	changed chan struct{}

	// handlers is how many streams of draal are being answered. The tests
	// read it to see that a request has ended.
	handlers atomic.Int64
}

// New returns an agent. Run makes it work.
func New(cfg Config) *Agent {
	if cfg.ReportEvery <= 0 {
		cfg.ReportEvery = 2 * time.Second
	}
	if cfg.RedialMin <= 0 {
		cfg.RedialMin = time.Second
	}
	if cfg.RedialMax <= 0 {
		cfg.RedialMax = 30 * time.Second
	}
	if cfg.RedialMax < cfg.RedialMin {
		cfg.RedialMax = cfg.RedialMin
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Agent{cfg: cfg, log: cfg.Log, changed: make(chan struct{}, 1)}
}

// State returns the whole state: the supervisor's, Prepared, Preparing
// and the latest sample.
func (a *Agent) State() wire.State {
	st := a.cfg.Supervisor.State()
	a.mu.Lock()
	defer a.mu.Unlock()
	st.Prepared = slices.Clone(a.prepared)
	if n := len(a.preparing); n > 0 {
		st.Preparing = a.preparing[n-1]
	}
	if a.sample != nil {
		sample := *a.sample
		st.Sample = &sample
	}
	return st
}

// Run keeps a connection to draal until ctx is done: it dials, registers,
// reports and answers prepare, activate, logs and shell, and dials again
// when the connection has ended. It ends the shells before it returns.
func (a *Agent) Run(ctx context.Context) error {
	wait := redial{min: a.cfg.RedialMin, max: a.cfg.RedialMax}
	for ctx.Err() == nil {
		registered := a.connection(ctx)
		if ctx.Err() != nil {
			break
		}
		delay := wait.next(registered)
		a.log.Info("dialing draal again", "in", delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return nil
}

// redial says how long to wait before draal is dialed again: min after a
// connection that registered, and otherwise twice as long as the time
// before, up to max.
type redial struct {
	min, max time.Duration
	last     time.Duration // what next returned last; 0 at first
}

// next returns the time to wait after a connection that has ended, or
// that was not made. registered says whether it got as far as registering.
func (r *redial) next(registered bool) time.Duration {
	switch {
	case registered || r.last == 0:
		r.last = r.min
	case r.last >= r.max/2:
		r.last = r.max
	default:
		r.last *= 2
	}
	return r.last
}

// connection dials draal, registers, and reports and answers until the
// connection has ended or ctx is done. Everything that belonged to the
// connection has ended when it returns: the prepares, the followers of
// the log and the shells. It says whether the registration worked.
func (a *Agent) connection(ctx context.Context) (registered bool) {
	dialCtx, cancelDial := context.WithTimeout(ctx, a.cfg.RedialMax)
	conn, err := a.cfg.Dial(dialCtx)
	cancelDial()
	if err != nil {
		if ctx.Err() == nil {
			a.log.Warn("draal cannot be reached", "error", err)
		}
		return false
	}

	// ctx is the life of the connection from here on: it is done when the
	// connection has ended, and the connection is closed when it is done.
	ctx, cancel := context.WithCancel(ctx)
	var handlers sync.WaitGroup
	defer func() {
		cancel()
		conn.Close()
		handlers.Wait()
	}()
	go func() {
		select {
		case <-conn.Done():
			cancel()
		case <-ctx.Done():
			conn.Close()
		}
	}()

	reports, err := a.register(ctx, conn)
	if err != nil {
		if ctx.Err() == nil {
			a.log.Warn("registering with draal failed", "draal", conn.RemoteID(), "error", err)
		}
		return false
	}
	defer reports.Close()
	a.log.Info("registered with draal", "draal", conn.RemoteID())

	handlers.Go(func() {
		for {
			s, err := conn.AcceptStream(ctx)
			if err != nil {
				return
			}
			handlers.Go(func() { a.handle(ctx, s) })
		}
	})

	err = a.report(ctx, reports)
	if ctx.Err() == nil {
		a.log.Warn("reporting to draal failed", "error", err)
	} else {
		a.log.Info("the connection to draal has ended")
	}
	return true
}

// register opens the stream of the reports, sends the register request
// with the state as it is and waits for draal to answer it. A draal that
// does not answer within RedialMax loses the connection.
func (a *Agent) register(ctx context.Context, conn node.Conn) (node.Stream, error) {
	timeout := time.AfterFunc(a.cfg.RedialMax, func() { conn.Close() })
	defer timeout.Stop()

	s, err := conn.OpenStream(ctx)
	if err != nil {
		return nil, fmt.Errorf("opening a stream: %w", err)
	}
	a.takeSample()
	resp, err := wire.Call(s, &wire.Request{Register: &wire.Register{
		Service: a.cfg.Service,
		Name:    a.cfg.Name,
		Version: a.cfg.Version,
		State:   a.State(),
	}})
	if err == nil && resp.Registered == nil {
		err = fmt.Errorf("draal answered something else than registered")
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// report writes the state to s whenever it has changed and at least every
// ReportEvery, until ctx is done or a write fails. A sample is taken
// every ReportEvery and goes out with the report of that time.
func (a *Agent) report(ctx context.Context, s io.Writer) error {
	ticker := time.NewTicker(a.cfg.ReportEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			a.takeSample()
		case <-a.cfg.Supervisor.Changes():
		case <-a.changed:
		}
		st := a.State()
		if err := wire.Write(s, &st); err != nil {
			return fmt.Errorf("writing a report: %w", err)
		}
	}
}

// takeSample reads a sample, which is the latest one from then on, or
// none where the system gives none. It is called by the goroutine of the
// connection alone, as a stats.Reader is used by one goroutine.
func (a *Agent) takeSample() {
	var sample *wire.Sample
	if a.cfg.Stats != nil {
		if s, ok := a.cfg.Stats.Read(time.Now()); ok {
			sample = &s
		}
	}
	a.mu.Lock()
	a.sample = sample
	a.mu.Unlock()
}

// stateChanged has the next report sent at once.
func (a *Agent) stateChanged() {
	select {
	case a.changed <- struct{}{}:
	default:
	}
}

// handle answers one stream that draal opened. ctx is done when the
// connection has ended.
func (a *Agent) handle(ctx context.Context, s node.Stream) {
	a.handlers.Add(1)
	defer a.handlers.Add(-1)
	defer s.Close()

	var req wire.Request
	if err := wire.Read(s, &req); err != nil {
		if err != io.EOF {
			wire.WriteError(s, wire.Errorf(wire.CodeBadRequest, "reading the request: %s", strings.ToValidUTF8(err.Error(), "�")))
			s.CloseWrite()
			io.Copy(io.Discard, s) // until draal has read the answer and closed
		}
		return
	}
	if req.Shell != nil {
		// A shell reads its stream itself: what is typed comes on it.
		a.answerShell(ctx, s, req.Shell)
		return
	}

	// Nothing but the request is sent on any other stream, so that the
	// end of what is read, or an error, is the end of the request: draal
	// has closed the stream, or the connection has ended. That is how a
	// prepare is withdrawn and how a follower of the log goes.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		io.Copy(io.Discard, s)
		cancel()
	}()

	a.answer(ctx, s, &req)

	// The answer is complete. draal closes the stream when it has read it,
	// and only then is the stream closed here: a Close before that could
	// take away what draal has not read yet.
	s.CloseWrite()
	<-ctx.Done()
}
