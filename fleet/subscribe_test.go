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
