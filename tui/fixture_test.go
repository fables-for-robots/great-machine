package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/fables-for-robots/great-machine/wire"
)

// t0 is the time of the fleet the tests draw.
var t0 = time.Date(2026, 10, 9, 12, 0, 0, 0, time.Local)

// fakeClock is the clock of a model under test.
type fakeClock struct{ at time.Time }

func (c *fakeClock) now() time.Time { return c.at }

// idOf returns the endpoint ID of the n-th zathras of the tests: they
// differ in their first characters.
func idOf(n int) wire.ID {
	return wire.ID(fmt.Sprintf("%02x", n) + strings.Repeat("ab12cd34", 8)[:62])
}

// longName is a name as long as a name may be: 253 characters.
var longName = ("worker-batch-eu-central-1a." + strings.Repeat("node-0123456789abcdef.", 12))[:245] + "internal"

// sampleOf returns a sample of the time t0.
func sampleOf(cpu float64, memory, limit uint64) *wire.Sample {
	return &wire.Sample{At: t0, CPU: cpu, Memory: memory, MemoryLimit: limit, DiskRead: 0, DiskWrite: 22 << 10, NetRx: 48 << 10, NetTx: 31 << 10}
}

// fixtureFleet returns the fleet of the tests: four services with what
// the views have to show, as the demo has them.
//
//   - empty: rolled out, and no zathras is connected.
//   - fresh: two zathrases, and nothing was rolled out.
//   - web: a rollout under way, zathrases in every stage of it and in
//     every phase, two of one name.
//   - worker: a rollout that is halted, a zathras without samples, one
//     that runs nothing, and one with a name of 253 characters.
func fixtureFleet() wire.Fleet {
	binary := func(service string, generation uint64) string {
		return fmt.Sprintf("/nix/store/kzrvqcn6yd2p8bqlhbqm8wl5bh7f0r1s-%s-1.4.%d/bin/%s", service, generation, service)
	}
	webArgs := []string{"--listen", ":8080", "--db", "postgres://db.internal/web", "--workers", "8"}
	web := func(n int, name string, st wire.State) wire.Zathras {
		st.Binary, st.Args = binary("web", st.Generation), webArgs
		st.Sample = sampleOf(0.25+0.15*float64(n), uint64(200+20*n)<<20, 512<<20)
		return wire.Zathras{ID: idOf(0x20 + n), Name: name, Connected: t0.Add(-time.Duration(5+3*n) * time.Hour), State: st}
	}
	worker := func(n int, name string, st wire.State) wire.Zathras {
		if st.Generation != 0 {
			st.Binary, st.Args = binary("worker", st.Generation), []string{"--queue", "jobs"}
		}
		return wire.Zathras{ID: idOf(0x30 + n), Name: name, Connected: t0.Add(-71 * time.Hour), State: st}
	}
	return wire.Fleet{Services: []wire.Service{
		{
			Name: "empty", Stable: 2, Zathrases: []wire.Zathras{},
			Rollout: &wire.RolloutInfo{ID: 2, Service: "empty", Generation: 2, Parallel: 2, State: wire.RolloutCompleted,
				Created: t0.Add(-30 * time.Hour), CreatedBy: idOf(0xee), Finished: t0.Add(-30*time.Hour + 3*time.Second)},
		},
		{
			Name: "fresh",
			Zathrases: []wire.Zathras{
				{ID: idOf(0x10), Name: "fresh-0", Connected: t0.Add(-3 * time.Minute), State: wire.State{Phase: wire.PhaseIdle, Sample: sampleOf(0.002, 14<<20, 256<<20)}},
				{ID: idOf(0x11), Name: "fresh-1", Connected: t0.Add(-2 * time.Minute), State: wire.State{Phase: wire.PhaseIdle}},
			},
		},
		{
			Name: "web", Stable: 20,
			Rollout: &wire.RolloutInfo{ID: 31, Service: "web", Generation: 21, Parallel: 2, State: wire.RolloutActivating,
				Created: t0.Add(-14 * time.Second), CreatedBy: idOf(0xee), Prepared: 5, Activated: 1, Awaited: 1},
			Zathrases: []wire.Zathras{
				web(0, "web-7d9f8b6c4-2xkqp", wire.State{Generation: 21, Phase: wire.PhaseRunning, PID: 4242, RunningFor: 9 * time.Second, Prepared: []uint64{20, 21}}),
				web(1, "web-7d9f8b6c4-8hjwz", wire.State{Generation: 20, Phase: wire.PhaseStopping, Prepared: []uint64{20, 21}}),
				web(2, "web-7d9f8b6c4-8hjwz", wire.State{Generation: 20, Phase: wire.PhaseRunning, PID: 977, RunningFor: 46*time.Minute + 12*time.Second, Prepared: []uint64{20}, Preparing: 21}),
				web(3, "web-7d9f8b6c4-mn4tc", wire.State{Generation: 21, Phase: wire.PhaseBackoff, RestartAt: t0.Add(6 * time.Second), Restarts: 4, Prepared: []uint64{20, 21},
					LastExit: &wire.Exit{Generation: 21, Status: 1, At: t0.Add(-3 * time.Second), Ran: 2300 * time.Millisecond}}),
				web(4, "web-7d9f8b6c4-q5rvd", wire.State{Generation: 20, Phase: wire.PhaseRunning, PID: 31000, RunningFor: 46 * time.Minute, Prepared: []uint64{19, 20},
					LastExit: &wire.Exit{Generation: 19, Status: -1, Signal: "SIGKILL", At: t0.Add(-3 * time.Hour), Ran: 41 * time.Minute}}),
			},
		},
		{
			Name: "worker", Stable: 3,
			Rollout: &wire.RolloutInfo{ID: 30, Service: "worker", Generation: 4, Parallel: 2, State: wire.RolloutHalted,
				Reason:  "worker-1: exited with status 2 after 1.3s",
				Created: t0.Add(-26 * time.Minute), CreatedBy: idOf(0xee), Finished: t0.Add(-26*time.Minute + 21*time.Second), Prepared: 5, Activated: 0, Awaited: 0},
			Zathrases: []wire.Zathras{
				worker(0, "worker-0", wire.State{Generation: 3, Phase: wire.PhaseRunning, PID: 812, RunningFor: 26 * time.Hour, Prepared: []uint64{3, 4}, Sample: sampleOf(0.6, 300<<20, 0)}),
				worker(1, "worker-1", wire.State{Generation: 4, Phase: wire.PhaseBackoff, RestartAt: t0.Add(4 * time.Second), Restarts: 187, Prepared: []uint64{3, 4}, Sample: sampleOf(0.002, 14<<20, 512<<20),
					LastExit: &wire.Exit{Generation: 4, Status: 2, At: t0.Add(-4 * time.Second), Ran: 1300 * time.Millisecond}}),
				worker(2, "worker-2", wire.State{Generation: 3, Phase: wire.PhaseRunning, PID: 99, RunningFor: 9 * time.Hour, Prepared: []uint64{3, 4}}), // no samples
				worker(3, "worker-3", wire.State{Phase: wire.PhaseIdle}),
				worker(4, longName, wire.State{Generation: 3, Phase: wire.PhaseRunning, PID: 7, RunningFor: 50 * time.Hour, Prepared: []uint64{3, 4}, Sample: sampleOf(0.9, 700<<20, 1024<<20)}),
			},
		},
	}}
}

// fixtureRollouts returns the rollouts of a service of fixtureFleet as
// draal lists them: newest first, without the counts of a fleet.
func fixtureRollouts(service string) []wire.RolloutInfo {
	f := fixtureFleet()
	var list []wire.RolloutInfo
	for _, svc := range f.Services {
		if svc.Name != service || svc.Rollout == nil {
			continue
		}
		last := *svc.Rollout
		last.Prepared, last.Activated, last.Awaited = 0, 0, 0
		list = append(list, last)
		for i := 1; i < 20 && uint64(i) < last.ID && uint64(i) < last.Generation; i++ {
			r := wire.RolloutInfo{ID: last.ID - uint64(i), Service: service, Generation: last.Generation - uint64(i), Parallel: 2, State: wire.RolloutCompleted,
				Created: last.Created.Add(-time.Duration(i) * 47 * time.Minute), CreatedBy: idOf(0xee)}
			r.Finished = r.Created.Add(time.Duration(60+7*i) * time.Second)
			switch {
			case i%6 == 3:
				r.State, r.Reason = wire.RolloutHalted, "web-7d9f8b6c4-8hjwz: could not be started: fork/exec /nix/store/kzrvqcn6yd2p8bqlhbqm8wl5bh7f0r1s-web-1.4.6/bin/web: exec format error"
			case i%5 == 2:
				r.State = wire.RolloutSuperseded
			}
			list = append(list, r)
		}
	}
	return list
}

// fixtureSamples returns fifteen minutes of samples that end at t0, two
// seconds apart.
func fixtureSamples() []wire.Sample {
	var out []wire.Sample
	for i := range 450 {
		// A load that swings, with a peak towards the end, memory that
		// creeps up and is collected, and a network with a burst.
		wave := 0.5 + 0.5*math.Sin(float64(i)/40)
		cpu := 0.08 + 0.22*wave
		if i > 380 && i < 400 {
			cpu += 0.4
		}
		burst := 0.0
		if i > 200 && i < 230 {
			burst = 3 << 20
		}
		read := 0.0
		if i%90 == 45 {
			read = 6 << 20
		}
		out = append(out, wire.Sample{
			At: t0.Add(-time.Duration(449-i) * 2 * time.Second), CPU: cpu, Memory: uint64(180+(i%150)/2) << 20, MemoryLimit: 512 << 20,
			DiskRead: read, DiskWrite: (4 + 30*wave) * 1024, NetRx: (20+60*wave)*1024 + burst, NetTx: (10+30*wave)*1024 + burst/3,
		})
	}
	return out
}

// fixtureLines returns n lines of a log, the last of them at t0: what a
// service says of its requests, among them lines on standard error, a
// line of zathras, a line with colours and one longer than any
// terminal is wide.
func fixtureLines(n int) []wire.LogLine {
	var out []wire.LogLine
	for i := range n {
		l := wire.LogLine{At: t0.Add(-time.Duration(n-1-i) * 300 * time.Millisecond), Stream: "stdout",
			Text: fmt.Sprintf("\x1b[2m12:00:00.000\x1b[0m \x1b[32mINFO\x1b[0m  http: GET /api/orders/%d \x1b[32m200\x1b[0m 1.2ms", i)}
		switch {
		case i%61 == 60:
			l.Stream, l.Text = "stderr", "\x1b[1;31mERROR\x1b[0m upstream timed out after 5s: POST /api/checkout\tat checkout.submit (/build/web/checkout.go:214)\tat http.serve (/build/web/server.go:88)"
		case i%97 == 96:
			l.Stream, l.Text = "zathras", "started generation 20: /nix/store/kzrvqcn6yd2p8bqlhbqm8wl5bh7f0r1s-web-1.4.20/bin/web (pid 977)"
		case i%211 == 210:
			l.Text = "DEBUG request body: " + strings.Repeat("0123456789abcdef", 1024)
		}
		out = append(out, l)
	}
	return out
}

// newTestModel returns a model in a terminal of the given size that has
// begun: it has asked for its first dial.
func newTestModel(w, h int) (*model, *fakeClock) {
	clock := &fakeClock{at: t0}
	m := newModel(Config{Draal: "3f9a2c1b", Now: clock.now})
	m.update(tea.WindowSizeMsg{Width: w, Height: h})
	m.start()
	return m, clock
}

// goLive has the dial that is under way succeed, and the fleet come.
func goLive(m *model, f wire.Fleet) []effect {
	m.update(dialedMsg{epoch: m.epoch})
	return m.update(fleetMsg{epoch: m.epoch, fleet: f})
}

// keyOfName returns the message of a key by the name the tests have for
// it: what Bubble Tea makes of the key when it is typed.
func keyOfName(name string) tea.KeyPressMsg {
	special := map[string]rune{
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight, "enter": tea.KeyEnter,
		"esc": tea.KeyEscape, "backspace": tea.KeyBackspace, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "tab": tea.KeyTab,
	}
	switch code, ok := special[name]; {
	case ok:
		return tea.KeyPressMsg{Code: code}
	case name == "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: []rune(name)[0], Text: name}
}

// press types keys, one after the other, and returns what the model
// wanted done for all of them.
func press(m *model, names ...string) []effect {
	var effs []effect
	for _, name := range names {
		effs = append(effs, m.update(keyOfName(name))...)
	}
	return effs
}

// onService returns a model that is live and shows the service.
func onService(w, h int, service string) (*model, *fakeClock) {
	m, clock := newTestModel(w, h)
	goLive(m, fixtureFleet())
	for m.svcName != service {
		press(m, "down")
	}
	press(m, "enter")
	m.update(rolloutsMsg{epoch: m.epoch, n: m.rolloutsN, service: service, rollouts: fixtureRollouts(service)})
	return m, clock
}

// onZathras returns a model that is live and shows the n-th zathras of
// the service, with its samples and four hundred lines of its log.
func onZathras(w, h int, service string, n int) (*model, *fakeClock) {
	m, clock := onService(w, h, service)
	for range n {
		press(m, "down")
	}
	press(m, "enter")
	if m.seen.State.Sample != nil {
		m.update(historyMsg{epoch: m.epoch, n: m.historyN, zathras: m.zathras, samples: fixtureSamples()})
	} else {
		m.update(historyMsg{epoch: m.epoch, n: m.historyN, zathras: m.zathras})
	}
	m.update(logMsg{epoch: m.epoch, id: m.logID, lines: fixtureLines(400)})
	return m, clock
}
