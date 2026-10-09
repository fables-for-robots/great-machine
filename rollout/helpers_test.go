package rollout

import (
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/fleet"
	"github.com/fables-for-robots/great-machine/wire"
)

// The durations of the spec, and the times the tests go by: every rollout
// of a test was made at t0, and it is now a minute later unless the test
// says otherwise.
const (
	stableAfter    = 30 * time.Second
	prepareTimeout = 30 * time.Minute
	retryAfter     = 30 * time.Second
	absentGrace    = 2 * time.Minute
)

var (
	t0  = time.Unix(1_800_000_000, 0).UTC()
	now = t0.Add(time.Minute)
)

// id returns the endpoint ID of the test's zathras with the name: the name
// in hexadecimal behind zeros, so that a failure can print the name.
func id(name string) wire.ID {
	h := hex.EncodeToString([]byte(name))
	return wire.ID(strings.Repeat("0", 64-len(h)) + h)
}

// nameOf is the name an ID of id was made of.
func nameOf(id wire.ID) string {
	b, err := hex.DecodeString(strings.TrimLeft(string(id), "0"))
	if err != nil {
		return string(id)
	}
	return string(b)
}

// option changes a member that member makes.
type option func(*fleet.Member)

// member returns a zathras of the service web with the name, which is idle
// unless an option says otherwise.
func member(name string, opts ...option) fleet.Member {
	m := fleet.Member{
		ID:        id(name),
		Service:   "web",
		Name:      name,
		Connected: t0,
		Reported:  now,
		State:     wire.State{Phase: wire.PhaseIdle},
	}
	for _, opt := range opts {
		opt(&m)
	}
	return m
}

// idle: the zathras has no generation.
func idle() option {
	return func(m *fleet.Member) {
		m.State.Generation = 0
		m.State.Phase = wire.PhaseIdle
	}
}

// running: the zathras runs the generation and has for d. A zathras has
// prepared what it runs.
func running(generation uint64, d time.Duration) option {
	return func(m *fleet.Member) {
		m.State.Generation = generation
		m.State.Phase = wire.PhaseRunning
		m.State.PID = 4711
		m.State.RunningFor = d
		prepared(generation)(m)
	}
}

// backoff: the service of the generation ended within 30 seconds of its
// start, with status 1 after 2.1 seconds, and waits to be started again.
func backoff(generation uint64) option {
	return func(m *fleet.Member) {
		m.State.Generation = generation
		m.State.Phase = wire.PhaseBackoff
		m.State.RestartAt = now.Add(4 * time.Second)
		m.State.Restarts = 3
		m.State.LastExit = &wire.Exit{
			Generation: generation,
			Status:     1,
			At:         now.Add(-time.Second),
			Ran:        2100 * time.Millisecond,
		}
		prepared(generation)(m)
	}
}

// stopping: the service of the generation was told to end and has not.
func stopping(generation uint64) option {
	return func(m *fleet.Member) {
		m.State.Generation = generation
		m.State.Phase = wire.PhaseStopping
		prepared(generation)(m)
	}
}

// prepared: the zathras has prepared the generations, besides what it has
// prepared already.
func prepared(generations ...uint64) option {
	return func(m *fleet.Member) {
		all := append(slices.Clone(m.State.Prepared), generations...)
		slices.Sort(all)
		m.State.Prepared = slices.Compact(all)
	}
}

// exited: how the service ended last.
func exited(exit wire.Exit) option {
	return func(m *fleet.Member) {
		m.State.LastExit = &exit
	}
}

// withID gives the zathras another endpoint ID than that of its name, for
// two zathrases of one name.
func withID(of string) option {
	return func(m *fleet.Member) {
		m.ID = id(of)
	}
}

// newRollout returns a rollout of web that was made at t0.
func newRollout(state wire.RolloutState, generation uint64, parallel int) *wire.RolloutInfo {
	return &wire.RolloutInfo{
		ID:         7,
		Service:    "web",
		Generation: generation,
		Parallel:   parallel,
		State:      state,
		Created:    t0,
		CreatedBy:  id("operator"),
	}
}

// input returns what Decide is given a minute after t0, with the durations
// of the spec, no command on its way and nobody absent.
func input(r *wire.RolloutInfo, stable uint64, members ...fleet.Member) Input {
	return Input{
		Now:            now,
		Stable:         stable,
		Rollout:        r,
		Members:        members,
		Preparing:      map[wire.ID]uint64{},
		Activating:     map[wire.ID]uint64{},
		Failed:         map[wire.ID]Failure{},
		Absent:         map[wire.ID]time.Time{},
		StableAfter:    stableAfter,
		PrepareTimeout: prepareTimeout,
		RetryAfter:     retryAfter,
		AbsentGrace:    absentGrace,
	}
}

// prepare and activate are the commands to the zathras with the name.
func prepare(name string, generation uint64) Command {
	return Command{Zathras: id(name), Generation: generation}
}

func activate(name string, generation uint64) Command {
	return Command{Zathras: id(name), Activate: true, Generation: generation}
}

// show prints commands as a person wrote them: "activate web-a 2".
func show(commands []Command) string {
	if len(commands) == 0 {
		return "none"
	}
	var out []string
	for _, c := range commands {
		verb := "prepare"
		if c.Activate {
			verb = "activate"
		}
		out = append(out, fmt.Sprintf("%s %s %d", verb, nameOf(c.Zathras), c.Generation))
	}
	return strings.Join(out, ", ")
}

// check compares a decision with the one wanted, field by field.
func check(t *testing.T, got, want Decision) {
	t.Helper()
	if !slices.Equal(got.Commands, want.Commands) {
		t.Errorf("commands:\n got %s\nwant %s", show(got.Commands), show(want.Commands))
	}
	if got.State != want.State {
		t.Errorf("state: got %q, want %q", got.State, want.State)
	}
	if got.Reason != want.Reason {
		t.Errorf("reason:\n got %q\nwant %q", got.Reason, want.Reason)
	}
	if got.Prepared != want.Prepared || got.Activated != want.Activated || got.Awaited != want.Awaited {
		t.Errorf("prepared, activated, awaited: got %d, %d, %d, want %d, %d, %d",
			got.Prepared, got.Activated, got.Awaited, want.Prepared, want.Activated, want.Awaited)
	}
	if !got.Recheck.Equal(want.Recheck) {
		t.Errorf("recheck: got %v, want %v", got.Recheck, want.Recheck)
	}
}
