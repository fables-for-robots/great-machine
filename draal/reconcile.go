package draal

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/fleet"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/rollout"
	"github.com/fables-for-robots/great-machine/wire"
)

const (
	// breath is the least time between two looks at the fleet. Every
	// report of every zathras asks for a look, and a Recheck goes by the
	// last report and can lie in the past until the next one comes: those
	// that come within a breath are answered by one look.
	breath = 20 * time.Millisecond
	// rounds is how often a service is decided about in one pass: every
	// move of its rollout asks for one more decision.
	rounds = 4
)

// command is a prepare or an activate that was sent to a zathras.
type command struct {
	sess       *session
	activate   bool
	generation uint64
	rollout    uint64 // the rollout under way it was sent for; 0 for none
	cancel     context.CancelFunc
	accepted   bool // an activate that was answered and that no report shows yet
}

// failure is the last command a zathras refused or failed, and the
// session it was sent on.
type failure struct {
	sess *session
	rollout.Failure
}

// result is the answer to a command.
type result struct {
	cmd *command
	err error
}

// reconciler is the state of the reconciler loop. Its goroutine alone
// touches it; the answers to commands come back over results.
type reconciler struct {
	d          *Draal
	ctx        context.Context
	sending    sync.WaitGroup
	results    chan result
	preparing  map[wire.ID]*command
	activating map[wire.ID]*command
	failed     map[wire.ID]failure
	seen       map[string]uint64 // by service: the last rollout that was looked at
	// absent is, by service, who left while a rollout of the service was
	// under way without being done on its target, and is not back: the
	// Absent of rollout.Input. It is kept over a rollout that supersedes,
	// and emptied when no rollout of the service is under way.
	absent map[string]map[wire.ID]time.Time
	// left is the leaves taken from the Draal that have not been weighed
	// against the rollouts of their service yet.
	left []leave
}

// reconcile is the reconciler loop. It does nothing before Settle has
// passed. Then it looks at every service whenever the fleet changed, a
// rollout was recorded, a command was answered or a Recheck has come.
func (d *Draal) reconcile(ctx context.Context) {
	r := &reconciler{
		d:          d,
		ctx:        ctx,
		results:    make(chan result),
		preparing:  map[wire.ID]*command{},
		activating: map[wire.ID]*command{},
		failed:     map[wire.ID]failure{},
		seen:       map[string]uint64{},
		absent:     map[string]map[wire.ID]time.Time{},
	}
	defer r.sending.Wait()
	members, stopMembers := d.fleet.Subscribe()
	defer stopMembers()
	rollouts, stopRollouts := d.subscribe()
	defer stopRollouts()

	settle := time.NewTimer(d.cfg.Settle)
	defer settle.Stop()
	select {
	case <-ctx.Done():
		return
	case <-settle.C:
	}
	for {
		recheck := r.pass()
		rest := time.NewTimer(breath)
		var due <-chan time.Time
		var timer *time.Timer
		if !recheck.IsZero() {
			timer = time.NewTimer(recheck.Sub(d.cfg.Now()))
			due = timer.C
		}
		select {
		case <-ctx.Done():
		case <-members:
		case <-rollouts:
		case res := <-r.results:
			r.answered(res)
		case <-due:
		}
		if timer != nil {
			timer.Stop()
		}
		// What else comes within the breath is taken with it.
		for resting := true; resting; {
			select {
			case <-ctx.Done():
				resting = false
			case res := <-r.results:
				r.answered(res)
			case <-rest.C:
				resting = false
			}
		}
		rest.Stop()
		if ctx.Err() != nil {
			return
		}
	}
}

// pass looks at every service once, and returns when to look again if
// nothing happens before, or the zero time.
func (r *reconciler) pass() time.Time {
	d := r.d
	r.forget()
	// A leave whose grace has passed holds nothing, whatever its service.
	now := d.cfg.Now()
	r.left = slices.DeleteFunc(r.left, func(l leave) bool { return !now.Before(l.at.Add(d.cfg.AbsentGrace)) })
	names, err := d.serviceNames(r.ctx)
	if err != nil {
		if r.ctx.Err() == nil {
			d.log.Error("reconciler: reading the services", "error", err)
		}
		return d.cfg.Now().Add(d.cfg.RetryAfter)
	}
	var recheck time.Time
	changed := false
	for _, name := range names {
		at, moved, err := r.service(name)
		if err != nil {
			if r.ctx.Err() == nil {
				d.log.Error("reconciler: a service", "service", name, "error", err)
			}
			at = d.cfg.Now().Add(d.cfg.RetryAfter)
		}
		changed = changed || moved
		if !at.IsZero() && (recheck.IsZero() || at.Before(recheck)) {
			recheck = at
		}
	}
	if changed {
		d.changed()
	}
	return recheck
}

// forget drops what is kept of a zathras that left, or that came back on
// another connection, and the activations that a report shows.
func (r *reconciler) forget() {
	for id, cmd := range r.preparing {
		if r.d.session(id) != cmd.sess {
			cmd.cancel()
			delete(r.preparing, id)
		}
	}
	for id, cmd := range r.activating {
		if r.d.session(id) != cmd.sess {
			cmd.cancel()
			delete(r.activating, id)
			continue
		}
		if m, ok := r.d.fleet.Get(id); ok && cmd.accepted && m.State.Generation == cmd.generation {
			delete(r.activating, id)
		}
	}
	for id, f := range r.failed {
		if r.d.session(id) != f.sess {
			delete(r.failed, id)
		}
	}
}

// service decides about one service and does what the decision says. It
// returns the Recheck of the decision and whether the rollout or its
// counts changed.
func (r *reconciler) service(name string) (recheck time.Time, changed bool, err error) {
	d := r.d
	for range rounds {
		// The members first, and the leaves with them: the rollout is read
		// after, so that it is never older than a leave the members show.
		members, back := r.look(name)
		var stable uint64
		row, err := d.cfg.DB.Service(r.ctx, name)
		switch {
		case err == nil:
			stable = row.Stable
		case !errors.Is(err, db.ErrNotFound):
			return time.Time{}, changed, err
		}
		last, has, err := d.cfg.DB.LastRollout(r.ctx, name)
		if err != nil {
			return time.Time{}, changed, err
		}
		var current *wire.RolloutInfo
		if has {
			current = &last
			if r.seen[name] != last.ID {
				r.seen[name] = last.ID
				r.recorded(name, last.Generation)
			}
		}

		now := d.cfg.Now()
		if err := r.note(name, current, now, back); err != nil {
			return time.Time{}, changed, err
		}
		in := rollout.Input{
			Now:            now,
			Stable:         stable,
			Rollout:        current,
			Members:        members,
			Preparing:      map[wire.ID]uint64{},
			Activating:     map[wire.ID]uint64{},
			Failed:         map[wire.ID]rollout.Failure{},
			Absent:         maps.Clone(r.absent[name]),
			StableAfter:    d.cfg.StableAfter,
			PrepareTimeout: d.cfg.PrepareTimeout,
			RetryAfter:     d.cfg.RetryAfter,
			AbsentGrace:    d.cfg.AbsentGrace,
		}
		for _, m := range in.Members {
			if cmd := r.preparing[m.ID]; cmd != nil {
				in.Preparing[m.ID] = cmd.generation
			}
			if cmd := r.activating[m.ID]; cmd != nil {
				in.Activating[m.ID] = cmd.generation
			}
			if f, ok := r.failed[m.ID]; ok {
				in.Failed[m.ID] = f.Failure
			}
		}
		dec := rollout.Decide(in)

		moved := false
		if dec.State != "" && has {
			ok, err := d.cfg.DB.SetRolloutState(r.ctx, last.ID, dec.State, dec.Reason, d.cfg.Now())
			if err != nil {
				return time.Time{}, changed, err
			}
			if !ok {
				// Superseded meanwhile: the decision was about a
				// rollout that is over.
				continue
			}
			d.log.Info("rollout", "service", name, "rollout", last.ID, "state", dec.State, "reason", dec.Reason)
			last.State, last.Reason = dec.State, dec.Reason
			moved, changed = true, true
			if !last.State.UnderWay() {
				delete(r.absent, name)
			}
		}
		if has {
			t := tally{rollout: last.ID, prepared: dec.Prepared, activated: dec.Activated, awaited: dec.Awaited}
			d.mu.Lock()
			if d.tallies[name] != t {
				d.tallies[name] = t
				changed = true
			}
			d.mu.Unlock()
		}
		r.withdraw(name, current)
		for _, c := range dec.Commands {
			r.send(name, current, c)
		}
		if !moved {
			return dec.Recheck, changed, nil
		}
	}
	return d.cfg.Now(), changed, nil
}

// look returns the members of a service, takes the leaves the Draal has
// noted since the last look, and says which zathrases of the service that
// left, or are absent, are connected again. It does all of it under d.mu,
// with which a zathras joins and leaves, so that the three agree: a
// zathras that left is among the members only when it came back after.
func (r *reconciler) look(service string) ([]fleet.Member, map[wire.ID]bool) {
	d := r.d
	d.mu.Lock()
	defer d.mu.Unlock()
	members := d.fleet.Service(service)
	r.left = append(r.left, d.left...)
	d.left = nil
	back := map[wire.ID]bool{}
	connected := func(id wire.ID) {
		if _, ok := d.fleet.Get(id); ok {
			back[id] = true
		}
	}
	for _, l := range r.left {
		if l.service == service {
			connected(l.id)
		}
	}
	for id := range r.absent[service] {
		connected(id)
	}
	return members, back
}

// note weighs the leaves of a service against its rollouts, and keeps its
// book of who is absent: a zathras that left while a rollout of the
// service was under way, and was not done on that rollout's target, is
// entered with the time it left. Whoever is back, and whoever left
// AbsentGrace ago or longer, is dropped; everyone is when no rollout of
// the service is under way.
func (r *reconciler) note(service string, current *wire.RolloutInfo, now time.Time, back map[wire.ID]bool) error {
	grace := r.d.cfg.AbsentGrace
	underWay := current != nil && current.State.UnderWay()
	// The rollouts of the service are read for a leave from before current
	// was made, before anything is changed.
	var rollouts []wire.RolloutInfo
	for _, l := range r.left {
		if underWay && l.service == service && l.at.Before(current.Created) {
			list, err := r.d.cfg.DB.Rollouts(r.ctx, service, rolloutsListed)
			if err != nil {
				return err
			}
			rollouts = list
			break
		}
	}

	book := r.absent[service]
	if book == nil {
		book = map[wire.ID]time.Time{}
	}
	rest := r.left[:0]
	for _, l := range r.left {
		if l.service != service {
			rest = append(rest, l)
			continue
		}
		// A zathras that left again was back in between.
		delete(book, l.id)
		if !underWay || back[l.id] || !now.Before(l.at.Add(grace)) {
			continue
		}
		if target, ok := during(current, rollouts, l.at); ok && !rollout.Done(l.state, target, r.d.cfg.StableAfter) {
			book[l.id] = l.at
		}
	}
	clear(r.left[len(rest):])
	r.left = rest
	for id, at := range book {
		if !underWay || back[id] || !now.Before(at.Add(grace)) {
			delete(book, id)
		}
	}
	if len(book) == 0 {
		delete(r.absent, service)
	} else {
		r.absent[service] = book
	}
	return nil
}

// during returns the target of the rollout that was under way when a
// zathras left at the time given, and false when none was. current is the
// last rollout of the service, and under way; rollouts are the last of the
// service, newest first, which a leave from before current was made needs.
//
// A rollout is under way from when it is made until it ends, a superseded
// one until the next was made. So the rollout that was under way is the
// last one made before the zathras left, if it is current or was
// superseded; one that completed or halted took its absent with it. When
// draal cannot tell, because more rollouts were made since than it reads,
// it answers current: it waits rather than not.
func during(current *wire.RolloutInfo, rollouts []wire.RolloutInfo, at time.Time) (uint64, bool) {
	if !at.Before(current.Created) {
		return current.Generation, true
	}
	for _, ro := range rollouts {
		if ro.Created.After(at) {
			continue
		}
		switch ro.State {
		case wire.RolloutSuperseded:
			return ro.Generation, true
		case wire.RolloutCompleted, wire.RolloutHalted:
			return 0, false
		default:
			// current, by a clock that went back.
			return current.Generation, true
		}
	}
	if len(rollouts) == rolloutsListed {
		return current.Generation, true
	}
	// It left before the service had a rollout.
	return 0, false
}

// recorded is called when a service has a rollout that was not seen
// before: the failures of its zathrases are forgotten, and the prepares
// under way for another generation are withdrawn.
func (r *reconciler) recorded(service string, target uint64) {
	for id, f := range r.failed {
		if f.sess.service == service {
			delete(r.failed, id)
		}
	}
	for id, cmd := range r.preparing {
		if cmd.sess.service == service && cmd.generation != target {
			cmd.cancel()
			delete(r.preparing, id)
		}
	}
}

// withdraw ends the prepares that were sent for a rollout which is not
// under way any more.
func (r *reconciler) withdraw(service string, current *wire.RolloutInfo) {
	for id, cmd := range r.preparing {
		if cmd.sess.service != service || cmd.rollout == 0 {
			continue
		}
		if current == nil || current.ID != cmd.rollout || !current.State.UnderWay() {
			cmd.cancel()
			delete(r.preparing, id)
		}
	}
}

// send sends a command to a zathras, in a goroutine of its own.
func (r *reconciler) send(service string, current *wire.RolloutInfo, c rollout.Command) {
	d := r.d
	if r.preparing[c.Zathras] != nil || r.activating[c.Zathras] != nil {
		return
	}
	sess := d.session(c.Zathras)
	if sess == nil {
		return
	}
	gen, err := d.cfg.DB.Generation(r.ctx, service, c.Generation)
	if err != nil {
		d.log.Error("reconciler: reading a generation", "service", service, "generation", c.Generation, "error", err)
		return
	}
	ctx, cancel := context.WithCancel(r.ctx)
	cmd := &command{sess: sess, activate: c.Activate, generation: c.Generation, cancel: cancel}
	if current != nil && current.State.UnderWay() {
		cmd.rollout = current.ID
	}
	var req wire.Request
	if c.Activate {
		req.Activate = &wire.Activate{Generation: gen.Number, Binary: gen.Binary, Args: gen.Args}
		r.activating[c.Zathras] = cmd
	} else {
		req.Prepare = &wire.Prepare{Generation: gen.Number, Binary: gen.Binary, Closure: gen.Closure}
		r.preparing[c.Zathras] = cmd
	}
	d.log.Info("command sent", "service", service, "zathras", c.Zathras, "activate", c.Activate, "generation", c.Generation)
	r.sending.Go(func() {
		err := call(ctx, sess.conn, &req)
		select {
		case r.results <- result{cmd: cmd, err: err}:
		case <-r.ctx.Done():
		}
	})
}

// call sends one request on a stream of its own and returns the error it
// was answered with. The stream is closed when ctx is done, which
// withdraws the request.
func call(ctx context.Context, conn node.Conn, req *wire.Request) error {
	s, err := conn.OpenStream(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	stop := context.AfterFunc(ctx, func() { s.Close() })
	defer stop()
	_, err = wire.Call(s, req)
	return err
}

// answered takes the answer to a command into the books. Only what the
// zathras answered counts as a failure: a command that ended without an
// answer, because the stream or the connection broke, and one the zathras
// answered with unavailable, because it shuts down, just leave the books.
// If the zathras is still there, the next decision sends the command again.
func (r *reconciler) answered(res result) {
	cmd := res.cmd
	cmd.cancel()
	id := cmd.sess.id
	sent := r.preparing
	if cmd.activate {
		sent = r.activating
	}
	if sent[id] != cmd {
		return // withdrawn, or the zathras left
	}
	var refusal *wire.Error
	switch {
	case res.err == nil && cmd.activate:
		// It stays until a report shows the generation.
		cmd.accepted = true
	case res.err == nil:
		delete(sent, id)
	case errors.As(res.err, &refusal) && refusal.Code != wire.CodeUnavailable:
		delete(sent, id)
		r.failed[id] = failure{sess: cmd.sess, Failure: rollout.Failure{
			Generation: cmd.generation,
			Activate:   cmd.activate,
			Reason:     refusal.Message,
			At:         r.d.cfg.Now(),
		}}
	default:
		delete(sent, id)
		r.d.log.Warn("a command was not answered", "zathras", id, "activate", cmd.activate, "generation", cmd.generation, "error", res.err)
	}
}
