// Package rollout is the reconciler of draal as a function: Decide looks at
// the last rollout of a service and at what its zathrases report, and says
// what is missing. Nothing of a rollout is kept between two calls, so a
// draal that was restarted, and a zathras that was gone and is back, are
// carried on with.
package rollout

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/fables-for-robots/great-machine/fleet"
	"github.com/fables-for-robots/great-machine/wire"
)

// Failure is a command a zathras refused or failed.
type Failure struct {
	Generation uint64
	Activate   bool // false: a prepare
	Reason     string
	At         time.Time
}

// Input is everything Decide looks at for one service.
type Input struct {
	Now     time.Time
	Stable  uint64            // 0 for none
	Rollout *wire.RolloutInfo // the last rollout of the service; nil for none
	Members []fleet.Member    // its connected zathrases
	// Preparing: a prepare that was sent and not answered, by generation.
	Preparing map[wire.ID]uint64
	// Activating: an activate that was sent and that no report shows yet.
	Activating map[wire.ID]uint64
	// Failed: the last failure of a zathras, if it had one.
	Failed map[wire.ID]Failure
	// Absent: the zathrases that left since this rollout was made, without
	// being done, and when each left, by the clock of Now and of
	// Rollout.Created. The caller removes one when it is back. Each holds
	// the rollout open for AbsentGrace after it left.
	Absent         map[wire.ID]time.Time
	StableAfter    time.Duration // 30 seconds
	PrepareTimeout time.Duration // 30 minutes
	RetryAfter     time.Duration // 30 seconds
	AbsentGrace    time.Duration // 2 minutes
}

// Command is something to send to a zathras: prepare the generation, or
// activate it.
type Command struct {
	Zathras    wire.ID
	Activate   bool // false: prepare
	Generation uint64
}

// Decision is what Decide asks for.
type Decision struct {
	Commands []Command
	// State is the state the rollout is to be moved to, with Reason, or ""
	// when it stays as it is.
	State  wire.RolloutState
	Reason string
	// The counts of the rollout, for wire.RolloutInfo.
	Prepared, Activated, Awaited int
	// Recheck is when to decide again if nothing happens before, or the
	// zero time.
	Recheck time.Time
}

// Decide says what section 6.2 of the spec asks for. It has no state and
// reads no clock: the same Input gives the same Decision.
//
// The commands are in the order of the zathrases by name and then by ID. A
// zathras that Preparing or Activating has is sent nothing, and no zathras
// is sent two commands.
func Decide(in Input) Decision {
	if in.Rollout == nil {
		// A service that never had a rollout has nothing to run.
		return Decision{}
	}
	members := slices.Clone(in.Members)
	slices.SortFunc(members, func(a, b fleet.Member) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	d := &decider{in: in, members: members, target: in.Rollout.Generation}
	d.count()
	switch in.Rollout.State {
	case wire.RolloutPreparing:
		d.preparing()
	case wire.RolloutActivating:
		d.activating()
	case wire.RolloutHalted:
		d.settled(true)
	default:
		d.settled(false)
	}
	return d.out
}

// decider is one call of Decide.
type decider struct {
	in      Input
	members []fleet.Member // by name, then by ID
	target  uint64         // of the last rollout
	out     Decision
}

// count fills the counts of the rollout.
func (d *decider) count() {
	for _, m := range d.members {
		if hasPrepared(m, d.target) {
			d.out.Prepared++
		}
		if d.done(m) {
			d.out.Activated++
		} else {
			d.out.Awaited++
		}
	}
}

// preparing: every zathras that has not prepared the target is sent
// prepare, and nothing is restarted.
func (d *decider) preparing() {
	// A prepare that fails halts the rollout.
	for _, m := range d.members {
		if f, ok := d.failure(m); ok {
			d.halt(m.Name + ": " + f.Reason)
			return
		}
	}
	// When every connected zathras has prepared the target, the rollout is
	// activating: with no zathras connected that is at once. The first
	// zathrases are moved in this same decision.
	if d.out.Prepared == len(d.members) {
		d.out.State = wire.RolloutActivating
		d.activating()
		return
	}
	// A rollout that is still preparing so long after it was made is halted.
	deadline := d.in.Rollout.Created.Add(d.in.PrepareTimeout)
	if !d.in.Now.Before(deadline) {
		d.halt(fmt.Sprintf("still preparing after %s", d.in.PrepareTimeout))
		return
	}
	for _, m := range d.members {
		if !hasPrepared(m, d.target) && !d.busy(m) {
			d.send(m, d.target)
		}
	}
	d.recheck(deadline)
}

// activating: the zathrases are moved to the target, some at a time.
func (d *decider) activating() {
	// A zathras that failed halts the rollout, with its name and how its
	// service ended.
	for _, m := range d.members {
		if f, ok := d.failure(m); ok {
			d.halt(m.Name + ": " + f.Reason)
			return
		}
		if m.State.Generation == d.target && m.State.Phase == wire.PhaseBackoff {
			d.halt(m.Name + ": " + ended(m.State.LastExit, d.target))
			return
		}
	}
	// When every connected zathras is done, the rollout is completed: not
	// while one that left during it without being done may still come back.
	if until, ok := d.absent(); ok {
		d.recheck(until)
	} else if d.out.Awaited == 0 {
		d.out.State = wire.RolloutCompleted
		return
	}

	var idle, waiting []fleet.Member
	underWay := 0 // of those that count against parallel
	for _, m := range d.members {
		switch {
		case d.done(m):
		case m.State.Phase == wire.PhaseIdle:
			// It has nothing to lose: it is not counted against parallel.
			if !d.busy(m) {
				idle = append(idle, m)
			}
		case m.State.Generation == d.target:
			underWay++
			if m.State.Phase == wire.PhaseRunning {
				// It is done when it has run for StableAfter.
				d.recheck(d.in.Now.Add(d.in.StableAfter - m.State.RunningFor))
			}
		case d.busy(m):
			// It was sent activate and has not reported it yet, or it is
			// being prepared as one of the batch: either way it has a place
			// in the batch.
			underWay++
		default:
			waiting = append(waiting, m)
		}
	}
	for _, m := range idle {
		d.send(m, d.target)
	}
	// While fewer than parallel zathrases are under way, the waiting
	// zathras that comes first by name is sent prepare, if it has not
	// prepared the target, and then activate. With parallel 0 all of them
	// are.
	for _, m := range waiting {
		if d.in.Rollout.Parallel != 0 && underWay >= d.in.Rollout.Parallel {
			break
		}
		d.send(m, d.target)
		underWay++
	}
}

// settled: no rollout is under way. When the last one completed, every
// zathras whose generation is not the stable one is sent it. When the last
// one halted, the service is frozen, and only a zathras that is idle is.
func (d *decider) settled(frozen bool) {
	stable := d.in.Stable
	if stable == 0 {
		return
	}
	for _, m := range d.members {
		if m.State.Generation == stable || d.busy(m) {
			continue
		}
		if frozen && m.State.Phase != wire.PhaseIdle {
			continue
		}
		// A zathras that failed the stable generation is left alone for
		// RetryAfter, and then tried again.
		if f, ok := d.in.Failed[m.ID]; ok && f.Generation == stable {
			if retry := f.At.Add(d.in.RetryAfter); d.in.Now.Before(retry) {
				d.recheck(retry)
				continue
			}
		}
		d.send(m, stable)
	}
}

// done says whether m runs the target and has for StableAfter. It goes by
// what m reports alone: no time of m's clock is compared with Now.
func (d *decider) done(m fleet.Member) bool {
	return m.State.Generation == d.target &&
		m.State.Phase == wire.PhaseRunning &&
		m.State.RunningFor >= d.in.StableAfter
}

// busy says whether a command to m is on its way.
func (d *decider) busy(m fleet.Member) bool {
	_, preparing := d.in.Preparing[m.ID]
	_, activating := d.in.Activating[m.ID]
	return preparing || activating
}

// absent says whether a zathras of Absent still holds the rollout open,
// and until when the first of them does. One that is connected is not
// absent, and one that left AbsentGrace ago or longer holds nothing.
func (d *decider) absent() (time.Time, bool) {
	var until time.Time
	held := false
	if d.in.AbsentGrace <= 0 {
		return until, held
	}
	for z, left := range d.in.Absent {
		end := left.Add(d.in.AbsentGrace)
		if !d.in.Now.Before(end) || d.connected(z) {
			continue
		}
		if !held || end.Before(until) {
			until, held = end, true
		}
	}
	return until, held
}

// connected says whether the zathras is one of the members.
func (d *decider) connected(z wire.ID) bool {
	return slices.ContainsFunc(d.members, func(m fleet.Member) bool { return m.ID == z })
}

// failure returns the command for the target that m refused or failed
// since the rollout was made. An older one is of an earlier attempt at the
// same generation (a rollback targets a generation again), not of this
// rollout.
func (d *decider) failure(m fleet.Member) (Failure, bool) {
	f, ok := d.in.Failed[m.ID]
	if !ok || f.Generation != d.target || f.At.Before(d.in.Rollout.Created) {
		return Failure{}, false
	}
	return f, true
}

// send sends m toward the generation: prepare if it has not prepared it,
// and activate if it has.
func (d *decider) send(m fleet.Member, generation uint64) {
	d.out.Commands = append(d.out.Commands, Command{
		Zathras:    m.ID,
		Activate:   hasPrepared(m, generation),
		Generation: generation,
	})
}

// halt ends the rollout with the reason, and sends nothing.
func (d *decider) halt(reason string) {
	d.out.State = wire.RolloutHalted
	d.out.Reason = reason
	d.out.Commands = nil
	d.out.Recheck = time.Time{}
}

// recheck asks for another decision at the time, if none is asked for
// sooner.
func (d *decider) recheck(at time.Time) {
	if d.out.Recheck.IsZero() || at.Before(d.out.Recheck) {
		d.out.Recheck = at
	}
}

// hasPrepared says whether m has prepared the generation since it started.
func hasPrepared(m fleet.Member, generation uint64) bool {
	return slices.Contains(m.State.Prepared, generation)
}

// ended says how the service of a zathras that is in backoff on the
// generation ended.
func ended(exit *wire.Exit, generation uint64) string {
	switch {
	case exit == nil || exit.Generation != generation:
		return "its service ended soon after it was started"
	case exit.Error != "":
		return "could not be started: " + exit.Error
	case exit.Signal != "":
		return fmt.Sprintf("was ended by %s after %s", exit.Signal, short(exit.Ran))
	default:
		return fmt.Sprintf("exited with status %d after %s", exit.Status, short(exit.Ran))
	}
}

// short is d as a person reads it: 2.1s, not 2.123456789s.
func short(d time.Duration) time.Duration {
	if d < time.Second {
		return d.Round(time.Millisecond)
	}
	return d.Round(100 * time.Millisecond)
}
