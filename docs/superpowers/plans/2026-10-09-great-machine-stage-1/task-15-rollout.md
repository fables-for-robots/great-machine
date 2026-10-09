### Task 15: rollout, the reconciler as a function

**Files:**
- Create: `rollout/decide.go`
- Test: `rollout/helpers_test.go` (builders of zathrases and inputs), `rollout/decide_test.go` (one decision at a time), `rollout/play_test.go` (whole rollouts against a simulated fleet)

**Interfaces:**
- Consumes, from `wire` (Task 2):

  ```go
  type ID string

  type Phase string

  const (
  	PhaseIdle     Phase = "idle"
  	PhaseRunning  Phase = "running"
  	PhaseStopping Phase = "stopping"
  	PhaseBackoff  Phase = "backoff"
  )

  // Exit is how a service ended.
  type Exit struct {
  	Generation uint64        `json:"generation"`
  	Status     int           `json:"status"`           // the exit status; -1 when a signal ended it or it never started
  	Signal     string        `json:"signal,omitempty"` // such as "SIGKILL"
  	Error      string        `json:"error,omitempty"`  // why the binary could not be started
  	At         time.Time     `json:"at"`
  	Ran        time.Duration `json:"ran"`
  }

  // Sample is the figures of a container at one time. Decide does not look at it.
  type Sample struct {
  	At     time.Time `json:"at"`
  	CPU    float64   `json:"cpu"`
  	Memory uint64    `json:"memory"`
  	// and five more figures
  }

  // State is what a zathras reports.
  type State struct {
  	Generation uint64        `json:"generation"` // 0 for none
  	Binary     string        `json:"binary,omitempty"`
  	Args       []string      `json:"args,omitempty"`
  	Phase      Phase         `json:"phase"`
  	PID        int           `json:"pid,omitempty"`         // while running
  	RunningFor time.Duration `json:"running_for,omitempty"` // while running
  	RestartAt  time.Time     `json:"restart_at"`            // while in backoff; the zero time otherwise
  	LastExit   *Exit         `json:"last_exit,omitempty"`
  	Restarts   uint64        `json:"restarts"`
  	Prepared   []uint64      `json:"prepared,omitempty"` // ascending
  	Preparing  uint64        `json:"preparing,omitempty"`
  	Sample     *Sample       `json:"sample,omitempty"`
  }

  type RolloutState string

  const (
  	RolloutPreparing  RolloutState = "preparing"
  	RolloutActivating RolloutState = "activating"
  	RolloutCompleted  RolloutState = "completed"
  	RolloutHalted     RolloutState = "halted"
  	RolloutSuperseded RolloutState = "superseded"
  )

  // UnderWay says whether s is preparing or activating.
  func (s RolloutState) UnderWay() bool

  type RolloutInfo struct {
  	ID         uint64       `json:"id"`
  	Service    string       `json:"service"`
  	Generation uint64       `json:"generation"`
  	Parallel   int          `json:"parallel"`
  	State      RolloutState `json:"state"`
  	Reason     string       `json:"reason,omitempty"`
  	Created    time.Time    `json:"created"`
  	CreatedBy  ID           `json:"created_by"`
  	Finished   time.Time    `json:"finished"` // the zero time while under way
  	Prepared  int `json:"prepared"`
  	Activated int `json:"activated"`
  	Awaited   int `json:"awaited"`
  }
  ```

- Consumes, from `fleet` (Task 14):

  ```go
  // Member is a zathras that is connected.
  type Member struct {
  	ID        wire.ID
  	Service   string
  	Name      string
  	Connected time.Time
  	State     wire.State
  	Reported  time.Time // when State came
  }
  ```

- Produces, package `rollout`:

  ```go
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
  	Failed         map[wire.ID]Failure
  	StableAfter    time.Duration // 30 seconds
  	PrepareTimeout time.Duration // 30 minutes
  	RetryAfter     time.Duration // 30 seconds
  }

  type Command struct {
  	Zathras    wire.ID
  	Activate   bool // false: prepare
  	Generation uint64
  }

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
  func Decide(in Input) Decision
  ```

**What Decide does.** T is the target, `Rollout.Generation`. A zathras *has
prepared* G when `State.Prepared` contains G. A command is *on its way* to
a zathras that `Preparing` or `Activating` has, for whatever generation;
such a zathras is sent nothing. To *send a zathras toward* G is one
command: `prepare` if it has not prepared G, `activate` if it has. The
commands of a decision are in the order of the zathrases by name, then by
ID (in `activating`: the idle ones first, then the batch), whatever the
order of `Members`, which is left as it is.

The counts, whenever `Rollout` is not nil: `Prepared` is the zathrases that
have prepared T; `Activated` those that are done; `Awaited` the others.

- **done**: `State.Generation == T`, `State.Phase` is `running` and
  `State.RunningFor >= StableAfter`. Nothing else is looked at: no time a
  zathras reports is compared with `Now`.
- **A failure of the rollout**: a `Failed` entry for T whose `At` is not
  before `Rollout.Created`. An older one is of an earlier attempt at the
  same generation, which a rollback targets again, and is ignored. (So
  whoever fills `Failed` must set `At`, from the clock that `Created` and
  `Now` come from.)

`Rollout == nil`: the zero `Decision`.

`preparing`, in this order:
1. A failure of the rollout halts it: `State` `halted`, `Reason`
   `<name>: <reason>`, of the first such zathras by name.
2. When every zathras has prepared T, also when there is none: `State`
   is `activating`, and what `activating` does below is done in the same
   decision. That may make it `completed` (nobody connected, or everybody
   done already) or `halted` instead.
3. If `Now >= Created + PrepareTimeout`: `halted`, `still preparing after 30m0s`.
4. Otherwise `prepare` to every zathras that has not prepared T and has no
   command on its way. `Recheck` is `Created + PrepareTimeout`.

`activating`, in this order:
1. The first zathras by name that failed halts the rollout. A failure of
   the rollout gives `<name>: <reason>`. A zathras with
   `State.Generation == T` in phase `backoff` gives its name and, from
   `State.LastExit` when that is of T: `exited with status 1 after 2.1s`,
   `was ended by SIGKILL after 12.3s`, or `could not be started: <error>`;
   and `its service ended soon after it was started` when there is no exit
   of T. The time is rounded to a tenth of a second, and below a second to
   a millisecond.
2. When every zathras is done, also when there is none: `completed`.
3. Every zathras that is not done is one of:
   - phase `idle`: sent toward T at once, unless a command is on its way.
     It has no place in the batch, before or after.
   - `State.Generation == T`: under way, with a place in the batch. If it
     is `running`, `Recheck` is at most `Now + StableAfter - RunningFor`.
   - a command on its way: a place in the batch (it was sent `activate`
     and has not reported it, or it is being prepared as one of the batch).
   - every other: waiting.
4. The waiting ones, by name: while the places taken are fewer than
   `Parallel`, or always when `Parallel` is 0, the next is sent toward T
   and takes a place.

A zathras that was idle and now runs T has a place in the batch like any
other until it is done: nothing tells it apart. A zathras that has gone is
not in `Members` and holds no place, whatever `Activating` says of it.

No rollout under way (`completed`, or any other state that is not
`halted`): if `Stable` is 0, nothing. Otherwise every zathras whose
generation is not `Stable` and that has no command on its way is sent
toward `Stable`, all at once; except one with a `Failed` entry for `Stable`
and `Now < At + RetryAfter`, which is left alone, and `Recheck` is the
soonest such `At + RetryAfter`. **This is Review Focus 3**: the new pod
whose `prepare` failed is tried again every 30 seconds.

`halted`: the same, for the zathrases in phase `idle` alone.

- [ ] **Step 1: Write the builders of the tests**

`rollout/helpers_test.go`:

```go
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
// of the spec and no command on its way.
func input(r *wire.RolloutInfo, stable uint64, members ...fleet.Member) Input {
	return Input{
		Now:            now,
		Stable:         stable,
		Rollout:        r,
		Members:        members,
		Preparing:      map[wire.ID]uint64{},
		Activating:     map[wire.ID]uint64{},
		Failed:         map[wire.ID]Failure{},
		StableAfter:    stableAfter,
		PrepareTimeout: prepareTimeout,
		RetryAfter:     retryAfter,
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
```

- [ ] **Step 2: Write the failing tests of one decision**

`rollout/decide_test.go`:

```go
package rollout

import (
	"math/rand/v2"
	"reflect"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/fleet"
	"github.com/fables-for-robots/great-machine/wire"
)

const hour = time.Hour

// old returns three zathrases that have run generation 1 for an hour.
func old() []fleet.Member {
	return []fleet.Member{
		member("web-a", running(1, hour)),
		member("web-b", running(1, hour)),
		member("web-c", running(1, hour)),
	}
}

// ready returns the same three when they have prepared generation 2.
func ready() []fleet.Member {
	return []fleet.Member{
		member("web-a", running(1, hour), prepared(2)),
		member("web-b", running(1, hour), prepared(2)),
		member("web-c", running(1, hour), prepared(2)),
	}
}

func TestPreparing(t *testing.T) {
	deadline := t0.Add(prepareTimeout)

	t.Run("sends prepare to all, and asks to be called at the timeout", func(t *testing.T) {
		in := input(newRollout(wire.RolloutPreparing, 2, 1), 1, old()...)
		check(t, Decide(in), Decision{
			Commands: []Command{prepare("web-a", 2), prepare("web-b", 2), prepare("web-c", 2)},
			Awaited:  3,
			Recheck:  deadline,
		})
	})

	t.Run("an idle zathras is prepared like the others", func(t *testing.T) {
		in := input(newRollout(wire.RolloutPreparing, 1, 1), 0, member("web-a"), member("web-b"))
		check(t, Decide(in), Decision{
			Commands: []Command{prepare("web-a", 1), prepare("web-b", 1)},
			Awaited:  2,
			Recheck:  deadline,
		})
	})

	t.Run("not to those a prepare is on its way to", func(t *testing.T) {
		in := input(newRollout(wire.RolloutPreparing, 2, 1), 1, old()...)
		in.Preparing[id("web-a")] = 2
		check(t, Decide(in), Decision{
			Commands: []Command{prepare("web-b", 2), prepare("web-c", 2)},
			Awaited:  3,
			Recheck:  deadline,
		})
	})

	t.Run("not to those an activate is on its way to", func(t *testing.T) {
		in := input(newRollout(wire.RolloutPreparing, 2, 1), 1, old()...)
		in.Activating[id("web-b")] = 1
		check(t, Decide(in), Decision{
			Commands: []Command{prepare("web-a", 2), prepare("web-c", 2)},
			Awaited:  3,
			Recheck:  deadline,
		})
	})

	t.Run("not to those that have prepared", func(t *testing.T) {
		members := old()
		members[1] = member("web-b", running(1, hour), prepared(2))
		in := input(newRollout(wire.RolloutPreparing, 2, 1), 1, members...)
		check(t, Decide(in), Decision{
			Commands: []Command{prepare("web-a", 2), prepare("web-c", 2)},
			Prepared: 1,
			Awaited:  3,
			Recheck:  deadline,
		})
	})

	t.Run("nothing is restarted while one is still preparing", func(t *testing.T) {
		members := ready()
		members[2] = member("web-c", running(1, hour))
		in := input(newRollout(wire.RolloutPreparing, 2, 0), 1, members...)
		in.Preparing[id("web-c")] = 2
		check(t, Decide(in), Decision{
			Prepared: 2,
			Awaited:  3,
			Recheck:  deadline,
		})
	})

	t.Run("moves to activating with the first batch in the same decision", func(t *testing.T) {
		in := input(newRollout(wire.RolloutPreparing, 2, 1), 1, ready()...)
		check(t, Decide(in), Decision{
			Commands: []Command{activate("web-a", 2)},
			State:    wire.RolloutActivating,
			Prepared: 3,
			Awaited:  3,
		})
	})

	t.Run("with no members completes at once", func(t *testing.T) {
		in := input(newRollout(wire.RolloutPreparing, 2, 1), 1)
		check(t, Decide(in), Decision{State: wire.RolloutCompleted})
	})

	t.Run("a failed prepare halts with name and reason", func(t *testing.T) {
		in := input(newRollout(wire.RolloutPreparing, 2, 1), 1, old()...)
		in.Failed[id("web-b")] = Failure{
			Generation: 2,
			Reason:     "preload: 502 Bad Gateway: fetch abc-web: no such reference",
			At:         now,
		}
		check(t, Decide(in), Decision{
			State:   wire.RolloutHalted,
			Reason:  "web-b: preload: 502 Bad Gateway: fetch abc-web: no such reference",
			Awaited: 3,
		})
	})

	t.Run("a failure of another generation does not halt", func(t *testing.T) {
		in := input(newRollout(wire.RolloutPreparing, 2, 1), 1, old()...)
		in.Failed[id("web-b")] = Failure{Generation: 1, Reason: "an old story", At: now}
		check(t, Decide(in), Decision{
			Commands: []Command{prepare("web-a", 2), prepare("web-b", 2), prepare("web-c", 2)},
			Awaited:  3,
			Recheck:  deadline,
		})
	})

	t.Run("a failure from before the rollout was made does not halt", func(t *testing.T) {
		// A rollback targets a generation that was tried before. What
		// failed then is not held against the rollout that is made now.
		in := input(newRollout(wire.RolloutPreparing, 2, 1), 1, old()...)
		in.Failed[id("web-b")] = Failure{Generation: 2, Reason: "an old story", At: t0.Add(-time.Nanosecond)}
		check(t, Decide(in), Decision{
			Commands: []Command{prepare("web-a", 2), prepare("web-b", 2), prepare("web-c", 2)},
			Awaited:  3,
			Recheck:  deadline,
		})
	})

	t.Run("the timeout halts", func(t *testing.T) {
		in := input(newRollout(wire.RolloutPreparing, 2, 1), 1, old()...)
		in.Preparing[id("web-a")] = 2
		in.Now = deadline
		check(t, Decide(in), Decision{
			State:   wire.RolloutHalted,
			Reason:  "still preparing after 30m0s",
			Awaited: 3,
		})
	})

	t.Run("not a moment before the timeout", func(t *testing.T) {
		in := input(newRollout(wire.RolloutPreparing, 2, 1), 1, old()...)
		in.Now = deadline.Add(-time.Nanosecond)
		check(t, Decide(in), Decision{
			Commands: []Command{prepare("web-a", 2), prepare("web-b", 2), prepare("web-c", 2)},
			Awaited:  3,
			Recheck:  deadline,
		})
	})

	t.Run("a rollout whose zathrases have all prepared is not timed out", func(t *testing.T) {
		in := input(newRollout(wire.RolloutPreparing, 2, 1), 1, ready()...)
		in.Now = deadline.Add(hour)
		check(t, Decide(in), Decision{
			Commands: []Command{activate("web-a", 2)},
			State:    wire.RolloutActivating,
			Prepared: 3,
			Awaited:  3,
		})
	})
}

func TestActivating(t *testing.T) {
	t.Run("parallel 1: the first by name, whatever the order they are given in", func(t *testing.T) {
		m := ready()
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, m[2], m[0], m[1])
		check(t, Decide(in), Decision{
			Commands: []Command{activate("web-a", 2)},
			Prepared: 3,
			Awaited:  3,
		})
	})

	t.Run("parallel 1: nobody while the activate is on its way", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, ready()...)
		in.Activating[id("web-a")] = 2
		check(t, Decide(in), Decision{Prepared: 3, Awaited: 3})
	})

	t.Run("parallel 1: nobody while the first is under way, and recheck when it is done", func(t *testing.T) {
		m := ready()
		m[0] = member("web-a", running(2, 10*time.Second))
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, m...)
		check(t, Decide(in), Decision{
			Prepared: 3,
			Awaited:  3,
			Recheck:  now.Add(20 * time.Second),
		})
	})

	t.Run("parallel 1: not a moment before the first has run 30 seconds", func(t *testing.T) {
		m := ready()
		m[0] = member("web-a", running(2, stableAfter-time.Nanosecond))
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, m...)
		check(t, Decide(in), Decision{
			Prepared: 3,
			Awaited:  3,
			Recheck:  now.Add(time.Nanosecond),
		})
	})

	t.Run("parallel 1: the next when the first has run 30 seconds", func(t *testing.T) {
		m := ready()
		m[0] = member("web-a", running(2, stableAfter))
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, m...)
		check(t, Decide(in), Decision{
			Commands:  []Command{activate("web-b", 2)},
			Prepared:  3,
			Activated: 1,
			Awaited:   2,
		})
	})

	t.Run("parallel 1: a zathras that is stopping on the target is under way", func(t *testing.T) {
		m := ready()
		m[0] = member("web-a", stopping(2))
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, m...)
		check(t, Decide(in), Decision{Prepared: 3, Awaited: 3})
	})

	t.Run("parallel 2: two at once", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 2), 1, ready()...)
		check(t, Decide(in), Decision{
			Commands: []Command{activate("web-a", 2), activate("web-b", 2)},
			Prepared: 3,
			Awaited:  3,
		})
	})

	t.Run("parallel 2: one more beside the one under way", func(t *testing.T) {
		m := ready()
		m[0] = member("web-a", running(2, 5*time.Second))
		in := input(newRollout(wire.RolloutActivating, 2, 2), 1, m...)
		check(t, Decide(in), Decision{
			Commands: []Command{activate("web-b", 2)},
			Prepared: 3,
			Awaited:  3,
			Recheck:  now.Add(25 * time.Second),
		})
	})

	t.Run("parallel 2: recheck when the first of two is done", func(t *testing.T) {
		m := ready()
		m[0] = member("web-a", running(2, 5*time.Second))
		m[1] = member("web-b", running(2, 20*time.Second))
		in := input(newRollout(wire.RolloutActivating, 2, 2), 1, m...)
		check(t, Decide(in), Decision{
			Prepared: 3,
			Awaited:  3,
			Recheck:  now.Add(10 * time.Second),
		})
	})

	t.Run("parallel 0: all at once", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 0), 1, ready()...)
		check(t, Decide(in), Decision{
			Commands: []Command{activate("web-a", 2), activate("web-b", 2), activate("web-c", 2)},
			Prepared: 3,
			Awaited:  3,
		})
	})

	t.Run("parallel larger than the fleet: all at once", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 50), 1, ready()...)
		check(t, Decide(in), Decision{
			Commands: []Command{activate("web-a", 2), activate("web-b", 2), activate("web-c", 2)},
			Prepared: 3,
			Awaited:  3,
		})
	})

	t.Run("two zathrases of one name go by their IDs", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1,
			member("web", withID("zz"), running(1, hour), prepared(2)),
			member("web", withID("aa"), running(1, hour), prepared(2)),
		)
		check(t, Decide(in), Decision{
			Commands: []Command{activate("aa", 2)},
			Prepared: 2,
			Awaited:  2,
		})
	})

	t.Run("completed when all are done", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1,
			member("web-a", running(2, hour)),
			member("web-b", running(2, 31*time.Second)),
			member("web-c", running(2, stableAfter)),
		)
		check(t, Decide(in), Decision{
			State:     wire.RolloutCompleted,
			Prepared:  3,
			Activated: 3,
		})
	})

	t.Run("not completed while the last is under way", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1,
			member("web-a", running(2, hour)),
			member("web-b", running(2, hour)),
			member("web-c", running(2, 29*time.Second)),
		)
		check(t, Decide(in), Decision{
			Prepared:  3,
			Activated: 2,
			Awaited:   1,
			Recheck:   now.Add(time.Second),
		})
	})

	t.Run("with no members completes", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1)
		check(t, Decide(in), Decision{State: wire.RolloutCompleted})
	})
}

func TestActivatingFailures(t *testing.T) {
	// halted is the decision wanted of web-a, web-b and web-c with web-a
	// failed: nothing is sent, though web-b and web-c wait.
	halted := func(reason string) Decision {
		return Decision{State: wire.RolloutHalted, Reason: reason, Prepared: 3, Awaited: 3}
	}

	t.Run("a zathras in backoff on the target halts with its exit status", func(t *testing.T) {
		m := ready()
		m[0] = member("web-a", backoff(2))
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, m...)
		check(t, Decide(in), halted("web-a: exited with status 1 after 2.1s"))
	})

	t.Run("with the signal that ended it", func(t *testing.T) {
		m := ready()
		m[0] = member("web-a", backoff(2), exited(wire.Exit{
			Generation: 2, Status: -1, Signal: "SIGKILL", At: now, Ran: 12340 * time.Millisecond,
		}))
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, m...)
		check(t, Decide(in), halted("web-a: was ended by SIGKILL after 12.3s"))
	})

	t.Run("with why it could not be started", func(t *testing.T) {
		m := ready()
		m[0] = member("web-a", backoff(2), exited(wire.Exit{
			Generation: 2, Status: -1, Error: "fork/exec /nix/store/abc-web/bin/web: exec format error", At: now,
		}))
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, m...)
		check(t, Decide(in), halted("web-a: could not be started: fork/exec /nix/store/abc-web/bin/web: exec format error"))
	})

	t.Run("with an exit of milliseconds", func(t *testing.T) {
		m := ready()
		m[0] = member("web-a", backoff(2), exited(wire.Exit{
			Generation: 2, Status: 127, At: now, Ran: 3456789 * time.Nanosecond,
		}))
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, m...)
		check(t, Decide(in), halted("web-a: exited with status 127 after 3ms"))
	})

	t.Run("without an exit of the target", func(t *testing.T) {
		m := ready()
		m[0] = member("web-a", backoff(2), exited(wire.Exit{Generation: 1, Status: 0, At: now, Ran: hour}))
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, m...)
		check(t, Decide(in), halted("web-a: its service ended soon after it was started"))
	})

	t.Run("a refused activate halts", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, ready()...)
		in.Failed[id("web-a")] = Failure{
			Generation: 2,
			Activate:   true,
			Reason:     "/nix/store/abc-web/bin/web: not an executable file",
			At:         now,
		}
		check(t, Decide(in), halted("web-a: /nix/store/abc-web/bin/web: not an executable file"))
	})

	t.Run("a failed prepare halts in this phase as well", func(t *testing.T) {
		m := ready()
		m[0] = member("web-a", running(1, hour))
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, m...)
		in.Failed[id("web-a")] = Failure{Generation: 2, Reason: "preload: 503 Service Unavailable", At: now}
		d := halted("web-a: preload: 503 Service Unavailable")
		d.Prepared = 2
		check(t, Decide(in), d)
	})

	t.Run("the first by name is named when two have failed", func(t *testing.T) {
		m := ready()
		m[1] = member("web-b", backoff(2))
		m[2] = member("web-c", backoff(2), exited(wire.Exit{Generation: 2, Status: 9, Ran: time.Second}))
		in := input(newRollout(wire.RolloutActivating, 2, 0), 1, m[2], m[1], m[0])
		check(t, Decide(in), halted("web-b: exited with status 1 after 2.1s"))
	})

	t.Run("a zathras in backoff on another generation has not failed", func(t *testing.T) {
		// It crashed on what it ran before the rollout: it is waiting, and
		// its turn comes like any other's.
		m := ready()
		m[0] = member("web-a", backoff(1), prepared(2))
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, m...)
		check(t, Decide(in), Decision{
			Commands: []Command{activate("web-a", 2)},
			Prepared: 3,
			Awaited:  3,
		})
	})

	t.Run("a zathras that is done and one that failed: halted", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1,
			member("web-a", running(2, hour)),
			member("web-b", backoff(2)),
			member("web-c", running(1, hour), prepared(2)),
		)
		check(t, Decide(in), Decision{
			State:     wire.RolloutHalted,
			Reason:    "web-b: exited with status 1 after 2.1s",
			Prepared:  3,
			Activated: 1,
			Awaited:   2,
		})
	})
}

func TestJoiningAndLeaving(t *testing.T) {
	// under returns web-a under way on generation 2, with web-b and web-c
	// waiting; done returns the same when web-a is done.
	under := func() []fleet.Member {
		m := ready()
		m[0] = member("web-a", running(2, 10*time.Second))
		return m
	}
	done := func() []fleet.Member {
		m := ready()
		m[0] = member("web-a", running(2, hour))
		return m
	}
	recheck := now.Add(20 * time.Second)

	t.Run("an idle zathras that joins is prepared at once", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, append(under(), member("web-n"))...)
		check(t, Decide(in), Decision{
			Commands: []Command{prepare("web-n", 2)},
			Prepared: 3,
			Awaited:  4,
			Recheck:  recheck,
		})
	})

	t.Run("and activated when it has prepared, though the batch is full", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, append(under(), member("web-n", prepared(2)))...)
		check(t, Decide(in), Decision{
			Commands: []Command{activate("web-n", 2)},
			Prepared: 4,
			Awaited:  4,
			Recheck:  recheck,
		})
	})

	t.Run("an idle zathras takes no place in the batch", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, append(done(), member("web-n", prepared(2)))...)
		check(t, Decide(in), Decision{
			Commands:  []Command{activate("web-n", 2), activate("web-b", 2)},
			Prepared:  4,
			Activated: 1,
			Awaited:   3,
		})
	})

	t.Run("nor while its prepare is on its way", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, append(done(), member("web-n"))...)
		in.Preparing[id("web-n")] = 2
		check(t, Decide(in), Decision{
			Commands:  []Command{activate("web-b", 2)},
			Prepared:  3,
			Activated: 1,
			Awaited:   3,
		})
	})

	t.Run("nor while its activate is on its way", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, append(done(), member("web-n", prepared(2)))...)
		in.Activating[id("web-n")] = 2
		check(t, Decide(in), Decision{
			Commands:  []Command{activate("web-b", 2)},
			Prepared:  4,
			Activated: 1,
			Awaited:   3,
		})
	})

	t.Run("a zathras that joins running another generation waits its turn", func(t *testing.T) {
		// web-0 comes before web-b by name, and has not prepared.
		late := member("web-0", running(1, hour))
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, append(under(), late)...)
		check(t, Decide(in), Decision{Prepared: 3, Awaited: 4, Recheck: recheck})
	})

	t.Run("a waiting zathras that has not prepared is prepared first, in its turn", func(t *testing.T) {
		late := member("web-0", running(1, hour))
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, append(done(), late)...)
		check(t, Decide(in), Decision{
			Commands:  []Command{prepare("web-0", 2)},
			Prepared:  3,
			Activated: 1,
			Awaited:   3,
		})
	})

	t.Run("and holds its place in the batch while it prepares", func(t *testing.T) {
		late := member("web-0", running(1, hour))
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, append(done(), late)...)
		in.Preparing[id("web-0")] = 2
		check(t, Decide(in), Decision{Prepared: 3, Activated: 1, Awaited: 3})
	})

	t.Run("then it is activated", func(t *testing.T) {
		late := member("web-0", running(1, hour), prepared(2))
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, append(done(), late)...)
		check(t, Decide(in), Decision{
			Commands:  []Command{activate("web-0", 2)},
			Prepared:  4,
			Activated: 1,
			Awaited:   3,
		})
	})

	t.Run("with parallel 2 one prepares while the next is activated", func(t *testing.T) {
		late := member("web-0", running(1, hour))
		in := input(newRollout(wire.RolloutActivating, 2, 2), 1, append(done(), late)...)
		check(t, Decide(in), Decision{
			Commands:  []Command{prepare("web-0", 2), activate("web-b", 2)},
			Prepared:  3,
			Activated: 1,
			Awaited:   3,
		})
	})

	t.Run("a zathras that leaves while under way frees its place", func(t *testing.T) {
		m := under()
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, m[1], m[2])
		// What draal still holds of the one that went does not count.
		in.Activating[id("web-a")] = 2
		check(t, Decide(in), Decision{
			Commands: []Command{activate("web-b", 2)},
			Prepared: 2,
			Awaited:  2,
		})
	})

	t.Run("one that comes back running the target for a short time is under way", func(t *testing.T) {
		m := ready()
		m[0] = member("web-a", running(2, 5*time.Second))
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, m...)
		check(t, Decide(in), Decision{Prepared: 3, Awaited: 3, Recheck: now.Add(25 * time.Second)})
	})

	t.Run("one that comes back running the target for long is done", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, done()...)
		check(t, Decide(in), Decision{
			Commands:  []Command{activate("web-b", 2)},
			Prepared:  3,
			Activated: 1,
			Awaited:   2,
		})
	})

	t.Run("one that comes back running the old generation waits again", func(t *testing.T) {
		// Its activate was lost with the connection. It is the first by
		// name, and is sent it again.
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, ready()...)
		check(t, Decide(in), Decision{
			Commands: []Command{activate("web-a", 2)},
			Prepared: 3,
			Awaited:  3,
		})
	})

	t.Run("when the last one that is not done leaves, completed", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, member("web-a", running(2, hour)))
		check(t, Decide(in), Decision{State: wire.RolloutCompleted, Prepared: 1, Activated: 1})
	})
}

func TestRollback(t *testing.T) {
	// Generation 2 was rolled out to web-c and web-d when the operator
	// rolled back: the target is the stable generation, 1.
	fleetOf := func() []fleet.Member {
		return []fleet.Member{
			member("web-a", running(1, hour)),
			member("web-b", running(1, hour)),
			member("web-c", prepared(1), running(2, hour)),
			member("web-d", prepared(1), backoff(2)),
		}
	}

	t.Run("those that run the target for long are done and are not restarted", func(t *testing.T) {
		in := input(newRollout(wire.RolloutPreparing, 1, 1), 1, fleetOf()...)
		check(t, Decide(in), Decision{
			Commands:  []Command{activate("web-c", 1)},
			State:     wire.RolloutActivating,
			Prepared:  4,
			Activated: 2,
			Awaited:   2,
		})
	})

	t.Run("the others are moved, some at a time", func(t *testing.T) {
		m := fleetOf()
		m[2] = member("web-c", running(1, stableAfter), prepared(2))
		in := input(newRollout(wire.RolloutActivating, 1, 1), 1, m...)
		check(t, Decide(in), Decision{
			Commands:  []Command{activate("web-d", 1)},
			Prepared:  4,
			Activated: 3,
			Awaited:   1,
		})
	})

	t.Run("a rollback of a fleet that runs the target already completes at once", func(t *testing.T) {
		m := fleetOf()
		in := input(newRollout(wire.RolloutPreparing, 1, 1), 1, m[0], m[1])
		check(t, Decide(in), Decision{State: wire.RolloutCompleted, Prepared: 2, Activated: 2})
	})

	t.Run("a zathras that has to prepare the target again is prepared first", func(t *testing.T) {
		// web-c was restarted as a container since it ran generation 1: it
		// has prepared only what it runs now.
		m := fleetOf()
		m[2] = member("web-c", running(2, hour))
		in := input(newRollout(wire.RolloutPreparing, 1, 1), 1, m...)
		check(t, Decide(in), Decision{
			Commands:  []Command{prepare("web-c", 1)},
			Prepared:  3,
			Activated: 2,
			Awaited:   2,
			Recheck:   t0.Add(prepareTimeout),
		})
	})
}

func TestCompleted(t *testing.T) {
	t.Run("nothing is sent to those on the stable generation", func(t *testing.T) {
		in := input(newRollout(wire.RolloutCompleted, 2, 1), 2,
			member("web-a", running(2, hour)),
			member("web-b", running(2, time.Second)),
			member("web-c", backoff(2)),
			member("web-d", stopping(2)),
		)
		check(t, Decide(in), Decision{Prepared: 4, Activated: 1, Awaited: 3})
	})

	t.Run("a new idle zathras is sent prepare for the stable generation", func(t *testing.T) {
		in := input(newRollout(wire.RolloutCompleted, 2, 1), 2,
			member("web-a", running(2, hour)),
			member("web-n"),
		)
		check(t, Decide(in), Decision{
			Commands:  []Command{prepare("web-n", 2)},
			Prepared:  1,
			Activated: 1,
			Awaited:   1,
		})
	})

	t.Run("and then activate", func(t *testing.T) {
		in := input(newRollout(wire.RolloutCompleted, 2, 1), 2,
			member("web-a", running(2, hour)),
			member("web-n", prepared(2)),
		)
		check(t, Decide(in), Decision{
			Commands:  []Command{activate("web-n", 2)},
			Prepared:  2,
			Activated: 1,
			Awaited:   1,
		})
	})

	t.Run("every zathras on another generation is converged, whatever parallel is", func(t *testing.T) {
		in := input(newRollout(wire.RolloutCompleted, 2, 1), 2,
			member("web-d", running(1, hour)),
			member("web-c", running(1, hour), prepared(2)),
			member("web-b", backoff(1)),
			member("web-a", running(2, hour)),
		)
		check(t, Decide(in), Decision{
			Commands:  []Command{prepare("web-b", 2), activate("web-c", 2), prepare("web-d", 2)},
			Prepared:  2,
			Activated: 1,
			Awaited:   3,
		})
	})

	t.Run("nobody is sent a command while one is on its way", func(t *testing.T) {
		in := input(newRollout(wire.RolloutCompleted, 2, 1), 2,
			member("web-m"),
			member("web-n", prepared(2)),
		)
		in.Preparing[id("web-m")] = 2
		in.Activating[id("web-n")] = 2
		check(t, Decide(in), Decision{Prepared: 1, Awaited: 2})
	})

	t.Run("the stable generation is what is sent, not the target of the row", func(t *testing.T) {
		// A superseded rollout is never the last one, but if Decide is
		// given one it converges on the stable generation all the same.
		in := input(newRollout(wire.RolloutSuperseded, 3, 1), 2, member("web-n"))
		check(t, Decide(in), Decision{
			Commands: []Command{prepare("web-n", 2)},
			Awaited:  1,
		})
	})

	// Review Focus 3: a new pod whose prepare fails while no rollout is
	// under way is tried again every 30 seconds until it works; it does not
	// stay idle for ever.
	t.Run("Review Focus 3: a failed prepare is not sent again before RetryAfter", func(t *testing.T) {
		failedAt := now.Add(-10 * time.Second)
		in := input(newRollout(wire.RolloutCompleted, 2, 1), 2,
			member("web-a", running(2, hour)),
			member("web-n"),
		)
		in.Failed[id("web-n")] = Failure{Generation: 2, Reason: "preload: 503 Service Unavailable", At: failedAt}
		check(t, Decide(in), Decision{
			Prepared:  1,
			Activated: 1,
			Awaited:   1,
			Recheck:   failedAt.Add(retryAfter),
		})

		in.Now = failedAt.Add(retryAfter - time.Nanosecond)
		check(t, Decide(in), Decision{
			Prepared:  1,
			Activated: 1,
			Awaited:   1,
			Recheck:   failedAt.Add(retryAfter),
		})
	})

	t.Run("Review Focus 3: it is sent again at RetryAfter", func(t *testing.T) {
		failedAt := now.Add(-retryAfter)
		in := input(newRollout(wire.RolloutCompleted, 2, 1), 2,
			member("web-a", running(2, hour)),
			member("web-n"),
		)
		in.Failed[id("web-n")] = Failure{Generation: 2, Reason: "preload: 503 Service Unavailable", At: failedAt}
		check(t, Decide(in), Decision{
			Commands:  []Command{prepare("web-n", 2)},
			Prepared:  1,
			Activated: 1,
			Awaited:   1,
		})
	})

	t.Run("Review Focus 3: again and again until it works, and the rollout stays completed", func(t *testing.T) {
		// The store cannot be reached for five minutes. Decide is called
		// at every Recheck, as draal does, and whenever a prepare has
		// failed.
		in := input(newRollout(wire.RolloutCompleted, 2, 1), 2,
			member("web-a", running(2, hour)),
			member("web-n"),
		)
		start := in.Now
		var sentAt []time.Duration
		for len(sentAt) < 10 {
			d := Decide(in)
			if d.State != "" {
				t.Fatalf("the rollout was moved to %q for a prepare that failed", d.State)
			}
			switch {
			case len(d.Commands) == 1 && d.Commands[0] == prepare("web-n", 2):
				// The prepare fails at once.
				sentAt = append(sentAt, in.Now.Sub(start))
				in.Failed[id("web-n")] = Failure{Generation: 2, Reason: "preload: 503", At: in.Now}
			case len(d.Commands) == 0 && d.Recheck.After(in.Now):
				in.Now = d.Recheck
			default:
				t.Fatalf("at %v: commands %s, recheck %v: want one prepare, or a recheck that is later",
					in.Now.Sub(start), show(d.Commands), d.Recheck)
			}
		}
		for i, at := range sentAt {
			if want := time.Duration(i) * retryAfter; at != want {
				t.Errorf("prepare %d was sent after %v, want %v", i+1, at, want)
			}
		}

		// The store is back: the prepare works, and the activate follows.
		delete(in.Failed, id("web-n"))
		in.Members[1] = member("web-n", prepared(2))
		check(t, Decide(in), Decision{
			Commands:  []Command{activate("web-n", 2)},
			Prepared:  2,
			Activated: 1,
			Awaited:   1,
		})
	})

	t.Run("Review Focus 3: a refused activate is tried again as well", func(t *testing.T) {
		in := input(newRollout(wire.RolloutCompleted, 2, 1), 2, member("web-n", prepared(2)))
		in.Failed[id("web-n")] = Failure{Generation: 2, Activate: true, Reason: "not an executable file", At: now}
		check(t, Decide(in), Decision{Prepared: 1, Awaited: 1, Recheck: now.Add(retryAfter)})
		in.Now = now.Add(retryAfter)
		check(t, Decide(in), Decision{
			Commands: []Command{activate("web-n", 2)},
			Prepared: 1,
			Awaited:  1,
		})
	})

	t.Run("Review Focus 3: a failure of another generation holds nothing back", func(t *testing.T) {
		in := input(newRollout(wire.RolloutCompleted, 2, 1), 2, member("web-n"))
		in.Failed[id("web-n")] = Failure{Generation: 1, Reason: "preload: 503", At: now}
		check(t, Decide(in), Decision{
			Commands: []Command{prepare("web-n", 2)},
			Awaited:  1,
		})
	})

	t.Run("Review Focus 3: the recheck is the soonest of several", func(t *testing.T) {
		in := input(newRollout(wire.RolloutCompleted, 2, 1), 2,
			member("web-m"), member("web-n"), member("web-o"),
		)
		in.Failed[id("web-m")] = Failure{Generation: 2, Reason: "preload: 503", At: now.Add(-5 * time.Second)}
		in.Failed[id("web-n")] = Failure{Generation: 2, Reason: "preload: 503", At: now.Add(-20 * time.Second)}
		check(t, Decide(in), Decision{
			Commands: []Command{prepare("web-o", 2)},
			Awaited:  3,
			Recheck:  now.Add(10 * time.Second),
		})
	})
}

func TestHalted(t *testing.T) {
	// Generation 3 halted on web-b, after web-a was moved. Generation 2 is
	// stable.
	frozen := func() []fleet.Member {
		return []fleet.Member{
			member("web-a", running(3, hour)),
			member("web-b", backoff(3)),
			member("web-c", running(2, hour), prepared(3)),
			member("web-d", running(1, hour)),
		}
	}
	halted := func() *wire.RolloutInfo {
		r := newRollout(wire.RolloutHalted, 3, 1)
		r.Reason = "web-b: exited with status 1 after 2.1s"
		return r
	}

	t.Run("nobody is touched", func(t *testing.T) {
		in := input(halted(), 2, frozen()...)
		check(t, Decide(in), Decision{Prepared: 3, Activated: 1, Awaited: 3})
	})

	t.Run("but an idle zathras is sent the stable generation", func(t *testing.T) {
		in := input(halted(), 2, append(frozen(), member("web-n"))...)
		check(t, Decide(in), Decision{
			Commands:  []Command{prepare("web-n", 2)},
			Prepared:  3,
			Activated: 1,
			Awaited:   4,
		})
		in = input(halted(), 2, append(frozen(), member("web-n", prepared(2)))...)
		check(t, Decide(in), Decision{
			Commands:  []Command{activate("web-n", 2)},
			Prepared:  3,
			Activated: 1,
			Awaited:   4,
		})
	})

	t.Run("not while a command is on its way to it", func(t *testing.T) {
		in := input(halted(), 2, member("web-n"))
		in.Preparing[id("web-n")] = 2
		check(t, Decide(in), Decision{Awaited: 1})
	})

	t.Run("and tried again after a failure like any other", func(t *testing.T) {
		in := input(halted(), 2, member("web-n"))
		in.Failed[id("web-n")] = Failure{Generation: 2, Reason: "preload: 503", At: now}
		check(t, Decide(in), Decision{Awaited: 1, Recheck: now.Add(retryAfter)})
		in.Now = now.Add(retryAfter)
		check(t, Decide(in), Decision{
			Commands: []Command{prepare("web-n", 2)},
			Awaited:  1,
		})
	})

	t.Run("the failure that halted it does not move the rollout again", func(t *testing.T) {
		in := input(halted(), 2, frozen()...)
		in.Failed[id("web-b")] = Failure{Generation: 3, Activate: true, Reason: "no", At: now}
		check(t, Decide(in), Decision{Prepared: 3, Activated: 1, Awaited: 3})
	})

	t.Run("without a stable generation nothing at all is sent", func(t *testing.T) {
		in := input(newRollout(wire.RolloutHalted, 1, 1), 0,
			member("web-a", backoff(1)),
			member("web-n"),
			member("web-o", prepared(1)),
		)
		check(t, Decide(in), Decision{Prepared: 2, Awaited: 3})
	})
}

func TestNoRollout(t *testing.T) {
	t.Run("a service that never had a rollout has nothing to run", func(t *testing.T) {
		in := input(nil, 0, member("web-a"), member("web-b", prepared(1)))
		if d := Decide(in); !reflect.DeepEqual(d, Decision{}) {
			t.Errorf("Decide = %+v, want nothing at all", d)
		}
	})

	t.Run("nor with nobody connected", func(t *testing.T) {
		if d := Decide(Input{Now: now}); !reflect.DeepEqual(d, Decision{}) {
			t.Errorf("Decide = %+v, want nothing at all", d)
		}
	})

	t.Run("a completed rollout with nobody connected asks for nothing", func(t *testing.T) {
		check(t, Decide(input(newRollout(wire.RolloutCompleted, 2, 1), 2)), Decision{})
	})
}

// scenarios is an input of every kind, each with zathrases that report an
// exit, a restart time and a sample.
func scenarios() map[string]Input {
	sample := func(m fleet.Member) fleet.Member {
		m.State.Sample = &wire.Sample{At: now.Add(-time.Second), CPU: 0.5, Memory: 1 << 20}
		return m
	}
	crashed := exited(wire.Exit{Generation: 1, Status: 2, At: now.Add(-time.Minute), Ran: hour})

	preparing := input(newRollout(wire.RolloutPreparing, 2, 1), 1,
		sample(member("web-a", running(1, hour), crashed)),
		sample(member("web-b", backoff(1))),
		sample(member("web-c", running(1, hour), prepared(2))),
	)
	activating := input(newRollout(wire.RolloutActivating, 2, 2), 1,
		sample(member("web-a", running(2, hour), crashed)),
		sample(member("web-b", running(2, 10*time.Second), crashed)),
		sample(member("web-c", running(1, hour), prepared(2), crashed)),
		sample(member("web-d", backoff(1), prepared(2))),
		sample(member("web-e")),
	)
	failing := input(newRollout(wire.RolloutActivating, 2, 1), 1,
		sample(member("web-a", running(2, hour))),
		sample(member("web-b", backoff(2))),
		sample(member("web-c", running(1, hour), prepared(2))),
	)
	completing := input(newRollout(wire.RolloutActivating, 2, 1), 1,
		sample(member("web-a", running(2, hour), crashed)),
		sample(member("web-b", running(2, stableAfter))),
	)
	completed := input(newRollout(wire.RolloutCompleted, 2, 1), 2,
		sample(member("web-a", running(2, hour))),
		sample(member("web-b", backoff(1))),
		sample(member("web-c")),
		sample(member("web-d", prepared(2))),
	)
	completed.Failed[id("web-c")] = Failure{Generation: 2, Reason: "preload: 503", At: now.Add(-time.Second)}
	halted := input(newRollout(wire.RolloutHalted, 3, 1), 2,
		sample(member("web-a", running(3, hour))),
		sample(member("web-b", backoff(3))),
		sample(member("web-c")),
	)
	return map[string]Input{
		"preparing":  preparing,
		"activating": activating,
		"failing":    failing,
		"completing": completing,
		"completed":  completed,
		"halted":     halted,
	}
}

// skewed returns the input as it would be if the clocks of the zathrases
// were off by d: every time a zathras reports is moved, and nothing else.
func skewed(in Input, d time.Duration) Input {
	out := in
	out.Members = make([]fleet.Member, len(in.Members))
	for i, m := range in.Members {
		if !m.State.RestartAt.IsZero() {
			m.State.RestartAt = m.State.RestartAt.Add(d)
		}
		if m.State.LastExit != nil {
			exit := *m.State.LastExit
			exit.At = exit.At.Add(d)
			m.State.LastExit = &exit
		}
		if m.State.Sample != nil {
			sample := *m.State.Sample
			sample.At = sample.At.Add(d)
			m.State.Sample = &sample
		}
		out.Members[i] = m
	}
	return out
}

// The clock of a pod is not that of draal. Zathrases whose times are years
// off in either direction are decided exactly like the same zathrases with
// sane times.
func TestClockSkew(t *testing.T) {
	const year = 365 * 24 * time.Hour
	for name, in := range scenarios() {
		t.Run(name, func(t *testing.T) {
			want := Decide(in)
			if len(want.Commands) == 0 && want.State == "" {
				t.Fatalf("the scenario decides nothing, and so shows nothing")
			}
			for _, skew := range []time.Duration{-50 * year, -3 * year, -time.Hour, time.Hour, 3 * year, 50 * year} {
				if got := Decide(skewed(in, skew)); !reflect.DeepEqual(got, want) {
					t.Errorf("with clocks off by %v:\n got %+v\nwant %+v", skew, got, want)
				}
			}
		})
	}
}

func TestDeterminism(t *testing.T) {
	for name, in := range scenarios() {
		t.Run(name, func(t *testing.T) {
			want := Decide(in)
			if got := Decide(in); !reflect.DeepEqual(got, want) {
				t.Errorf("the same input twice:\n got %+v\nwant %+v", got, want)
			}

			given := make([]fleet.Member, len(in.Members))
			copy(given, in.Members)
			random := rand.New(rand.NewPCG(1, 2))
			for range 50 {
				shuffled := in
				shuffled.Members = make([]fleet.Member, len(in.Members))
				copy(shuffled.Members, in.Members)
				random.Shuffle(len(shuffled.Members), func(i, j int) {
					shuffled.Members[i], shuffled.Members[j] = shuffled.Members[j], shuffled.Members[i]
				})
				if got := Decide(shuffled); !reflect.DeepEqual(got, want) {
					t.Fatalf("with the members in another order:\n got %+v\nwant %+v", got, want)
				}
			}
			// Decide leaves what it is given as it was.
			if !reflect.DeepEqual(in.Members, given) {
				t.Error("Decide changed the order of the members it was given")
			}
		})
	}
}

// Whatever the input, no zathras is sent two commands, and none that a
// command is on its way to is sent one.
func TestNeverTwoCommandsToOne(t *testing.T) {
	for name, in := range scenarios() {
		t.Run(name, func(t *testing.T) {
			for _, parallel := range []int{0, 1, 2, 9} {
				for _, busy := range []string{"", "web-a", "web-c", "web-d", "web-e"} {
					in.Rollout.Parallel = parallel
					in.Preparing = map[wire.ID]uint64{}
					in.Activating = map[wire.ID]uint64{}
					if busy != "" {
						in.Preparing[id(busy)] = in.Rollout.Generation
					}
					seen := map[wire.ID]bool{}
					for _, c := range Decide(in).Commands {
						if seen[c.Zathras] {
							t.Errorf("parallel %d: two commands to %s", parallel, nameOf(c.Zathras))
						}
						seen[c.Zathras] = true
						if nameOf(c.Zathras) == busy {
							t.Errorf("parallel %d: a command to %s, which is being prepared", parallel, busy)
						}
					}
				}
			}
		})
	}
}
```

- [ ] **Step 3: Run the tests to see them fail**

Run: `go test ./rollout/`
Expected: FAIL — the package does not build: `undefined: Input`, `undefined: Command`, `undefined: Decision`, `undefined: Failure`, `undefined: Decide`

- [ ] **Step 4: Write Decide**

`rollout/decide.go`:

```go
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
	Failed         map[wire.ID]Failure
	StableAfter    time.Duration // 30 seconds
	PrepareTimeout time.Duration // 30 minutes
	RetryAfter     time.Duration // 30 seconds
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
	// When every connected zathras is done, the rollout is completed.
	if d.out.Awaited == 0 {
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
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `go test -race ./rollout/ -v`
Expected: PASS — `TestPreparing`, `TestActivating`, `TestActivatingFailures`, `TestJoiningAndLeaving`, `TestRollback`, `TestCompleted` (with the six subtests named `Review Focus 3: …`), `TestHalted`, `TestNoRollout`, `TestClockSkew`, `TestDeterminism`, `TestNeverTwoCommandsToOne`, every subtest of each

Run: `test -z "$(gofmt -l .)" && go vet ./...`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add rollout/
git commit -m "Rollout: Decide, the reconciler as a function"
```

- [ ] **Step 7: Write the tests that play whole rollouts**

These tests put a simulated fleet and a clock of the test around `Decide`
and carry its decisions out, step by step, as draal will. They pin what
only shows over many decisions: that never more than `parallel` zathrases
are restarting, the order, that a draal which forgot everything in the
middle carries on, and that nobody is sent `activate` for what it runs.

`rollout/play_test.go`:

```go
package rollout

import (
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/fleet"
	"github.com/fables-for-robots/great-machine/wire"
)

// sim is a service with a fleet of fake zathrases and a clock of the test.
// It does what draal does around Decide: it keeps the rollout and the
// stable generation as the database would, remembers the commands that are
// on their way, and carries the decisions out.
type sim struct {
	t       *testing.T
	now     time.Time
	stable  uint64
	rollout *wire.RolloutInfo
	members []*fleet.Member

	preparing  map[wire.ID]uint64
	activating map[wire.ID]uint64
	failed     map[wire.ID]Failure

	// held: the commands of a decision stay on their way until deliver and
	// acknowledge are called. Otherwise decide answers them at once.
	held bool
	// crashing: a generation whose binary exits at once.
	crashing uint64

	undelivered []Command
	sent        []Command // every command there was
	most        int       // the most zathrases that were restarting at once
}

func newSim(t *testing.T, stable uint64, members ...fleet.Member) *sim {
	s := &sim{
		t:          t,
		now:        now,
		stable:     stable,
		preparing:  map[wire.ID]uint64{},
		activating: map[wire.ID]uint64{},
		failed:     map[wire.ID]Failure{},
	}
	for _, m := range members {
		s.members = append(s.members, &m)
	}
	return s
}

// begin makes a rollout of the generation, as db.NewRollout does: the one
// that is under way is superseded.
func (s *sim) begin(generation uint64, parallel int) {
	r := newRollout(wire.RolloutPreparing, generation, parallel)
	r.Created = s.now
	if s.rollout != nil {
		r.ID = s.rollout.ID + 1
	}
	s.rollout = r
}

func (s *sim) member(id wire.ID) *fleet.Member {
	for _, m := range s.members {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// leave takes the zathras with the name out of the fleet, and returns it.
func (s *sim) leave(name string) fleet.Member {
	m := s.member(id(name))
	if m == nil {
		s.t.Fatalf("%s is not there", name)
	}
	s.members = slices.DeleteFunc(s.members, func(o *fleet.Member) bool { return o == m })
	delete(s.preparing, m.ID)
	delete(s.activating, m.ID)
	delete(s.failed, m.ID)
	return *m
}

func (s *sim) join(m fleet.Member) {
	s.members = append(s.members, &m)
}

// decide calls Decide with the service as it is, checks what no decision
// may do, and carries the decision out.
func (s *sim) decide() Decision {
	s.t.Helper()
	in := Input{
		Now:            s.now,
		Stable:         s.stable,
		Rollout:        s.rollout,
		Preparing:      maps.Clone(s.preparing),
		Activating:     maps.Clone(s.activating),
		Failed:         maps.Clone(s.failed),
		StableAfter:    stableAfter,
		PrepareTimeout: prepareTimeout,
		RetryAfter:     retryAfter,
	}
	for _, m := range s.members {
		in.Members = append(in.Members, *m)
	}
	d := Decide(in)

	seen := map[wire.ID]bool{}
	for _, c := range d.Commands {
		m := s.member(c.Zathras)
		_, preparing := s.preparing[c.Zathras]
		_, activating := s.activating[c.Zathras]
		switch {
		case m == nil:
			s.t.Fatalf("a command to %s, which is not connected", nameOf(c.Zathras))
		case seen[c.Zathras]:
			s.t.Errorf("two commands to %s in one decision", m.Name)
		case preparing || activating:
			s.t.Errorf("a command to %s while one is on its way", m.Name)
		case c.Activate && m.State.Generation == c.Generation:
			s.t.Errorf("%s was sent activate for generation %d, which it runs already", m.Name, c.Generation)
		case c.Activate && !hasPrepared(*m, c.Generation):
			s.t.Errorf("%s was sent activate for generation %d, which it has not prepared", m.Name, c.Generation)
		}
		seen[c.Zathras] = true
		if c.Activate {
			s.activating[c.Zathras] = c.Generation
		} else {
			s.preparing[c.Zathras] = c.Generation
		}
		s.undelivered = append(s.undelivered, c)
		s.sent = append(s.sent, c)
	}
	if d.State != "" {
		if !s.rollout.State.UnderWay() {
			s.t.Errorf("a rollout that is %s was moved to %s", s.rollout.State, d.State)
		}
		s.rollout.State, s.rollout.Reason = d.State, d.Reason
		if d.State == wire.RolloutCompleted {
			s.stable = s.rollout.Generation
		}
	}
	s.most = max(s.most, s.restarting())
	if !s.held {
		s.deliver()
		s.acknowledge()
	}
	return d
}

// deliver has the zathrases do what they were sent. What draal remembers
// of the commands stays until acknowledge.
func (s *sim) deliver() {
	for _, c := range s.undelivered {
		m := s.member(c.Zathras)
		switch {
		case m == nil:
			// It has gone.
		case !c.Activate:
			prepared(c.Generation)(m)
		case c.Generation == s.crashing:
			backoff(c.Generation)(m)
			m.State.LastExit.Ran = 1500 * time.Millisecond
		default:
			running(c.Generation, 0)(m)
		}
	}
	s.undelivered = nil
}

// acknowledge is draal hearing of what the zathrases did: the prepares
// were answered, and the reports show the activates.
func (s *sim) acknowledge() {
	clear(s.preparing)
	clear(s.activating)
}

// restart is draal starting again: what it remembered of the commands on
// their way is gone. The rollout is in the database, and the zathrases
// connect again with what they report.
func (s *sim) restart() {
	clear(s.preparing)
	clear(s.activating)
	clear(s.failed)
}

// advance moves the clock, and with it how long the services have run.
func (s *sim) advance(d time.Duration) {
	s.now = s.now.Add(d)
	for _, m := range s.members {
		if m.State.Phase == wire.PhaseRunning {
			m.State.RunningFor += d
		}
	}
}

// restarting counts the zathrases that are being moved to the target and
// have not run it for StableAfter yet.
func (s *sim) restarting() int {
	target := s.rollout.Generation
	n := 0
	for _, m := range s.members {
		st := m.State
		done := st.Generation == target && st.Phase == wire.PhaseRunning && st.RunningFor >= stableAfter
		if !done && (st.Generation == target || s.activating[m.ID] == target) {
			n++
		}
	}
	return n
}

// activated returns the names of the zathrases that were sent activate for
// the generation, in the order they were sent it.
func (s *sim) activated(generation uint64) []string {
	var out []string
	for _, c := range s.sent {
		if c.Activate && c.Generation == generation {
			out = append(out, nameOf(c.Zathras))
		}
	}
	return out
}

// generations returns what every zathras runs, by name: "web-1=2 web-2=1".
func (s *sim) generations() string {
	var out []string
	for _, m := range s.members {
		out = append(out, fmt.Sprintf("%s=%d", m.Name, m.State.Generation))
	}
	slices.Sort(out)
	return fmt.Sprint(out)
}

// play decides every 10 seconds until the rollout is no longer under way.
// between is called after every decision, with the number of the step.
func (s *sim) play(between func(step int)) {
	s.t.Helper()
	for step := 0; s.rollout.State.UnderWay(); step++ {
		if step > 200 {
			s.t.Fatalf("the rollout is still %s after %d decisions: %s", s.rollout.State, step, s.generations())
		}
		s.decide()
		if between != nil {
			between(step)
		}
		s.advance(10 * time.Second)
	}
}

// five returns web-1 to web-5, which have run generation 1 for an hour, in
// an order that is not that of their names.
func five() []fleet.Member {
	var out []fleet.Member
	for _, n := range []int{4, 2, 5, 1, 3} {
		out = append(out, member(fmt.Sprintf("web-%d", n), running(1, hour)))
	}
	return out
}

var fiveNames = []string{"web-1", "web-2", "web-3", "web-4", "web-5"}

func TestPlayRolloutOfFive(t *testing.T) {
	s := newSim(t, 1, five()...)
	s.begin(2, 2)
	s.play(nil)

	if s.rollout.State != wire.RolloutCompleted {
		t.Fatalf("the rollout is %s (%s), want completed", s.rollout.State, s.rollout.Reason)
	}
	if s.stable != 2 {
		t.Errorf("stable = %d, want 2", s.stable)
	}
	if got, want := s.generations(), "[web-1=2 web-2=2 web-3=2 web-4=2 web-5=2]"; got != want {
		t.Errorf("the fleet runs %s, want %s", got, want)
	}
	if s.most != 2 {
		t.Errorf("at most %d were restarting at once, want 2: never more, and both places used", s.most)
	}
	if got := s.activated(2); !slices.Equal(got, fiveNames) {
		t.Errorf("activated in the order %v, want %v: by name, each once", got, fiveNames)
	}
	if got := len(s.sent); got != 10 {
		t.Errorf("%d commands, want 10: a prepare and an activate for each", got)
	}

	// Completed: nothing more is asked for.
	s.advance(hour)
	if d := s.decide(); len(d.Commands) != 0 || d.State != "" || !d.Recheck.IsZero() {
		t.Errorf("after the rollout completed: %+v, want nothing", d)
	}
}

func TestPlayParallelZeroRestartsAllAtOnce(t *testing.T) {
	s := newSim(t, 1, five()...)
	s.begin(2, 0)
	s.play(nil)
	if s.rollout.State != wire.RolloutCompleted {
		t.Fatalf("the rollout is %s (%s), want completed", s.rollout.State, s.rollout.Reason)
	}
	if s.most != 5 {
		t.Errorf("at most %d were restarting at once, want all 5", s.most)
	}
	if got := s.activated(2); !slices.Equal(got, fiveNames) {
		t.Errorf("activated %v, want %v", got, fiveNames)
	}
}

func TestPlayFirstRolloutOfIdleFleet(t *testing.T) {
	// Nobody runs anything: there is nothing to lose, and parallel holds
	// nobody back.
	s := newSim(t, 0, member("web-1"), member("web-2"), member("web-3"))
	s.begin(1, 1)
	s.decide() // prepare
	d := s.decide()
	if got, want := show(d.Commands), "activate web-1 1, activate web-2 1, activate web-3 1"; got != want {
		t.Errorf("commands: %s, want %s", got, want)
	}
	s.advance(stableAfter)
	s.decide()
	if s.rollout.State != wire.RolloutCompleted || s.stable != 1 {
		t.Errorf("the rollout is %s and stable %d, want completed and 1", s.rollout.State, s.stable)
	}
}

// draal is restarted in the middle of a rollout, more than once: what it
// remembered of the commands on their way is gone, and the rollout is
// carried on with from what the zathrases report.
func TestPlayRestartOfDraal(t *testing.T) {
	for _, at := range [][]int{{0}, {1}, {2}, {3}, {5}, {8}, {1, 2, 3, 4}, {0, 4, 9, 13}} {
		t.Run(fmt.Sprint("restarts at steps ", at), func(t *testing.T) {
			s := newSim(t, 1, five()...)
			s.held = true
			s.begin(2, 2)
			s.play(func(step int) {
				// The zathrases do what they were sent. Deciding again
				// before draal has heard of it must send nothing twice.
				s.deliver()
				s.decide()
				s.deliver()
				if slices.Contains(at, step) {
					s.restart()
				} else {
					s.acknowledge()
				}
				s.decide()
				s.deliver()
				s.acknowledge()
			})

			if s.rollout.State != wire.RolloutCompleted {
				t.Fatalf("the rollout is %s (%s), want completed", s.rollout.State, s.rollout.Reason)
			}
			if got, want := s.generations(), "[web-1=2 web-2=2 web-3=2 web-4=2 web-5=2]"; got != want {
				t.Errorf("the fleet runs %s, want %s", got, want)
			}
			if s.most > 2 {
				t.Errorf("%d were restarting at once, want 2 at most", s.most)
			}
			// decide has checked that nobody was sent activate for a
			// generation it runs already; so everyone was sent it once.
			if got := s.activated(2); !slices.Equal(got, fiveNames) {
				t.Errorf("activated in the order %v, want %v: by name, each once", got, fiveNames)
			}
		})
	}
}

func TestPlayCrashHaltsAtTheFirstAndRollbackRestoresIt(t *testing.T) {
	s := newSim(t, 1, five()...)
	s.crashing = 2
	s.begin(2, 2)
	s.play(nil)

	if s.rollout.State != wire.RolloutHalted {
		t.Fatalf("the rollout is %s, want halted", s.rollout.State)
	}
	if got, want := s.rollout.Reason, "web-1: exited with status 1 after 1.5s"; got != want {
		t.Errorf("reason %q, want %q", got, want)
	}
	if s.stable != 1 {
		t.Errorf("stable = %d, want 1", s.stable)
	}
	if got, want := s.generations(), "[web-1=2 web-2=2 web-3=1 web-4=1 web-5=1]"; got != want {
		t.Errorf("the fleet runs %s, want %s: the first batch and nobody else", got, want)
	}

	// Halted: the service is frozen, but a new pod gets the stable
	// generation.
	s.advance(hour)
	if d := s.decide(); len(d.Commands) != 0 || d.State != "" {
		t.Errorf("a halted rollout: %+v, want nothing", d)
	}
	s.join(member("web-6"))
	s.decide()
	s.decide()
	if got := s.member(id("web-6")).State.Generation; got != 1 {
		t.Errorf("the new pod runs generation %d, want the stable one", got)
	}

	// The rollback moves the two that crash back, and restarts nobody
	// that runs generation 1.
	s.advance(hour)
	before := len(s.sent)
	s.most = 0
	s.begin(1, 1)
	s.play(nil)
	if s.rollout.State != wire.RolloutCompleted {
		t.Fatalf("the rollback is %s (%s), want completed", s.rollout.State, s.rollout.Reason)
	}
	if got, want := show(s.sent[before:]), "activate web-1 1, activate web-2 1"; got != want {
		t.Errorf("the rollback sent %s, want %s", got, want)
	}
	if s.most != 1 {
		t.Errorf("at most %d were restarting at once, want 1", s.most)
	}
}

func TestPlayJoinAndLeaveDuringARollout(t *testing.T) {
	s := newSim(t, 1, five()...)
	s.begin(2, 1)
	var back fleet.Member
	s.play(func(step int) {
		switch step {
		case 2:
			// A new pod, a pod that goes, and one that will be back.
			s.join(member("web-0"))
			s.leave("web-3")
			back = s.leave("web-5")
		case 6:
			// It comes back as it went: running generation 1, with what it
			// had prepared.
			s.join(back)
		}
	})
	if s.rollout.State != wire.RolloutCompleted {
		t.Fatalf("the rollout is %s (%s), want completed", s.rollout.State, s.rollout.Reason)
	}
	if got, want := s.generations(), "[web-0=2 web-1=2 web-2=2 web-4=2 web-5=2]"; got != want {
		t.Errorf("the fleet runs %s, want %s", got, want)
	}
	if got, want := s.activated(2), []string{"web-1", "web-0", "web-2", "web-4", "web-5"}; !slices.Equal(got, want) {
		t.Errorf("activated in the order %v, want %v", got, want)
	}
}

func TestPlaySupersede(t *testing.T) {
	// Generation 3 is rolled out while generation 2 is half way.
	s := newSim(t, 1, five()...)
	s.begin(2, 1)
	s.play(func(step int) {
		if step == 5 {
			s.begin(3, 2)
			s.restart() // draal ends the commands of the rollout that was superseded
		}
	})
	if s.rollout.State != wire.RolloutCompleted || s.rollout.Generation != 3 || s.stable != 3 {
		t.Fatalf("the rollout of %d is %s and stable %d, want 3 completed",
			s.rollout.Generation, s.rollout.State, s.stable)
	}
	if got, want := s.generations(), "[web-1=3 web-2=3 web-3=3 web-4=3 web-5=3]"; got != want {
		t.Errorf("the fleet runs %s, want %s", got, want)
	}
	if got := s.activated(3); !slices.Equal(got, fiveNames) {
		t.Errorf("activated for 3 in the order %v, want %v", got, fiveNames)
	}
}
```

- [ ] **Step 8: Run the tests to see them pass, and see that they can fail**

Run: `go test -race -count=3 ./rollout/`
Expected: PASS (`ok  	github.com/fables-for-robots/great-machine/rollout`). The function of Step 4 is already whole, so these pass at once. If one fails, the fault is in `decide.go`, not in the test.

To see that they hold the function to something, break it for a moment: in
`rollout/decide.go`, in `activating`, change
`underWay >= d.in.Rollout.Parallel` to `underWay > d.in.Rollout.Parallel`.

Run: `go test ./rollout/ -run 'TestPlayRolloutOfFive|TestPlayRestartOfDraal'`
Expected: FAIL — `at most 3 were restarting at once, want 2: never more, and both places used`, and `3 were restarting at once, want 2 at most`

Undo the change:

```bash
git checkout rollout/decide.go
go test -race -count=3 ./rollout/
```

Expected: PASS

Run: `test -z "$(gofmt -l .)" && go vet ./...`
Expected: no output.

- [ ] **Step 9: Commit**

```bash
git add rollout/
git commit -m "Rollout: whole rollouts played against a simulated fleet"
```
