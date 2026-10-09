package rollout

import (
	"maps"
	"math/rand/v2"
	"reflect"
	"slices"
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

// A zathras that left during the rollout without being done holds its
// completion for AbsentGrace: zathrases that lose draal all at once do not
// complete the rollout and come back to be moved all at once.
func TestAbsent(t *testing.T) {
	left := now.Add(-30 * time.Second)
	until := left.Add(absentGrace)

	t.Run("one that left holds the rollout open, and the recheck is when it stops", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, member("web-a", running(2, hour)))
		in.Absent[id("web-b")] = left
		check(t, Decide(in), Decision{Prepared: 1, Activated: 1, Recheck: until})
		in.Now = until.Add(-time.Nanosecond)
		check(t, Decide(in), Decision{Prepared: 1, Activated: 1, Recheck: until})
	})

	t.Run("completed at the end of the grace, and after", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, member("web-a", running(2, hour)))
		in.Absent[id("web-b")] = left
		for _, at := range []time.Time{until, until.Add(time.Nanosecond), until.Add(hour)} {
			in.Now = at
			check(t, Decide(in), Decision{State: wire.RolloutCompleted, Prepared: 1, Activated: 1})
		}
	})

	t.Run("with no members activating stays", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1)
		in.Absent[id("web-a")] = left
		check(t, Decide(in), Decision{Recheck: until})
		delete(in.Absent, id("web-a"))
		check(t, Decide(in), Decision{State: wire.RolloutCompleted})
	})

	t.Run("preparing moves to activating, not to completed", func(t *testing.T) {
		in := input(newRollout(wire.RolloutPreparing, 2, 1), 1,
			member("web-a", running(2, hour)),
			member("web-b", running(2, hour)),
		)
		in.Absent[id("web-c")] = left
		check(t, Decide(in), Decision{
			State:     wire.RolloutActivating,
			Prepared:  2,
			Activated: 2,
			Recheck:   until,
		})
	})

	t.Run("preparing with no members moves to activating", func(t *testing.T) {
		in := input(newRollout(wire.RolloutPreparing, 2, 1), 1)
		in.Absent[id("web-a")] = left
		check(t, Decide(in), Decision{State: wire.RolloutActivating, Recheck: until})
	})

	t.Run("preparing with the first batch, and the recheck", func(t *testing.T) {
		in := input(newRollout(wire.RolloutPreparing, 2, 1), 1, ready()...)
		in.Absent[id("web-d")] = left
		check(t, Decide(in), Decision{
			Commands: []Command{activate("web-a", 2)},
			State:    wire.RolloutActivating,
			Prepared: 3,
			Awaited:  3,
			Recheck:  until,
		})
	})

	t.Run("one that is connected is not absent, whatever Absent says", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1,
			member("web-a", running(2, hour)),
			member("web-b", running(2, hour)),
		)
		in.Absent[id("web-b")] = left
		check(t, Decide(in), Decision{State: wire.RolloutCompleted, Prepared: 2, Activated: 2})
	})

	t.Run("the recheck is the earliest end of several", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, member("web-a", running(2, hour)))
		in.Absent[id("web-b")] = left
		in.Absent[id("web-c")] = left.Add(-20 * time.Second)
		in.Absent[id("web-d")] = left.Add(10 * time.Second)
		in.Absent[id("web-e")] = now.Add(-hour) // its grace is over
		in.Absent[id("web-a")] = left.Add(-time.Minute)
		for range 20 {
			check(t, Decide(in), Decision{Prepared: 1, Activated: 1, Recheck: until.Add(-20 * time.Second)})
		}
	})

	t.Run("and earlier than the one of a zathras under way", func(t *testing.T) {
		m := ready()
		m[0] = member("web-a", running(2, 10*time.Second))
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, m...)
		in.Absent[id("web-d")] = now.Add(5*time.Second - absentGrace)
		check(t, Decide(in), Decision{Prepared: 3, Awaited: 3, Recheck: now.Add(5 * time.Second)})
	})

	t.Run("an earlier recheck that is due anyway wins", func(t *testing.T) {
		m := ready()
		m[0] = member("web-a", running(2, 10*time.Second))
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, m...)
		in.Absent[id("web-d")] = left
		in.Absent[id("web-e")] = left.Add(time.Second)
		check(t, Decide(in), Decision{Prepared: 3, Awaited: 3, Recheck: now.Add(20 * time.Second)})
	})

	t.Run("a failure halts all the same", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1,
			member("web-a", running(2, hour)),
			member("web-b", backoff(2)),
		)
		in.Absent[id("web-c")] = left
		check(t, Decide(in), Decision{
			State:     wire.RolloutHalted,
			Reason:    "web-b: exited with status 1 after 2.1s",
			Prepared:  2,
			Activated: 1,
			Awaited:   1,
		})
	})

	t.Run("and so does the prepare timeout", func(t *testing.T) {
		in := input(newRollout(wire.RolloutPreparing, 2, 1), 1, old()...)
		in.Absent[id("web-d")] = t0.Add(prepareTimeout - time.Second)
		in.Now = t0.Add(prepareTimeout)
		check(t, Decide(in), Decision{
			State:   wire.RolloutHalted,
			Reason:  "still preparing after 30m0s",
			Awaited: 3,
		})
	})

	t.Run("with AbsentGrace zero the entries are ignored", func(t *testing.T) {
		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, member("web-a", running(2, hour)))
		in.Absent[id("web-b")] = now
		in.AbsentGrace = 0
		check(t, Decide(in), Decision{State: wire.RolloutCompleted, Prepared: 1, Activated: 1})
	})

	t.Run("outside a rollout under way Absent is not looked at", func(t *testing.T) {
		for _, state := range []wire.RolloutState{wire.RolloutCompleted, wire.RolloutHalted} {
			in := input(newRollout(state, 2, 1), 2,
				member("web-a", running(2, hour)),
				member("web-n"),
			)
			want := Decide(in)
			in.Absent[id("web-b")] = left
			check(t, Decide(in), want)
		}
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
	held := input(newRollout(wire.RolloutActivating, 2, 1), 1,
		sample(member("web-a", running(2, hour), crashed)),
		sample(member("web-c", running(1, hour), prepared(2))),
		sample(member("web-e")),
	)
	held.Absent[id("web-a")] = now.Add(-time.Minute - 50*time.Second)
	held.Absent[id("web-b")] = now.Add(-30 * time.Second)
	held.Absent[id("web-d")] = now.Add(-time.Minute)
	held.Absent[id("web-f")] = now.Add(-hour)
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
		"held":       held,
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
			absent := maps.Clone(in.Absent)
			random := rand.New(rand.NewPCG(1, 2))
			for range 50 {
				shuffled := in
				shuffled.Members = make([]fleet.Member, len(in.Members))
				copy(shuffled.Members, in.Members)
				random.Shuffle(len(shuffled.Members), func(i, j int) {
					shuffled.Members[i], shuffled.Members[j] = shuffled.Members[j], shuffled.Members[i]
				})
				// Absent is made again in another order: what it is made of
				// decides, not the order of a map.
				ids := slices.Sorted(maps.Keys(in.Absent))
				random.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
				shuffled.Absent = map[wire.ID]time.Time{}
				for _, z := range ids {
					shuffled.Absent[z] = in.Absent[z]
				}
				if got := Decide(shuffled); !reflect.DeepEqual(got, want) {
					t.Fatalf("with the members and Absent in another order:\n got %+v\nwant %+v", got, want)
				}
			}
			// Decide leaves what it is given as it was.
			if !reflect.DeepEqual(in.Members, given) {
				t.Error("Decide changed the order of the members it was given")
			}
			if !maps.Equal(in.Absent, absent) {
				t.Error("Decide changed Absent")
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
