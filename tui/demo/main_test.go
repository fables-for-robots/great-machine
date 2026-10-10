package main

import (
	"bytes"
	"context"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/fables-for-robots/great-machine/demo"
	"github.com/fables-for-robots/great-machine/wire"
)

// deadline is how long a test waits for something that is to happen.
const deadline = 30 * time.Second

// sequences is what a terminal does not show of what is written to it.
var sequences = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]|\x1b[=>]|\\s")

// The smoke test: the demo is started in a pseudo-terminal, shows the
// made-up fleet, or with --empty that there is no service, and ends
// when q is typed.
func TestDemoRuns(t *testing.T) {
	for _, tt := range []struct {
		empty       bool
		says, shows string
	}{
		{false, "demo: a made-up fleet behind the real terminal UI of great-machine\n", "4services"},
		{true, "demo: a fleet without a service behind the real terminal UI of great-machine\n", "draalknowsnoserviceyet."},
	} {
		master, slave, err := pty.Open()
		if err != nil {
			t.Fatalf("opening a pseudo-terminal: %v", err)
		}
		if err := pty.Setsize(master, &pty.Winsize{Cols: 80, Rows: 24}); err != nil {
			t.Fatalf("sizing the pseudo-terminal: %v", err)
		}
		var mu sync.Mutex
		var out bytes.Buffer
		read := make(chan struct{})
		go func() {
			defer close(read)
			buf := make([]byte, 32<<10)
			for {
				n, err := master.Read(buf)
				mu.Lock()
				out.Write(buf[:n])
				mu.Unlock()
				if err != nil {
					return
				}
			}
		}()
		var said bytes.Buffer
		ended := make(chan error, 1)
		go func() { ended <- run(t.Context(), slave, slave, tt.empty, &said) }()
		for end := time.Now().Add(deadline); ; time.Sleep(5 * time.Millisecond) {
			mu.Lock()
			shown := sequences.ReplaceAllString(out.String(), "")
			mu.Unlock()
			if strings.Contains(shown, tt.shows) && strings.Contains(shown, "live") {
				break
			}
			if time.Now().After(end) {
				t.Fatalf("timed out waiting for the demo to show %q; it shows %q", tt.shows, shown)
			}
		}
		if _, err := master.WriteString("q"); err != nil {
			t.Fatalf("typing q: %v", err)
		}
		select {
		case err := <-ended:
			if err != nil {
				t.Errorf("run = %v, want nil", err)
			}
		case <-time.After(deadline):
			t.Fatal("timed out waiting for the demo to end")
		}
		if got := said.String(); !strings.HasPrefix(got, "demo: the shell of the terminal UI is a REAL SHELL ON THIS MACHINE") || !strings.HasSuffix(got, tt.says) {
			t.Errorf("the demo says %q", got)
		}
		// Bubble Tea may ask the terminal for its size for a moment
		// after it has ended: it is closed when nothing of it is left.
		for end := time.Now().Add(deadline); ; time.Sleep(5 * time.Millisecond) {
			buf := make([]byte, 4<<20)
			if stacks := string(buf[:runtime.Stack(buf, true)]); !strings.Contains(stacks, "charm.land/bubbletea") {
				break
			}
			if time.Now().After(end) {
				t.Fatal("timed out waiting for the goroutines of Bubble Tea to end")
			}
		}
		slave.Close()
		master.Close()
		select {
		case <-read:
		case <-time.After(deadline):
			t.Error("timed out waiting for the pseudo-terminal to be read to its end")
		}
	}
}

// The connection of the demo is closed as a client's is: what was asked
// of it ends, and the shell it has ready is a session of the terminal
// UI.
func TestOperatorOfTheDemoEndsWhatWasAskedOfIt(t *testing.T) {
	fleet := demo.New(false)
	op := newOperator(fleet)
	fleets, err := op.Watch(context.Background())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	f := <-fleets
	if len(f.Services) != 4 {
		t.Fatalf("the fleet has %d services", len(f.Services))
	}
	var z wire.Zathras
	for _, svc := range f.Services {
		if svc.Name == "web" {
			z = svc.Zathrases[0]
		}
	}
	if list, err := op.Rollouts(context.Background(), "web"); err != nil || len(list) != 20 {
		t.Errorf("Rollouts = %d of them, %v", len(list), err)
	}
	if samples, err := op.History(context.Background(), z.ID); err != nil || len(samples) < 400 {
		t.Errorf("History = %d samples, %v", len(samples), err)
	}
	lines, err := op.Logs(context.Background(), z.ID, true)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	<-lines
	sh, err := op.Shell(context.Background(), z.ID, wire.ShellRequest{Cols: 80, Rows: 24, Term: "dumb"})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if err := sh.Close(); err != nil {
		t.Errorf("closing the shell: %v", err)
	}

	op.Close()
	timeout := time.After(deadline)
	for fleets != nil || lines != nil {
		select {
		case _, open := <-fleets:
			if !open {
				fleets = nil
			}
		case _, open := <-lines:
			if !open {
				lines = nil
			}
		case <-timeout:
			t.Fatal("timed out waiting for the watch and the log to end when the connection was closed")
		}
	}
}
