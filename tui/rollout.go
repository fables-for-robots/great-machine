package tui

import (
	"slices"
	"time"

	"github.com/fables-for-robots/great-machine/human"
	"github.com/fables-for-robots/great-machine/wire"
)

// What the terminal UI works out from the fleet about a rollout. It
// reads what a zathras reports and nothing else: the fleet has no record
// of a rollout for each zathras, so where a zathras stands in a rollout
// is what its own state says about the rollout's generation. The web
// page reads the same (ui/src/state/rollout.ts), and the two agree.

// stage is where a zathras stands on the way to the generation of a
// rollout.
type stage int

// The stages, in the order of a rollout.
const (
	// stageWaiting: it has not prepared the generation and is not
	// preparing it.
	stageWaiting stage = iota
	// stagePreparing: it is making the closure of the generation present.
	stagePreparing
	// stagePrepared: it has the closure, and runs something else or
	// nothing.
	stagePrepared
	// stageOn: it has taken the generation as its own. Whether draal
	// counts it as activated the terminal UI cannot tell: that is a
	// matter of how long it has run, by a time draal is configured with.
	stageOn
	// stageFailed: it has taken the generation as its own, and the
	// service has ended and waits to be started again.
	stageFailed
)

// stages are all of them, in order.
var stages = []stage{stageWaiting, stagePreparing, stagePrepared, stageOn, stageFailed}

// String returns the words the terminal UI has for a stage.
func (s stage) String() string {
	switch s {
	case stagePreparing:
		return "preparing"
	case stagePrepared:
		return "prepared"
	case stageOn:
		return "on it"
	case stageFailed:
		return "ended on it"
	}
	return "not prepared"
}

// stageOf returns where a zathras that reports st stands on the way to
// the generation target.
//
// One that has taken the generation as its own is on it whatever its
// phase, but for backoff: also one that reports itself idle on it, which
// no zathras does. One that is on another generation, an older or a
// newer one (a rollback goes to an older one), stands where its
// preparing and its prepared put it. And one that prepares the
// generation while it lists it as prepared is preparing: it is fetching
// it once more.
func stageOf(st wire.State, target uint64) stage {
	switch {
	case target == 0:
		return stageWaiting
	case st.Generation == target && st.Phase == wire.PhaseBackoff:
		return stageFailed
	case st.Generation == target:
		return stageOn
	case st.Preparing == target:
		return stagePreparing
	case slices.Contains(st.Prepared, target):
		return stagePrepared
	}
	return stageWaiting
}

// phases are the phases in the order the terminal UI lists them.
var phases = []wire.Phase{wire.PhaseRunning, wire.PhaseStopping, wire.PhaseBackoff, wire.PhaseIdle}

// knownPhase returns the phase, or idle for one the terminal UI does not
// know: it runs nothing that could be named.
func knownPhase(p wire.Phase) wire.Phase {
	switch p {
	case wire.PhaseRunning, wire.PhaseStopping, wire.PhaseBackoff:
		return p
	}
	return wire.PhaseIdle
}

// byPhase counts zathrases by phase.
func byPhase(zs []wire.Zathras) map[wire.Phase]int {
	count := map[wire.Phase]int{}
	for _, z := range zs {
		count[knownPhase(z.State.Phase)]++
	}
	return count
}

// phaseNote returns what there is to say of a phase besides its name:
// for how long, or until when. It is empty when the state says nothing
// of it.
func phaseNote(st wire.State, now time.Time) string {
	switch knownPhase(st.Phase) {
	case wire.PhaseRunning:
		if st.RunningFor <= 0 {
			return ""
		}
		return "for " + human.Age(st.RunningFor)
	case wire.PhaseBackoff:
		switch until := human.Until(st.RestartAt, now); until {
		case human.None:
			return ""
		case "now":
			return "starts again now"
		default:
			return "starts again " + until
		}
	case wire.PhaseStopping:
		return "told to end"
	}
	if st.Generation == 0 {
		return "has no generation"
	}
	return ""
}

// rolloutOpen says whether a rollout is one to show with where every
// zathras stands in it: under way, or halted.
func rolloutOpen(r *wire.RolloutInfo) bool {
	return r != nil && (r.State.UnderWay() || r.State == wire.RolloutHalted)
}
