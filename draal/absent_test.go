package draal

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/rollout"
	"github.com/fables-for-robots/great-machine/wire"
)

// absentConfig returns the Config of the tests of who left during a
// rollout: a zathras is done after 100 milliseconds, and one that left is
// waited for for grace.
func absentConfig(t *testing.T, grace time.Duration) Config {
	cfg := testConfig(t)
	cfg.StableAfter = 100 * time.Millisecond
	cfg.AbsentGrace = grace
	return cfg
}

// rolloutNow returns a rollout as it is now.
func (w *world) rolloutNow(info wire.RolloutInfo) wire.RolloutInfo {
	w.t.Helper()
	list, err := w.d.Rollouts(context.Background(), info.Service)
	if err != nil {
		w.t.Fatalf("Rollouts: %v", err)
	}
	for _, r := range list {
		if r.ID == info.ID {
			return r
		}
	}
	w.t.Fatalf("rollout %d is not listed", info.ID)
	return wire.RolloutInfo{}
}

// stays fails the test unless the rollout is in the state from now until
// the time given.
func (w *world) stays(info wire.RolloutInfo, state wire.RolloutState, until time.Time) {
	w.t.Helper()
	for time.Now().Before(until) {
		if got := w.rolloutNow(info); got.State != state {
			w.t.Fatalf("rollout %d is %s (%s) %v before the time it is to stay %s",
				info.ID, got.State, got.Reason, time.Until(until), state)
		}
		time.Sleep(time.Millisecond)
	}
}

// close ends the connection of the fake, from its side.
func (f *fake) close() {
	f.mu.Lock()
	conn := f.conn
	f.mu.Unlock()
	conn.Close()
}

// gone waits until draal has let the fake go.
func (f *fake) gone() {
	f.t.Helper()
	eventually(f.t, f.name+" to leave the fleet", func() bool {
		_, ok := f.w.member(f.service, f.id)
		return !ok
	})
}

// leave ends the connection of the fake and waits until draal has let it
// go.
func (f *fake) leave() {
	f.t.Helper()
	f.close()
	f.gone()
}

// quiet stops the reports of the fake, or starts them again.
func (f *fake) quiet(silent bool) {
	f.mu.Lock()
	f.silent = silent
	f.mu.Unlock()
}

// doneOn says whether the fleet shows the fake done on the generation.
func (f *fake) doneOn(generation uint64) bool {
	m, ok := f.w.member(f.service, f.id)
	return ok && rollout.Done(m.State, generation, f.w.cfg.StableAfter)
}

// runs says whether the fleet shows the fake on the generation.
func (f *fake) runs(generation uint64) bool {
	m, ok := f.w.member(f.service, f.id)
	return ok && m.State.Generation == generation
}

// gate is a channel a hook of a fake waits on, and the function that
// opens it, once; the test opens it at its end at the latest.
func gate(t *testing.T) (<-chan struct{}, func()) {
	ch := make(chan struct{})
	open := sync.OnceFunc(func() { close(ch) })
	t.Cleanup(open)
	return ch, open
}

// oneActivate fails the test unless each fake was sent one activate, for
// the generation.
func oneActivate(t *testing.T, generation uint64, fakes ...*fake) {
	t.Helper()
	for _, z := range fakes {
		if got := z.sent().activates; len(got) != 1 || got[0].Generation != generation {
			t.Errorf("%s was sent the activates %+v, want one for generation %d", z.name, got, generation)
		}
	}
}

// a. One leaves before it is done and is back within the grace: the
// rollout waits for it, and it takes its place again.
func TestAZathrasThatLeftIsWaitedForUntilItIsBack(t *testing.T) {
	w := startWorld(t, absentConfig(t, time.Hour))
	w.stableWeb()
	a := w.zathras(10, "web", "web-a", running(1))
	b := w.zathras(11, "web", "web-b", running(1))

	info := w.rollout(newWeb(1))
	eventually(t, "the activate of web-a", func() bool { return len(a.sent().activates) == 1 })
	if n := len(b.sent().activates); n != 0 {
		t.Fatalf("web-b was sent %d activates with web-a under way and parallel 1", n)
	}
	b.leave()
	eventually(t, "web-a to be done", func() bool { return a.doneOn(info.Generation) })
	w.stays(info, wire.RolloutActivating, time.Now().Add(5*w.cfg.StableAfter))

	// It comes back as it went, on generation 1.
	b.connect()
	w.awaitRollout(info, wire.RolloutCompleted)
	oneActivate(t, info.Generation, a, b)
	for _, z := range []*fake{a, b} {
		if !z.runs(info.Generation) {
			t.Errorf("%s does not run generation %d", z.name, info.Generation)
		}
	}
}

// b. One leaves before it is done and never comes back: the rollout
// completes when the grace has passed, and not before. Nothing else
// happens meanwhile, so the end of the grace is what wakes draal.
func TestAZathrasThatStaysAwayIsWaitedForForTheGrace(t *testing.T) {
	const grace = 600 * time.Millisecond
	w := startWorld(t, absentConfig(t, grace))
	w.stableWeb()
	a := w.zathras(10, "web", "web-a", running(1))
	b := w.zathras(11, "web", "web-b", running(1))

	info := w.rollout(newWeb(1))
	eventually(t, "the activate of web-a", func() bool { return len(a.sent().activates) == 1 })
	left := time.Now()
	b.leave()
	eventually(t, "web-a to be done", func() bool { return a.doneOn(info.Generation) })
	a.quiet(true)

	w.stays(info, wire.RolloutActivating, left.Add(grace-100*time.Millisecond))
	got := w.awaitRollout(info, wire.RolloutCompleted)
	if after := got.Finished.Sub(left); after < grace {
		t.Errorf("the rollout completed %v after web-b left, before the grace (%v) had passed", after, grace)
	} else if after > grace+2*time.Second {
		t.Errorf("the rollout completed %v after web-b left, long after the grace (%v)", after, grace)
	}
	oneActivate(t, info.Generation, a)
	if n := len(b.sent().activates); n != 0 {
		t.Errorf("web-b was sent %d activates", n)
	}
}

// c. Every zathras leaves in the middle of a rollout, and they come back:
// the rollout goes on at its pace, nobody is sent activate twice, and it
// completes.
func TestAllLeaveAndComeBack(t *testing.T) {
	w := startWorld(t, absentConfig(t, time.Hour))
	w.stableWeb()
	// web-a is the first to be moved, and its activate waits for the test:
	// until then nobody else is.
	held, release := gate(t)
	a := w.newFake(10, "web", "web-a", running(1))
	a.onActivate = func(wire.Activate) error { <-held; return nil }
	a.connect()
	fakes := []*fake{a}
	for i, name := range []string{"web-b", "web-c", "web-d"} {
		fakes = append(fakes, w.zathras(11+i, "web", name, running(1)))
	}

	info := w.rollout(newWeb(1))
	eventually(t, "the activate of web-a", func() bool { return len(a.sent().activates) == 1 })
	for _, z := range fakes[1:] {
		z.close()
	}
	for _, z := range fakes[1:] {
		z.gone()
	}
	release()
	eventually(t, "web-a to report generation 2", func() bool { return a.runs(info.Generation) })
	a.leave()
	w.stays(info, wire.RolloutActivating, time.Now().Add(3*w.cfg.StableAfter))

	for _, z := range fakes {
		z.connect()
	}
	w.awaitRollout(info, wire.RolloutCompleted)
	oneActivate(t, info.Generation, fakes...)
	var at []time.Time
	for _, z := range fakes {
		if !z.runs(info.Generation) {
			t.Errorf("%s does not run generation %d", z.name, info.Generation)
		}
		at = append(at, z.sent().activatedAt...)
	}
	// With parallel 1 each is activated when the one before has run for
	// StableAfter.
	slices.SortFunc(at, time.Time.Compare)
	for i := 1; i < len(at); i++ {
		if gap := at[i].Sub(at[i-1]); gap < w.cfg.StableAfter {
			t.Errorf("activates %d and %d were sent %v apart, less than StableAfter (%v): more than one at once", i-1, i, gap, w.cfg.StableAfter)
		}
	}
}

// d. One leaves during a rollout, and a second rollout supersedes it while
// it is away: the second is held by it as the first was.
func TestWhoLeftHoldsTheRolloutThatSupersedes(t *testing.T) {
	w := startWorld(t, absentConfig(t, time.Hour))
	w.stableWeb()
	a := w.zathras(10, "web", "web-a", running(1))
	b := w.zathras(11, "web", "web-b", running(1))

	first := w.rollout(newWeb(1))
	b.leave()
	second := w.rollout(newWeb(1))
	eventually(t, "web-a to be done on the second rollout", func() bool { return a.doneOn(second.Generation) })
	w.stays(second, wire.RolloutActivating, time.Now().Add(5*w.cfg.StableAfter))

	b.connect()
	w.awaitRollout(second, wire.RolloutCompleted)
	w.awaitRollout(first, wire.RolloutSuperseded)
	if !b.runs(second.Generation) {
		t.Errorf("web-b does not run generation %d", second.Generation)
	}
}

// e. One that leaves while no rollout is under way is not waited for by a
// rollout that is made afterwards.
func TestWhoLeftBeforeARolloutIsNotWaitedFor(t *testing.T) {
	w := startWorld(t, absentConfig(t, time.Hour))
	w.stableWeb()
	a := w.zathras(10, "web", "web-a", running(1))
	b := w.zathras(11, "web", "web-b", running(1))
	b.leave()

	info := w.awaitRollout(w.rollout(newWeb(1)), wire.RolloutCompleted)
	oneActivate(t, info.Generation, a)
}

// f. One that was done on the target when it left does not hold the
// rollout.
func TestWhoLeftDoneIsNotWaitedFor(t *testing.T) {
	w := startWorld(t, absentConfig(t, time.Hour))
	w.stableWeb()
	// Both are moved at once; the activate of web-a waits for the test.
	held, release := gate(t)
	a := w.newFake(10, "web", "web-a", running(1))
	a.onActivate = func(wire.Activate) error { <-held; return nil }
	a.connect()
	b := w.zathras(11, "web", "web-b", running(1))

	info := w.rollout(newWeb(2))
	eventually(t, "web-b to be done", func() bool { return b.doneOn(info.Generation) })
	b.leave()
	release()
	w.awaitRollout(info, wire.RolloutCompleted)
	oneActivate(t, info.Generation, a, b)
}

// g. A zathras answers activate at once, and its reports show the new
// generation only later: meanwhile it is not sent activate again, and it
// counts against parallel.
func TestAnActivateCountsUntilTheReportShowsIt(t *testing.T) {
	w := startWorld(t, absentConfig(t, time.Hour))
	w.stableWeb()
	a := w.newFake(10, "web", "web-a", running(1))
	// The state the activate leads to is not reported until the test says.
	a.afterActivate = func(*wire.State) { a.silent = true }
	a.connect()
	b := w.zathras(11, "web", "web-b", running(1))

	info := w.rollout(newWeb(1))
	eventually(t, "the activate of web-a", func() bool { return len(a.sent().activates) == 1 })
	end := time.Now().Add(5 * w.cfg.StableAfter)
	for time.Now().Before(end) {
		if n := len(a.sent().activates); n != 1 {
			t.Fatalf("web-a was sent %d activates before its report showed the generation", n)
		}
		if n := len(b.sent().activates); n != 0 {
			t.Fatalf("web-b was sent activate while web-a, with parallel 1, was not seen to run the generation")
		}
		time.Sleep(time.Millisecond)
	}

	a.mu.Lock()
	a.silent = false
	a.started = time.Now()
	shown := a.started
	a.mu.Unlock()
	a.report()
	w.awaitRollout(info, wire.RolloutCompleted)
	oneActivate(t, info.Generation, a, b)
	if got := b.sent().activatedAt; len(got) > 0 && got[0].Sub(shown) < w.cfg.StableAfter {
		gap := got[0].Sub(shown)
		t.Errorf("web-b was activated %v after web-a reported the generation, sooner than StableAfter (%v)", gap, w.cfg.StableAfter)
	}
}

// h. The defaults are those of the spec.
func TestTheDefaultsOfWaiting(t *testing.T) {
	cfg := Config{}.withDefaults()
	if cfg.Settle != 45*time.Second {
		t.Errorf("Settle = %v, want 45s", cfg.Settle)
	}
	if cfg.AbsentGrace != 2*time.Minute {
		t.Errorf("AbsentGrace = %v, want 2m", cfg.AbsentGrace)
	}
}

// during tells which rollout a zathras left, from the rollouts of its
// service.
func TestDuring(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	ro := func(id, generation uint64, state wire.RolloutState, created int) wire.RolloutInfo {
		return wire.RolloutInfo{ID: id, Service: "web", Generation: generation, State: state, Created: t0.Add(time.Duration(created) * time.Second)}
	}
	current := ro(99, 5, wire.RolloutActivating, 10)
	// As many as draal reads, all made after the zathras left.
	many := []wire.RolloutInfo{current}
	for len(many) < rolloutsListed {
		n := uint64(len(many))
		many = append(many, ro(current.ID-n, current.Generation-1, wire.RolloutSuperseded, 2))
	}
	cases := []struct {
		name     string
		at       int // seconds after t0
		rollouts []wire.RolloutInfo
		target   uint64
		held     bool
	}{
		{"after current was made", 10, nil, 5, true},
		{"long after current was made", 99, nil, 5, true},
		{"during a rollout current superseded", 7, []wire.RolloutInfo{current, ro(8, 4, wire.RolloutSuperseded, 6), ro(7, 3, wire.RolloutCompleted, 1)}, 4, true},
		{"during the first of two that were superseded", 5, []wire.RolloutInfo{current, ro(8, 4, wire.RolloutSuperseded, 6), ro(7, 3, wire.RolloutSuperseded, 1)}, 3, true},
		{"after a rollout completed", 7, []wire.RolloutInfo{current, ro(7, 3, wire.RolloutCompleted, 1)}, 0, false},
		{"after a rollout halted", 7, []wire.RolloutInfo{current, ro(7, 3, wire.RolloutHalted, 1)}, 0, false},
		{"before the service had a rollout", 7, []wire.RolloutInfo{current}, 0, false},
		{"before every rollout that is read", 1, many, 5, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, held := during(&current, tc.rollouts, t0.Add(time.Duration(tc.at)*time.Second))
			if target != tc.target || held != tc.held {
				t.Errorf("during = %d, %v; want %d, %v", target, held, tc.target, tc.held)
			}
		})
	}
}
