package draal

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/wire"
)

// Watch sends the fleet at once and after every change, at most once in
// WatchEvery, until ctx is done, and then closes the channel. A watcher
// that does not read misses the fleets between: what it reads next is the
// newest. No watcher waits for another.
func (d *Draal) Watch(ctx context.Context) (<-chan wire.Fleet, error) {
	members, stopMembers := d.fleet.Subscribe()
	rollouts, stopRollouts := d.subscribe()
	first, err := d.snapshot(ctx)
	if err != nil {
		stopMembers()
		stopRollouts()
		return nil, wire.Errorf(wire.CodeInternal, "reading the fleet: %v", err)
	}
	out := make(chan wire.Fleet, 1)
	out <- first
	go func() {
		defer close(out)
		defer stopMembers()
		defer stopRollouts()
		pause := time.NewTimer(d.cfg.WatchEvery)
		defer pause.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-members:
			case <-rollouts:
			}
			// Not before WatchEvery has passed since the fleet before.
			select {
			case <-ctx.Done():
				return
			case <-pause.C:
			}
			f, err := d.snapshot(ctx)
			if err != nil {
				if ctx.Err() == nil {
					d.log.Error("reading the fleet for a watcher", "error", err)
				}
			} else {
				// This goroutine alone sends: once the fleet that
				// nobody read is taken out, there is room.
				select {
				case out <- f:
				default:
					select {
					case <-out:
					default:
					}
					out <- f
				}
			}
			pause.Reset(d.cfg.WatchEvery)
		}
	}()
	return out, nil
}

// serviceNames returns the names of the services in the database and of
// those with members, sorted.
func (d *Draal) serviceNames(ctx context.Context) ([]string, error) {
	rows, err := d.cfg.DB.Services(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}
	for _, name := range d.fleet.Services() {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names, nil
}

// snapshot builds the fleet as it is now.
func (d *Draal) snapshot(ctx context.Context) (wire.Fleet, error) {
	names, err := d.serviceNames(ctx)
	if err != nil {
		return wire.Fleet{}, err
	}
	f := wire.Fleet{Services: make([]wire.Service, 0, len(names))}
	for _, name := range names {
		svc := wire.Service{Name: name, Zathrases: []wire.Zathras{}}
		last, ok, err := d.cfg.DB.LastRollout(ctx, name)
		if err != nil {
			return wire.Fleet{}, err
		}
		// The stable generation is read after the rollout, so that a
		// rollout that shows as completed never comes with the stable
		// generation of before.
		row, err := d.cfg.DB.Service(ctx, name)
		switch {
		case err == nil:
			svc.Stable = row.Stable
		case !errors.Is(err, db.ErrNotFound):
			return wire.Fleet{}, err
		}
		if ok {
			d.mu.Lock()
			t := d.tallies[name]
			d.mu.Unlock()
			if t.rollout == last.ID {
				last.Prepared, last.Activated, last.Awaited = t.prepared, t.activated, t.awaited
			}
			svc.Rollout = &last
		}
		for _, m := range d.fleet.Service(name) {
			svc.Zathrases = append(svc.Zathrases, wire.Zathras{ID: m.ID, Name: m.Name, Connected: m.Connected, State: m.State})
		}
		f.Services = append(f.Services, svc)
	}
	return f, nil
}
