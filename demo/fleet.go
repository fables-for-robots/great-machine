// Package demo is a made-up fleet behind the interface of draal: a
// draal.Controller that invents four services, their zathrases, their
// figures and their logs, and moves them along. The web page and the
// terminal UI are developed against it (web/demo and tui/demo).
//
// Everything of it is made up but the shell: Shell starts A REAL SHELL ON
// THE MACHINE THAT RUNS IT, as the user who runs it.
//
// It is not part of the program: the program great-machine does not use
// it, and only the demos of the web page and of the terminal UI do.
package demo

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/wire"
)

const (
	// watchEvery is the least time between two fleets for one watcher, as
	// draal has it.
	watchEvery = time.Second
	// cycleEvery is how often the service web is rolled out.
	cycleEvery = time.Minute
	// rolloutsKept is how many rollouts of a service are listed.
	rolloutsKept = 20
	// followBuffer is how many lines may wait for one who follows a log,
	// as a zathras has it.
	followBuffer = 1024
	// longestName is how long the name of a zathras may be.
	longestName = 253
)

// Demo is the made-up fleet. It is a draal.Controller.
//
// All of it moves by tasks that are to be done at a time: advance does
// those whose time has come. So the fleet of any time can be had without
// waiting for it, which is how its test looks at three minutes of it.
type Demo struct {
	start time.Time

	mu       sync.Mutex
	at       time.Time           // the time of the task that is being done
	last     time.Time           // when advance was called last
	rng      *rand.Rand          // for what is made up as it goes
	services map[string]*service // by name
	all      []*zathras          // in the order they were made up
	byID     map[wire.ID]*zathras
	rollouts uint64 // how many there were, of all services
	todo     []task
	dirty    bool                       // the fleet has changed since the watchers were told
	subs     map[chan struct{}]struct{} // who is told that the fleet changed
}

// A Demo is a Controller.
var _ draal.Controller = (*Demo)(nil)

// task is something that is to happen at a time. It is given that time,
// which is the present for it, however late it is done.
type task struct {
	at time.Time
	do func(at time.Time)
}

// service is a made-up service.
type service struct {
	name       string
	generation uint64             // the newest there is
	stable     uint64             // 0 for none
	rollouts   []wire.RolloutInfo // newest first
	zathrases  []*zathras
	args       []string
}

// zathras is a made-up zathras.
type zathras struct {
	id      wire.ID
	name    string
	service *service

	present   bool          // connected: one that is not is in no fleet and answers nothing
	left      chan struct{} // closed when it goes; a new one when it is back
	connected time.Time
	state     wire.State // without RunningFor and Sample, which the fleet adds
	since     time.Time  // when its service was started
	pid       int        // of its service, when that runs

	ring  *logring.Ring
	talks time.Duration // about how long between two lines of its service; 0: it says nothing
	lines int           // how many its service has written

	figures *figures // nil: a machine without cgroup v2, and no samples
	samples []wire.Sample
}

// operator is who made the rollouts.
var operator = idOf("an operator")

// idOf returns an endpoint ID for a text: always the same one, so that a
// link to a zathras of the demo is good for the next run of it.
func idOf(text string) wire.ID {
	sum := sha256.Sum256([]byte(text))
	return wire.ID(hex.EncodeToString(sum[:]))
}

// binaryOf returns the path a generation of a service has in the store.
func binaryOf(service string, generation uint64) string {
	const alphabet = "0123456789abcdfghijklmnpqrsvwxyz" // what Nix writes a hash with
	sum := sha256.Sum256(fmt.Appendf(nil, "%s %d", service, generation))
	hash := make([]byte, 32)
	for i := range hash {
		hash[i] = alphabet[sum[i]%32]
	}
	minor := map[string]int{"web": 4, "worker": 9}[service]
	return fmt.Sprintf("/nix/store/%s-%s-1.%d.%d/bin/%s", hash, service, minor, generation, service)
}

// newDemo returns the fleet as it is at start, with what is to happen to
// it from then on. With empty it has no service and nothing happens to
// it: a draal that was started for the first time.
func newDemo(start time.Time, empty bool) *Demo {
	d := &Demo{
		start:    start,
		at:       start,
		last:     start,
		rng:      rand.New(rand.NewPCG(2026, 10)),
		services: map[string]*service{},
		byID:     map[wire.ID]*zathras{},
		subs:     map[chan struct{}]struct{}{},
	}
	if !empty {
		d.seed()
	}
	return d
}

// New returns the made-up controller of web/demo: four services
// (empty, fresh, web, worker), or none when empty is set, with a fleet
// that moves by itself once Run is called.
func New(empty bool) *Demo {
	return newDemo(time.Now(), empty)
}

// after has do done when delay has passed since at.
func (d *Demo) after(at time.Time, delay time.Duration, do func(at time.Time)) {
	d.todo = append(d.todo, task{at: at.Add(delay), do: do})
}

// every has do done now and then again whenever pause has passed.
func (d *Demo) every(at time.Time, pause time.Duration, do func(at time.Time)) {
	do(at)
	d.after(at, pause, func(at time.Time) { d.every(at, pause, do) })
}

// Run moves the fleet until ctx is done.
func (d *Demo) Run(ctx context.Context) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			d.advance(now)
		}
	}
}

// advance does every task whose time has come by now, oldest first, and
// tells the watchers if the fleet has changed.
func (d *Demo) advance(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// A machine that slept does not make up for the time: what was to
	// happen is put off by as long as it slept.
	if gap := now.Sub(d.last); gap > 5*time.Second {
		for i := range d.todo {
			d.todo[i].at = d.todo[i].at.Add(gap)
		}
	}
	d.last = now
	for {
		next := -1
		for i, t := range d.todo {
			if !t.at.After(now) && (next < 0 || t.at.Before(d.todo[next].at)) {
				next = i
			}
		}
		if next < 0 {
			break
		}
		t := d.todo[next]
		d.todo = slices.Delete(d.todo, next, next+1)
		d.at = t.at
		t.do(t.at)
	}
	d.at = now
	if d.dirty {
		d.dirty = false
		for ch := range d.subs {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

// seed makes the fleet of the start: four services, their rollouts so
// far and their zathrases, and what is to happen to them.
func (d *Demo) seed() {
	// fresh: what a new user sees first. Two zathrases have registered,
	// and nothing was rolled out: no rollout, no generation, and logs
	// without a line, as the supervisor of a zathras says nothing before
	// its first start.
	fresh := d.addService("fresh")
	d.addZathras(fresh, "fresh-0", 0, 3*time.Minute).figures = newFigures(31, 0.1, 256*mib)
	d.addZathras(fresh, "fresh-1", 0, 2*time.Minute)

	// empty: rolled out twice, and nobody is there to run it.
	empty := d.addService("empty")
	d.record(empty, wire.RolloutSuperseded, "", d.start.Add(-30*time.Hour), 40*time.Second)
	d.record(empty, wire.RolloutCompleted, "", d.start.Add(-30*time.Hour+40*time.Second), 3*time.Second)

	// worker: its last rollout is halted, and its zathrases are on two
	// generations: the one that was tried first, and the others.
	worker := d.addService("worker", "--queue", "jobs", "--concurrency", "4")
	for i := range 3 {
		d.record(worker, wire.RolloutCompleted, "", d.start.Add(time.Duration(i-80)*time.Hour), 95*time.Second)
	}
	d.record(worker, wire.RolloutHalted, "worker-1: exited with status 2 after 1.3s", d.start.Add(-26*time.Minute), 21*time.Second)

	steady := d.addZathras(worker, "worker-0", 0, 71*time.Hour)
	steady.figures = newFigures(11, 0.6, 0) // no limit of memory
	d.runs(steady, worker.stable, d.start.Add(-26*time.Hour))
	steady.talks = 4 * time.Second

	crashing := d.addZathras(worker, "worker-1", 0, 71*time.Hour)
	crashing.figures = newFigures(12, 0.3, 512*mib)
	crashing.state.Generation = worker.generation
	crashing.state.Binary, crashing.state.Args = binaryOf("worker", worker.generation), worker.args
	crashing.state.Prepared = []uint64{worker.generation}
	crashing.state.Restarts = 187

	bare := d.addZathras(worker, "worker-2", 0, 9*time.Hour) // a machine without cgroup v2
	d.runs(bare, worker.stable, d.start.Add(-9*time.Hour))
	bare.talks = 6 * time.Second

	d.addZathras(worker, "worker-3", 0, 4*time.Minute) // has just come, and runs nothing

	long := "worker-batch-eu-central-1a." + strings.Repeat("node-0123456789abcdef.", 12)
	long = long[:longestName-len("internal")] + "internal"
	slow := d.addZathras(worker, long, 0, 50*time.Hour)
	slow.figures = newFigures(13, 0.9, 1024*mib)
	d.runs(slow, worker.stable, d.start.Add(-50*time.Hour))
	slow.talks = 5 * time.Second

	// web: twenty rollouts of every state, and one under way for most of
	// every minute.
	web := d.addService("web", "--listen", ":8080", "--db", "postgres://db.internal/web", "--workers", "8")
	reasons := []string{
		"web-7d9f8b6c4-mn4tc: exited with status 1 after 2.3s",
		"still preparing after 30m0s: web-7d9f8b6c4-q5rvd",
		"web-7d9f8b6c4-2xkqp: was ended by SIGKILL after 14.2s",
		"web-7d9f8b6c4-8hjwz: could not be started: fork/exec " + binaryOf("web", 6) + ": exec format error",
	}
	for i := 19; i >= 1; i-- {
		created := d.start.Add(-time.Duration(i) * 47 * time.Minute)
		switch {
		case i%6 == 3:
			d.record(web, wire.RolloutHalted, reasons[(i/6)%len(reasons)], created, time.Duration(20+i)*time.Second)
		case i%5 == 2:
			d.record(web, wire.RolloutSuperseded, "", created, time.Duration(3+i)*time.Second)
		default:
			d.record(web, wire.RolloutCompleted, "", created, time.Duration(60+7*i)*time.Second)
		}
	}
	names := []string{
		"web-7d9f8b6c4-2xkqp", // talks all the time
		"web-7d9f8b6c4-8hjwz", // floods its log after every start
		"web-7d9f8b6c4-8hjwz", // has the name of the one before
		"web-7d9f8b6c4-mn4tc", // exits now and then
		"web-7d9f8b6c4-q5rvd", // goes now and then, and comes back
	}
	for i, name := range names {
		z := d.addZathras(web, name, i, time.Duration(5+3*i)*time.Hour)
		z.figures = newFigures(uint64(20+i), 0.25+0.15*float64(i), 512*mib)
		d.runs(z, web.stable, d.start.Add(-47*time.Minute+time.Duration(i)*8*time.Second))
		z.talks = 5 * time.Second
	}
	web.zathrases[0].talks = 300 * time.Millisecond
	web.zathrases[3].state.Restarts = 4
	web.zathrases[3].state.LastExit = &wire.Exit{
		Generation: web.stable - 1, Status: -1, Signal: "SIGKILL", At: d.start.Add(-3 * time.Hour), Ran: 41 * time.Minute,
	}

	// What every zathras has from before the start: its samples and the
	// lines of its ring.
	for _, z := range d.all {
		d.past(z)
	}

	// And what happens from the start on.
	d.every(d.start, sampleEvery, d.sample)
	for _, z := range d.all {
		if z.talks > 0 {
			d.after(d.start, d.around(z.talks), func(at time.Time) { d.talk(z, at) })
		}
	}
	d.crash(crashing, d.start)
	d.every(d.start, 40*time.Second, func(at time.Time) { d.restart(slow, at) })
	d.cycle(d.start, 0)
}

// around returns a time about as long as pause: half of it to half more.
func (d *Demo) around(pause time.Duration) time.Duration {
	return pause/2 + time.Duration(d.rng.Int64N(int64(pause)))
}

// addService adds a service without a rollout.
func (d *Demo) addService(name string, args ...string) *service {
	svc := &service{name: name, args: args}
	d.services[name] = svc
	return svc
}

// addZathras adds a zathras that is connected and idle. salt tells two of
// one name apart.
func (d *Demo) addZathras(svc *service, name string, salt int, connectedFor time.Duration) *zathras {
	z := &zathras{
		id:        idOf(fmt.Sprintf("%s/%s/%d", svc.name, name, salt)),
		name:      name,
		service:   svc,
		present:   true,
		left:      make(chan struct{}),
		connected: d.start.Add(-connectedFor),
		state:     wire.State{Phase: wire.PhaseIdle},
		ring:      logring.New(400, 16<<10),
	}
	z.ring.SetClock(func() time.Time { return d.at })
	svc.zathrases = append(svc.zathrases, z)
	d.all = append(d.all, z)
	d.byID[z.id] = z
	return z
}

// runs puts a zathras on a generation that it has run since the time
// given, without a word in its log.
func (d *Demo) runs(z *zathras, generation uint64, since time.Time) {
	z.state.Generation = generation
	z.state.Binary, z.state.Args = binaryOf(z.service.name, generation), z.service.args
	z.state.Phase = wire.PhaseRunning
	z.state.RestartAt = time.Time{}
	z.state.Prepared = []uint64{generation}
	z.since = since
	z.pid = 2 + d.rng.IntN(30000)
	d.dirty = true
}

// started is runs for a service that is started now: the supervisor of
// the zathras says so in the log.
func (d *Demo) started(z *zathras, generation uint64, at time.Time) {
	prepared := z.state.Prepared
	d.runs(z, generation, at)
	if !slices.Contains(prepared, generation) {
		prepared = append(prepared, generation)
	}
	z.state.Prepared = prepared
	z.ring.Add(logring.Zathras, fmt.Sprintf("started generation %d: %s (pid %d)", generation, z.state.Binary, z.pid))
}

// exited records how the service of a zathras ended, as the supervisor
// does, and returns the words for it.
func (d *Demo) exited(z *zathras, status int, signal string, at time.Time) string {
	ran := at.Sub(z.since)
	z.state.LastExit = &wire.Exit{Generation: z.state.Generation, Status: status, Signal: signal, At: at, Ran: ran}
	z.pid = 0
	d.dirty = true
	if ran >= time.Second {
		ran = ran.Round(100 * time.Millisecond)
	}
	if signal != "" {
		return fmt.Sprintf("generation %d was ended by %s after %s", z.state.Generation, signal, ran)
	}
	return fmt.Sprintf("generation %d exited with status %d after %s", z.state.Generation, status, ran)
}

// backoff has the service of a zathras end by itself and be started
// again after the pause.
func (d *Demo) backoff(z *zathras, status int, signal string, pause time.Duration, at time.Time, then func(at time.Time)) {
	how := d.exited(z, status, signal, at)
	z.state.Phase = wire.PhaseBackoff
	z.state.RestartAt = at.Add(pause)
	z.state.Restarts++
	z.ring.Add(logring.Zathras, fmt.Sprintf("%s; starting it again in %s", how, pause))
	d.after(at, pause, func(at time.Time) {
		d.started(z, z.state.Generation, at)
		if then != nil {
			then(at)
		}
	})
}

// crash is the life of a zathras whose service exits a moment after
// every start: it is in backoff nearly all the time.
func (d *Demo) crash(z *zathras, at time.Time) {
	z.since = at.Add(-1300 * time.Millisecond)
	for _, line := range []string{
		"panic: runtime error: invalid memory address or nil pointer dereference",
		"[signal SIGSEGV: segmentation violation code=0x1 addr=0x18 pc=0x4a51c2]",
		"",
		"goroutine 1 [running]:",
		"main.(*queue).next(0x0)",
		"\t/build/worker/queue.go:88 +0x22",
		"main.main()",
		"\t/build/worker/main.go:41 +0x1b4",
	} {
		z.ring.Add(logring.Stderr, line)
	}
	d.backoff(z, 2, "", 8*time.Second, at, func(at time.Time) {
		z.ring.Add(logring.Stdout, "worker: connecting to the queue jobs")
		d.after(at, 1300*time.Millisecond, func(at time.Time) { d.crash(z, at) })
	})
}

// restart has a zathras stop its service, which takes its time over it,
// and start it again.
func (d *Demo) restart(z *zathras, at time.Time) {
	z.state.Phase = wire.PhaseStopping
	d.dirty = true
	d.after(at, 6*time.Second, func(at time.Time) {
		z.ring.Add(logring.Zathras, d.exited(z, 0, "", at))
		d.started(z, z.state.Generation, at)
	})
}

// record adds a rollout of a new generation that ended as it is given.
func (d *Demo) record(svc *service, state wire.RolloutState, reason string, created time.Time, took time.Duration) {
	r := d.rollOut(svc, created)
	r.State, r.Reason, r.Finished = state, reason, created.Add(took)
	if state == wire.RolloutCompleted {
		svc.stable = r.Generation
	}
}

// rollOut records a new generation of a service and the rollout to it,
// and returns the rollout, which is the last one of the service until
// the next.
func (d *Demo) rollOut(svc *service, at time.Time) *wire.RolloutInfo {
	svc.generation++
	d.rollouts++
	svc.rollouts = slices.Insert(svc.rollouts, 0, wire.RolloutInfo{
		ID:         d.rollouts,
		Service:    svc.name,
		Generation: svc.generation,
		Parallel:   2,
		State:      wire.RolloutPreparing,
		Created:    at,
		CreatedBy:  operator,
	})
	if len(svc.rollouts) > rolloutsKept {
		svc.rollouts = svc.rollouts[:rolloutsKept]
	}
	for _, z := range svc.zathrases {
		z.state.Preparing = svc.generation
	}
	d.dirty = true
	return &svc.rollouts[0]
}

// cycle is a minute of the service web: a rollout that is prepared, then
// activated two zathrases at a time, and completed, and after it a
// zathras whose service exits and one that goes. Every other time the
// first rollout does not get there: it is superseded, or it halts.
func (d *Demo) cycle(at time.Time, n int) {
	web := d.services["web"]
	zs := web.zathrases
	last := func() *wire.RolloutInfo { return &web.rollouts[0] }
	begin := time.Duration(0)

	d.rollOut(web, at)
	switch n % 4 {
	case 1:
		// The operator rolls out again before the first is prepared.
		begin = 6 * time.Second
		d.after(at, 2*time.Second, func(time.Time) { d.prepared(zs[0]) })
		d.after(at, begin, func(at time.Time) {
			d.finish(last(), wire.RolloutSuperseded, "", at)
			d.rollOut(web, at)
		})
	case 3:
		// A zathras cannot fetch the closure; the operator pushes it and
		// rolls out again.
		begin = 9 * time.Second
		d.after(at, 2*time.Second, func(time.Time) { d.prepared(zs[0]) })
		d.after(at, 5*time.Second, func(at time.Time) {
			d.finish(last(), wire.RolloutHalted, zs[2].name+": preload: the store has no "+
				strings.TrimSuffix(strings.TrimPrefix(binaryOf("web", web.generation), "/nix/store/"), "/bin/web"), at)
			for _, z := range zs {
				z.state.Preparing = 0
			}
		})
		d.after(at, begin, func(at time.Time) { d.rollOut(web, at) })
	}

	// Preparing: one zathras after the other has the closure.
	for i, z := range zs {
		d.after(at, begin+time.Duration(i+1)*1500*time.Millisecond, func(time.Time) { d.prepared(z) })
	}
	d.after(at, begin+9*time.Second, func(time.Time) {
		last().State = wire.RolloutActivating
		d.dirty = true
	})
	// Activating: two at a time are stopped, started on the generation,
	// and counted when they have run for a while.
	for b := 0; b*2 < len(zs); b++ {
		batch := zs[b*2 : min(b*2+2, len(zs))]
		t := begin + 10*time.Second + time.Duration(b)*8*time.Second
		d.after(at, t, func(time.Time) {
			for _, z := range batch {
				z.state.Phase = wire.PhaseStopping
			}
			last().Awaited = len(batch)
			d.dirty = true
		})
		d.after(at, t+2*time.Second, func(at time.Time) {
			for _, z := range batch {
				z.ring.Add(logring.Zathras, d.exited(z, 0, "", at))
				d.started(z, web.generation, at)
			}
		})
		d.after(at, t+7*time.Second, func(time.Time) {
			last().Activated += len(batch)
			last().Awaited = 0
			d.dirty = true
		})
	}
	d.after(at, begin+34*time.Second, func(at time.Time) {
		d.finish(last(), wire.RolloutCompleted, "", at)
		web.stable = web.generation
	})

	// The second zathras indexes after every start, and says so a
	// thousand times a second for ten seconds.
	d.after(at, begin+14*time.Second, func(at time.Time) { d.flood(zs[1], at, 0) })
	// Every other minute the service of the fourth exits by itself.
	if n%2 == 0 {
		d.after(at, begin+37*time.Second, func(at time.Time) {
			if n%4 == 0 {
				zs[3].ring.Add(logring.Stderr, "\x1b[31mFATAL\x1b[0m db: too many connections; giving up")
				d.backoff(zs[3], 1, "", 9*time.Second, at, nil)
			} else {
				d.backoff(zs[3], -1, "SIGKILL", 9*time.Second, at, nil) // out of memory
			}
		})
	}
	// The fifth goes for some seconds, and is back with the ID it had.
	d.after(at, begin+43*time.Second, func(time.Time) { d.leave(zs[4]) })
	d.after(at, begin+49*time.Second, func(at time.Time) { d.join(zs[4], at) })

	d.after(at, cycleEvery, func(at time.Time) { d.cycle(at, n+1) })
}

// prepared has a zathras have the closure of the newest generation of
// its service.
func (d *Demo) prepared(z *zathras) {
	r := &z.service.rollouts[0]
	if z.state.Preparing != r.Generation {
		return
	}
	z.state.Preparing = 0
	z.state.Prepared = append(z.state.Prepared, r.Generation)
	if len(z.state.Prepared) > 3 {
		z.state.Prepared = z.state.Prepared[len(z.state.Prepared)-3:]
	}
	r.Prepared++
	d.dirty = true
}

// finish ends a rollout.
func (d *Demo) finish(r *wire.RolloutInfo, state wire.RolloutState, reason string, at time.Time) {
	r.State, r.Reason, r.Finished = state, reason, at
	d.dirty = true
}

// leave has a zathras go: it is in no fleet, and who followed its log or
// had a shell in it is told.
func (d *Demo) leave(z *zathras) {
	z.present = false
	close(z.left)
	z.samples = nil // draal forgets one that has gone
	d.dirty = true
}

// join has a zathras that went come back.
func (d *Demo) join(z *zathras, at time.Time) {
	z.present = true
	z.left = make(chan struct{})
	z.connected = at
	d.dirty = true
}

// sample gives every zathras that has figures the sample of the time.
func (d *Demo) sample(at time.Time) {
	for _, z := range d.all {
		if z.figures == nil {
			continue
		}
		s := z.figures.next(at, z.state.Phase == wire.PhaseRunning || z.state.Phase == wire.PhaseStopping)
		if !z.present {
			continue
		}
		z.samples = append(z.samples, s)
		if most := int(history / sampleEvery); len(z.samples) > most {
			z.samples = slices.Delete(z.samples, 0, len(z.samples)-most)
		}
	}
	d.dirty = true
}

// past makes up what a zathras has from before the start: the samples
// of the last fifteen minutes and a ring that is full.
func (d *Demo) past(z *zathras) {
	if z.figures != nil {
		for at := d.start.Add(-history); at.Before(d.start); at = at.Add(sampleEvery) {
			z.samples = append(z.samples, z.figures.next(at, z.state.Phase == wire.PhaseRunning))
		}
	}
	if z.talks == 0 {
		return // its ring is empty
	}
	// As many lines as the ring holds and some more, the last of them
	// just before the start.
	const lines = 420
	d.at = d.start.Add(-time.Duration(lines) * z.talks)
	z.ring.Add(logring.Zathras, fmt.Sprintf("started generation %d: %s (pid %d)", z.state.Generation, z.state.Binary, z.pid))
	for range lines {
		d.at = d.at.Add(z.talks)
		z.ring.Add(d.line(z, d.at))
	}
	d.at = d.start
}

// talk has the service of a zathras write a line, and another one later.
func (d *Demo) talk(z *zathras, at time.Time) {
	if z.state.Phase == wire.PhaseRunning {
		z.ring.Add(d.line(z, at))
	}
	d.after(at, d.around(z.talks), func(at time.Time) { d.talk(z, at) })
}

// flood has the service of a zathras write a thousand lines a second for
// ten seconds, and then three thousand at once: more than waits for one
// who follows, who is told how many were lost.
func (d *Demo) flood(z *zathras, at time.Time, written int) {
	const each, all, burst = 10, 10000, 3000
	if z.state.Phase != wire.PhaseRunning {
		return
	}
	if written >= all {
		for i := range burst {
			z.ring.Add(logring.Stdout, fmt.Sprintf("INFO  reindex: flushed segment %04d of %d", i+1, burst))
		}
		z.ring.Add(logring.Stdout, "\x1b[32mINFO\x1b[0m  reindex: done")
		return
	}
	for i := range each {
		z.ring.Add(logring.Stdout, fmt.Sprintf("INFO  reindex: document %05d of %d indexed (shard %d)", written+i+1, all, (written+i)%7))
	}
	d.after(at, 10*time.Millisecond, func(at time.Time) { d.flood(z, at, written+each) })
}

// fleet returns the fleet as it is at now. The caller holds d.mu.
func (d *Demo) fleet(now time.Time) wire.Fleet {
	f := wire.Fleet{Services: make([]wire.Service, 0, len(d.services))}
	for _, svc := range d.services {
		out := wire.Service{Name: svc.name, Stable: svc.stable, Zathrases: []wire.Zathras{}}
		if len(svc.rollouts) > 0 {
			last := svc.rollouts[0]
			out.Rollout = &last
		}
		for _, z := range svc.zathrases {
			if !z.present {
				continue
			}
			state := z.state
			state.Prepared = slices.Clone(state.Prepared)
			if state.LastExit != nil {
				exit := *state.LastExit
				state.LastExit = &exit
			}
			if state.Phase == wire.PhaseRunning {
				state.PID, state.RunningFor = z.pid, now.Sub(z.since)
			}
			if len(z.samples) > 0 {
				s := z.samples[len(z.samples)-1]
				state.Sample = &s
			}
			out.Zathrases = append(out.Zathrases, wire.Zathras{ID: z.id, Name: z.name, Connected: z.connected, State: state})
		}
		slices.SortFunc(out.Zathrases, func(a, b wire.Zathras) int {
			return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
		})
		f.Services = append(f.Services, out)
	}
	slices.SortFunc(f.Services, func(a, b wire.Service) int { return cmp.Compare(a.Name, b.Name) })
	return f
}

// connected returns a zathras that is connected, or unavailable, as draal
// answers for one that is not.
func (d *Demo) connected(id wire.ID) (*zathras, error) {
	if z := d.byID[id]; z != nil && z.present {
		return z, nil
	}
	return nil, wire.Errorf(wire.CodeUnavailable, "zathras %s is not connected", id)
}

// Store is not of the demo: neither the page nor the terminal UI asks
// for a store.
func (d *Demo) Store(ctx context.Context, service string) (wire.StoreInfo, error) {
	return wire.StoreInfo{}, wire.Errorf(wire.CodeUnavailable, "the demo has no store")
}

// Rollout is not of the demo: neither the page nor the terminal UI rolls
// anything out.
func (d *Demo) Rollout(ctx context.Context, by wire.ID, req wire.RolloutRequest) (wire.RolloutInfo, error) {
	return wire.RolloutInfo{}, wire.Errorf(wire.CodeUnavailable, "the demo rolls out by itself")
}

// Rollback is not of the demo: neither the page nor the terminal UI rolls
// anything back.
func (d *Demo) Rollback(ctx context.Context, by wire.ID, req wire.RollbackRequest) (wire.RolloutInfo, error) {
	return wire.RolloutInfo{}, wire.Errorf(wire.CodeUnavailable, "the demo rolls back nothing")
}

// Watch sends the fleet at once and after every change, at most once in
// watchEvery, until ctx is done, as draal does: who does not read misses
// the fleets between and reads the newest next.
func (d *Demo) Watch(ctx context.Context) (<-chan wire.Fleet, error) {
	changed := make(chan struct{}, 1)
	d.mu.Lock()
	d.subs[changed] = struct{}{}
	first := d.fleet(time.Now())
	d.mu.Unlock()

	out := make(chan wire.Fleet, 1)
	out <- first
	go func() {
		defer close(out)
		defer func() {
			d.mu.Lock()
			delete(d.subs, changed)
			d.mu.Unlock()
		}()
		pause := time.NewTimer(watchEvery)
		defer pause.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-changed:
			}
			select {
			case <-ctx.Done():
				return
			case <-pause.C:
			}
			d.mu.Lock()
			f := d.fleet(time.Now())
			d.mu.Unlock()
			select {
			case out <- f:
			default:
				select {
				case <-out:
				default:
				}
				out <- f
			}
			pause.Reset(watchEvery)
		}
	}()
	return out, nil
}

// Rollouts returns the last 20 rollouts of a service, newest first, as
// the database of draal has them: without the counts of a fleet.
func (d *Demo) Rollouts(ctx context.Context, name string) ([]wire.RolloutInfo, error) {
	if err := wire.ValidService(name); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	svc := d.services[name]
	if svc == nil {
		return nil, nil
	}
	list := slices.Clone(svc.rollouts)
	for i := range list {
		list[i].Prepared, list[i].Activated, list[i].Awaited = 0, 0, 0
	}
	return list, nil
}

// History returns the samples of a connected zathras, oldest first.
func (d *Demo) History(ctx context.Context, id wire.ID) ([]wire.Sample, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	z, err := d.connected(id)
	if err != nil {
		return nil, err
	}
	return slices.Clone(z.samples), nil
}

// Logs sends the ring of a connected zathras, and with follow what
// follows, until ctx is done or the zathras goes.
func (d *Demo) Logs(ctx context.Context, id wire.ID, follow bool) (<-chan wire.LogLine, error) {
	d.mu.Lock()
	z, err := d.connected(id)
	if err != nil {
		d.mu.Unlock()
		return nil, err
	}
	left := z.left
	d.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	ring, more := z.ring.Lines(), (<-chan wire.LogLine)(nil)
	if follow {
		ring, more = z.ring.Follow(ctx, followBuffer)
	}
	out := make(chan wire.LogLine)
	go func() {
		defer close(out)
		defer cancel()
		send := func(l wire.LogLine) bool {
			select {
			case out <- l:
				return true
			case <-ctx.Done():
			case <-left:
			}
			return false
		}
		for _, l := range ring {
			if !send(l) {
				return
			}
		}
		for follow {
			select {
			case l, ok := <-more:
				if !ok || !send(l) {
					return
				}
			case <-ctx.Done():
				return
			case <-left:
				return
			}
		}
	}()
	return out, nil
}

// Shell starts a shell on this machine: there is no container.
func (d *Demo) Shell(ctx context.Context, id wire.ID, req wire.ShellRequest) (draal.Shell, error) {
	d.mu.Lock()
	z, err := d.connected(id)
	if err != nil {
		d.mu.Unlock()
		return nil, err
	}
	left := z.left
	d.mu.Unlock()
	return startShell(id, req, left)
}

// ServiceOf returns the service of a connected zathras.
func (d *Demo) ServiceOf(id wire.ID) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	z, err := d.connected(id)
	if err != nil {
		return "", false
	}
	return z.service.name, true
}
