package zathras

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fables-for-robots/great-machine/testservice/build"
	"github.com/fables-for-robots/great-machine/wire"
)

// sidecar is a preload endpoint of a test.
type sidecar struct {
	url     string
	bodies  chan string   // what was posted
	entered chan struct{} // gets a value when a request has come
	release chan struct{} // if hang is set, a request waits for a value
	gone    chan struct{} // gets a value when a request that waited was ended by its client
}

// newSidecar returns an endpoint that answers with status and answer. One
// that hangs answers only when the test sends on release, or never if the
// client goes first.
func newSidecar(t *testing.T, status int, answer string, hang bool) *sidecar {
	t.Helper()
	sc := &sidecar{
		bodies:  make(chan string, 16),
		entered: make(chan struct{}, 16),
		release: make(chan struct{}),
		gone:    make(chan struct{}, 16),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sc.bodies <- string(body)
		sc.entered <- struct{}{}
		if hang {
			select {
			case <-sc.release:
			case <-r.Context().Done():
				sc.gone <- struct{}{}
				return
			}
		}
		w.WriteHeader(status)
		io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	sc.url = srv.URL + "/v1/preload"
	return sc
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
	sc := newSidecar(t, http.StatusOK, "fetched 2 paths\n", false)
	w := newWorld(t, setup{preloadURL: sc.url})
	l := w.accept()
	binary := build.Binary(t)

	resp, err := l.call(prepare(7, binary, "6in5jlbspq9szjvlrdxq9rpmxyvca529-busybox-1.37.0", "2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0"))
	if err != nil || resp.Prepared == nil {
		t.Fatalf("prepare answered %+v, %v, want prepared", resp, err)
	}
	want := "/nix/store/6in5jlbspq9szjvlrdxq9rpmxyvca529-busybox-1.37.0\n/nix/store/2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0\n"
	if got := <-sc.bodies; got != want {
		t.Errorf("the endpoint was sent %q, want %q", got, want)
	}
	st := l.report("with generation 7 prepared", func(st wire.State) bool { return slices.Contains(st.Prepared, 7) })
	if st.Preparing != 0 || st.Phase != wire.PhaseIdle {
		t.Errorf("the report is %+v, want nothing preparing and the service idle: a prepare starts nothing", st)
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
	sc := newSidecar(t, http.StatusOK, "fetched 1 paths\n", true)
	w := newWorld(t, setup{preloadURL: sc.url})
	l := w.accept()

	s := l.open(prepare(4, build.Binary(t), "a"))
	st := l.report("with generation 4 preparing", func(st wire.State) bool { return st.Preparing == 4 })
	if len(st.Prepared) != 0 {
		t.Errorf("Prepared = %v while the prepare runs, want none", st.Prepared)
	}

	sc.release <- struct{}{}
	if resp, err := l.read(s); err != nil || resp.Prepared == nil {
		t.Fatalf("prepare answered %+v, %v, want prepared", resp, err)
	}
	l.report("with generation 4 prepared and nothing preparing", func(st wire.State) bool {
		return st.Preparing == 0 && slices.Equal(st.Prepared, []uint64{4})
	})
}

func TestPrepareThatTheSidecarRefuses(t *testing.T) {
	const said = "/nix/store/2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0: the server has no such path"
	sc := newSidecar(t, http.StatusNotFound, said+"\n", false)
	w := newWorld(t, setup{preloadURL: sc.url})
	l := w.accept()

	resp, err := l.call(prepare(5, build.Binary(t), "2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0"))
	if err == nil {
		t.Fatalf("prepare answered %+v, want an error", resp)
	}
	if !strings.Contains(err.Error(), said) || !strings.Contains(err.Error(), "404") {
		t.Errorf("the error is %q, want one with the status and what the sidecar said, %q", err, said)
	}
	w.idle()
	if st := w.agent.State(); len(st.Prepared) != 0 || st.Preparing != 0 {
		t.Errorf("State() = %+v after a prepare that failed, want nothing prepared or preparing", st)
	}
}

func TestPrepareWithoutASidecar(t *testing.T) {
	w := newWorld(t, setup{}) // nothing listens at the preload endpoint
	l := w.accept()
	resp, err := l.call(prepare(5, build.Binary(t), "a"))
	if wire.CodeOf(err) != wire.CodeInternal || !strings.Contains(err.Error(), "preload") {
		t.Fatalf("prepare answered %+v, %v, want an internal error that says preload", resp, err)
	}
}

func TestPrepareOfABinaryThatIsNotExecutable(t *testing.T) {
	sc := newSidecar(t, http.StatusOK, "fetched 1 paths\n", false)
	w := newWorld(t, setup{preloadURL: sc.url})
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
	sc := newSidecar(t, http.StatusOK, "", true)
	w := newWorld(t, setup{preloadURL: sc.url})
	l := w.accept()

	s := l.open(prepare(6, build.Binary(t), "a"))
	within(t, "the request to reach the endpoint", func() struct{} { return <-sc.entered })
	s.Close()

	// The request to the sidecar is ended, which is how its fetches end.
	within(t, "the endpoint to be told that the client is gone", func() struct{} { return <-sc.gone })
	w.idle()
	if st := w.agent.State(); len(st.Prepared) != 0 || st.Preparing != 0 {
		t.Errorf("State() = %+v after a prepare that was withdrawn, want nothing prepared or preparing", st)
	}
}

func TestPrepareEndsWithTheConnection(t *testing.T) {
	sc := newSidecar(t, http.StatusOK, "", true)
	w := newWorld(t, setup{preloadURL: sc.url})
	l := w.accept()

	l.open(prepare(6, build.Binary(t), "a"))
	within(t, "the request to reach the endpoint", func() struct{} { return <-sc.entered })
	l.conn.Close()

	within(t, "the endpoint to be told that the client is gone", func() struct{} { return <-sc.gone })
	w.idle()
	// And the agent is back.
	w.accept()
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
	sc := newSidecar(t, http.StatusOK, "", false)
	w := newWorld(t, setup{preloadURL: sc.url, stats: fixtureStats(t)})
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
