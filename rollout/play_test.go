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
	// absent: who left during the rollout without being done, and when.
	// It holds the rollout open for grace, which is 0 unless a test sets it.
	absent map[wire.ID]time.Time
	grace  time.Duration

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
		absent:     map[wire.ID]time.Time{},
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
	// Who left before it was made did not leave during it.
	clear(s.absent)
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
// One that leaves a rollout under way without being done is absent.
func (s *sim) leave(name string) fleet.Member {
	m := s.member(id(name))
	if m == nil {
		s.t.Fatalf("%s is not there", name)
	}
	st := m.State
	done := st.Generation == s.rollout.Generation && st.Phase == wire.PhaseRunning && st.RunningFor >= stableAfter
	if s.rollout.State.UnderWay() && !done {
		s.absent[m.ID] = s.now
	}
	s.members = slices.DeleteFunc(s.members, func(o *fleet.Member) bool { return o == m })
	delete(s.preparing, m.ID)
	delete(s.activating, m.ID)
	delete(s.failed, m.ID)
	return *m
}

func (s *sim) join(m fleet.Member) {
	s.members = append(s.members, &m)
	delete(s.absent, m.ID)
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
		Absent:         maps.Clone(s.absent),
		StableAfter:    stableAfter,
		PrepareTimeout: prepareTimeout,
		RetryAfter:     retryAfter,
		AbsentGrace:    s.grace,
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

// All of them lose draal at once in the middle of the rollout, and are back
// twenty seconds later with what they ran: the rollout is not completed
// while they are away, and goes on one at a time when they are back.
func TestPlayAllLeaveAndComeBack(t *testing.T) {
	s := newSim(t, 1, five()...)
	s.grace = absentGrace
	s.begin(2, 1)
	var gone []fleet.Member
	s.play(func(step int) {
		switch step {
		case 4:
			if n := len(s.activated(2)); n == 0 || n == 5 {
				t.Fatalf("%d were sent activate before they left, want some and not all", n)
			}
			for _, name := range fiveNames {
				gone = append(gone, s.leave(name))
			}
		case 5, 6:
			if s.rollout.State != wire.RolloutActivating {
				t.Fatalf("the rollout is %s while they are away, want activating", s.rollout.State)
			}
			if step == 6 {
				for _, m := range gone {
					s.join(m)
				}
			}
		}
	})
	if s.rollout.State != wire.RolloutCompleted {
		t.Fatalf("the rollout is %s (%s), want completed", s.rollout.State, s.rollout.Reason)
	}
	if got, want := s.generations(), "[web-1=2 web-2=2 web-3=2 web-4=2 web-5=2]"; got != want {
		t.Errorf("the fleet runs %s, want %s", got, want)
	}
	if s.most != 1 {
		t.Errorf("at most %d were restarting at once, want 1", s.most)
	}
	// decide has checked that nobody was sent activate for a generation it
	// runs already; so everyone was sent it once.
	if got := s.activated(2); !slices.Equal(got, fiveNames) {
		t.Errorf("activated in the order %v, want %v: by name, each once", got, fiveNames)
	}
}

// One leaves for good: the rollout completes when the grace has passed.
func TestPlayOneLeavesForGood(t *testing.T) {
	s := newSim(t, 1, five()...)
	s.grace = absentGrace
	s.begin(2, 1)
	var left, completed time.Time
	s.play(func(step int) {
		if step == 2 {
			s.leave("web-5")
			left = s.now
		}
		if s.rollout.State == wire.RolloutCompleted {
			completed = s.now
		}
	})
	if s.rollout.State != wire.RolloutCompleted {
		t.Fatalf("the rollout is %s (%s), want completed", s.rollout.State, s.rollout.Reason)
	}
	if got, want := s.generations(), "[web-1=2 web-2=2 web-3=2 web-4=2]"; got != want {
		t.Errorf("the fleet runs %s, want %s", got, want)
	}
	if end := left.Add(absentGrace); completed.Before(end) || !completed.Before(end.Add(10*time.Second)) {
		t.Errorf("completed %v after web-5 left, want at the first decision from %v on", completed.Sub(left), absentGrace)
	}
}
