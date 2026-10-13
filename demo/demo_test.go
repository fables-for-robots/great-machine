package demo

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/wire"
)

// deadline is how long a test waits for something that is to happen.
const deadline = 30 * time.Second

// fleetAt moves the fleet to a time and returns it.
func fleetAt(d *Demo, at time.Time) wire.Fleet {
	d.advance(at)
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fleet(at)
}

// serviceOf returns a service of a fleet.
func serviceOf(t *testing.T, f wire.Fleet, name string) wire.Service {
	t.Helper()
	for _, svc := range f.Services {
		if svc.Name == name {
			return svc
		}
	}
	t.Fatalf("the fleet has no service %s", name)
	return wire.Service{}
}

// The demo is what the page is developed against: this is what it has
// to show for that, at its start and in the minutes after.
func TestFleetHasWhatThePageIsToShow(t *testing.T) {
	start := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	d := newDemo(start, false)
	f := fleetAt(d, start)

	if len(f.Services) != 4 || f.Services[0].Name != "empty" || f.Services[1].Name != "fresh" || f.Services[2].Name != "web" || f.Services[3].Name != "worker" {
		t.Fatalf("the services are %+v, want empty, fresh, web and worker", f.Services)
	}
	empty, fresh, web, worker := f.Services[0], f.Services[1], f.Services[2], f.Services[3]
	// fresh is the state before the first rollout, in the JSON draal
	// sends for it: no rollout at all.
	if fresh.Stable != 0 || fresh.Rollout != nil || len(fresh.Zathrases) != 2 {
		t.Errorf("fresh = %+v, want no stable generation, no rollout and two zathrases", fresh)
	}
	for _, z := range fresh.Zathrases {
		if s := z.State; s.Phase != wire.PhaseIdle || s.Generation != 0 || s.Binary != "" || s.Args != nil || s.LastExit != nil || s.Restarts != 0 || s.Prepared != nil {
			t.Errorf("%s of fresh has the state %+v, want it idle and without a generation", z.Name, s)
		}
		lines, err := d.Logs(t.Context(), z.ID, false)
		if err != nil {
			t.Fatalf("Logs of %s: %v", z.Name, err)
		}
		for l := range lines {
			t.Errorf("the log of %s has the line %+v, want none before the first rollout", z.Name, l)
		}
	}
	if data, _ := json.Marshal(fresh); !strings.HasPrefix(string(data), `{"name":"fresh","stable":0,"zathrases":[{"id":"`) {
		t.Errorf("fresh is sent as %s, want it without the key rollout", data)
	}
	if list, err := d.Rollouts(t.Context(), "fresh"); err != nil || len(list) != 0 {
		t.Errorf("Rollouts of fresh = %v, %v; want none", list, err)
	}
	if empty.Stable == 0 || empty.Rollout == nil || empty.Zathrases == nil || len(empty.Zathrases) != 0 {
		t.Errorf("empty = %+v, want a stable generation, a rollout and no zathras", empty)
	}
	if r := worker.Rollout; r == nil || r.State != wire.RolloutHalted || !strings.Contains(r.Reason, "worker-1") || !strings.Contains(r.Reason, "status 2") {
		t.Errorf("the rollout of worker is %+v, want it halted with a zathras and an exit status in the reason", r)
	}
	if len(web.Zathrases) != 5 || web.Rollout == nil || web.Rollout.State != wire.RolloutPreparing {
		t.Errorf("web has %d zathrases and the rollout %+v, want five and one that is preparing", len(web.Zathrases), web.Rollout)
	}

	phases, generations := map[wire.Phase]bool{}, map[uint64]bool{}
	var bare, longest, pair, restarted, exited bool
	for _, svc := range f.Services {
		for i, z := range svc.Zathrases {
			if len(z.ID) != 64 || z.Connected.IsZero() {
				t.Errorf("%s has the ID %q and connected at %v", z.Name, z.ID, z.Connected)
			}
			phases[z.State.Phase] = true
			bare = bare || z.State.Sample == nil && z.State.Phase == wire.PhaseRunning
			longest = longest || len(z.Name) == longestName
			pair = pair || i > 0 && svc.Zathrases[i-1].Name == z.Name && svc.Zathrases[i-1].ID < z.ID
			restarted = restarted || z.State.Restarts > 0
			exited = exited || z.State.LastExit != nil
			if svc.Name == "worker" && z.State.Generation != 0 {
				generations[z.State.Generation] = true
			}
			if z.State.Phase == wire.PhaseRunning && (z.State.PID == 0 || z.State.RunningFor <= 0 || !strings.HasPrefix(z.State.Binary, "/nix/store/") || len(z.State.Args) == 0) {
				t.Errorf("%s runs with the state %+v", z.Name, z.State)
			}
			if z.State.Phase == wire.PhaseBackoff && z.State.RestartAt.IsZero() {
				t.Errorf("%s is in backoff without a time of its restart", z.Name)
			}
		}
	}
	for _, phase := range []wire.Phase{wire.PhaseIdle, wire.PhaseRunning, wire.PhaseStopping, wire.PhaseBackoff} {
		if !phases[phase] {
			t.Errorf("no zathras is %s at the start", phase)
		}
	}
	if !bare || !longest || !pair || !restarted || !exited || len(generations) != 2 {
		t.Errorf("without samples: %v; a name of %d characters: %v; two of one name: %v; restarts: %v; a last exit: %v; generations of worker: %v",
			bare, longestName, longest, pair, restarted, exited, generations)
	}

	// Twenty rollouts of web, newest first.
	rollouts, err := d.Rollouts(t.Context(), "web")
	if err != nil || len(rollouts) != 20 {
		t.Fatalf("Rollouts = %d of them, %v; want 20", len(rollouts), err)
	}
	states := map[wire.RolloutState]bool{}
	for i, r := range rollouts {
		states[r.State] = true
		if i > 0 && (r.ID >= rollouts[i-1].ID || r.Created.After(rollouts[i-1].Created)) {
			t.Errorf("rollout %d comes behind %d: not newest first", r.ID, rollouts[i-1].ID)
		}
		if r.State.UnderWay() != r.Finished.IsZero() || (r.State == wire.RolloutHalted) != (r.Reason != "") {
			t.Errorf("rollout %+v: its end or its reason does not go with its state", r)
		}
	}

	// Fifteen minutes of samples, two seconds apart, oldest first.
	samples, err := d.History(t.Context(), web.Zathrases[0].ID)
	if err != nil || len(samples) != int(history/sampleEvery) {
		t.Fatalf("History = %d samples, %v; want %d", len(samples), err, history/sampleEvery)
	}
	for i, s := range samples[1:] {
		if s.At.Sub(samples[i].At) != sampleEvery || s.CPU <= 0 || s.Memory == 0 {
			t.Fatalf("sample %d is %+v, behind %+v", i+1, s, samples[i])
		}
	}

	// A ring that is full, with every kind of line.
	lines, err := d.Logs(t.Context(), web.Zathrases[0].ID, false)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	streams, count := map[string]bool{}, 0
	var long, tabs, colours, wide bool
	for l := range lines {
		count++
		streams[l.Stream] = true
		long = long || len(l.Text) == 16<<10
		tabs = tabs || strings.Contains(l.Text, "\t")
		colours = colours || strings.Contains(l.Text, "\x1b[")
		wide = wide || strings.Contains(l.Text, "注文")
		if strings.Contains(l.Text, "\n") {
			t.Errorf("the line %q has a line feed in it", l.Text)
		}
	}
	if count != 400 || !long || !tabs || !colours || !wide || !streams["stdout"] || !streams["stderr"] {
		t.Errorf("%d lines; one of 16 KiB: %v; tabs: %v; colours: %v; wide characters: %v; streams: %v", count, long, tabs, colours, wide, streams)
	}

	// Four minutes, a twentieth of a second at a time.
	goner := d.services["web"].zathrases[4].id
	var quiet time.Duration // how long no rollout of web has been under way
	var gone, back, backoff bool
	for at := start; at.Before(start.Add(4 * time.Minute)); at = at.Add(50 * time.Millisecond) {
		f := fleetAt(d, at)
		if _, err := json.Marshal(f); err != nil {
			t.Fatalf("the fleet at %s: %v", at.Sub(start), err)
		}
		web := serviceOf(t, f, "web")
		states[web.Rollout.State] = true
		if web.Rollout.State.UnderWay() {
			quiet = 0
		} else if quiet += 50 * time.Millisecond; quiet > time.Minute {
			t.Fatalf("at %s no rollout of web has been under way for a minute", at.Sub(start))
		}
		// The counts are those of the last decision about the rollout:
		// of the five there were, also when one has gone since.
		if r := web.Rollout; r.Prepared > 5 || r.Activated > r.Prepared || r.Awaited > r.Parallel {
			t.Fatalf("at %s the rollout counts %d prepared, %d activated and %d awaited", at.Sub(start), r.Prepared, r.Activated, r.Awaited)
		}
		here := slices.ContainsFunc(web.Zathrases, func(z wire.Zathras) bool { return z.ID == goner })
		gone = gone || !here
		back = back || gone && here
		if !here {
			if _, err := d.History(t.Context(), goner); wire.CodeOf(err) != wire.CodeUnavailable {
				t.Fatalf("History of the zathras that has gone = %v, want unavailable", err)
			}
		}
		for _, z := range web.Zathrases {
			backoff = backoff || z.State.Phase == wire.PhaseBackoff
		}
		if !slices.IsSortedFunc(web.Zathrases, func(a, b wire.Zathras) int {
			return strings.Compare(a.Name+" "+string(a.ID), b.Name+" "+string(b.ID))
		}) {
			t.Fatalf("at %s the zathrases of web are not by name and then by ID", at.Sub(start))
		}
	}
	for _, state := range []wire.RolloutState{wire.RolloutPreparing, wire.RolloutActivating, wire.RolloutCompleted, wire.RolloutHalted, wire.RolloutSuperseded} {
		if !states[state] {
			t.Errorf("no rollout of web was %s", state)
		}
	}
	if !gone || !back || !backoff {
		t.Errorf("a zathras of web went: %v, and came back: %v; one was in backoff: %v", gone, back, backoff)
	}
	if svc := serviceOf(t, fleetAt(d, start.Add(4*time.Minute)), "web"); svc.Stable < web.Stable+3 {
		t.Errorf("the stable generation of web is %d after four minutes, and was %d", svc.Stable, web.Stable)
	}
}

// With --empty there is no service, and the fleet says so as draal's
// does: with a list that is empty, not with null.
func TestEmptyFleetHasNoService(t *testing.T) {
	start := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	d := newDemo(start, true)
	for _, at := range []time.Time{start, start.Add(5 * time.Minute)} {
		data, err := json.Marshal(fleetAt(d, at))
		if err != nil || string(data) != `{"services":[]}` {
			t.Errorf("the fleet is %s (%v), want {\"services\":[]}", data, err)
		}
	}
	if list, err := d.Rollouts(t.Context(), "web"); err != nil || len(list) != 0 {
		t.Errorf("Rollouts = %v, %v; want none", list, err)
	}
	fleets, err := d.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if f := <-fleets; f.Services == nil || len(f.Services) != 0 {
		t.Errorf("the first fleet of a watcher is %+v, want an empty list of services", f)
	}
}

// A zathras that goes ends the log that is followed and the shell that
// is open, as draal's do: the channel is closed, and Read answers
// unavailable.
func TestZathrasThatGoesEndsItsLogAndItsShell(t *testing.T) {
	start := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	d := newDemo(start, false)
	z := d.services["web"].zathrases[4]
	lines, err := d.Logs(t.Context(), z.id, true)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	sh, err := d.Shell(t.Context(), z.id, wire.ShellRequest{Cols: 80, Rows: 24, Term: "dumb"})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	defer sh.Close()
	for range 400 {
		<-lines
	}

	d.mu.Lock()
	d.leave(z)
	d.mu.Unlock()
	timeout := time.After(deadline)
	for open := true; open; {
		select {
		case _, open = <-lines:
		case <-timeout:
			t.Fatal("timed out waiting for the log to end")
		}
	}
	if _, err := io.ReadAll(sh); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("the shell ended with %v, want unavailable", err)
	}
	if _, err := d.Logs(t.Context(), z.id, false); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("Logs of a zathras that has gone = %v, want unavailable", err)
	}
	if _, err := d.Shell(t.Context(), z.id, wire.ShellRequest{}); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("Shell of a zathras that has gone = %v, want unavailable", err)
	}
}

// New and Run are what the two demos use: the fleet of now, which a
// watcher is sent again once Run moves it, and Run ends with its context.
func TestNewFleetMovesWhileRunRuns(t *testing.T) {
	var c draal.Controller = New(false)
	fleets, err := c.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if f := <-fleets; len(f.Services) != 4 {
		t.Fatalf("the first fleet has %d services, want four", len(f.Services))
	}
	if f, _ := New(true).Watch(t.Context()); len((<-f).Services) != 0 {
		t.Error("New(true) has a service, want none")
	}

	ctx, stop := context.WithCancel(t.Context())
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		c.(*Demo).Run(ctx)
	}()
	select {
	case f, ok := <-fleets:
		if !ok || len(f.Services) != 4 {
			t.Errorf("the second fleet is %+v (open: %v), want four services", f, ok)
		}
	case <-time.After(deadline):
		t.Error("timed out waiting for the fleet to move")
	}
	stop()
	select {
	case <-ended:
	case <-time.After(deadline):
		t.Fatal("timed out waiting for Run to end")
	}
}

// The demo has grants to show, and grants and revokes as draal does.
func TestTheDemoHasGrants(t *testing.T) {
	d := newDemo(time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC), false)
	ctx := t.Context()
	me, err := d.Me(ctx)
	if err != nil || me.ID != operator || len(me.Grants) != 1 || me.Grants[0].Role != wire.RoleAdmin || !me.Grants[0].Fixed {
		t.Fatalf("Me = %+v, %v; want the admin of the command line", me, err)
	}
	before, err := d.Grants(ctx)
	if err != nil || len(before) < 4 {
		t.Fatalf("Grants = %d of them, %v", len(before), err)
	}
	roles := map[wire.Role]bool{}
	for _, g := range before {
		roles[g.Role] = true
	}
	if !roles[wire.RoleAdmin] || !roles[wire.RoleDeployer] || !roles[wire.RoleZathras] {
		t.Errorf("the demo has the roles %v, want all three", roles)
	}

	key := idOf("a new key")
	req := wire.GrantRequest{ID: key, Role: wire.RoleDeployer, Service: "web", Note: "new"}
	if err := d.Grant(ctx, req); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	req.Note = "renamed"
	if err := d.Grant(ctx, req); err != nil {
		t.Fatalf("Grant again: %v", err)
	}
	after, _ := d.Grants(ctx)
	if len(after) != len(before)+1 || after[len(after)-1].Note != "renamed" {
		t.Errorf("after a grant that was made twice there are %d grants, the last %+v; want one more, with the new note", len(after), after[len(after)-1])
	}
	if err := d.Revoke(ctx, wire.RevokeRequest{ID: key, Role: wire.RoleDeployer, Service: "web"}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := d.Revoke(ctx, wire.RevokeRequest{ID: key, Role: wire.RoleDeployer, Service: "web"}); wire.CodeOf(err) != wire.CodeNotFound {
		t.Errorf("revoking what is not there: %v, want not-found", err)
	}
	if err := d.Revoke(ctx, wire.RevokeRequest{ID: operator, Role: wire.RoleAdmin}); wire.CodeOf(err) != wire.CodeBadRequest {
		t.Errorf("revoking the admin of the command line: %v, want a bad-request", err)
	}
	for _, bad := range []wire.GrantRequest{
		{ID: key, Role: "root"},
		{ID: "short", Role: wire.RoleAdmin},
		{ID: key, Role: wire.RoleAdmin, Service: "web"},
		{ID: key, Role: wire.RoleDeployer, Service: "Not A Service"},
	} {
		if err := d.Grant(ctx, bad); wire.CodeOf(err) != wire.CodeBadRequest {
			t.Errorf("Grant(%+v) = %v, want a bad-request", bad, err)
		}
	}
}
