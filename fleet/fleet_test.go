package fleet

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// t0 is the time the tests begin at.
var t0 = time.Unix(1_800_000_000, 0).UTC()

// id returns an endpoint ID for tests: 64 hexadecimal characters made of n.
func id(n int) wire.ID {
	return wire.ID(fmt.Sprintf("%064x", n))
}

// member is a zathras of a service that connected at t0 and is idle.
func member(n int, service, name string) Member {
	return Member{
		ID:        id(n),
		Service:   service,
		Name:      name,
		Connected: t0,
		State:     wire.State{Phase: wire.PhaseIdle},
		Reported:  t0,
	}
}

// sampled is a state with a sample taken at the time given.
func sampled(at time.Time, cpu float64) wire.State {
	return wire.State{
		Generation: 1,
		Phase:      wire.PhaseRunning,
		Sample:     &wire.Sample{At: at, CPU: cpu},
	}
}

func names(members []Member) []string {
	var out []string
	for _, m := range members {
		out = append(out, m.Name+"/"+string(m.ID[len(m.ID)-2:]))
	}
	return out
}

func cpus(samples []wire.Sample) []float64 {
	var out []float64
	for _, s := range samples {
		out = append(out, s.CPU)
	}
	return out
}

func TestJoinAndGet(t *testing.T) {
	r := New(15 * time.Minute)
	if _, ok := r.Get(id(1)); ok {
		t.Fatal("Get found a member in an empty registry")
	}
	m := member(1, "web", "web-1")
	m.State = wire.State{Generation: 4, Phase: wire.PhaseRunning, Prepared: []uint64{3, 4}}
	r.Join(m)
	got, ok := r.Get(id(1))
	if !ok {
		t.Fatal("Get did not find the member that joined")
	}
	if got.ID != m.ID || got.Service != "web" || got.Name != "web-1" ||
		!got.Connected.Equal(t0) || !got.Reported.Equal(t0) ||
		got.State.Generation != 4 || got.State.Phase != wire.PhaseRunning ||
		!slices.Equal(got.State.Prepared, []uint64{3, 4}) {
		t.Errorf("Get = %+v, want what joined", got)
	}
	if _, ok := r.Get(id(2)); ok {
		t.Error("Get found a member that never joined")
	}
}

func TestJoinWithoutReportedCountsFromConnected(t *testing.T) {
	r := New(15 * time.Minute)
	m := member(1, "web", "web-1")
	m.Reported = time.Time{}
	r.Join(m)
	got, _ := r.Get(id(1))
	if !got.Reported.Equal(t0) {
		t.Errorf("Reported = %v, want the time it connected", got.Reported)
	}
}

func TestServiceOrder(t *testing.T) {
	r := New(15 * time.Minute)
	// Two members have the same name, as pods of a template with a fixed
	// --name have: they are listed apart, by ID.
	r.Join(member(7, "web", "web-b"))
	r.Join(member(9, "web", "same"))
	r.Join(member(3, "web", "web-a"))
	r.Join(member(2, "web", "same"))
	r.Join(member(1, "api", "api-1"))

	got := names(r.Service("web"))
	want := []string{"same/02", "same/09", "web-a/03", "web-b/07"}
	if !slices.Equal(got, want) {
		t.Errorf("Service(web) = %v, want %v", got, want)
	}
	if got := names(r.Service("api")); !slices.Equal(got, []string{"api-1/01"}) {
		t.Errorf("Service(api) = %v", got)
	}
	if got := r.Service("none"); len(got) != 0 {
		t.Errorf("Service(none) = %v, want no members", got)
	}
}

func TestServices(t *testing.T) {
	r := New(15 * time.Minute)
	if got := r.Services(); len(got) != 0 {
		t.Errorf("Services of an empty registry = %v", got)
	}
	r.Join(member(1, "web", "web-1"))
	r.Join(member(2, "api", "api-1"))
	epoch := r.Join(member(3, "worker", "worker-1"))
	r.Join(member(4, "web", "web-2"))
	if got, want := r.Services(), []string{"api", "web", "worker"}; !slices.Equal(got, want) {
		t.Errorf("Services = %v, want %v", got, want)
	}
	// A service whose last member has gone is no longer listed.
	r.Leave(id(3), epoch)
	if got, want := r.Services(), []string{"api", "web"}; !slices.Equal(got, want) {
		t.Errorf("Services after the worker left = %v, want %v", got, want)
	}
}

func TestReportUpdatesStateAndHistory(t *testing.T) {
	r := New(15 * time.Minute)
	epoch := r.Join(member(1, "web", "web-1"))
	if got := r.History(id(1)); len(got) != 0 {
		t.Fatalf("History of a member that joined without a sample = %v", got)
	}

	at := t0.Add(2 * time.Second)
	if !r.Report(id(1), epoch, sampled(at, 0.5), at) {
		t.Fatal("Report returned false for a member that is there")
	}
	got, _ := r.Get(id(1))
	if got.State.Generation != 1 || got.State.Phase != wire.PhaseRunning || !got.Reported.Equal(at) {
		t.Errorf("after the report: %+v", got)
	}
	if !got.Connected.Equal(t0) {
		t.Errorf("Connected = %v, want it unchanged", got.Connected)
	}

	// A report without a sample changes the state and not the history.
	later := at.Add(time.Second)
	r.Report(id(1), epoch, wire.State{Generation: 1, Phase: wire.PhaseBackoff}, later)
	got, _ = r.Get(id(1))
	if got.State.Phase != wire.PhaseBackoff || !got.Reported.Equal(later) {
		t.Errorf("after the second report: %+v", got)
	}

	// A report that carries the sample of the report before, as one that
	// was sent for a change of state does, adds nothing.
	r.Report(id(1), epoch, sampled(at, 0.5), later)
	r.Report(id(1), epoch, sampled(at.Add(2*time.Second), 0.7), later)
	if got, want := cpus(r.History(id(1))), []float64{0.5, 0.7}; !slices.Equal(got, want) {
		t.Errorf("History = %v, want %v", got, want)
	}

	if got := r.History(id(2)); got != nil {
		t.Errorf("History of a member that is not there = %v", got)
	}
}

func TestHistoryIsACopy(t *testing.T) {
	r := New(15 * time.Minute)
	epoch := r.Join(member(1, "web", "web-1"))
	r.Report(id(1), epoch, sampled(t0, 1), t0)
	r.History(id(1))[0].CPU = 99
	if got := r.History(id(1))[0].CPU; got != 1 {
		t.Errorf("a change of what History returned reached the registry: %v", got)
	}
}

func TestJoinKeepsTheSampleOfItsState(t *testing.T) {
	r := New(15 * time.Minute)
	m := member(1, "web", "web-1")
	m.State = sampled(t0, 0.25)
	r.Join(m)
	if got, want := cpus(r.History(id(1))), []float64{0.25}; !slices.Equal(got, want) {
		t.Errorf("History = %v, want %v", got, want)
	}
}

func TestHistoryIsTrimmedByTime(t *testing.T) {
	r := New(10 * time.Second)
	epoch := r.Join(member(1, "web", "web-1"))
	// The samples carry the clock of the zathras, which here is years
	// behind that of draal. What is kept goes by the samples alone.
	zathras := t0.AddDate(-3, 0, 0)
	for i := range 8 {
		at := zathras.Add(time.Duration(2*i) * time.Second)
		r.Report(id(1), epoch, sampled(at, float64(i)), t0.Add(time.Duration(2*i)*time.Second))
	}
	// The newest is at 14s: what is older than 4s is dropped, and the one
	// at 4s is exactly 10 seconds old and stays.
	if got, want := cpus(r.History(id(1))), []float64{2, 3, 4, 5, 6, 7}; !slices.Equal(got, want) {
		t.Errorf("History = %v, want %v", got, want)
	}
	// After a long silence one sample drops all the others.
	r.Report(id(1), epoch, sampled(zathras.Add(time.Hour), 100), t0.Add(time.Hour))
	if got, want := cpus(r.History(id(1))), []float64{100}; !slices.Equal(got, want) {
		t.Errorf("History after an hour = %v, want %v", got, want)
	}
}

func TestJoinReplacesAndStartsANewEpoch(t *testing.T) {
	r := New(15 * time.Minute)
	first := r.Join(member(1, "web", "web-1"))
	r.Report(id(1), first, sampled(t0, 1), t0)

	// The zathras dials again before draal has noticed that the first
	// connection is dead.
	again := member(1, "web", "web-1")
	again.Connected = t0.Add(time.Minute)
	again.Reported = t0.Add(time.Minute)
	again.State = wire.State{Generation: 2, Phase: wire.PhaseRunning}
	second := r.Join(again)
	if second == first {
		t.Fatalf("both memberships have epoch %d", first)
	}
	if got := r.History(id(1)); len(got) != 0 {
		t.Errorf("History after the member was replaced = %v, want none", got)
	}
	if got := r.Service("web"); len(got) != 1 {
		t.Fatalf("%d members after the replacing Join, want 1", len(got))
	}

	// What the old connection still does changes nothing.
	if r.Report(id(1), first, wire.State{Generation: 9, Phase: wire.PhaseBackoff}, t0.Add(time.Hour)) {
		t.Error("Report with the old epoch returned true")
	}
	if r.Leave(id(1), first) {
		t.Error("Leave with the old epoch returned true")
	}
	got, ok := r.Get(id(1))
	if !ok {
		t.Fatal("Leave with the old epoch removed the member")
	}
	if got.State.Generation != 2 || !got.Connected.Equal(again.Connected) || !got.Reported.Equal(again.Reported) {
		t.Errorf("the member = %+v, want that of the second Join", got)
	}

	// The new one works.
	if !r.Report(id(1), second, wire.State{Generation: 3, Phase: wire.PhaseRunning}, t0.Add(2*time.Minute)) {
		t.Error("Report with the new epoch returned false")
	}
	if !r.Leave(id(1), second) {
		t.Error("Leave with the new epoch returned false")
	}
}

func TestEpochsDifferAcrossMembers(t *testing.T) {
	r := New(15 * time.Minute)
	a := r.Join(member(1, "web", "web-1"))
	b := r.Join(member(2, "web", "web-2"))
	if a == b {
		t.Fatalf("two members have epoch %d", a)
	}
	if r.Report(id(1), b, wire.State{}, t0) {
		t.Error("Report took the epoch of another member")
	}
	// A member that left and joins again has a new epoch as well.
	r.Leave(id(1), a)
	if c := r.Join(member(1, "web", "web-1")); c == a {
		t.Errorf("the epoch %d was given twice", a)
	}
}

func TestLeave(t *testing.T) {
	r := New(15 * time.Minute)
	epoch := r.Join(member(1, "web", "web-1"))
	r.Report(id(1), epoch, sampled(t0, 1), t0)
	if !r.Leave(id(1), epoch) {
		t.Fatal("Leave returned false for a member that is there")
	}
	if _, ok := r.Get(id(1)); ok {
		t.Error("the member is still there")
	}
	if got := r.Service("web"); len(got) != 0 {
		t.Errorf("Service = %v after its only member left", got)
	}
	if got := r.History(id(1)); got != nil {
		t.Errorf("History = %v of a member that left", got)
	}
	if r.Report(id(1), epoch, wire.State{}, t0) {
		t.Error("Report returned true after Leave")
	}
	if _, ok := r.Get(id(1)); ok {
		t.Error("a Report after Leave brought the member back")
	}
	if r.Leave(id(1), epoch) {
		t.Error("a second Leave returned true")
	}
	if r.Leave(id(2), 1) {
		t.Error("Leave returned true for a member that never joined")
	}
}

func TestStale(t *testing.T) {
	r := New(15 * time.Minute)
	const after = 10 * time.Second
	e1 := r.Join(member(1, "web", "web-1"))
	e2 := r.Join(member(2, "web", "web-2"))
	r.Join(member(3, "api", "api-1"))

	if got := r.Stale(t0.Add(after), after); len(got) != 0 {
		t.Errorf("Stale after exactly 10s = %v, want none: not older yet", got)
	}
	r.Report(id(1), e1, wire.State{}, t0.Add(8*time.Second))
	now := t0.Add(after + time.Second)
	if got, want := r.Stale(now, after), []wire.ID{id(2), id(3)}; !slices.Equal(got, want) {
		t.Errorf("Stale = %v, want %v", got, want)
	}
	r.Leave(id(2), e2)
	if got, want := r.Stale(now, after), []wire.ID{id(3)}; !slices.Equal(got, want) {
		t.Errorf("Stale after one left = %v, want %v", got, want)
	}
	if got, want := r.Stale(t0.Add(time.Hour), after), []wire.ID{id(1), id(3)}; !slices.Equal(got, want) {
		t.Errorf("Stale an hour later = %v, want %v", got, want)
	}
}
