package draal

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/node/nodetest"
	"github.com/fables-for-robots/great-machine/wire"
)

// logged is a slog.Handler that keeps every record, for a test to look at.
type logged struct {
	mu      sync.Mutex
	records []slog.Record
}

func (l *logged) Enabled(context.Context, slog.Level) bool { return true }
func (l *logged) WithAttrs([]slog.Attr) slog.Handler       { return l }
func (l *logged) WithGroup(string) slog.Handler            { return l }

func (l *logged) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, r.Clone())
	return nil
}

// find returns the first record at the level that has every attribute
// given, as text, and whether there is one.
func (l *logged) find(level slog.Level, attrs map[string]string) (slog.Record, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.records {
		if r.Level != level {
			continue
		}
		matched := 0
		r.Attrs(func(a slog.Attr) bool {
			if want, ok := attrs[a.Key]; ok && a.Value.String() == want {
				matched++
			}
			return true
		})
		if matched == len(attrs) {
			return r, true
		}
	}
	return slog.Record{}, false
}

// logTo makes cfg log to a new logged, and returns it.
func logTo(cfg *Config) *logged {
	l := &logged{}
	cfg.Log = slog.New(l)
	return l
}

// isClean says whether s is valid UTF-8 without a control character.
func isClean(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsFunc(s, func(r rune) bool {
		return r < 0x20 || (r >= 0x7f && r <= 0x9f)
	})
}

// send writes a report of st, also for a fake that is silent.
func (f *fake) send(st wire.State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := wire.Write(f.reg, &st); err != nil {
		f.t.Errorf("a report of %s: %v", f.name, err)
	}
}

func TestARegistrationThatIsNotValidIsRefused(t *testing.T) {
	w := startWorld(t, testConfig(t))
	cases := []struct {
		name, zathras, version string
		phase                  wire.Phase
	}{
		{"a name of 4 MiB", strings.Repeat("a", 4<<20-512), "test", wire.PhaseIdle},
		{"a name of 254 bytes", strings.Repeat("a", 254), "test", wire.PhaseIdle},
		{"a name with a line feed", "web\nweb-1", "test", wire.PhaseIdle},
		{"a name with an escape", "web-\x1b[31m1", "test", wire.PhaseIdle},
		{"a name with a space", "web 1", "test", wire.PhaseIdle},
		{"an empty name", "", "test", wire.PhaseIdle},
		{"a name that begins with a dot", ".web", "test", wire.PhaseIdle},
		{"a name that begins with a hyphen", "-web", "test", wire.PhaseIdle},
		{"a name with a character beyond ASCII", "wéb", "test", wire.PhaseIdle},
		{"a version of 65 bytes", "web-1", strings.Repeat("1", 65), wire.PhaseIdle},
		{"a version with a line feed", "web-1", "v1\n", wire.PhaseIdle},
		{"a version beyond ASCII", "web-1", "v1é", wire.PhaseIdle},
		{"a phase draal does not know", "web-1", "test", "dancing"},
	}
	for i, tc := range cases {
		conn := w.dial(nodetest.ID(100 + i))
		_, _, err := ask(t, conn, &wire.Request{Register: &wire.Register{
			Service: "web", Name: tc.zathras, Version: tc.version, State: wire.State{Phase: tc.phase},
		}})
		if wire.CodeOf(err) != wire.CodeBadRequest {
			t.Errorf("register with %s = %v, want a bad-request", tc.name, err)
		}
	}
	if f := w.fleetNow(); len(f.Services) != 0 {
		t.Errorf("the fleet has %d services after refused registrations", len(f.Services))
	}

	// What fits is taken: a host name, and a name and a version at their
	// longest.
	for i, name := range []string{"web-7d9f8b6c5-x2k4p", "Web_1.example.org", strings.Repeat("a", 253)} {
		conn := w.dial(nodetest.ID(200 + i))
		_, resp, err := ask(t, conn, &wire.Request{Register: &wire.Register{
			Service: "web", Name: name, Version: strings.Repeat("~", 64), State: idle(),
		}})
		if err != nil || resp.Registered == nil {
			t.Errorf("register as %.20q = %+v, %v; want it registered", name, resp, err)
		}
	}
}

func TestAReportIsBoundedAndClean(t *testing.T) {
	w := startWorld(t, testConfig(t))
	z := w.newFake(10, "web", "web-1", idle())
	z.silent = true
	z.connect()
	op := w.dial(operatorID)
	s, resp, err := ask(t, op, &wire.Request{Watch: &wire.WatchRequest{}})
	if err != nil || resp.Fleet == nil {
		t.Fatalf("watch = %+v, %v", resp, err)
	}

	dirt := "a\x00b\nc\x1b[31md\x7fe\u0085f\u009fé"
	mega := func(n int) string { return strings.Repeat(dirt, n/len(dirt)) }
	// The cut of Binary falls in the middle of an é.
	binary := "x" + strings.Repeat("é", 1<<19)
	var args []string
	for range 200 {
		args = append(args, mega(4<<10))
	}
	var prepared []uint64
	for g := range uint64(1000) {
		prepared = append(prepared, g+1)
	}
	z.send(wire.State{
		Generation: 3,
		Binary:     binary,
		Args:       args,
		Phase:      wire.PhaseBackoff,
		LastExit:   &wire.Exit{Generation: 3, Status: -1, Signal: mega(1 << 10), Error: mega(1 << 20)},
		Prepared:   prepared,
	})

	var got wire.State
	for {
		resp, err := wire.ReadResponse(s)
		if err != nil || resp.Fleet == nil {
			t.Fatalf("watch = %+v, %v; want the fleet with the report", resp, err)
		}
		if f := resp.Fleet; len(f.Services) == 1 && len(f.Services[0].Zathrases) == 1 && f.Services[0].Zathrases[0].State.Generation == 3 {
			got = f.Services[0].Zathrases[0].State
			break
		}
	}
	if len(got.Binary) > 4096 || len(got.Binary) < 4000 || !isClean(got.Binary) {
		t.Errorf("the binary is %d bytes, clean: %v; want at most 4096, cut on a rune, and clean", len(got.Binary), isClean(got.Binary))
	}
	if len(got.Args) != 128 {
		t.Errorf("%d arguments, want 128", len(got.Args))
	}
	for i, arg := range got.Args {
		if len(arg) > 1024 || !isClean(arg) {
			t.Fatalf("argument %d is %d bytes, clean: %v; want at most 1024 and clean", i, len(arg), isClean(arg))
		}
	}
	if exit := got.LastExit; exit == nil || len(exit.Error) > 1024 || len(exit.Signal) > 32 || !isClean(exit.Error) || !isClean(exit.Signal) {
		t.Errorf("the last exit is %.200v; want an error of at most 1024 bytes and a signal of 32, clean", exit)
	}
	if want := prepared[len(prepared)-64:]; !slices.Equal(got.Prepared, want) {
		t.Errorf("prepared = %v, want the 64 highest", got.Prepared)
	}
	if len(got.Args) > 0 && !strings.HasPrefix(got.Args[0], "a b c [31md e f é") {
		t.Errorf("the control characters were not replaced by spaces: %.40q", got.Args[0])
	}
}

func TestAReportWithAPhaseDraalDoesNotKnowEndsTheConnection(t *testing.T) {
	cfg := testConfig(t)
	cfg.GoneAfter = time.Minute // the zathras is silent
	log := logTo(&cfg)
	w := startWorld(t, cfg)
	z := w.newFake(10, "web", "web-1", idle())
	z.silent = true
	z.connect()
	z.send(wire.State{Phase: "dancing"})
	eventually(t, "the connection to be closed", func() bool { return closed(z.conn) })
	eventually(t, "the zathras to leave the fleet", func() bool {
		_, ok := w.member("web", z.id)
		return !ok
	})
	if _, ok := log.find(slog.LevelWarn, map[string]string{"zathras": string(z.id), "phase": "dancing"}); !ok {
		t.Error("nothing was logged of the phase")
	}
}

func TestAReasonOfMegabytesIsBounded(t *testing.T) {
	w := startWorld(t, testConfig(t))
	w.stableWeb()
	a := w.newFake(10, "web", "web-a", running(1))
	a.onPrepare = func(wire.Prepare, node.Stream) error {
		return wire.Errorf(wire.CodeInternal, "%s", strings.Repeat("no\x1b[31m\n", 3<<20/8))
	}
	a.connect()

	halted := w.awaitRollout(w.rollout(newWeb(1)), wire.RolloutHalted)
	if len(halted.Reason) > 4096+len("web-a: ") || !strings.HasPrefix(halted.Reason, "web-a: no") || !isClean(halted.Reason) {
		t.Errorf("the reason is %d bytes, clean: %v, %.40q; want at most 4096 and the name, clean", len(halted.Reason), isClean(halted.Reason), halted.Reason)
	}
	op := w.dial(operatorID)
	if _, resp, err := ask(t, op, &wire.Request{Rollouts: &wire.RolloutsRequest{Service: "web"}}); err != nil || resp.Rollouts == nil {
		t.Errorf("rollouts = %+v, %v", resp, err)
	}
	if _, resp, err := ask(t, op, &wire.Request{Watch: &wire.WatchRequest{}}); err != nil || resp.Fleet == nil {
		t.Errorf("watch = %+v, %v", resp, err)
	}
}

// A fleet that is more than a frame holds cannot be sent: draal says so
// in its log, with whom and what, and the watch ends.
func TestAnAnswerThatCannotBeWrittenIsLogged(t *testing.T) {
	cfg := testConfig(t)
	cfg.GoneAfter = time.Minute // the zathrases are silent
	log := logTo(&cfg)
	w := startWorld(t, cfg)
	// Each zathras reports as much as draal keeps: 34 of them are more
	// than a frame.
	var args []string
	for range 128 {
		args = append(args, strings.Repeat("a", 1024))
	}
	for i := range 34 {
		z := w.newFake(10+i, "web", fmt.Sprintf("web-%02d", i), wire.State{Phase: wire.PhaseRunning, Args: args})
		z.silent = true
		z.connect()
	}
	s, _, err := ask(t, w.dial(operatorID), &wire.Request{Watch: &wire.WatchRequest{}})
	if err == nil {
		t.Fatal("watch answered with a fleet of more than a frame")
	}
	s.Close()
	eventually(t, "the log to say why", func() bool {
		_, ok := log.find(slog.LevelError, map[string]string{"peer": string(operatorID), "request": "watch"})
		return ok
	})
}
