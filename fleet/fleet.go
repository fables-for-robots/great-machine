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
