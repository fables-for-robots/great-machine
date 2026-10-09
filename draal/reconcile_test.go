package draal

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// rollout asks draal for a rollout.
func (w *world) rollout(req wire.RolloutRequest) wire.RolloutInfo {
	w.t.Helper()
	info, err := w.d.Rollout(context.Background(), operatorID, req)
	if err != nil {
		w.t.Fatalf("Rollout: %v", err)
	}
	return info
}

// awaitRollout waits until a rollout is in the state given, and returns it.
func (w *world) awaitRollout(info wire.RolloutInfo, state wire.RolloutState) wire.RolloutInfo {
	w.t.Helper()
	var got wire.RolloutInfo
	end := time.Now().Add(deadline)
	for {
		list, err := w.d.Rollouts(context.Background(), info.Service)
		if err != nil {
			w.t.Fatalf("Rollouts: %v", err)
		}
		for _, r := range list {
			if r.ID == info.ID {
				got = r
			}
		}
		if got.State == state {
			return got
		}
		if time.Now().After(end) {
			w.t.Fatalf("rollout %d is %s (%s), want %s", info.ID, got.State, got.Reason, state)
		}
		time.Sleep(time.Millisecond)
	}
}

// stableWeb rolls generation 1 of web out while no zathras is connected,
// which completes at once and makes it the stable generation.
func (w *world) stableWeb() wire.RolloutInfo {
	w.t.Helper()
	return w.awaitRollout(w.rollout(webRollout(1)), wire.RolloutCompleted)
}

// newWeb returns a rollout of another binary of web.
func newWeb(parallel int) wire.RolloutRequest {
	return wire.RolloutRequest{
		Service:  "web",
		Binary:   "/nix/store/xyz-web/bin/web",
		Args:     []string{"--new"},
		Closure:  []string{"xyz-web", "def-libc"},
		Parallel: parallel,
	}
}

// rolloutIs returns a condition on a fleet: the last rollout of web is
// the one given and in that state.
func rolloutIs(info wire.RolloutInfo, state wire.RolloutState) func(wire.Fleet) bool {
	return func(f wire.Fleet) bool {
		for _, svc := range f.Services {
			if svc.Name == info.Service && svc.Rollout != nil {
				return svc.Rollout.ID == info.ID && svc.Rollout.State == state
			}
		}
		return false
	}
}

func TestARolloutRunsToCompleted(t *testing.T) {
	cfg := testConfig(t)
	cfg.StableAfter = 100 * time.Millisecond
	w := startWorld(t, cfg)
	w.stableWeb()

	// Two zathrases that run generation 1, and whose prepare waits for
	// the test.
	gate := make(chan struct{})
	hold := func(wire.Prepare, node.Stream) error { <-gate; return nil }
	a := w.newFake(10, "web", "web-a", running(1))
	b := w.newFake(11, "web", "web-b", running(1))
	a.onPrepare, b.onPrepare = hold, hold
	a.connect()
	b.connect()

	fleets, err := w.d.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	info := w.rollout(newWeb(1))
	if info.Generation != 2 {
		t.Fatalf("the rollout targets generation %d, want 2", info.Generation)
	}

	fleetWhere(t, fleets, rolloutIs(info, wire.RolloutPreparing))
	eventually(t, "a prepare for both", func() bool {
		return len(a.sent().prepares) == 1 && len(b.sent().prepares) == 1
	})
	if len(a.sent().activates)+len(b.sent().activates) != 0 {
		t.Fatal("an activate was sent while the rollout prepares")
	}
	close(gate)

	f, _ := fleetWhere(t, fleets, rolloutIs(info, wire.RolloutActivating))
	for _, svc := range f.Services {
		if svc.Name == "web" && svc.Rollout.Prepared != 2 {
			t.Errorf("while activating the fleet counts %d prepared, want 2", svc.Rollout.Prepared)
		}
	}
	f, _ = fleetWhere(t, fleets, rolloutIs(info, wire.RolloutCompleted))
	for _, svc := range f.Services {
		if svc.Name == "web" && svc.Stable != 2 {
			t.Errorf("after the rollout the stable generation is %d, want 2", svc.Stable)
		}
	}

	want := newWeb(1)
	var activated []time.Time
	for _, z := range []*fake{a, b} {
		got := z.sent()
		// A prepare may be sent twice: its answer can come before the
		// report that lists the generation.
		if len(got.prepares) == 0 || len(got.activates) != 1 {
			t.Fatalf("%s got %d prepares and %d activates, want a prepare and one activate", z.name, len(got.prepares), len(got.activates))
		}
		for _, p := range got.prepares {
			if p.Generation != 2 || p.Binary != want.Binary || !slices.Equal(p.Closure, want.Closure) {
				t.Errorf("%s was sent the prepare %+v", z.name, p)
			}
		}
		if act := got.activates[0]; act.Generation != 2 || act.Binary != want.Binary || !slices.Equal(act.Args, want.Args) {
			t.Errorf("%s was sent the activate %+v", z.name, act)
		}
		activated = append(activated, got.activatedAt[0])
	}
	// With Parallel 1 the second is activated when the first has run
	// for StableAfter.
	if gap := activated[1].Sub(activated[0]).Abs(); gap < cfg.StableAfter {
		t.Errorf("the two were activated %v apart, less than StableAfter (%v): not one after the other", gap, cfg.StableAfter)
	}
	events := w.noted()
	if last := events[len(events)-2:]; !strings.HasPrefix(last[0], "activate ") || !strings.HasPrefix(last[1], "activate ") {
		t.Errorf("the last commands were %q, want the two activates behind every prepare", last)
	}
}

func TestAPrepareThatFailsHaltsTheRollout(t *testing.T) {
	w := startWorld(t, testConfig(t))
	w.stableWeb()
	a := w.newFake(10, "web", "web-a", running(1))
	a.onPrepare = func(wire.Prepare, node.Stream) error {
		return wire.Errorf(wire.CodeInternal, "the sidecar said no")
	}
	a.connect()
	b := w.zathras(11, "web", "web-b", running(1))

	halted := w.awaitRollout(w.rollout(newWeb(1)), wire.RolloutHalted)
	if !strings.Contains(halted.Reason, "web-a") || !strings.Contains(halted.Reason, "the sidecar said no") {
		t.Errorf("the reason is %q, want the name of the zathras and what it answered", halted.Reason)
	}
	if halted.Finished.IsZero() {
		t.Error("a halted rollout has no time it finished")
	}
	for _, z := range []*fake{a, b} {
		if len(z.sent().activates) != 0 {
			t.Errorf("%s was sent an activate in a rollout that halted while preparing", z.name)
		}
	}
	if svc, _ := w.service("web"); svc.Stable != 1 {
		t.Errorf("the stable generation is %d, want 1 still", svc.Stable)
	}
}

func TestAZathrasThatFailsHaltsTheRollout(t *testing.T) {
	cases := []struct {
		name string
		rig  func(z *fake)
		want string
	}{
		{"its service ends at once", func(z *fake) {
			z.afterActivate = func(st *wire.State) {
				st.Phase, st.PID = wire.PhaseBackoff, 0
				st.RestartAt = time.Now().Add(time.Minute)
				st.LastExit = &wire.Exit{Generation: st.Generation, Status: 3, At: time.Now(), Ran: 10 * time.Millisecond}
			}
		}, "web-"},
		{"it refuses activate", func(z *fake) {
			z.onActivate = func(wire.Activate) error {
				return wire.Errorf(wire.CodeInternal, "not an executable file")
			}
		}, ": not an executable file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := startWorld(t, testConfig(t))
			w.stableWeb()
			// Both would fail: the rollout halts at the one that is
			// activated first, and the other is left alone.
			a := w.newFake(10, "web", "web-a", running(1))
			b := w.newFake(11, "web", "web-b", running(1))
			tc.rig(a)
			tc.rig(b)
			a.connect()
			b.connect()

			halted := w.awaitRollout(w.rollout(newWeb(1)), wire.RolloutHalted)
			if !strings.Contains(halted.Reason, tc.want) {
				t.Errorf("the reason is %q, want %q in it", halted.Reason, tc.want)
			}
			if n := len(a.sent().activates) + len(b.sent().activates); n != 1 {
				t.Errorf("%d activates were sent, want the rollout to halt at the first", n)
			}
		})
	}
}

func TestWhatIsNoAnswerHaltsNothing(t *testing.T) {
	cases := []struct {
		name string
		fail func(s node.Stream) error // what the first prepare and the first activate do
	}{
		// The zathras says that it shuts down.
		{"unavailable", func(node.Stream) error {
			return wire.Errorf(wire.CodeUnavailable, "the supervisor has stopped")
		}},
		// The stream ends without an answer.
		{"a broken stream", func(s node.Stream) error {
			s.Close()
			return errors.New("nothing is answered")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := startWorld(t, testConfig(t))
			w.stableWeb()
			z := w.newFake(10, "web", "web-a", running(1))
			prepares := 0
			z.onPrepare = func(_ wire.Prepare, s node.Stream) error {
				if prepares++; prepares == 1 {
					return tc.fail(s)
				}
				return nil
			}
			z.connect()

			// The command is sent again, and the rollout goes on.
			info := w.awaitRollout(w.rollout(newWeb(1)), wire.RolloutCompleted)
			got := z.sent()
			if len(got.prepares) < 2 {
				t.Errorf("the zathras got %d prepares, want the one that was not answered sent again", len(got.prepares))
			}
			if len(got.activates) != 1 || got.activates[0].Generation != info.Generation {
				t.Errorf("activates = %+v, want one for generation %d", got.activates, info.Generation)
			}
		})
	}
}

func TestAZathrasThatComesLaterIsSentTheStableGeneration(t *testing.T) {
	w := startWorld(t, testConfig(t))
	first := w.stableWeb() // with nobody connected it completes
	if svc, _ := w.service("web"); svc.Stable != first.Generation {
		t.Fatalf("the stable generation is %d, want %d", svc.Stable, first.Generation)
	}

	// Its first prepare fails, as when the store cannot be reached: it
	// is tried again after RetryAfter.
	z := w.newFake(10, "web", "web-new", idle())
	failed := false
	z.onPrepare = func(wire.Prepare, node.Stream) error {
		if !failed {
			failed = true
			return wire.Errorf(wire.CodeInternal, "the store cannot be reached")
		}
		return nil
	}
	z.connect()

	eventually(t, "the zathras to run the stable generation", func() bool {
		got, _ := w.member("web", z.id)
		return got.State.Generation == first.Generation && got.State.Phase == wire.PhaseRunning
	})
	want := webRollout(1)
	got := z.sent()
	if len(got.prepares) < 2 || len(got.activates) != 1 {
		t.Fatalf("the zathras got %d prepares and %d activates, want a second prepare and one activate", len(got.prepares), len(got.activates))
	}
	if p := got.prepares[1]; p.Generation != 1 || p.Binary != want.Binary || !slices.Equal(p.Closure, want.Closure) {
		t.Errorf("prepare = %+v", p)
	}
	if act := got.activates[0]; act.Generation != 1 || act.Binary != want.Binary || !slices.Equal(act.Args, want.Args) {
		t.Errorf("activate = %+v", act)
	}
	if gap := got.prepareAt[1].Sub(got.prepareAt[0]); gap < w.cfg.RetryAfter {
		t.Errorf("the prepare was sent again after %v, sooner than RetryAfter (%v)", gap, w.cfg.RetryAfter)
	}
	if last, _ := w.service("web"); last.Rollout.State != wire.RolloutCompleted {
		t.Errorf("the rollout is %s, want completed still", last.Rollout.State)
	}
}

func TestASecondRolloutSupersedesTheFirst(t *testing.T) {
	w := startWorld(t, testConfig(t))
	w.stableWeb()
	withdrawn := make(chan struct{}, 1)
	z := w.newFake(10, "web", "web-a", running(1))
	z.onPrepare = func(p wire.Prepare, s node.Stream) error {
		if p.Generation != 2 {
			return nil
		}
		// Generation 2 never gets prepared: this waits until draal
		// closes the stream.
		io.Copy(io.Discard, s)
		withdrawn <- struct{}{}
		return errors.New("withdrawn")
	}
	z.connect()

	first := w.rollout(newWeb(1))
	eventually(t, "the prepare of the first rollout", func() bool { return len(z.sent().prepares) == 1 })
	second := w.rollout(newWeb(1))
	wait(t, "draal to close the stream of the prepare that hangs", withdrawn)

	w.awaitRollout(second, wire.RolloutCompleted)
	w.awaitRollout(first, wire.RolloutSuperseded)
	got := z.sent()
	if got.prepares[0].Generation != first.Generation {
		t.Errorf("the first prepare was for generation %d, want %d", got.prepares[0].Generation, first.Generation)
	}
	for _, p := range got.prepares[1:] {
		if p.Generation != second.Generation {
			t.Errorf("after the second rollout a prepare for generation %d was sent, want %d", p.Generation, second.Generation)
		}
	}
	if len(got.activates) != 1 || got.activates[0].Generation != second.Generation {
		t.Errorf("activates = %+v, want one for generation %d", got.activates, second.Generation)
	}
	if closed(z.conn) {
		t.Error("the connection of the zathras was closed with the prepare")
	}
}

func TestNothingIsSentBeforeSettle(t *testing.T) {
	cfg := testConfig(t)
	// A stable generation, recorded by a draal that does not serve.
	before := New(cfg)
	info, err := before.Rollout(context.Background(), operatorID, webRollout(1))
	if err != nil {
		t.Fatalf("Rollout: %v", err)
	}
	if ok, err := cfg.DB.SetRolloutState(context.Background(), info.ID, wire.RolloutCompleted, "", time.Now()); err != nil || !ok {
		t.Fatalf("SetRolloutState: %v, %v", ok, err)
	}

	cfg.Settle = 300 * time.Millisecond
	start := time.Now()
	w := startWorld(t, cfg)
	z := w.zathras(10, "web", "web-new", idle())
	eventually(t, "a prepare", func() bool { return len(z.sent().prepares) > 0 })
	if after := z.sent().prepareAt[0].Sub(start); after < cfg.Settle {
		t.Errorf("a command was sent %v after the start, before Settle (%v) had passed", after, cfg.Settle)
	}
}

func TestANewDraalCarriesARolloutOn(t *testing.T) {
	cfg := testConfig(t)
	cfg.StableAfter = time.Hour // nobody is done while the first draal runs
	w := startWorld(t, cfg)
	w.stableWeb()
	a := w.zathras(10, "web", "web-a", running(1))
	b := w.zathras(11, "web", "web-b", running(1))

	info := w.rollout(newWeb(1))
	w.awaitRollout(info, wire.RolloutActivating)
	eventually(t, "the first activate", func() bool {
		return len(a.sent().activates)+len(b.sent().activates) == 1
	})
	eventually(t, "the reports that list the generation as prepared", func() bool {
		for _, z := range []*fake{a, b} {
			if got, _ := w.member("web", z.id); !slices.Contains(got.State.Prepared, info.Generation) {
				return false
			}
		}
		return true
	})
	prepares := len(a.sent().prepares) + len(b.sent().prepares)

	// draal ends, and another begins on the same database.
	w.stop()
	eventually(t, "the connections to end with draal", func() bool { return closed(a.conn) && closed(b.conn) })
	if n := len(a.sent().activates) + len(b.sent().activates); n != 1 {
		t.Fatalf("%d activates were sent while the first is under way, want 1", n)
	}
	cfg.StableAfter = 50 * time.Millisecond
	w.serve(cfg)
	if svc, _ := w.service("web"); svc.Rollout == nil || svc.Rollout.ID != info.ID || svc.Rollout.State != wire.RolloutActivating {
		t.Fatalf("the new draal has the rollout %+v, want it activating", svc.Rollout)
	}
	a.connect()
	b.connect()

	w.awaitRollout(info, wire.RolloutCompleted)
	for _, z := range []*fake{a, b} {
		if got := z.sent(); len(got.activates) != 1 || got.activates[0].Generation != info.Generation {
			t.Errorf("%s got the activates %+v, want one for generation %d", z.name, got.activates, info.Generation)
		}
	}
	if n := len(a.sent().prepares) + len(b.sent().prepares); n != prepares {
		t.Errorf("the new draal sent %d prepares to zathrases that had prepared", n-prepares)
	}
	if svc, _ := w.service("web"); svc.Stable != info.Generation {
		t.Errorf("the stable generation is %d, want %d", svc.Stable, info.Generation)
	}
}
