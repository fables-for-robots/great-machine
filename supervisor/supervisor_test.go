package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/testservice/build"
	"github.com/fables-for-robots/great-machine/wire"
)

// eventually polls cond until it is true, and fails the test when it is
// not within 20 seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("not within 20 seconds: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// harness is a supervisor that runs, and its ring.
type harness struct {
	*Supervisor
	t      *testing.T
	ring   *logring.Ring
	cancel context.CancelFunc
	done   chan error // what Run returned
}

// run starts a supervisor with cfg, in which durations that are zero are
// made short: a service that ends within 300 ms waits 20 ms, 40 ms and
// then 80 ms. The stop timeout is long, so that only a test that sets it
// sees a SIGKILL. The supervisor is stopped when the test ends.
func run(t *testing.T, cfg Config) *harness {
	t.Helper()
	if cfg.StopTimeout == 0 {
		cfg.StopTimeout = 30 * time.Second
	}
	if cfg.QuickExit == 0 {
		cfg.QuickExit = 300 * time.Millisecond
	}
	if cfg.BackoffMin == 0 {
		cfg.BackoffMin = 20 * time.Millisecond
	}
	if cfg.BackoffMax == 0 {
		cfg.BackoffMax = 80 * time.Millisecond
	}
	h := &harness{t: t, ring: logring.New(400, 16<<10), done: make(chan error, 1)}
	cfg.Ring = h.ring
	cfg.Log = slog.New(slog.DiscardHandler)
	h.Supervisor = New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { h.done <- h.Run(ctx) }()
	t.Cleanup(func() { h.stop() })
	// An Activate before Run has begun is kept for Run, and the tests
	// look at what an Activate does that Run takes.
	eventually(t, "Run has begun", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.began
	})
	return h
}

// stop ends Run and returns what it returned.
func (h *harness) stop() error {
	h.t.Helper()
	h.cancel()
	select {
	case err := <-h.done:
		h.done <- err
		return err
	case <-time.After(60 * time.Second):
		h.t.Fatal("Run did not return within 60 seconds of the end of its context")
	}
	return nil
}

// lines returns the texts of the ring's lines of stream.
func (h *harness) lines(stream string) []string {
	var out []string
	for _, l := range h.ring.Lines() {
		if l.Stream == stream {
			out = append(out, l.Text)
		}
	}
	return out
}

// matching returns the supervisor's own lines that match re.
func (h *harness) matching(re string) []string {
	var out []string
	exp := regexp.MustCompile(re)
	for _, text := range h.lines(logring.Zathras) {
		if exp.MatchString(text) {
			out = append(out, text)
		}
	}
	return out
}

// wrote waits for the service to write text as a line of stream.
func (h *harness) wrote(stream, text string) {
	h.t.Helper()
	eventually(h.t, fmt.Sprintf("the line %q on %s", text, stream), func() bool {
		return slices.Contains(h.lines(stream), text)
	})
}

// phase waits for the phase and returns the state that has it.
func (h *harness) phase(want wire.Phase) wire.State {
	h.t.Helper()
	var st wire.State
	eventually(h.t, "phase "+string(want), func() bool {
		st = h.State()
		return st.Phase == want
	})
	return st
}

// activate fails the test when Activate does.
func (h *harness) activate(generation uint64, binary string, args ...string) {
	h.t.Helper()
	if err := h.Activate(generation, binary, args); err != nil {
		h.t.Fatalf("Activate(%d, %s, %q): %v", generation, binary, args, err)
	}
}

// alive says whether the process is there. A process that has ended and
// that nobody has waited for yet is not.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	// On Linux a zombie still takes signals. Its state is the field
	// behind the name, which is in parentheses.
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	rest := stat[bytes.LastIndexByte(stat, ')')+1:]
	return !bytes.HasPrefix(bytes.TrimSpace(rest), []byte("Z"))
}

// script writes a shell script and returns its path.
func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "service.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// buffer is a bytes.Buffer for more than one goroutine.
type buffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestIdleUntilActivateThenRunning(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{})

	st := h.State()
	if st.Phase != wire.PhaseIdle || st.Generation != 0 || st.Binary != "" || st.PID != 0 ||
		st.RunningFor != 0 || !st.RestartAt.IsZero() || st.LastExit != nil || st.Restarts != 0 {
		t.Fatalf("before Activate: %+v, want idle and nothing else", st)
	}

	h.activate(7, bin, "--stdout", "ready")
	st = h.State()
	if st.Phase != wire.PhaseRunning {
		t.Fatalf("when Activate has returned the phase is %s, want running", st.Phase)
	}
	if st.Generation != 7 || st.Binary != bin || !slices.Equal(st.Args, []string{"--stdout", "ready"}) {
		t.Errorf("got generation %d, binary %s, args %q", st.Generation, st.Binary, st.Args)
	}
	if st.PID <= 0 || !alive(st.PID) {
		t.Errorf("PID %d is no process that runs", st.PID)
	}
	if !st.RestartAt.IsZero() || st.LastExit != nil || st.Restarts != 0 {
		t.Errorf("got RestartAt %v, LastExit %+v, Restarts %d; want none", st.RestartAt, st.LastExit, st.Restarts)
	}
	if st.Prepared != nil || st.Preparing != 0 || st.Sample != nil {
		t.Errorf("fields that are not the supervisor's are set: %+v", st)
	}
	eventually(t, "RunningFor grows", func() bool {
		later := h.State()
		return later.RunningFor > st.RunningFor && later.PID == st.PID
	})

	// What State returns is the caller's.
	st.Args[0] = "changed"
	if got := h.State().Args[0]; got != "--stdout" {
		t.Errorf("the state's args were changed through what State returned: %q", got)
	}

	want := fmt.Sprintf("started generation 7: %s (pid %d)", bin, st.PID)
	if got := h.lines(logring.Zathras); !slices.Equal(got, []string{want}) {
		t.Errorf("the supervisor's lines are %q, want %q", got, want)
	}
}

func TestActivateBeforeRun(t *testing.T) {
	bin := build.Binary(t)
	ring := logring.New(400, 16<<10)
	s := New(Config{Ring: ring, Log: slog.New(slog.DiscardHandler)})
	if err := s.Activate(3, bin, []string{"--stdout", "ready"}); err != nil {
		t.Fatal(err)
	}
	if st := s.State(); st.Generation != 3 || st.Binary != bin || st.Phase != wire.PhaseIdle {
		t.Errorf("before Run: %+v, want generation 3, idle", st)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	eventually(t, "running", func() bool { return s.State().Phase == wire.PhaseRunning })
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run returned %v", err)
	}
}

func TestArgumentsReachTheProgramVerbatim(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{})
	texts := []string{
		"two words",
		`"double" and 'single' quotes`,
		`back\slash $HOME * ~ ; | &`,
		"  blanks at both ends  ",
		"zażółć gęślą jaźń 日本語 😀",
		"--exit-code",
	}
	for i, text := range texts {
		h.activate(uint64(i+1), bin, "--stdout", text)
		h.wrote(logring.Stdout, text)
	}
	// An empty argument: were it lost, --stderr would be taken for the
	// text of --stdout.
	h.activate(100, bin, "--stdout", "", "--stderr", "after the empty one")
	h.wrote(logring.Stderr, "after the empty one")
	h.wrote(logring.Stdout, "")
	if got := h.State().Args; !slices.Equal(got, []string{"--stdout", "", "--stderr", "after the empty one"}) {
		t.Errorf("the state has the args %q", got)
	}
}

func TestOutputGoesToTheRingAndToTheCopies(t *testing.T) {
	bin := build.Binary(t)
	last := script(t, "printf 'no line feed'\nprintf 'none here either' >&2\n")
	var stdout, stderr buffer
	h := run(t, Config{Stdout: &stdout, Stderr: &stderr, QuickExit: time.Hour})

	h.activate(1, bin, "--stdout", "to stdout", "--stderr", "to stderr")
	h.wrote(logring.Stdout, "to stdout")
	h.wrote(logring.Stderr, "to stderr")
	eventually(t, "the copies", func() bool {
		return stdout.String() == "to stdout\n" && stderr.String() == "to stderr\n"
	})
	if slices.Contains(h.lines(logring.Stderr), "to stdout") || slices.Contains(h.lines(logring.Stdout), "to stderr") {
		t.Errorf("the streams are mixed: stdout %q, stderr %q", h.lines(logring.Stdout), h.lines(logring.Stderr))
	}
	pid := h.State().PID

	// A last line without a line feed is in the ring when the service
	// has ended.
	h.activate(2, last)
	h.wrote(logring.Stdout, "no line feed")
	h.wrote(logring.Stderr, "none here either")
	eventually(t, "the copies of the last lines", func() bool {
		return stdout.String() == "to stdout\nno line feed" && stderr.String() == "to stderr\nnone here either"
	})

	// The supervisor's own lines, in the contract's words.
	eventually(t, "the line of the exit of generation 2", func() bool {
		return len(h.matching(`^generation 2 exited`)) > 0
	})
	got := h.lines(logring.Zathras)
	if len(got) < 4 {
		t.Fatalf("the supervisor's lines are %q, want four at least", got)
	}
	want := []string{
		fmt.Sprintf(`^started generation 1: %s \(pid %d\)$`, regexp.QuoteMeta(bin), pid),
		`^generation 1 exited with status 0 after [0-9.]+m?s$`,
		fmt.Sprintf(`^started generation 2: %s \(pid [0-9]+\)$`, regexp.QuoteMeta(last)),
		`^generation 2 exited with status 0 after [0-9.]+m?s; starting it again in 20ms$`,
	}
	for i, re := range want {
		if !regexp.MustCompile(re).MatchString(got[i]) {
			t.Errorf("line %d of the supervisor is %q, want it to match %s", i, got[i], re)
		}
	}
	for _, l := range h.ring.Lines() {
		if l.At.IsZero() {
			t.Errorf("a line without a time: %+v", l)
		}
	}
}

func TestServiceThatRanLongIsStartedAgainAtOnce(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{QuickExit: 100 * time.Millisecond})

	// Every phase the supervisor says it is in, as far as the test gets
	// to see them.
	var mu sync.Mutex
	var phases []wire.Phase
	watching := make(chan struct{})
	defer close(watching)
	go func() {
		for {
			select {
			case <-h.Changes():
				mu.Lock()
				phases = append(phases, h.State().Phase)
				mu.Unlock()
			case <-watching:
				return
			}
		}
	}()

	// Exit status 0 is no reason to leave a service down.
	h.activate(1, bin, "--exit-after", "600ms")
	first := h.State().PID
	var st wire.State
	eventually(t, "the second start", func() bool {
		st = h.State()
		return st.Restarts == 1 && st.Phase == wire.PhaseRunning
	})
	if st.PID == first || st.PID <= 0 {
		t.Errorf("the PID is %d after the restart, and was %d before", st.PID, first)
	}
	if st.LastExit == nil || st.LastExit.Status != 0 || st.LastExit.Signal != "" || st.LastExit.Generation != 1 {
		t.Fatalf("LastExit is %+v, want status 0 of generation 1", st.LastExit)
	}
	if st.LastExit.Ran < 100*time.Millisecond {
		t.Errorf("it ran for %s, want more than QuickExit", st.LastExit.Ran)
	}
	if !st.RestartAt.IsZero() {
		t.Errorf("RestartAt is %v, want the zero time", st.RestartAt)
	}
	exits := h.matching(`^generation 1 exited`)
	if len(exits) == 0 || !regexp.MustCompile(`^generation 1 exited with status 0 after [0-9.]+m?s; starting it again$`).MatchString(exits[0]) {
		t.Errorf("the lines of the exit are %q, want one that ends with: starting it again", exits)
	}
	mu.Lock()
	defer mu.Unlock()
	if slices.Contains(phases, wire.PhaseBackoff) {
		t.Errorf("the phases were %v, want no backoff", phases)
	}
}

// delays returns, for every exit of the generation that the supervisor
// has written a line of, what follows "starting it again": " in 20ms", or
// "" for at once.
func (h *harness) delays(generation uint64) []string {
	exp := regexp.MustCompile(fmt.Sprintf(`^generation %d .*; starting it again(.*)$`, generation))
	var out []string
	for _, text := range h.lines(logring.Zathras) {
		if m := exp.FindStringSubmatch(text); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

func TestServiceThatEndsSoonWaitsTwiceAsLongEachTime(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{QuickExit: time.Hour})
	h.activate(3, bin, "--exit-after", "0", "--exit-code", "7")

	st := h.phase(wire.PhaseBackoff)
	if st.PID != 0 || st.RunningFor != 0 {
		t.Errorf("in backoff: PID %d, RunningFor %s; want none", st.PID, st.RunningFor)
	}
	if st.Generation != 3 || st.Binary != bin {
		t.Errorf("in backoff: generation %d, binary %s", st.Generation, st.Binary)
	}
	exit := st.LastExit
	if exit == nil || exit.Status != 7 || exit.Signal != "" || exit.Error != "" || exit.Generation != 3 {
		t.Fatalf("LastExit is %+v, want status 7 of generation 3", exit)
	}
	if exit.Ran <= 0 || exit.Ran > time.Minute || exit.At.IsZero() || time.Since(exit.At) > time.Minute {
		t.Errorf("LastExit is %+v, want the time it ended and how long it ran", exit)
	}
	// The delay is what lies between the end and RestartAt.
	delay := st.RestartAt.Sub(exit.At)
	if !slices.Contains([]time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond}, delay) {
		t.Errorf("RestartAt is %s after the end, want 20ms, 40ms or 80ms", delay)
	}

	eventually(t, "six exits", func() bool { return len(h.delays(3)) >= 6 })
	want := []string{" in 20ms", " in 40ms", " in 80ms", " in 80ms", " in 80ms", " in 80ms"}
	if got := h.delays(3)[:6]; !slices.Equal(got, want) {
		t.Errorf("the delays were %q, want %q", got, want)
	}
	eventually(t, "five restarts", func() bool { return h.State().Restarts >= 5 })
	if line := h.matching(`^generation 3 exited`)[0]; !regexp.MustCompile(`^generation 3 exited with status 7 after [0-9.]+m?s; starting it again in 20ms$`).MatchString(line) {
		t.Errorf("the line of the first exit is %q", line)
	}

	// The gaps between the starts are the delays at least.
	var starts []time.Time
	for _, l := range h.ring.Lines() {
		if l.Stream == logring.Zathras && strings.HasPrefix(l.Text, "started generation 3") {
			starts = append(starts, l.At)
		}
	}
	for i, min := range []time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond, 80 * time.Millisecond} {
		if gap := starts[i+1].Sub(starts[i]); gap < min {
			t.Errorf("start %d came %s after the one before, want %s at least", i+1, gap, min)
		}
	}
}

func TestLongRunMakesTheNextDelayTheFirstAgain(t *testing.T) {
	// The fourth run is a long one, every other ends at once.
	service := script(t, `n=$(cat "$0.runs" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$0.runs"
if [ "$n" -eq 4 ]; then sleep 2; fi
exit 1
`)
	h := run(t, Config{QuickExit: time.Second})
	h.activate(1, service)
	eventually(t, "six exits", func() bool { return len(h.delays(1)) >= 6 })
	want := []string{" in 20ms", " in 40ms", " in 80ms", "", " in 20ms", " in 40ms"}
	if got := h.delays(1)[:6]; !slices.Equal(got, want) {
		t.Errorf("the delays were %q, want %q", got, want)
	}
}

func TestServiceEndedBySignal(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{QuickExit: time.Hour})
	h.activate(1, bin, "--stdout", "ready")
	h.wrote(logring.Stdout, "ready")
	pid := h.State().PID
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	var st wire.State
	eventually(t, "the exit", func() bool {
		st = h.State()
		return st.LastExit != nil
	})
	if st.LastExit.Status != -1 || st.LastExit.Signal != "SIGKILL" || st.LastExit.Error != "" {
		t.Errorf("LastExit is %+v, want status -1 and SIGKILL", st.LastExit)
	}
	if got := h.matching(`^generation 1 was ended by SIGKILL after [0-9.]+m?s; starting it again in 20ms$`); len(got) != 1 {
		t.Errorf("the supervisor's lines are %q, want one of the end by SIGKILL", h.lines(logring.Zathras))
	}
	eventually(t, "the restart", func() bool {
		st = h.State()
		return st.Phase == wire.PhaseRunning && st.Restarts == 1 && st.PID != pid
	})
}

func TestBinaryThatCannotBeStarted(t *testing.T) {
	// A file with an execute bit that is no program.
	noProgram := filepath.Join(t.TempDir(), "no-program")
	if err := os.WriteFile(noProgram, []byte("this is no program\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := run(t, Config{})
	h.activate(1, noProgram)

	st := h.State()
	if st.Phase != wire.PhaseBackoff || st.Generation != 1 || st.PID != 0 {
		t.Fatalf("when Activate has returned: %+v, want backoff of generation 1", st)
	}
	exit := st.LastExit
	if exit == nil || exit.Status != -1 || exit.Error == "" || exit.Signal != "" || exit.Generation != 1 || exit.Ran != 0 {
		t.Fatalf("LastExit is %+v, want status -1 and the error", exit)
	}
	if !strings.Contains(exit.Error, "exec format error") {
		t.Errorf("the error is %q, want it to say: exec format error", exit.Error)
	}
	if delay := st.RestartAt.Sub(exit.At); delay != 20*time.Millisecond {
		t.Errorf("RestartAt is %s after the end, want 20ms", delay)
	}
	eventually(t, "three tries", func() bool { return len(h.delays(1)) >= 3 })
	if got, want := h.delays(1)[:3], []string{" in 20ms", " in 40ms", " in 80ms"}; !slices.Equal(got, want) {
		t.Errorf("the delays were %q, want %q", got, want)
	}
	line := h.matching(`^generation 1 could not be started`)[0]
	if !regexp.MustCompile(`^generation 1 could not be started: .*exec format error; starting it again in 20ms$`).MatchString(line) {
		t.Errorf("the line is %q", line)
	}
	if got := h.matching(`^started`); len(got) != 0 {
		t.Errorf("lines of starts that never were: %q", got)
	}
}

func TestActivateRefusesWhatIsNoExecutableFile(t *testing.T) {
	bin := build.Binary(t)
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	if err := os.WriteFile(data, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := run(t, Config{})

	// With nothing running, and with a service running.
	for _, running := range []bool{false, true} {
		if running {
			h.activate(1, bin, "--stdout", "ready")
			h.wrote(logring.Stdout, "ready")
		}
		before := h.State()
		for name, path := range map[string]string{
			"a missing file":                 filepath.Join(dir, "missing"),
			"a directory":                    dir,
			"a file without the execute bit": data,
		} {
			err := h.Activate(2, path, []string{"x"})
			if !errors.Is(err, ErrNotExecutable) {
				t.Errorf("running %v, %s: Activate returned %v, want ErrNotExecutable", running, name, err)
			}
			after := h.State()
			if after.Generation != before.Generation || after.Binary != before.Binary || after.PID != before.PID ||
				after.Phase != before.Phase || !slices.Equal(after.Args, before.Args) {
				t.Errorf("running %v, %s: the state went from %+v to %+v", running, name, before, after)
			}
		}
		if running && !alive(before.PID) {
			t.Errorf("the service was stopped")
		}
	}
	if got := h.matching(`generation 2`); len(got) != 0 {
		t.Errorf("the supervisor wrote of the generation it refused: %q", got)
	}
}

func TestActivateReplacesTheServiceThatRuns(t *testing.T) {
	bin := build.Binary(t)
	other := filepath.Join(t.TempDir(), "other")
	if err := os.Link(bin, other); err != nil {
		t.Fatal(err)
	}
	h := run(t, Config{QuickExit: time.Hour})
	h.activate(1, bin, "--stdout", "one")
	h.wrote(logring.Stdout, "one")
	old := h.State().PID

	h.activate(2, other, "--stdout", "two")
	// At once: the generation and the binary are the new ones, and the
	// phase is not that of the old service.
	st := h.State()
	if st.Generation != 2 || st.Binary != other || !slices.Equal(st.Args, []string{"--stdout", "two"}) {
		t.Errorf("when Activate has returned: generation %d, binary %s, args %q", st.Generation, st.Binary, st.Args)
	}
	if st.PID == old || (st.Phase != wire.PhaseStopping && st.Phase != wire.PhaseRunning) {
		t.Errorf("when Activate has returned: phase %s, PID %d (the old one is %d)", st.Phase, st.PID, old)
	}

	st = h.phase(wire.PhaseRunning)
	h.wrote(logring.Stdout, "two")
	if st.PID == old || st.PID <= 0 || st.Generation != 2 {
		t.Errorf("running: %+v, the old PID is %d", st, old)
	}
	// The old one was sent SIGTERM, which ends testservice with 0.
	if st.LastExit == nil || st.LastExit.Generation != 1 || st.LastExit.Status != 0 || st.LastExit.Signal != "" {
		t.Errorf("LastExit is %+v, want status 0 of generation 1", st.LastExit)
	}
	if st.Restarts != 0 {
		t.Errorf("Restarts is %d: a start after Activate is no restart", st.Restarts)
	}
	if alive(old) {
		t.Errorf("the old service, %d, is still there", old)
	}
	if got := h.delays(1); len(got) != 0 {
		t.Errorf("the supervisor wrote that it starts generation 1 again: %q", h.lines(logring.Zathras))
	}
}

func TestServiceThatIgnoresSIGTERMIsKilledAfterStopTimeout(t *testing.T) {
	bin := build.Binary(t)
	const stopTimeout = 400 * time.Millisecond
	h := run(t, Config{StopTimeout: stopTimeout, QuickExit: time.Hour})
	h.activate(1, bin, "--ignore-term", "--stdout", "ready")
	h.wrote(logring.Stdout, "ready")
	old := h.State().PID

	asked := time.Now()
	h.activate(2, bin, "--stdout", "two")
	st := h.State()
	if st.Phase != wire.PhaseStopping || st.Generation != 2 || st.PID != 0 || st.RunningFor != 0 {
		t.Errorf("when Activate has returned: %+v, want stopping, generation 2, no PID", st)
	}
	if !alive(old) {
		t.Errorf("the service that ignores SIGTERM is gone before the stop timeout")
	}

	st = h.phase(wire.PhaseRunning)
	if waited := time.Since(asked); waited < stopTimeout {
		t.Errorf("generation 2 runs after %s, want the stop timeout of %s to pass first", waited, stopTimeout)
	}
	if st.LastExit == nil || st.LastExit.Generation != 1 || st.LastExit.Status != -1 || st.LastExit.Signal != "SIGKILL" {
		t.Errorf("LastExit is %+v, want SIGKILL of generation 1", st.LastExit)
	}
	if st.LastExit != nil && st.LastExit.Ran < stopTimeout {
		t.Errorf("it ran for %s, want the stop timeout at least", st.LastExit.Ran)
	}
	if got := h.matching(`^generation 1 was ended by SIGKILL after [0-9.]+m?s$`); len(got) != 1 {
		t.Errorf("the supervisor's lines are %q, want one of the end by SIGKILL", h.lines(logring.Zathras))
	}
	h.wrote(logring.Stdout, "two")
}

func TestWhatTheServiceLeftInItsGroupIsKilled(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{QuickExit: time.Hour})
	h.activate(1, bin, "--spawn", "--stdout", "ready")
	h.wrote(logring.Stdout, "ready")
	var child int
	for _, text := range h.lines(logring.Stdout) {
		fmt.Sscanf(text, "child %d", &child)
	}
	if child == 0 {
		t.Fatalf("no line child <pid> in %q", h.lines(logring.Stdout))
	}
	t.Cleanup(func() { syscall.Kill(child, syscall.SIGKILL) })
	if !alive(child) {
		t.Fatalf("the child %d does not run", child)
	}

	// The service ends at SIGTERM. Its child ignores it, and holds the
	// output of the service open.
	h.activate(2, bin, "--stdout", "two")
	st := h.phase(wire.PhaseRunning)
	if st.LastExit == nil || st.LastExit.Status != 0 {
		t.Errorf("LastExit is %+v, want status 0", st.LastExit)
	}
	eventually(t, "the child is gone", func() bool { return !alive(child) })
}

func TestActivateDuringBackoffStartsAtOnce(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{QuickExit: time.Hour, BackoffMin: 20 * time.Millisecond, BackoffMax: time.Hour})
	h.activate(1, bin, "--exit-after", "0", "--exit-code", "1")
	// 20 ms, 40 ms, 80 ms, 160 ms, 320 ms, and then 640 ms and more to
	// be in backoff in.
	eventually(t, "six exits of generation 1", func() bool { return len(h.delays(1)) >= 6 })
	st := h.phase(wire.PhaseBackoff)
	if st.RestartAt.IsZero() {
		t.Errorf("in backoff without RestartAt")
	}

	h.activate(2, bin, "--exit-after", "0", "--exit-code", "2")
	if got := h.matching(`^started generation 2`); len(got) != 1 {
		t.Fatalf("when Activate has returned, generation 2 was not started: %q", h.lines(logring.Zathras))
	}
	eventually(t, "two exits of generation 2", func() bool { return len(h.delays(2)) >= 2 })
	if got, want := h.delays(2)[:2], []string{" in 20ms", " in 40ms"}; !slices.Equal(got, want) {
		t.Errorf("the delays of generation 2 were %q, want %q: the backoff was not forgotten", got, want)
	}
	if got := len(h.matching(`^started generation 1`)); got != 6 {
		t.Errorf("generation 1 was started %d times, want 6: it was started after Activate", got)
	}
}

func TestEndOfContextStopsTheService(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{})
	h.activate(1, bin, "--stdout", "ready")
	h.wrote(logring.Stdout, "ready")
	pid := h.State().PID

	if err := h.stop(); err != nil {
		t.Errorf("Run returned %v, want nil", err)
	}
	if alive(pid) {
		t.Errorf("the service %d runs after Run has returned", pid)
	}
	st := h.State()
	if st.Phase != wire.PhaseStopping || st.PID != 0 || st.Generation != 1 {
		t.Errorf("after Run: %+v, want stopping, no PID, generation 1", st)
	}
	if st.LastExit == nil || st.LastExit.Status != 0 || st.LastExit.Generation != 1 {
		t.Errorf("LastExit is %+v, want status 0 of generation 1", st.LastExit)
	}
	if err := h.Activate(2, bin, nil); !errors.Is(err, ErrStopped) {
		t.Errorf("Activate after Run returned %v, want ErrStopped", err)
	}
	if st := h.State(); st.Generation != 1 {
		t.Errorf("the refused Activate changed the generation to %d", st.Generation)
	}
	if got := h.matching(`^started`); len(got) != 1 {
		t.Errorf("starts: %q, want the one", got)
	}
	if got := h.matching(`^generation 1 exited with status 0 after [0-9.]+m?s$`); len(got) != 1 {
		t.Errorf("the supervisor's lines are %q, want one of the exit, with nothing of a start behind it", h.lines(logring.Zathras))
	}
}

func TestEndOfContextKillsAfterStopTimeout(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{StopTimeout: 300 * time.Millisecond})
	h.activate(1, bin, "--ignore-term", "--stdout", "ready")
	h.wrote(logring.Stdout, "ready")
	pid := h.State().PID

	asked := time.Now()
	if err := h.stop(); err != nil {
		t.Errorf("Run returned %v, want nil", err)
	}
	if waited := time.Since(asked); waited < 300*time.Millisecond {
		t.Errorf("Run returned after %s, want the stop timeout to pass first", waited)
	}
	if alive(pid) {
		t.Errorf("the service %d runs after Run has returned", pid)
	}
	if exit := h.State().LastExit; exit == nil || exit.Signal != "SIGKILL" {
		t.Errorf("LastExit is %+v, want SIGKILL", exit)
	}
}

func TestEndOfContextDuringBackoffStartsNothing(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{QuickExit: time.Hour, BackoffMin: time.Hour, BackoffMax: time.Hour})
	h.activate(1, bin, "--exit-after", "0")
	h.phase(wire.PhaseBackoff)
	if err := h.stop(); err != nil {
		t.Errorf("Run returned %v, want nil", err)
	}
	st := h.State()
	if st.Phase != wire.PhaseStopping || !st.RestartAt.IsZero() {
		t.Errorf("after Run: phase %s, RestartAt %v; want stopping and the zero time", st.Phase, st.RestartAt)
	}
	if got := h.matching(`^started`); len(got) != 1 {
		t.Errorf("starts: %q, want the one", got)
	}
}

func TestEndOfContextWhileIdle(t *testing.T) {
	h := run(t, Config{})
	if err := h.stop(); err != nil {
		t.Errorf("Run returned %v, want nil", err)
	}
	if st := h.State(); st.Phase != wire.PhaseIdle {
		t.Errorf("after Run: phase %s, want idle", st.Phase)
	}
	if err := h.Activate(1, "/bin/sh", nil); !errors.Is(err, ErrStopped) {
		t.Errorf("Activate after Run returned %v, want ErrStopped", err)
	}
}

func TestChanges(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{QuickExit: time.Hour})

	// changed waits for a value, and for the state that cond describes.
	changed := func(what string, cond func(wire.State) bool) {
		t.Helper()
		deadline := time.After(20 * time.Second)
		for {
			select {
			case <-h.Changes():
				if cond(h.State()) {
					return
				}
			case <-deadline:
				t.Fatalf("no value on Changes for: %s", what)
			}
		}
	}
	select {
	case <-h.Changes():
		t.Fatal("a value on Changes before anything changed")
	default:
	}

	h.activate(1, bin, "--stdout", "ready")
	changed("the start", func(st wire.State) bool { return st.Phase == wire.PhaseRunning && st.Generation == 1 })
	h.wrote(logring.Stdout, "ready")

	h.activate(2, bin, "--exit-after", "0")
	changed("the next generation", func(st wire.State) bool { return st.Generation == 2 })
	changed("the backoff", func(st wire.State) bool { return st.Phase == wire.PhaseBackoff })
	changed("the restart", func(st wire.State) bool { return st.Restarts >= 1 })

	// Nobody reads Changes from here on, and the supervisor goes on.
	before := h.State().Restarts
	eventually(t, "five more restarts", func() bool { return h.State().Restarts >= before+5 })
	if n := len(h.Changes()); n != 1 {
		t.Errorf("Changes holds %d values, want the one it has room for", n)
	}
}
