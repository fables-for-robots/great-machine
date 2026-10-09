### Task 14: fleet, the zathrases that are connected

**Files:**
- Create: `fleet/fleet.go` (the registry: members, reports, samples)
- Create: `fleet/subscribe.go` (who is told of changes)
- Test: `fleet/fleet_test.go`, `fleet/subscribe_test.go`

**Interfaces:**
- Consumes, from `wire` (Task 2):

  ```go
  type ID string

  type Phase string

  const (
  	PhaseIdle     Phase = "idle"
  	PhaseRunning  Phase = "running"
  	PhaseStopping Phase = "stopping"
  	PhaseBackoff  Phase = "backoff"
  )

  // Sample is the figures of a container at one time.
  type Sample struct {
  	At          time.Time `json:"at"`
  	CPU         float64   `json:"cpu"`          // cores
  	Memory      uint64    `json:"memory"`       // bytes
  	MemoryLimit uint64    `json:"memory_limit"` // bytes; 0 for none
  	DiskRead    float64   `json:"disk_read"`    // bytes per second
  	DiskWrite   float64   `json:"disk_write"`
  	NetRx       float64   `json:"net_rx"`
  	NetTx       float64   `json:"net_tx"`
  }

  // State is what a zathras reports.
  type State struct {
  	Generation uint64        `json:"generation"` // 0 for none
  	Binary     string        `json:"binary,omitempty"`
  	Args       []string      `json:"args,omitempty"`
  	Phase      Phase         `json:"phase"`
  	PID        int           `json:"pid,omitempty"`         // while running
  	RunningFor time.Duration `json:"running_for,omitempty"` // while running
  	RestartAt  time.Time     `json:"restart_at"`            // while in backoff; the zero time otherwise
  	LastExit   *Exit         `json:"last_exit,omitempty"`
  	Restarts   uint64        `json:"restarts"`
  	Prepared   []uint64      `json:"prepared,omitempty"` // ascending
  	Preparing  uint64        `json:"preparing,omitempty"`
  	Sample     *Sample       `json:"sample,omitempty"`
  }
  ```

- Produces, package `fleet`:

  ```go
  // Member is a zathras that is connected.
  type Member struct {
  	ID        wire.ID
  	Service   string
  	Name      string
  	Connected time.Time
  	State     wire.State
  	Reported  time.Time // when State came
  }

  type Registry struct{ /* unexported */ }

  // New returns a registry that keeps the samples of the last history.
  func New(history time.Duration) *Registry

  func (r *Registry) Join(m Member) (epoch uint64)
  func (r *Registry) Report(id wire.ID, epoch uint64, st wire.State, now time.Time) bool
  func (r *Registry) Leave(id wire.ID, epoch uint64) bool
  func (r *Registry) Get(id wire.ID) (Member, bool)
  func (r *Registry) Service(name string) []Member // by name, then by ID
  func (r *Registry) Services() []string           // sorted
  func (r *Registry) History(id wire.ID) []wire.Sample // oldest first
  func (r *Registry) Stale(now time.Time, after time.Duration) []wire.ID
  func (r *Registry) Subscribe() (<-chan struct{}, func())
  ```

  What the contract leaves open, and this task settles:

  - A `Registry` is safe for concurrent use.
  - `Join` with a `Reported` that is the zero time sets it to `Connected`.
    If the state that joins has a sample, it is the first of the history.
    A `Join` of an ID that is there replaces the member, starts a new
    epoch and drops its history. No two `Join`s return the same epoch.
  - `Report` and `Leave` return false, and change nothing, for a member
    that is not there and for an epoch that is not the member's.
  - The history is trimmed by the samples' own times (the zathras' clock):
    a sample older than `history` before the newest one is dropped. A
    report whose sample has the `At` of the sample added last adds nothing,
    as a zathras sends its latest sample with every report.
  - `History` returns a copy, and nil for a member that is not there.
    `Stale` returns the IDs sorted; a member is stale when
    `now - Reported > after`.
  - The channel of `Subscribe` is never closed. After the function that
    ends the subscription has returned, nothing more is sent; it may be
    called more than once. A change never waits for a reader: changes that
    were not read are one value.

- [ ] **Step 1: Write the failing tests of the members**

`fleet/fleet_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./fleet/`
Expected: FAIL — the package does not build: `undefined: Member`, `undefined: New`

- [ ] **Step 3: Write the registry**

`fleet/fleet.go`:

```go
// Package fleet is the zathrases that are connected, as draal keeps them in
// memory: the last report of each and the samples of the last minutes. A
// zathras that has gone is forgotten.
package fleet

import (
	"cmp"
	"slices"
	"sync"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// Member is a zathras that is connected.
type Member struct {
	ID        wire.ID
	Service   string
	Name      string
	Connected time.Time
	State     wire.State
	Reported  time.Time // when State came
}

// entry is a member with what the registry keeps of it.
type entry struct {
	member  Member
	epoch   uint64
	samples []wire.Sample // oldest first
}

// Registry is the members there are. It is safe for concurrent use.
type Registry struct {
	history time.Duration

	mu      sync.Mutex
	members map[wire.ID]*entry
	epoch   uint64 // of the last Join
}

// New returns a registry that keeps the samples of the last history.
func New(history time.Duration) *Registry {
	return &Registry{
		history: history,
		members: map[wire.ID]*entry{},
	}
}

// Join adds m, in place of a member with the same ID if there is one, and
// returns the epoch of this membership. Report and Leave with another
// epoch do nothing: they are of a connection that was replaced.
//
// The history of a member that is replaced is dropped. A Reported that is
// the zero time becomes Connected, so that a member which never reports
// goes stale counted from when it joined.
func (r *Registry) Join(m Member) (epoch uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m.Reported.IsZero() {
		m.Reported = m.Connected
	}
	r.epoch++
	e := &entry{member: m, epoch: r.epoch}
	e.keep(m.State.Sample, r.history)
	r.members[m.ID] = e
	return e.epoch
}

// Report records st as the state of the member, and now as the time it
// came. The sample of st, when it has one, is added to the member's
// history. It returns false, and does nothing, when the member is not there
// or epoch is not the one its Join returned.
func (r *Registry) Report(id wire.ID, epoch uint64, st wire.State, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.members[id]
	if !ok || e.epoch != epoch {
		return false
	}
	e.member.State = st
	e.member.Reported = now
	e.keep(st.Sample, r.history)
	return true
}

// Leave removes the member. It returns false, and does nothing, when the
// member is not there or epoch is not the one its Join returned.
func (r *Registry) Leave(id wire.ID, epoch uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.members[id]
	if !ok || e.epoch != epoch {
		return false
	}
	delete(r.members, id)
	return true
}

// keep adds s to the samples, unless it is nil or the sample that was
// added last (a zathras reports its latest sample with every report, and
// reports more often than it samples), and drops the samples that are
// older than history before the newest. The times compared are all the
// zathras' own, so its clock may be wrong.
func (e *entry) keep(s *wire.Sample, history time.Duration) {
	if s == nil {
		return
	}
	if n := len(e.samples); n > 0 && e.samples[n-1].At.Equal(s.At) {
		return
	}
	e.samples = append(e.samples, *s)
	oldest := s.At.Add(-history)
	drop := 0
	for drop < len(e.samples) && e.samples[drop].At.Before(oldest) {
		drop++
	}
	if drop > 0 {
		// A new slice, so that what was dropped is freed.
		e.samples = slices.Clone(e.samples[drop:])
	}
}

// Get returns the member with the ID, and false when there is none.
func (r *Registry) Get(id wire.ID) (Member, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.members[id]
	if !ok {
		return Member{}, false
	}
	return e.member, true
}

// Service returns the members of a service, by name and then by ID.
func (r *Registry) Service(name string) []Member {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Member
	for _, e := range r.members {
		if e.member.Service == name {
			out = append(out, e.member)
		}
	}
	slices.SortFunc(out, func(a, b Member) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// Services returns the names of the services that have members, sorted.
func (r *Registry) Services() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[string]struct{}{}
	var out []string
	for _, e := range r.members {
		if _, ok := seen[e.member.Service]; !ok {
			seen[e.member.Service] = struct{}{}
			out = append(out, e.member.Service)
		}
	}
	slices.Sort(out)
	return out
}

// History returns the samples kept of a member, oldest first. It is a
// copy, and nil for a member that is not there.
func (r *Registry) History(id wire.ID) []wire.Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.members[id]
	if !ok {
		return nil
	}
	return slices.Clone(e.samples)
}

// Stale returns the members whose last report is older than after, sorted.
func (r *Registry) Stale(now time.Time, after time.Duration) []wire.ID {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []wire.ID
	for id, e := range r.members {
		if now.Sub(e.member.Reported) > after {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test -race ./fleet/ -v`
Expected: PASS — `TestJoinAndGet`, `TestJoinWithoutReportedCountsFromConnected`, `TestServiceOrder`, `TestServices`, `TestReportUpdatesStateAndHistory`, `TestHistoryIsACopy`, `TestJoinKeepsTheSampleOfItsState`, `TestHistoryIsTrimmedByTime`, `TestJoinReplacesAndStartsANewEpoch`, `TestEpochsDifferAcrossMembers`, `TestLeave`, `TestStale`

- [ ] **Step 5: Commit**

```bash
git add fleet/
git commit -m "Fleet: the connected zathrases, their reports and samples"
```

- [ ] **Step 6: Write the failing tests of Subscribe**

`fleet/subscribe_test.go`:

```go
package fleet

import (
	"sync"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// got says whether a value waits in the channel, and takes it.
func got(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestSubscribeGetsAValueAfterEveryChange(t *testing.T) {
	r := New(15 * time.Minute)
	changes, stop := r.Subscribe()
	defer stop()
	if got(changes) {
		t.Fatal("a value before anything changed")
	}

	epoch := r.Join(member(1, "web", "web-1"))
	if !got(changes) {
		t.Error("no value after Join")
	}
	r.Report(id(1), epoch, wire.State{Phase: wire.PhaseRunning}, t0)
	if !got(changes) {
		t.Error("no value after Report")
	}
	r.Leave(id(1), epoch)
	if !got(changes) {
		t.Error("no value after Leave")
	}
	if got(changes) {
		t.Error("a second value after one change")
	}
}

func TestSubscribeIsNotToldOfWhatChangedNothing(t *testing.T) {
	r := New(15 * time.Minute)
	old := r.Join(member(1, "web", "web-1"))
	epoch := r.Join(member(1, "web", "web-1"))
	changes, stop := r.Subscribe()
	defer stop()

	r.Report(id(1), old, wire.State{}, t0)
	r.Leave(id(1), old)
	r.Report(id(2), epoch, wire.State{}, t0)
	r.Leave(id(2), epoch)
	r.Get(id(1))
	r.Service("web")
	r.Services()
	r.History(id(1))
	r.Stale(t0, time.Second)
	if got(changes) {
		t.Error("a value though nothing changed")
	}
}

func TestSubscribeCoalesces(t *testing.T) {
	r := New(15 * time.Minute)
	changes, stop := r.Subscribe()
	defer stop()

	// Nobody reads. If a change waited for a reader, this would never
	// return and the test would time out.
	epoch := r.Join(member(1, "web", "web-1"))
	for i := range 100 {
		r.Report(id(1), epoch, wire.State{Restarts: uint64(i)}, t0)
	}
	if !got(changes) {
		t.Fatal("no value after a hundred changes")
	}
	if got(changes) {
		t.Error("two values for changes nobody read between: want one")
	}
	// The next change gives a value again.
	r.Report(id(1), epoch, wire.State{}, t0)
	if !got(changes) {
		t.Error("no value for the change after the read")
	}
}

func TestUnsubscribe(t *testing.T) {
	r := New(15 * time.Minute)
	changes, stop := r.Subscribe()
	epoch := r.Join(member(1, "web", "web-1"))
	if !got(changes) {
		t.Fatal("no value after Join")
	}
	stop()
	r.Report(id(1), epoch, wire.State{}, t0)
	r.Leave(id(1), epoch)
	if got(changes) {
		t.Error("a value after the subscription was ended")
	}
	stop() // a second call does nothing
}

func TestSeveralSubscribers(t *testing.T) {
	r := New(15 * time.Minute)
	a, stopA := r.Subscribe()
	b, stopB := r.Subscribe()
	c, stopC := r.Subscribe()
	defer stopB()
	defer stopC()

	epoch := r.Join(member(1, "web", "web-1"))
	if !got(a) || !got(b) {
		t.Error("a subscriber got no value after Join")
	}
	// c has not read: its value still waits, and one that ends its
	// subscription does not disturb the others.
	stopA()
	r.Report(id(1), epoch, wire.State{}, t0)
	if got(a) {
		t.Error("a value for the subscriber that ended")
	}
	if !got(b) {
		t.Error("no value for the subscriber that stayed")
	}
	if !got(c) {
		t.Error("no value for the subscriber that never read")
	}
	if got(c) {
		t.Error("two values for the subscriber that never read: want one")
	}
}

// Reports, reads, joins that replace and subscriptions at the same time:
// the race detector is the judge.
func TestConcurrentUse(t *testing.T) {
	r := New(time.Minute)
	const members, rounds = 8, 200
	var wg sync.WaitGroup
	for n := range members {
		wg.Add(1)
		go func() {
			defer wg.Done()
			epoch := r.Join(member(n, "web", "web"))
			for i := range rounds {
				at := t0.Add(time.Duration(i) * time.Second)
				if i == rounds/2 {
					epoch = r.Join(member(n, "web", "web"))
				}
				if !r.Report(id(n), epoch, sampled(at, float64(i)), at) {
					t.Errorf("member %d: report %d was refused", n, i)
					return
				}
			}
			r.Leave(id(n), epoch)
		}()
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			changes, stop := r.Subscribe()
			defer stop()
			for range rounds {
				got(changes)
				for _, m := range r.Service("web") {
					r.Get(m.ID)
					r.History(m.ID)
				}
				r.Services()
				r.Stale(t0.Add(time.Hour), 10*time.Second)
			}
		}()
	}
	wg.Wait()
	if left := r.Service("web"); len(left) != 0 {
		t.Errorf("%d members are left after all of them left", len(left))
	}
}
```

- [ ] **Step 7: Run the tests to see them fail**

Run: `go test ./fleet/`
Expected: FAIL — the package does not build: `r.Subscribe undefined (type *Registry has no field or method Subscribe)`

- [ ] **Step 8: Write Subscribe, and tell the subscribers of every change**

`fleet/subscribe.go`:

```go
package fleet

// Subscribe returns a channel with room for one value that gets one after
// every Join, Report and Leave that changed something, and a function
// that ends the subscription.
//
// Changes that nobody has read yet are one value: the registry never waits
// for a reader. After the function has returned nothing more is sent; a
// value that was sent before may still be in the channel, which is not
// closed. The function may be called more than once.
func (r *Registry) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	r.mu.Lock()
	r.subs[ch] = struct{}{}
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		delete(r.subs, ch)
		r.mu.Unlock()
	}
}

// notify tells every subscriber that something changed. The caller holds
// r.mu.
func (r *Registry) notify() {
	for ch := range r.subs {
		select {
		case ch <- struct{}{}:
		default:
			// A value waits there already, and says the same.
		}
	}
}
```

`fleet/fleet.go` gets five new lines: the field `subs` of `Registry`, its
map in `New`, and `r.notify()` before the `return` of `Join`, of `Report`
and of `Leave` where they changed something. The whole file is then:

```go
// Package fleet is the zathrases that are connected, as draal keeps them in
// memory: the last report of each and the samples of the last minutes. A
// zathras that has gone is forgotten.
package fleet

import (
	"cmp"
	"slices"
	"sync"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// Member is a zathras that is connected.
type Member struct {
	ID        wire.ID
	Service   string
	Name      string
	Connected time.Time
	State     wire.State
	Reported  time.Time // when State came
}

// entry is a member with what the registry keeps of it.
type entry struct {
	member  Member
	epoch   uint64
	samples []wire.Sample // oldest first
}

// Registry is the members there are. It is safe for concurrent use.
type Registry struct {
	history time.Duration

	mu      sync.Mutex
	members map[wire.ID]*entry
	epoch   uint64 // of the last Join
	subs    map[chan struct{}]struct{}
}

// New returns a registry that keeps the samples of the last history.
func New(history time.Duration) *Registry {
	return &Registry{
		history: history,
		members: map[wire.ID]*entry{},
		subs:    map[chan struct{}]struct{}{},
	}
}

// Join adds m, in place of a member with the same ID if there is one, and
// returns the epoch of this membership. Report and Leave with another
// epoch do nothing: they are of a connection that was replaced.
//
// The history of a member that is replaced is dropped. A Reported that is
// the zero time becomes Connected, so that a member which never reports
// goes stale counted from when it joined.
func (r *Registry) Join(m Member) (epoch uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m.Reported.IsZero() {
		m.Reported = m.Connected
	}
	r.epoch++
	e := &entry{member: m, epoch: r.epoch}
	e.keep(m.State.Sample, r.history)
	r.members[m.ID] = e
	r.notify()
	return e.epoch
}

// Report records st as the state of the member, and now as the time it
// came. The sample of st, when it has one, is added to the member's
// history. It returns false, and does nothing, when the member is not there
// or epoch is not the one its Join returned.
func (r *Registry) Report(id wire.ID, epoch uint64, st wire.State, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.members[id]
	if !ok || e.epoch != epoch {
		return false
	}
	e.member.State = st
	e.member.Reported = now
	e.keep(st.Sample, r.history)
	r.notify()
	return true
}

// Leave removes the member. It returns false, and does nothing, when the
// member is not there or epoch is not the one its Join returned.
func (r *Registry) Leave(id wire.ID, epoch uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.members[id]
	if !ok || e.epoch != epoch {
		return false
	}
	delete(r.members, id)
	r.notify()
	return true
}

// keep adds s to the samples, unless it is nil or the sample that was
// added last (a zathras reports its latest sample with every report, and
// reports more often than it samples), and drops the samples that are
// older than history before the newest. The times compared are all the
// zathras' own, so its clock may be wrong.
func (e *entry) keep(s *wire.Sample, history time.Duration) {
	if s == nil {
		return
	}
	if n := len(e.samples); n > 0 && e.samples[n-1].At.Equal(s.At) {
		return
	}
	e.samples = append(e.samples, *s)
	oldest := s.At.Add(-history)
	drop := 0
	for drop < len(e.samples) && e.samples[drop].At.Before(oldest) {
		drop++
	}
	if drop > 0 {
		// A new slice, so that what was dropped is freed.
		e.samples = slices.Clone(e.samples[drop:])
	}
}

// Get returns the member with the ID, and false when there is none.
func (r *Registry) Get(id wire.ID) (Member, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.members[id]
	if !ok {
		return Member{}, false
	}
	return e.member, true
}

// Service returns the members of a service, by name and then by ID.
func (r *Registry) Service(name string) []Member {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Member
	for _, e := range r.members {
		if e.member.Service == name {
			out = append(out, e.member)
		}
	}
	slices.SortFunc(out, func(a, b Member) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// Services returns the names of the services that have members, sorted.
func (r *Registry) Services() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[string]struct{}{}
	var out []string
	for _, e := range r.members {
		if _, ok := seen[e.member.Service]; !ok {
			seen[e.member.Service] = struct{}{}
			out = append(out, e.member.Service)
		}
	}
	slices.Sort(out)
	return out
}

// History returns the samples kept of a member, oldest first. It is a
// copy, and nil for a member that is not there.
func (r *Registry) History(id wire.ID) []wire.Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.members[id]
	if !ok {
		return nil
	}
	return slices.Clone(e.samples)
}

// Stale returns the members whose last report is older than after, sorted.
func (r *Registry) Stale(now time.Time, after time.Duration) []wire.ID {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []wire.ID
	for id, e := range r.members {
		if now.Sub(e.member.Reported) > after {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}
```

- [ ] **Step 9: Run the tests to see them pass**

Run: `go test -race -count=3 ./fleet/`
Expected: PASS (`ok  	github.com/fables-for-robots/great-machine/fleet`)

Run: `test -z "$(gofmt -l .)" && go vet ./...`
Expected: no output.

- [ ] **Step 10: Commit**

```bash
git add fleet/
git commit -m "Fleet: Subscribe"
```
