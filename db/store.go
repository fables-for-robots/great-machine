package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fables-for-robots/great-machine/db/dbq"
	"github.com/fables-for-robots/great-machine/wire"
)

// Service is a service as the database has it.
type Service struct {
	Name   string
	Stable uint64 // 0 for none
}

// Generation is what a service runs: a binary, its arguments and the closure
// of the store path the binary is in. Args is nil for a generation without
// arguments.
type Generation struct {
	Service   string
	Number    uint64
	Binary    string
	Args      []string
	Closure   []string
	Created   time.Time
	CreatedBy wire.ID
}

func serviceOf(r dbq.Service) Service {
	return Service{Name: r.Name, Stable: uint64(r.Stable)}
}

func generationOf(r dbq.Generation) (Generation, error) {
	args, err := listOf(r.Args)
	if err != nil {
		return Generation{}, fmt.Errorf("db: generation %d of %s: args: %w", r.Number, r.Service, err)
	}
	closure, err := listOf(r.Closure)
	if err != nil {
		return Generation{}, fmt.Errorf("db: generation %d of %s: closure: %w", r.Number, r.Service, err)
	}
	return Generation{
		Service:   r.Service,
		Number:    uint64(r.Number),
		Binary:    r.Binary,
		Args:      args,
		Closure:   closure,
		Created:   timeOf(r.CreatedAt),
		CreatedBy: wire.ID(r.CreatedBy),
	}, nil
}

// rolloutOf is a row as it travels. The counts stay zero: they are of the
// zathrases that are connected, which the database knows nothing of.
func rolloutOf(r dbq.Rollout) wire.RolloutInfo {
	return wire.RolloutInfo{
		ID:         uint64(r.ID),
		Service:    r.Service,
		Generation: uint64(r.Generation),
		Parallel:   int(r.Parallel),
		State:      wire.RolloutState(r.State),
		Reason:     r.Reason,
		Created:    timeOf(r.CreatedAt),
		CreatedBy:  wire.ID(r.CreatedBy),
		Finished:   timeOf(r.FinishedAt),
	}
}

// listText is a list of strings as a column holds it: a JSON array, "[]"
// for none.
func listText(list []string) (string, error) {
	if list == nil {
		list = []string{}
	}
	b, err := json.Marshal(list)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// listOf is the list a column holds: nil for an empty one.
func listOf(text string) ([]string, error) {
	var list []string
	if err := json.Unmarshal([]byte(text), &list); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return list, nil
}

// Services returns every service, by name.
func (d *DB) Services(ctx context.Context) ([]Service, error) {
	var out []Service
	err := d.read(ctx, func(q *dbq.Queries) error {
		rows, err := q.ListServices(ctx)
		if err != nil {
			return err
		}
		out = make([]Service, len(rows))
		for i, r := range rows {
			out[i] = serviceOf(r)
		}
		return nil
	})
	return out, err
}

// Service returns ErrNotFound for a service that has no row.
func (d *DB) Service(ctx context.Context, name string) (Service, error) {
	var out Service
	err := d.read(ctx, func(q *dbq.Queries) error {
		r, err := q.ServiceByName(ctx, name)
		if err != nil {
			return notFound(err)
		}
		out = serviceOf(r)
		return nil
	})
	return out, err
}

// Generation returns ErrNotFound for one that is not there.
func (d *DB) Generation(ctx context.Context, service string, number uint64) (Generation, error) {
	var out Generation
	err := d.read(ctx, func(q *dbq.Queries) error {
		r, err := q.GenerationByNumber(ctx, dbq.GenerationByNumberParams{
			Service: service,
			Number:  int64(number),
		})
		if err != nil {
			return notFound(err)
		}
		out, err = generationOf(r)
		return err
	})
	return out, err
}

// NewRollout does in one transaction: makes the service's row if it is
// missing, records a generation with the next number, marks the rollout
// that is under way as superseded, and records a rollout that is
// preparing and targets the new generation. The counts of what it
// returns are zero.
func (d *DB) NewRollout(ctx context.Context, req wire.RolloutRequest, by wire.ID, now time.Time) (wire.RolloutInfo, error) {
	args, err := listText(req.Args)
	if err != nil {
		return wire.RolloutInfo{}, fmt.Errorf("db: args: %w", err)
	}
	closure, err := listText(req.Closure)
	if err != nil {
		return wire.RolloutInfo{}, fmt.Errorf("db: closure: %w", err)
	}
	var out wire.RolloutInfo
	err = d.write(ctx, func(q *dbq.Queries) error {
		if err := q.InsertService(ctx, dbq.InsertServiceParams{
			Name:      req.Service,
			CreatedAt: now.UnixNano(),
		}); err != nil {
			return err
		}
		last, err := q.LastGenerationNumber(ctx, req.Service)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// No row leaves last at 0, and the first generation is 1.
		number := last + 1
		if err := q.InsertGeneration(ctx, dbq.InsertGenerationParams{
			Service:   req.Service,
			Number:    number,
			Binary:    req.Binary,
			Args:      args,
			Closure:   closure,
			CreatedAt: now.UnixNano(),
			CreatedBy: string(by),
		}); err != nil {
			return err
		}
		out, err = supersedeAndInsert(ctx, q, req.Service, number, req.Parallel, by, now)
		return err
	})
	if err != nil {
		return wire.RolloutInfo{}, fmt.Errorf("db: new rollout of %s: %w", req.Service, err)
	}
	return out, nil
}

// NewRollback records a rollout that targets the stable generation, and
// supersedes the one under way. ErrNotFound: no such service.
// ErrNoStable. ErrNothingToRollBack: the last rollout completed.
func (d *DB) NewRollback(ctx context.Context, req wire.RollbackRequest, by wire.ID, now time.Time) (wire.RolloutInfo, error) {
	var out wire.RolloutInfo
	err := d.write(ctx, func(q *dbq.Queries) error {
		service, err := q.ServiceByName(ctx, req.Service)
		if err != nil {
			return notFound(err)
		}
		if service.Stable == 0 {
			return ErrNoStable
		}
		last, err := q.LastRollout(ctx, req.Service)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && wire.RolloutState(last.State) == wire.RolloutCompleted {
			return ErrNothingToRollBack
		}
		out, err = supersedeAndInsert(ctx, q, req.Service, service.Stable, req.Parallel, by, now)
		return err
	})
	if err != nil {
		return wire.RolloutInfo{}, fmt.Errorf("db: new rollback of %s: %w", req.Service, err)
	}
	return out, nil
}

// supersedeAndInsert ends the rollout of the service that is under way and
// records a new one, which is preparing.
func supersedeAndInsert(ctx context.Context, q *dbq.Queries, service string, generation int64, parallel int, by wire.ID, now time.Time) (wire.RolloutInfo, error) {
	if _, err := q.SupersedeUnderWay(ctx, dbq.SupersedeUnderWayParams{
		Service:    service,
		FinishedAt: now.UnixNano(),
	}); err != nil {
		return wire.RolloutInfo{}, err
	}
	r, err := q.InsertRollout(ctx, dbq.InsertRolloutParams{
		Service:    service,
		Generation: generation,
		Parallel:   int64(parallel),
		CreatedAt:  now.UnixNano(),
		CreatedBy:  string(by),
	})
	if err != nil {
		return wire.RolloutInfo{}, err
	}
	return rolloutOf(r), nil
}

// LastRollout returns the newest rollout of the service, and false when
// it never had one.
func (d *DB) LastRollout(ctx context.Context, service string) (wire.RolloutInfo, bool, error) {
	var out wire.RolloutInfo
	var found bool
	err := d.read(ctx, func(q *dbq.Queries) error {
		r, err := q.LastRollout(ctx, service)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out, found = rolloutOf(r), true
		return nil
	})
	return out, found, err
}

// Rollouts returns the newest limit rollouts of the service, newest first.
func (d *DB) Rollouts(ctx context.Context, service string, limit int) ([]wire.RolloutInfo, error) {
	var out []wire.RolloutInfo
	err := d.read(ctx, func(q *dbq.Queries) error {
		// A negative LIMIT means no limit at all to SQLite.
		rows, err := q.ListRollouts(ctx, dbq.ListRolloutsParams{
			Service: service,
			Max:     int64(max(limit, 0)),
		})
		if err != nil {
			return err
		}
		out = make([]wire.RolloutInfo, len(rows))
		for i, r := range rows {
			out[i] = rolloutOf(r)
		}
		return nil
	})
	return out, err
}

// SetRolloutState moves a rollout that is under way to state, with the
// reason. To completed and halted it also sets Finished, and to completed
// it makes the target the stable generation of the service. It returns
// false, and changes nothing, when the rollout is not under way any more.
// The states a rollout can be moved to are activating, completed and halted;
// any other is an error.
func (d *DB) SetRolloutState(ctx context.Context, id uint64, state wire.RolloutState, reason string, now time.Time) (bool, error) {
	var finished int64
	switch state {
	case wire.RolloutActivating:
	case wire.RolloutCompleted, wire.RolloutHalted:
		finished = now.UnixNano()
	default:
		return false, fmt.Errorf("db: a rollout cannot be moved to %q", state)
	}
	var moved bool
	err := d.write(ctx, func(q *dbq.Queries) error {
		n, err := q.SetRolloutState(ctx, dbq.SetRolloutStateParams{
			ID:         int64(id),
			State:      string(state),
			Reason:     reason,
			FinishedAt: finished,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		moved = true
		if state != wire.RolloutCompleted {
			return nil
		}
		r, err := q.RolloutByID(ctx, int64(id))
		if err != nil {
			return err
		}
		return q.SetStable(ctx, dbq.SetStableParams{Name: r.Service, Stable: r.Generation})
	})
	if err != nil {
		return false, fmt.Errorf("db: set rollout %d to %s: %w", id, state, err)
	}
	return moved, nil
}
