package supervisor

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/launcher"
	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/testservice/build"
	"github.com/fables-for-robots/great-machine/wire"
)

// Start, if set, is what starts the service: it is given the command as
// the supervisor made it, and what starts it.
func TestStartIsGivenTheCommandAndWhatStartsIt(t *testing.T) {
	bin := build.Binary(t)
	type start struct {
		path    string
		args    []string
		ownPgid bool
	}
	var mu sync.Mutex
	var starts []start
	h := run(t, Config{Start: func(cmd *exec.Cmd, begin func() error) error {
		mu.Lock()
		starts = append(starts, start{cmd.Path, slices.Clone(cmd.Args), cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid})
		mu.Unlock()
		return begin()
	}})
	h.activate(1, bin, "--stdout", "ready")
	h.wrote(logring.Stdout, "ready")
	st := h.phase(wire.PhaseRunning)

	mu.Lock()
	defer mu.Unlock()
	want := start{bin, []string{bin, "--stdout", "ready"}, true}
	if len(starts) != 1 || starts[0].path != want.path || !slices.Equal(starts[0].args, want.args) || !starts[0].ownPgid {
		t.Fatalf("Start was given %+v, want once %+v", starts, want)
	}
	if !alive(st.PID) {
		t.Errorf("the process %d of the state does not run", st.PID)
	}
}

// What Start returns is what came of the start: its error is that of a
// binary that could not be started.
func TestWhatStartReturnsIsWhatCameOfTheStart(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{Start: func(*exec.Cmd, func() error) error {
		return errors.New("the launcher would not")
	}})
	h.activate(1, bin)

	st := h.State()
	if st.Phase != wire.PhaseBackoff || st.PID != 0 {
		t.Fatalf("when Activate has returned: %+v, want backoff", st)
	}
	if exit := st.LastExit; exit == nil || exit.Status != -1 || exit.Error != "the launcher would not" {
		t.Fatalf("LastExit is %+v, want status -1 and the error of Start", exit)
	}
	eventually(t, "three tries", func() bool { return len(h.delays(1)) >= 3 })
	if line := h.matching(`^generation 1 could not be started`)[0]; line != "generation 1 could not be started: the launcher would not; starting it again in 20ms" {
		t.Errorf("the line is %q", line)
	}
	if got := h.matching(`^started`); len(got) != 0 {
		t.Errorf("lines of starts that never were: %q", got)
	}
}

// throughALauncher returns cfg with a real launcher as its Start.
func throughALauncher(t *testing.T, cfg Config) Config {
	t.Helper()
	l, err := launcher.New(launcher.Config{Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("launcher.New: %v", err)
	}
	cfg.Start = l.Start
	return cfg
}

// Through a launcher the service runs as it does without one: the
// arguments, the output, the process and its group, how it is stopped
// and what it exited with.
func TestThroughALauncherTheServiceRunsAsBefore(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, throughALauncher(t, Config{QuickExit: time.Hour}))
	h.activate(1, bin, "--stdout", "", "--stderr", "two words", "--spawn")
	h.wrote(logring.Stderr, "two words")
	h.wrote(logring.Stdout, "")
	st := h.phase(wire.PhaseRunning)

	// The process of the state is the service, and leads its own group.
	if pgid, err := syscall.Getpgid(st.PID); err != nil || pgid != st.PID {
		t.Errorf("the process group of the service %d is %d (%v), want its own", st.PID, pgid, err)
	}
	var child int
	for _, text := range h.lines(logring.Stdout) {
		fmt.Sscanf(text, "child %d", &child)
	}
	if child == 0 {
		t.Fatalf("no line child <pid> in %q", h.lines(logring.Stdout))
	}
	t.Cleanup(func() { syscall.Kill(child, syscall.SIGKILL) })
	if pgid, err := syscall.Getpgid(child); err != nil || pgid != st.PID {
		t.Errorf("the process group of the service's child is %d (%v), want the service's, %d", pgid, err, st.PID)
	}

	// SIGTERM stops it, and what it left in its group goes with it.
	h.activate(2, bin, "--exit-after", "0", "--exit-code", "3")
	eventually(t, "the first generation has ended", func() bool {
		return len(h.matching(`^generation 1 exited with status 0 after`)) == 1
	})
	eventually(t, "the child is gone", func() bool { return !alive(child) })

	// And an exit status is the service's own.
	eventually(t, "the second generation has exited with 3", func() bool {
		exit := h.State().LastExit
		return exit != nil && exit.Generation == 2 && exit.Status == 3 && exit.Error == ""
	})
}

// A binary that is gone when it is started was a file the supervisor took
// (Activate has looked). Through a launcher, as without one, that is a
// start that failed: a service that ended at once, whose last exit has
// the error and no status of a process, for there was none.
func TestThroughALauncherABinaryThatIsGone(t *testing.T) {
	h := run(t, throughALauncher(t, Config{}))
	path := script(t, "exit 0\n")
	h.activate(1, path)
	eventually(t, "the service has run and ended", func() bool {
		exit := h.State().LastExit
		return exit != nil && exit.Status == 0
	})
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a start that found no program", func() bool {
		exit := h.State().LastExit
		return exit != nil && exit.Status == -1
	})
	exit := h.State().LastExit
	if !strings.Contains(exit.Error, path) || !strings.Contains(exit.Error, "no such file") || exit.Signal != "" || exit.Generation != 1 {
		t.Errorf("LastExit is %+v, want status -1 and an error that names the binary and says that it is not there", exit)
	}
	// It is started again like any service that ended at once: later
	// each time, up to the most.
	eventually(t, "a wait of the longest delay after a start that found no program", func() bool {
		lines := h.matching(`^generation 1 could not be started: .*no such file.*; starting it again in 80ms$`)
		return len(lines) >= 2
	})
}

// A file that is no program is a start that failed too, with the reason.
func TestThroughALauncherAFileThatIsNoProgram(t *testing.T) {
	noProgram := filepath.Join(t.TempDir(), "no-program")
	if err := os.WriteFile(noProgram, []byte("this is no program\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := run(t, throughALauncher(t, Config{}))
	h.activate(1, noProgram)
	eventually(t, "a start that could not execute the file", func() bool {
		exit := h.State().LastExit
		return exit != nil && exit.Status == -1
	})
	if exit := h.State().LastExit; !strings.Contains(exit.Error, noProgram) || !strings.Contains(exit.Error, "exec format error") {
		t.Errorf("LastExit is %+v, want an error that names the file and says why it could not be executed", exit)
	}
}
