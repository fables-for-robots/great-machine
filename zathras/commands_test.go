package zathras

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fables-for-robots/great-machine/storemount"
	"github.com/fables-for-robots/great-machine/testservice/build"
	"github.com/fables-for-robots/great-machine/wire"
)

// store is the store of a test: a Preloader that says what it was asked
// to fetch, and answers as the test has it.
type store struct {
	err     error         // what Preload returns when it does not hang
	hang    bool          // a preload waits for a value on release, or for its context
	asked   chan []string // the names of every preload
	entered chan struct{} // gets a value when a preload has begun
	release chan struct{}
	gone    chan struct{} // gets a value when a preload that waited was ended by its context
}

// newStore returns a store whose preloads return err. One that hangs
// returns only when the test sends on release, or when its context ends.
func newStore(err error, hang bool) *store {
	return &store{
		err:     err,
		hang:    hang,
		asked:   make(chan []string, 16),
		entered: make(chan struct{}, 16),
		release: make(chan struct{}),
		gone:    make(chan struct{}, 16),
	}
}

func (s *store) Preload(ctx context.Context, names []string) error {
	s.asked <- slices.Clone(names)
	s.entered <- struct{}{}
	if s.hang {
		select {
		case <-s.release:
		case <-ctx.Done():
			s.gone <- struct{}{}
			return ctx.Err()
		}
	}
	return s.err
}

// notExecutable returns a file that is there and may not be executed.
func notExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "not-executable")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func prepare(generation uint64, binary string, closure ...string) *wire.Request {
	return &wire.Request{Prepare: &wire.Prepare{Generation: generation, Binary: binary, Closure: closure}}
}

func activate(generation uint64, binary string, args ...string) *wire.Request {
	return &wire.Request{Activate: &wire.Activate{Generation: generation, Binary: binary, Args: args}}
}

func TestPrepare(t *testing.T) {
	st := newStore(nil, false)
	w := newWorld(t, setup{store: st})
	l := w.accept()
	binary := build.Binary(t)

	closure := []string{"6in5jlbspq9szjvlrdxq9rpmxyvca529-busybox-1.37.0", "2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0"}
	resp, err := l.call(prepare(7, binary, closure...))
	if err != nil || resp.Prepared == nil {
		t.Fatalf("prepare answered %+v, %v, want prepared", resp, err)
	}
	if got := <-st.asked; !slices.Equal(got, closure) {
		t.Errorf("the store was asked to fetch %q, want the closure, %q", got, closure)
	}
	state := l.report("with generation 7 prepared", func(st wire.State) bool { return slices.Contains(st.Prepared, 7) })
	if state.Preparing != 0 || state.Phase != wire.PhaseIdle {
		t.Errorf("the report is %+v, want nothing preparing and the service idle: a prepare starts nothing", state)
	}

	// Prepared is kept ascending, and holds a generation once.
	for _, generation := range []uint64{3, 9, 7, 3} {
		if _, err := l.call(prepare(generation, binary, "a")); err != nil {
			t.Fatalf("prepare of generation %d: %v", generation, err)
		}
	}
	if got := w.agent.State().Prepared; !slices.Equal(got, []uint64{3, 7, 9}) {
		t.Errorf("State().Prepared = %v, want [3 7 9]", got)
	}
	l.report("with 3, 7 and 9 prepared", func(st wire.State) bool { return slices.Equal(st.Prepared, []uint64{3, 7, 9}) })
	w.idle()
}

func TestPrepareIsReportedWhileItRuns(t *testing.T) {
	store := newStore(nil, true)
	w := newWorld(t, setup{store: store})
	l := w.accept()

	s := l.open(prepare(4, build.Binary(t), "a"))
	st := l.report("with generation 4 preparing", func(st wire.State) bool { return st.Preparing == 4 })
	if len(st.Prepared) != 0 {
		t.Errorf("Prepared = %v while the prepare runs, want none", st.Prepared)
	}

	store.release <- struct{}{}
	if resp, err := l.read(s); err != nil || resp.Prepared == nil {
		t.Fatalf("prepare answered %+v, %v, want prepared", resp, err)
	}
	l.report("with generation 4 prepared and nothing preparing", func(st wire.State) bool {
		return st.Preparing == 0 && slices.Equal(st.Prepared, []uint64{4})
	})
}

// A store path of the closure that the store does not have for the
// service is what the operator has to read: the reason of the failure
// names the path and the service, as the store said it.
func TestPrepareOfAStorePathTheStoreDoesNotHave(t *testing.T) {
	missing := &storemount.NotFoundError{Path: "/nix/store/2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0", Service: "web"}
	w := newWorld(t, setup{store: newStore(missing, false)})
	l := w.accept()

	resp, err := l.call(prepare(5, build.Binary(t), "2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0"))
	if wire.CodeOf(err) != wire.CodeInternal {
		t.Fatalf("prepare answered %+v, %v, want an internal error", resp, err)
	}
	for _, word := range []string{"/nix/store/2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0", "the service web", "the store has no"} {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("the error does not say %q: %v", word, err)
		}
	}
	w.idle()
	if st := w.agent.State(); len(st.Prepared) != 0 || st.Preparing != 0 {
		t.Errorf("State() = %+v after a prepare that failed, want nothing prepared or preparing", st)
	}
}

// And one that could not be fetched: the reason is the store's, with the
// path and why.
func TestPrepareOfAStorePathThatCouldNotBeFetched(t *testing.T) {
	const said = "/nix/store/2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0 could not be fetched from the store: dialing the server: no route to host"
	w := newWorld(t, setup{store: newStore(errors.New(said), false)})
	l := w.accept()
	resp, err := l.call(prepare(5, build.Binary(t), "2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0"))
	if wire.CodeOf(err) != wire.CodeInternal || !strings.Contains(err.Error(), said) {
		t.Fatalf("prepare answered %+v, %v, want an internal error with what the store said, %q", resp, err, said)
	}
}

// A zathras without a store of its own (--no-store) fetches nothing: its
// /nix/store is what the container has, and a prepare is the check of
// the binary alone.
func TestPrepareWithoutAStore(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	if resp, err := l.call(prepare(5, build.Binary(t), "a")); err != nil || resp.Prepared == nil {
		t.Fatalf("prepare answered %+v, %v, want prepared", resp, err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if resp, err := l.call(prepare(6, missing, "a")); wire.CodeOf(err) != wire.CodeBadRequest || !strings.Contains(err.Error(), missing) {
		t.Errorf("prepare of a binary that is not there answered %+v, %v, want bad-request with the path", resp, err)
	}
	w.idle()
	if got := w.agent.State().Prepared; !slices.Equal(got, []uint64{5}) {
		t.Errorf("State().Prepared = %v, want [5]", got)
	}
}

func TestPrepareOfABinaryThatIsNotExecutable(t *testing.T) {
	w := newWorld(t, setup{store: newStore(nil, false)})
	l := w.accept()

	for _, binary := range []string{notExecutable(t), filepath.Join(t.TempDir(), "missing"), t.TempDir()} {
		resp, err := l.call(prepare(5, binary, "a"))
		if wire.CodeOf(err) != wire.CodeBadRequest || !strings.Contains(err.Error(), binary) {
			t.Errorf("prepare of %s answered %+v, %v, want bad-request with the path", binary, resp, err)
		}
	}
	w.idle()
	if st := w.agent.State(); len(st.Prepared) != 0 {
		t.Errorf("State().Prepared = %v, want none", st.Prepared)
	}
}

func TestPrepareWithoutAGeneration(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	if resp, err := l.call(prepare(0, build.Binary(t), "a")); wire.CodeOf(err) != wire.CodeBadRequest {
		t.Fatalf("prepare of generation 0 answered %+v, %v, want bad-request", resp, err)
	}
}

func TestPrepareIsWithdrawnByClosingItsStream(t *testing.T) {
	st := newStore(nil, true)
	w := newWorld(t, setup{store: st})
	l := w.accept()

	s := l.open(prepare(6, build.Binary(t), "a"))
	within(t, "the preload to begin", func() struct{} { return <-st.entered })
	s.Close()

	// The context of the preload ends, which is how its fetches end.
	within(t, "the preload to be ended by its context", func() struct{} { return <-st.gone })
	w.idle()
	if st := w.agent.State(); len(st.Prepared) != 0 || st.Preparing != 0 {
		t.Errorf("State() = %+v after a prepare that was withdrawn, want nothing prepared or preparing", st)
	}
}

func TestPrepareEndsWithTheConnection(t *testing.T) {
	st := newStore(nil, true)
	w := newWorld(t, setup{store: st})
	l := w.accept()

	l.open(prepare(6, build.Binary(t), "a"))
	within(t, "the preload to begin", func() struct{} { return <-st.entered })
	l.conn.Close()

	within(t, "the preload to be ended by its context", func() struct{} { return <-st.gone })
	w.idle()
	// And the agent is back.
	w.accept()
}

// A zathras that is told to stop while it prepares does not wait for the
// store: the preload is ended, and Run returns.
func TestPrepareEndsWhenTheAgentIsStopped(t *testing.T) {
	st := newStore(nil, true)
	w := newWorld(t, setup{store: st})
	l := w.accept()

	l.open(prepare(6, build.Binary(t), "a"))
	within(t, "the preload to begin", func() struct{} { return <-st.entered })
	w.stop()
	within(t, "the preload to be ended by its context", func() struct{} { return <-st.gone })
	w.idle()
}

func TestActivate(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	binary := build.Binary(t)

	resp, err := l.call(activate(7, binary, "--stdout", "hello"))
	if err != nil || resp.Activated == nil {
		t.Fatalf("activate answered %+v, %v, want activated", resp, err)
	}
	st := l.report("of a service that runs", func(st wire.State) bool { return st.Phase == wire.PhaseRunning })
	if st.Generation != 7 || st.Binary != binary || !slices.Equal(st.Args, []string{"--stdout", "hello"}) || st.PID == 0 {
		t.Errorf("the report is %+v, want generation 7, the binary, its arguments and a process ID", st)
	}
}

func TestActivateOfABinaryThatIsNotExecutable(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	binary := build.Binary(t)
	if _, err := l.call(activate(7, binary)); err != nil {
		t.Fatal(err)
	}
	before := l.report("of a service that runs", func(st wire.State) bool { return st.Phase == wire.PhaseRunning })

	bad := notExecutable(t)
	resp, err := l.call(activate(8, bad))
	if wire.CodeOf(err) != wire.CodeBadRequest || !strings.Contains(err.Error(), bad) {
		t.Fatalf("activate answered %+v, %v, want bad-request with the path", resp, err)
	}
	if resp, err := l.call(activate(0, binary)); wire.CodeOf(err) != wire.CodeBadRequest {
		t.Fatalf("activate of generation 0 answered %+v, %v, want bad-request", resp, err)
	}
	// What runs was left alone.
	st := w.agent.State()
	if st.Generation != 7 || st.Binary != binary || st.Phase != wire.PhaseRunning || st.PID != before.PID {
		t.Errorf("State() = %+v, want generation 7 running as process %d still", st, before.PID)
	}
}

// The service is not touched by what happens to the connection: the spec's
// "a draal that is down or unreachable stops no service".
func TestServiceOutlivesTheConnection(t *testing.T) {
	w := newWorld(t, setup{})
	first := w.accept()
	binary := build.Binary(t)
	if _, err := first.call(activate(7, binary)); err != nil {
		t.Fatal(err)
	}
	before := first.report("of a service that runs", func(st wire.State) bool { return st.Phase == wire.PhaseRunning })

	first.conn.Close()
	second := w.accept()

	// It registers with the state it has then.
	st := second.register.State
	if st.Phase != wire.PhaseRunning || st.Generation != 7 || st.Binary != binary || st.PID != before.PID {
		t.Errorf("registered again with %+v, want generation 7 running as process %d still", st, before.PID)
	}
	if st.Restarts != 0 {
		t.Errorf("Restarts = %d, want 0", st.Restarts)
	}
}

func TestStateIsTheWholeState(t *testing.T) {
	w := newWorld(t, setup{store: newStore(nil, false), stats: fixtureStats(t)})
	l := w.accept()
	binary := build.Binary(t)

	if _, err := l.call(prepare(2, binary, "a")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.call(activate(2, binary, "--stdout", "hello")); err != nil {
		t.Fatal(err)
	}
	l.report("of a service that runs", func(st wire.State) bool { return st.Phase == wire.PhaseRunning })

	st := w.agent.State()
	// The supervisor's part.
	if st.Generation != 2 || st.Binary != binary || st.Phase != wire.PhaseRunning || st.PID == 0 {
		t.Errorf("State() = %+v, want generation 2 running", st)
	}
	// The agent's.
	if !slices.Equal(st.Prepared, []uint64{2}) || st.Preparing != 0 {
		t.Errorf("State() has Prepared %v and Preparing %d, want [2] and 0", st.Prepared, st.Preparing)
	}
	if st.Sample == nil || st.Sample.Memory != 104857600 {
		t.Errorf("State().Sample = %+v, want the sample of the fixture", st.Sample)
	}
	// What State returns is the caller's.
	st.Prepared[0] = 99
	st.Sample.Memory = 1
	if again := w.agent.State(); again.Prepared[0] != 2 || again.Sample.Memory != 104857600 {
		t.Errorf("State() gave away its own slice or sample: %+v", again)
	}
}
