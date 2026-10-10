package tuitest_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/tui"
	"github.com/fables-for-robots/great-machine/tui/tuitest"
	"github.com/fables-for-robots/great-machine/wire"
)

// emptyDraal is a draal without a service, and without anything else.
type emptyDraal struct{}

func (emptyDraal) Watch(ctx context.Context) (<-chan wire.Fleet, error) {
	fleets := make(chan wire.Fleet, 1)
	fleets <- wire.Fleet{}
	go func() {
		<-ctx.Done()
		close(fleets)
	}()
	return fleets, nil
}

func (emptyDraal) Rollouts(context.Context, string) ([]wire.RolloutInfo, error) { return nil, nil }

func (emptyDraal) History(context.Context, wire.ID) ([]wire.Sample, error) { return nil, nil }

func (emptyDraal) Logs(context.Context, wire.ID, bool) (<-chan wire.LogLine, error) {
	return nil, wire.Errorf(wire.CodeUnavailable, "there is no zathras")
}

func (emptyDraal) Shell(context.Context, wire.ID, wire.ShellRequest) (tui.Shell, error) {
	return nil, wire.Errorf(wire.CodeUnavailable, "there is no zathras")
}

func (emptyDraal) Close() error { return nil }

// A terminal UI runs in a Terminal: what it draws is the screen, keys
// reach it, a size that changes is drawn, and when it has ended the
// terminal is as it was. And a terminal that was left in another mode
// is told apart from one that was restored.
func TestTerminal(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	term := tuitest.Open(t, 80, 24)
	if !term.Usual() || term.OnAlternateScreen() || term.Written() != "" {
		t.Fatalf("a terminal that was just opened is not in its usual mode, or was written to: %q", term.Written())
	}
	ended := make(chan error, 1)
	go func() {
		ended <- tui.RunDrawn(t.Context(), tui.Config{
			Dial: func(context.Context) (tui.Operator, error) { return emptyDraal{}, nil },
			In:   term.File(), Out: term.File(), Draal: "3f9a2c1b",
		}, term.Drawn)
	}()
	screen := term.Wait("draal 3f9a2c1b · ● live", "draal knows no service yet.")
	if rows := strings.Split(screen, "\n"); len(rows) != 24 {
		t.Errorf("the screen has %d rows, want 24:\n%s", len(rows), screen)
	}
	if term.Usual() {
		t.Error("the terminal is in its usual mode while a terminal UI runs in it")
	}
	term.WaitWritten(0, "\x1b[?1049h")
	if !term.OnAlternateScreen() {
		t.Error("the terminal UI is not on the alternate screen")
	}
	term.Type("?")
	term.Wait("open what is chosen")
	term.Type("?")
	// Bubble Tea is told of a new size by a signal, which a test's
	// process does not get from its pseudo-terminal: it asks the terminal
	// for its size when it takes the terminal back from a shell, and at
	// its start.
	term.Resize(100, 30)
	term.Type("q")
	select {
	case err := <-ended:
		if err != nil {
			t.Errorf("RunDrawn = %v, want nil when the user quits", err)
		}
	case <-time.After(tuitest.Deadline):
		t.Fatal("timed out waiting for the terminal UI to end")
	}
	term.Restored()
	if left := tuitest.GoroutinesLeft(); len(left) != 0 {
		// Open's cleanup waits for them; here it is enough that they are
		// told: a stack of a goroutine names the package it is of.
		for _, g := range left {
			if !strings.Contains(g, "bubbletea") && !strings.Contains(g, "great-machine/tui.") && !strings.Contains(g, "cancelreader") && !strings.Contains(g, "ultraviolet") && !strings.Contains(g, "great-machine/term.") {
				t.Errorf("a goroutine that is none of the terminal UI is counted as one:\n%s", g)
			}
		}
	}
}
