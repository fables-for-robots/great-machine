package db

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

const (
	alice = wire.ID("a11ce00000000000000000000000000000000000000000000000000000000000")
	bob   = wire.ID("b0b0000000000000000000000000000000000000000000000000000000000000")
)

// request is a rollout of service, with a binary and a closure that tell
// one request from the next.
func request(service string, n int) wire.RolloutRequest {
	return wire.RolloutRequest{
		Service:  service,
		Binary:   fmt.Sprintf("/nix/store/%032d-web/bin/web", n),
		Args:     []string{"--listen", ":8080"},
		Closure:  []string{fmt.Sprintf("%032d-web", n), fmt.Sprintf("%032d-glibc", n)},
		Parallel: 1,
	}
}

func mustRollout(t *testing.T, d *DB, req wire.RolloutRequest, now time.Time) wire.RolloutInfo {
	t.Helper()
	r, err := d.NewRollout(ctx, req, alice, now)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustSet(t *testing.T, d *DB, id uint64, state wire.RolloutState, reason string, now time.Time) {
	t.Helper()
	moved, err := d.SetRolloutState(ctx, id, state, reason, now)
	if err != nil {
		t.Fatal(err)
	}
	if !moved {
		t.Fatalf("rollout %d was not moved to %s", id, state)
	}
}

func mustLast(t *testing.T, d *DB, service string) wire.RolloutInfo {
	t.Helper()
	r, found, err := d.LastRollout(ctx, service)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("%s has no rollout", service)
	}
	return r
}

func stableOf(t *testing.T, d *DB, service string) uint64 {
	t.Helper()
	s, err := d.Service(ctx, service)
	if err != nil {
		t.Fatal(err)
	}
	return s.Stable
}

// complete rolls a new generation of service out to the end.
func complete(t *testing.T, d *DB, service string, n int, now time.Time) wire.RolloutInfo {
	t.Helper()
	r := mustRollout(t, d, request(service, n), now)
	mustSet(t, d, r.ID, wire.RolloutActivating, "", now)
	mustSet(t, d, r.ID, wire.RolloutCompleted, "", now)
	return r
}

func TestNewRolloutOfANewService(t *testing.T) {
	d := open(t)
	if _, err := d.Service(ctx, "web"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Service before the first rollout: %v, want ErrNotFound", err)
	}
	if _, found, err := d.LastRollout(ctx, "web"); err != nil || found {
		t.Fatalf("LastRollout before the first rollout: found %v, %v", found, err)
	}

	req := request("web", 1)
	req.Parallel = 3
	got, err := d.NewRollout(ctx, req, alice, t0)
	if err != nil {
		t.Fatal(err)
	}
	want := wire.RolloutInfo{
		ID:         got.ID,
		Service:    "web",
		Generation: 1,
		Parallel:   3,
		State:      wire.RolloutPreparing,
		Created:    t0,
		CreatedBy:  alice,
	}
	if got != want {
		t.Errorf("NewRollout:\n got %+v\nwant %+v", got, want)
	}
	if got.ID == 0 {
		t.Error("the rollout has no number")
	}
	if !got.Finished.IsZero() {
		t.Errorf("Finished = %v, want the zero time while under way", got.Finished)
	}
	if last := mustLast(t, d, "web"); last != want {
		t.Errorf("LastRollout:\n got %+v\nwant %+v", last, want)
	}

	s, err := d.Service(ctx, "web")
	if err != nil {
		t.Fatal(err)
	}
	if s != (Service{Name: "web", Stable: 0}) {
		t.Errorf("Service = %+v, want web without a stable generation", s)
	}

	g, err := d.Generation(ctx, "web", 1)
	if err != nil {
		t.Fatal(err)
	}
	if g.Service != "web" || g.Number != 1 || g.Binary != req.Binary || g.CreatedBy != alice || !g.Created.Equal(t0) {
		t.Errorf("Generation = %+v", g)
	}
	if !slices.Equal(g.Args, req.Args) {
		t.Errorf("Args = %q, want %q", g.Args, req.Args)
	}
	if !slices.Equal(g.Closure, req.Closure) {
		t.Errorf("Closure = %q, want %q", g.Closure, req.Closure)
	}
}

func TestGenerationKeepsArgumentsExactly(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"spaces, quotes and what is no ASCII",
			[]string{"--motd", `it's a "großer" Tag, 日本 ☃`, "", " lead and trail ", "a\tb\nc", `back\slash`, "[]", `["x"]`},
			[]string{"--motd", `it's a "großer" Tag, 日本 ☃`, "", " lead and trail ", "a\tb\nc", `back\slash`, "[]", `["x"]`}},
		{"one empty argument", []string{""}, []string{""}},
		// No arguments come back as a nil slice, whether nil or an empty
		// slice went in.
		{"none, as nil", nil, nil},
		{"none, as an empty slice", []string{}, nil},
	}
	d := open(t)
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := request("web", i)
			req.Args = tt.args
			r := mustRollout(t, d, req, t0)
			g, err := d.Generation(ctx, "web", r.Generation)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(g.Args, tt.want) {
				t.Errorf("Args = %q, want %q", g.Args, tt.want)
			}
			if len(tt.want) == 0 && g.Args != nil {
				t.Errorf("Args = %#v, want nil for no arguments", g.Args)
			}
			if !slices.Equal(g.Closure, req.Closure) {
				t.Errorf("Closure = %q, want %q", g.Closure, req.Closure)
			}
		})
	}
}

func TestGenerationNotFound(t *testing.T) {
	d := open(t)
	if _, err := d.Generation(ctx, "web", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("of a service that is not there: %v, want ErrNotFound", err)
	}
	mustRollout(t, d, request("web", 1), t0)
	if _, err := d.Generation(ctx, "web", 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("a number that is not there: %v, want ErrNotFound", err)
	}
	if _, err := d.Generation(ctx, "web", 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("generation 0: %v, want ErrNotFound", err)
	}
}

func TestSecondRolloutSupersedesTheFirst(t *testing.T) {
	d := open(t)
	first := mustRollout(t, d, request("web", 1), t0)
	second, err := d.NewRollout(ctx, request("web", 2), bob, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation != 2 || second.State != wire.RolloutPreparing || second.CreatedBy != bob {
		t.Errorf("second = %+v, want generation 2, preparing, by bob", second)
	}
	if second.ID <= first.ID {
		t.Errorf("rollout numbers %d then %d, want them to grow", first.ID, second.ID)
	}

	all, err := d.Rollouts(ctx, "web", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != second.ID || all[1].ID != first.ID {
		t.Fatalf("Rollouts = %+v, want the second, then the first", all)
	}
	if all[1].State != wire.RolloutSuperseded {
		t.Errorf("the first is %s, want superseded", all[1].State)
	}
	if !all[1].Finished.Equal(t0.Add(time.Minute)) {
		t.Errorf("the first finished at %v, want when it was superseded", all[1].Finished)
	}

	// The generation of the first stays what it was.
	g, err := d.Generation(ctx, "web", 1)
	if err != nil {
		t.Fatal(err)
	}
	if g.Binary != request("web", 1).Binary {
		t.Errorf("generation 1 runs %s", g.Binary)
	}

	// Another service is not touched.
	other := mustRollout(t, d, request("api", 3), t0)
	if other.Generation != 1 {
		t.Errorf("the first generation of api is %d, want 1", other.Generation)
	}
	if got := mustLast(t, d, "web"); got.ID != second.ID || got.State != wire.RolloutPreparing {
		t.Errorf("web's last rollout = %+v, want the second, preparing", got)
	}
}

func TestSetRolloutStateCompletes(t *testing.T) {
	d := open(t)
	r := mustRollout(t, d, request("web", 1), t0)

	mustSet(t, d, r.ID, wire.RolloutActivating, "", t0.Add(time.Second))
	got := mustLast(t, d, "web")
	if got.State != wire.RolloutActivating || !got.Finished.IsZero() {
		t.Errorf("after activating: %+v, want activating and not finished", got)
	}
	if stable := stableOf(t, d, "web"); stable != 0 {
		t.Errorf("stable = %d while activating, want 0", stable)
	}

	done := t0.Add(time.Minute)
	mustSet(t, d, r.ID, wire.RolloutCompleted, "", done)
	got = mustLast(t, d, "web")
	if got.State != wire.RolloutCompleted || !got.Finished.Equal(done) || got.Reason != "" {
		t.Errorf("after completed: %+v, want completed, finished at %v", got, done)
	}
	if stable := stableOf(t, d, "web"); stable != 1 {
		t.Errorf("stable = %d, want 1: the target of the rollout that completed", stable)
	}

	// A rollout that has ended is not moved again.
	moved, err := d.SetRolloutState(ctx, r.ID, wire.RolloutHalted, "too late", done.Add(time.Second))
	if err != nil || moved {
		t.Errorf("halting a completed rollout: moved %v, %v; want false, nil", moved, err)
	}
	if got := mustLast(t, d, "web"); got.State != wire.RolloutCompleted || got.Reason != "" || !got.Finished.Equal(done) {
		t.Errorf("a completed rollout was changed: %+v", got)
	}
}

func TestSetRolloutStateFromPreparingToCompleted(t *testing.T) {
	// A service without a zathras completes in one decision.
	d := open(t)
	r := mustRollout(t, d, request("web", 1), t0)
	mustSet(t, d, r.ID, wire.RolloutCompleted, "", t0)
	if stable := stableOf(t, d, "web"); stable != 1 {
		t.Errorf("stable = %d, want 1", stable)
	}
}

func TestSetRolloutStateOnASupersededRollout(t *testing.T) {
	d := open(t)
	complete(t, d, "web", 1, t0)
	late := mustRollout(t, d, request("web", 2), t0)
	mustSet(t, d, late.ID, wire.RolloutActivating, "", t0)
	current := mustRollout(t, d, request("web", 3), t0.Add(time.Minute))

	// A decision that was made before the rollout was superseded arrives
	// after: it must not complete the rollout, and must not make generation
	// 2 the stable one.
	for _, state := range []wire.RolloutState{wire.RolloutCompleted, wire.RolloutHalted, wire.RolloutActivating} {
		moved, err := d.SetRolloutState(ctx, late.ID, state, "late", t0.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if moved {
			t.Errorf("a superseded rollout was moved to %s", state)
		}
	}
	if stable := stableOf(t, d, "web"); stable != 1 {
		t.Errorf("stable = %d, want 1 untouched", stable)
	}
	all, err := d.Rollouts(ctx, "web", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("%d rollouts, want 3", len(all))
	}
	if all[0].ID != current.ID || all[0].State != wire.RolloutPreparing {
		t.Errorf("the current rollout = %+v, want preparing", all[0])
	}
	if all[1].State != wire.RolloutSuperseded || all[1].Reason != "" || !all[1].Finished.Equal(t0.Add(time.Minute)) {
		t.Errorf("the superseded rollout was changed: %+v", all[1])
	}
}

func TestSetRolloutStateHalts(t *testing.T) {
	d := open(t)
	complete(t, d, "web", 1, t0)
	r := mustRollout(t, d, request("web", 2), t0)
	mustSet(t, d, r.ID, wire.RolloutActivating, "", t0)
	at := t0.Add(time.Minute)
	mustSet(t, d, r.ID, wire.RolloutHalted, "web-1: exited with status 1 after 2.1s", at)

	got := mustLast(t, d, "web")
	if got.State != wire.RolloutHalted || got.Reason != "web-1: exited with status 1 after 2.1s" || !got.Finished.Equal(at) {
		t.Errorf("after halted: %+v", got)
	}
	if stable := stableOf(t, d, "web"); stable != 1 {
		t.Errorf("stable = %d, want 1: a rollout that halted changes nothing", stable)
	}
}

func TestSetRolloutStateRefusesOtherStates(t *testing.T) {
	d := open(t)
	r := mustRollout(t, d, request("web", 1), t0)
	for _, state := range []wire.RolloutState{wire.RolloutPreparing, wire.RolloutSuperseded, "", "done"} {
		if moved, err := d.SetRolloutState(ctx, r.ID, state, "", t0); err == nil || moved {
			t.Errorf("to %q: moved %v, %v; want an error", state, moved, err)
		}
	}
	if got := mustLast(t, d, "web"); got.State != wire.RolloutPreparing {
		t.Errorf("the rollout is %s, want preparing", got.State)
	}
	if moved, err := d.SetRolloutState(ctx, r.ID+100, wire.RolloutCompleted, "", t0); err != nil || moved {
		t.Errorf("a rollout that is not there: moved %v, %v; want false, nil", moved, err)
	}
}

func TestNewRollback(t *testing.T) {
	d := open(t)
	back := wire.RollbackRequest{Service: "web", Parallel: 2}

	if _, err := d.NewRollback(ctx, back, alice, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("of a service that is not there: %v, want ErrNotFound", err)
	}

	first := mustRollout(t, d, request("web", 1), t0)
	if _, err := d.NewRollback(ctx, back, alice, t0); !errors.Is(err, ErrNoStable) {
		t.Errorf("before any rollout completed: %v, want ErrNoStable", err)
	}
	mustSet(t, d, first.ID, wire.RolloutCompleted, "", t0)
	if _, err := d.NewRollback(ctx, back, alice, t0); !errors.Is(err, ErrNothingToRollBack) {
		t.Errorf("after the last rollout completed: %v, want ErrNothingToRollBack", err)
	}
	// What was refused left nothing behind.
	if all, err := d.Rollouts(ctx, "web", 20); err != nil || len(all) != 1 {
		t.Fatalf("%d rollouts after three refusals, %v; want 1", len(all), err)
	}

	// Generation 2 is under way when the operator rolls back.
	second := mustRollout(t, d, request("web", 2), t0.Add(time.Minute))
	mustSet(t, d, second.ID, wire.RolloutActivating, "", t0.Add(time.Minute))
	at := t0.Add(2 * time.Minute)
	got, err := d.NewRollback(ctx, back, bob, at)
	if err != nil {
		t.Fatal(err)
	}
	want := wire.RolloutInfo{
		ID:         got.ID,
		Service:    "web",
		Generation: 1,
		Parallel:   2,
		State:      wire.RolloutPreparing,
		Created:    at,
		CreatedBy:  bob,
	}
	if got != want {
		t.Errorf("NewRollback:\n got %+v\nwant %+v", got, want)
	}
	all, err := d.Rollouts(ctx, "web", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0] != want || all[1].ID != second.ID || all[1].State != wire.RolloutSuperseded {
		t.Errorf("Rollouts = %+v, want the rollback, then the second, superseded", all)
	}
	// A rollback makes no generation.
	if _, err := d.Generation(ctx, "web", 3); !errors.Is(err, ErrNotFound) {
		t.Errorf("generation 3 after a rollback: %v, want ErrNotFound", err)
	}

	// It completes like any rollout, and then there is nothing to go back
	// from again.
	mustSet(t, d, got.ID, wire.RolloutCompleted, "", at)
	if stable := stableOf(t, d, "web"); stable != 1 {
		t.Errorf("stable = %d, want 1", stable)
	}
	if _, err := d.NewRollback(ctx, back, alice, at); !errors.Is(err, ErrNothingToRollBack) {
		t.Errorf("after the rollback completed: %v, want ErrNothingToRollBack", err)
	}

	// After a rollout that halted there is something to go back from, and
	// the next rollout is generation 3: a rollback took no number.
	third := mustRollout(t, d, request("web", 3), at)
	if third.Generation != 3 {
		t.Errorf("the rollout after a rollback is generation %d, want 3", third.Generation)
	}
	mustSet(t, d, third.ID, wire.RolloutHalted, "web-1: no", at)
	again, err := d.NewRollback(ctx, back, alice, at)
	if err != nil {
		t.Fatalf("after a rollout that halted: %v", err)
	}
	if again.Generation != 1 {
		t.Errorf("the rollback targets %d, want 1", again.Generation)
	}
}

func TestRolloutsOrderAndLimit(t *testing.T) {
	d := open(t)
	if all, err := d.Rollouts(ctx, "web", 20); err != nil || len(all) != 0 {
		t.Fatalf("of a service that is not there: %v, %v; want none", all, err)
	}
	var ids []uint64
	for i := 1; i <= 5; i++ {
		ids = append(ids, mustRollout(t, d, request("web", i), t0.Add(time.Duration(i)*time.Second)).ID)
	}
	mustRollout(t, d, request("api", 9), t0)
	slices.Reverse(ids)

	tests := []struct {
		limit int
		want  []uint64
	}{
		{20, ids},
		{5, ids},
		{2, ids[:2]},
		{1, ids[:1]},
		{0, nil},
		{-1, nil},
	}
	for _, tt := range tests {
		all, err := d.Rollouts(ctx, "web", tt.limit)
		if err != nil {
			t.Fatal(err)
		}
		var got []uint64
		for _, r := range all {
			if r.Service != "web" {
				t.Errorf("limit %d: a rollout of %s among web's", tt.limit, r.Service)
			}
			got = append(got, r.ID)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("limit %d: rollouts %v, want %v", tt.limit, got, tt.want)
		}
	}
	if last := mustLast(t, d, "web"); last.ID != ids[0] || last.Generation != 5 {
		t.Errorf("LastRollout = %+v, want generation 5", last)
	}
}

func TestServicesSorted(t *testing.T) {
	d := open(t)
	if all, err := d.Services(ctx); err != nil || len(all) != 0 {
		t.Fatalf("Services of an empty database: %v, %v", all, err)
	}
	for i, name := range []string{"web", "api", "worker-2", "worker-10"} {
		mustRollout(t, d, request(name, i), t0)
	}
	complete(t, d, "api", 7, t0)
	got, err := d.Services(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []Service{{"api", 2}, {"web", 0}, {"worker-10", 0}, {"worker-2", 0}}
	if !slices.Equal(got, want) {
		t.Errorf("Services = %+v, want %+v", got, want)
	}
}

// Rollouts that are asked for at the same time get generations that follow
// one another, and leave exactly one rollout under way.
func TestConcurrentRollouts(t *testing.T) {
	d := open(t)
	const callers = 8
	var wg sync.WaitGroup
	generations := make([]uint64, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := d.NewRollout(ctx, request("web", i), alice, t0)
			generations[i], errs[i] = r.Generation, err
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(generations)
	for i, g := range generations {
		if g != uint64(i+1) {
			t.Fatalf("generations %v, want 1 to %d, each once", generations, callers)
		}
	}

	all, err := d.Rollouts(ctx, "web", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != callers {
		t.Fatalf("%d rollouts, want %d", len(all), callers)
	}
	for i, r := range all {
		want := wire.RolloutSuperseded
		if i == 0 {
			want = wire.RolloutPreparing
		}
		if r.State != want {
			t.Errorf("rollout %d (generation %d) is %s, want %s", r.ID, r.Generation, r.State, want)
		}
	}
}

// A decision for a rollout races with the rollout that supersedes it:
// whichever comes first, the stable generation is never that of a rollout
// that was superseded.
func TestConcurrentSetStateAndSupersede(t *testing.T) {
	for range 20 {
		d := open(t)
		complete(t, d, "web", 1, t0)
		old := mustRollout(t, d, request("web", 2), t0)
		var wg sync.WaitGroup
		var moved bool
		var setErr, newErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			moved, setErr = d.SetRolloutState(ctx, old.ID, wire.RolloutCompleted, "", t0)
		}()
		go func() {
			defer wg.Done()
			_, newErr = d.NewRollout(ctx, request("web", 3), alice, t0)
		}()
		wg.Wait()
		if setErr != nil || newErr != nil {
			t.Fatal(setErr, newErr)
		}
		all, err := d.Rollouts(ctx, "web", 3)
		if err != nil {
			t.Fatal(err)
		}
		wantStable, wantState := uint64(1), wire.RolloutSuperseded
		if moved {
			wantStable, wantState = 2, wire.RolloutCompleted
		}
		if stable := stableOf(t, d, "web"); stable != wantStable {
			t.Errorf("moved %v: stable = %d, want %d", moved, stable, wantStable)
		}
		if all[1].State != wantState {
			t.Errorf("moved %v: the old rollout is %s, want %s", moved, all[1].State, wantState)
		}
		if all[0].State != wire.RolloutPreparing {
			t.Errorf("the new rollout is %s, want preparing", all[0].State)
		}
	}
}
