package draal

import (
	"context"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// nextFleet reads one fleet of a watcher.
func nextFleet(t *testing.T, fleets <-chan wire.Fleet) wire.Fleet {
	t.Helper()
	select {
	case f, ok := <-fleets:
		if !ok {
			t.Fatal("the watcher's channel was closed")
		}
		return f
	case <-time.After(deadline):
		t.Fatal("timed out waiting for a fleet")
	}
	return wire.Fleet{}
}

// fleetWhere reads fleets of a watcher until one meets cond, and returns
// it and how many were read.
func fleetWhere(t *testing.T, fleets <-chan wire.Fleet, cond func(wire.Fleet) bool) (wire.Fleet, int) {
	t.Helper()
	for n := 1; ; n++ {
		if f := nextFleet(t, fleets); cond(f) {
			return f, n
		}
	}
}

// rolloutOf records a rollout of a service of the given name.
func rolloutOf(t *testing.T, d *Draal, service string) wire.RolloutInfo {
	t.Helper()
	req := webRollout(1)
	req.Service = service
	info, err := d.Rollout(context.Background(), operatorID, req)
	if err != nil {
		t.Fatalf("Rollout of %s: %v", service, err)
	}
	return info
}

func TestWatchSendsAtOnceAndAfterAChange(t *testing.T) {
	d := New(testConfig(t))
	fleets, err := d.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if f := nextFleet(t, fleets); len(f.Services) != 0 {
		t.Fatalf("the first fleet = %+v, want no service", f)
	}

	rolloutOf(t, d, "web")
	info := rolloutOf(t, d, "api")
	f, _ := fleetWhere(t, fleets, func(f wire.Fleet) bool { return len(f.Services) == 2 })
	if f.Services[0].Name != "api" || f.Services[1].Name != "web" {
		t.Fatalf("services = %q, %q; want api, web", f.Services[0].Name, f.Services[1].Name)
	}
	api := f.Services[0]
	if api.Stable != 0 || api.Rollout == nil || api.Rollout.ID != info.ID || api.Rollout.State != wire.RolloutPreparing {
		t.Errorf("api = %+v with rollout %+v", api, api.Rollout)
	}
	if api.Rollout.Prepared != 0 || api.Rollout.Activated != 0 || api.Rollout.Awaited != 0 {
		t.Errorf("the counts of a rollout nobody decided about = %+v, want zeros", api.Rollout)
	}
	if len(api.Zathrases) != 0 {
		t.Errorf("zathrases = %v, want none", api.Zathrases)
	}
}

func TestWatchNotMoreOftenThanWatchEvery(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEvery = 300 * time.Millisecond
	d := New(cfg)

	start := time.Now()
	fleets, err := d.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	nextFleet(t, fleets)
	rolloutOf(t, d, "a")
	rolloutOf(t, d, "b")
	rolloutOf(t, d, "c")
	f := nextFleet(t, fleets)
	if took := time.Since(start); took < cfg.WatchEvery {
		t.Errorf("the second fleet came after %v, sooner than WatchEvery (%v)", took, cfg.WatchEvery)
	}
	if len(f.Services) != 3 {
		t.Errorf("the second fleet has %d services, want the three changes in one", len(f.Services))
	}
}

func TestWatchSlowWatcherHoldsNobodyUp(t *testing.T) {
	d := New(testConfig(t))
	slow, err := d.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	quick, err := d.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// Ten changes, each of which the quick watcher sees while the slow
	// one reads nothing.
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	for i, name := range names {
		rolloutOf(t, d, name)
		fleetWhere(t, quick, func(f wire.Fleet) bool { return len(f.Services) == i+1 })
	}

	// The slow one gets the newest, not the ten between.
	_, read := fleetWhere(t, slow, func(f wire.Fleet) bool { return len(f.Services) == len(names) })
	if read > 3 {
		t.Errorf("the slow watcher read %d fleets before the newest, want it to miss those between", read)
	}
}

func TestWatchClosesWithTheContext(t *testing.T) {
	d := New(testConfig(t))
	ctx, cancel := context.WithCancel(t.Context())
	fleets, err := d.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	nextFleet(t, fleets)
	cancel()
	select {
	case f, ok := <-fleets:
		if ok {
			// A fleet that was under way may still come; the end follows.
			if _, ok := <-fleets; ok {
				t.Fatalf("after the context was done the channel gave %+v and stayed open", f)
			}
		}
	case <-time.After(deadline):
		t.Fatal("the channel was not closed when the context was done")
	}
}
