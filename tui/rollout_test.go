package tui

import (
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

func TestStageOf(t *testing.T) {
	for _, tt := range []struct {
		name   string
		st     wire.State
		target uint64
		want   stage
	}{
		{"it has not heard of the generation", wire.State{Generation: 19, Phase: wire.PhaseRunning, Prepared: []uint64{19}}, 20, stageWaiting},
		{"it is preparing it", wire.State{Generation: 19, Phase: wire.PhaseRunning, Prepared: []uint64{19}, Preparing: 20}, 20, stagePreparing},
		{"it has prepared it", wire.State{Generation: 19, Phase: wire.PhaseRunning, Prepared: []uint64{19, 20}}, 20, stagePrepared},
		{"it runs it", wire.State{Generation: 20, Phase: wire.PhaseRunning, Prepared: []uint64{19, 20}}, 20, stageOn},
		{"it is stopping it", wire.State{Generation: 20, Phase: wire.PhaseStopping}, 20, stageOn},
		{"its service ended on it", wire.State{Generation: 20, Phase: wire.PhaseBackoff}, 20, stageFailed},
		{"without a generation and without a list", wire.State{Phase: wire.PhaseIdle}, 1, stageWaiting},
		{"idle, and it has prepared it", wire.State{Phase: wire.PhaseIdle, Prepared: []uint64{1}}, 1, stagePrepared},
		// The three cases the tests of the page lacked (the ledger of
		// stage 2, Task 8).
		{"idle on the rollout's generation, which no zathras reports", wire.State{Generation: 20, Phase: wire.PhaseIdle}, 20, stageOn},
		{"on a newer generation, and it has not prepared the older one: a rollback", wire.State{Generation: 21, Phase: wire.PhaseRunning, Prepared: []uint64{21}}, 20, stageWaiting},
		{"on a newer generation, and it has the older one", wire.State{Generation: 21, Phase: wire.PhaseRunning, Prepared: []uint64{20, 21}}, 20, stagePrepared},
		{"on a newer generation in backoff: it did not end on this one", wire.State{Generation: 21, Phase: wire.PhaseBackoff, Prepared: []uint64{20, 21}}, 20, stagePrepared},
		{"preparing what it lists as prepared", wire.State{Generation: 19, Phase: wire.PhaseRunning, Prepared: []uint64{19, 20}, Preparing: 20}, 20, stagePreparing},
		// And what the fleet of a draal that is wrong could say.
		{"a rollout without a generation", wire.State{Phase: wire.PhaseIdle}, 0, stageWaiting},
		{"a phase nobody knows, on it", wire.State{Generation: 20, Phase: "hibernating"}, 20, stageOn},
	} {
		if got := stageOf(tt.st, tt.target); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
	words := map[string]bool{}
	for _, s := range stages {
		words[s.String()] = true
	}
	if len(words) != 5 || stage(99).String() != "not prepared" {
		t.Errorf("the stages are named %v, and one that is none %q", words, stage(99))
	}
}

func TestPhases(t *testing.T) {
	zs := []wire.Zathras{
		{State: wire.State{Phase: wire.PhaseRunning}}, {State: wire.State{Phase: wire.PhaseRunning}},
		{State: wire.State{Phase: wire.PhaseBackoff}}, {State: wire.State{Phase: wire.PhaseStopping}},
		{State: wire.State{Phase: wire.PhaseIdle}}, {State: wire.State{Phase: "hibernating"}}, {State: wire.State{Phase: "\x1b[2J"}},
	}
	got := byPhase(zs)
	if got[wire.PhaseRunning] != 2 || got[wire.PhaseStopping] != 1 || got[wire.PhaseBackoff] != 1 || got[wire.PhaseIdle] != 3 || len(got) != 4 {
		t.Errorf("byPhase = %v; a phase nobody knows is counted as idle", got)
	}
	if len(byPhase(nil)) != 0 {
		t.Errorf("byPhase of none = %v", byPhase(nil))
	}

	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		st   wire.State
		want string
	}{
		{wire.State{Generation: 3, Phase: wire.PhaseRunning, RunningFor: 5*time.Hour + 3*time.Minute}, "for 5h03m"},
		{wire.State{Generation: 3, Phase: wire.PhaseRunning}, ""},
		{wire.State{Generation: 3, Phase: wire.PhaseRunning, RunningFor: -time.Second}, ""},
		{wire.State{Generation: 3, Phase: wire.PhaseBackoff, RestartAt: now.Add(6 * time.Second)}, "starts again in 6s"},
		{wire.State{Generation: 3, Phase: wire.PhaseBackoff, RestartAt: now.Add(-time.Minute)}, "starts again now"},
		{wire.State{Generation: 3, Phase: wire.PhaseBackoff}, ""},
		{wire.State{Generation: 3, Phase: wire.PhaseStopping}, "told to end"},
		{wire.State{Phase: wire.PhaseIdle}, "has no generation"},
		{wire.State{Generation: 3, Phase: wire.PhaseIdle}, ""},
		{wire.State{Phase: "hibernating"}, "has no generation"},
	} {
		if got := phaseNote(tt.st, now); got != tt.want {
			t.Errorf("phaseNote(%+v) = %q, want %q", tt.st, got, tt.want)
		}
	}
}

func TestRolloutOpen(t *testing.T) {
	for state, want := range map[wire.RolloutState]bool{
		wire.RolloutPreparing: true, wire.RolloutActivating: true, wire.RolloutHalted: true,
		wire.RolloutCompleted: false, wire.RolloutSuperseded: false, "odd": false,
	} {
		if got := rolloutOpen(&wire.RolloutInfo{State: state}); got != want {
			t.Errorf("rolloutOpen(%s) = %v, want %v", state, got, want)
		}
	}
	if rolloutOpen(nil) {
		t.Error("no rollout is open")
	}
}
