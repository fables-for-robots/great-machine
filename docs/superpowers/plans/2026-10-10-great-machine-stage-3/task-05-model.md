### Task 5: The model

The state of the terminal UI and its `update`: a function of the model and a message that touches no terminal and no network, reads no clock but the one it was given, and says what it wants done as a list of effects. The program of Task 7 hands it the messages and carries out the effects; the tests here do both by hand. No view yet beyond what the tests need.

**Review Focus pinned here:** 3, by `TestConnectionLostAndBack`, `TestViewsOpenedWhileTheConnectionIsLost` and `TestTheFirstDial` (lost and back on each view, on the view it was on, with what the view holds asked for again; pauses that double; a key ends a pause); 4, by `TestZathrasGoesWhileItsViewIsOpenAndComesBack` and `TestWhatIsChosenStaysChosen` (a zathras, and a service, that go while their view is open); 5, by `TestEverySizeOfTerminal` (80×24, 200×60, 20×5, and every size from 1×1 to 100×40: no panic, no more lines than rows, no line wider than the terminal, one line that says so when it is too small) and `TestLogScrolls`.

**Files:**
- Create: `tui/tui.go` (the package comment; `Operator`, `Shell`, `Config` of the contract)
- Create: `tui/model.go` (the model, the messages, the effects)
- Create: `tui/shell.go` (the place of the shell: its effect, its message, and what the key `s` does until the shell is built)
- Create: `tui/view.go` (a view for the tests of this task; Task 6 replaces it)
- Test: `tui/fixture_test.go`, `tui/scenes_test.go`, `tui/model_test.go`
- Modify: `go.mod`, `go.sum` (Bubble Tea v2.0.10)

**Interfaces:**
- Consumes: Task 4 (`clean`, `named`, `capped`, `cut`, `parseLine`, `wrapSpans`, `span`, `look`, `ellipsis`, `mergeSamples`, `logBuffer`, `entry`, `logKept`; in the tests `plain`, `unsafe`, `textOf`, `lineAt`); `term.Session` (`term/`); `wire`; of Bubble Tea the two messages `tea.KeyPressMsg` (its `String()` is the name of the key: `"up"`, `"enter"`, `"esc"`, `"pgup"`, `"G"`, `"ctrl+c"`) and `tea.WindowSizeMsg`.
- Produces, exported, exactly as the contract has them: `type Operator interface`, `type Shell interface`, `type Config struct`. And `func (cfg Config) withDefaults() Config`.
- Produces, for Tasks 6 and 7 and for the task of the shell (all unexported):
  - `type model struct` (its fields are what the views read: see the file), `func newModel(cfg Config) *model`, `func (m *model) start() []effect`, `func (m *model) update(msg tea.Msg) []effect`, `func (m *model) view() string`, `func (m *model) again() []effect`.
  - **The messages by which the program tells the model things.** Each but `tickMsg` and `retryMsg` carries `epoch`, the number of the connection it is of; the model counts the connections, and drops what is of another one than the one it waits for.
    - `dialedMsg{epoch int}`: `Dial` has connected and the watch was begun.
    - `dialFailedMsg{epoch int; err error}`: `Dial`, or the beginning of the watch, failed.
    - `fleetMsg{epoch int; fleet wire.Fleet}`: a fleet of the watch. The first one of a connection makes it live.
    - `lostMsg{epoch int; err error}`: the watch ended, or a request failed with something that is no `*wire.Error`.
    - `rolloutsMsg{epoch, n int; service string; rollouts []wire.RolloutInfo; err error}`, `historyMsg{epoch, n int; zathras wire.ID; samples []wire.Sample; err error}`: the answer, or draal's refusal. `n` is the number of the request it answers (`effRollouts.n`, `effHistory.n`): two requests may be under way, their answers may come in any order, and the model takes the answer to the newest alone.
    - `logMsg{epoch, id int; lines []wire.LogLine}`: lines of the log with that `id`. **Until the first one the model knows only that the log was asked for** (`askedFor`): draal answers a log with its lines alone, and `operator.Client.Logs` returns when the first line has come, so there is no moment at which a log is known to be followed and empty. `logEndedMsg{epoch, id int; err error}`.
    - `retryMsg{n int}`: the pause with that number is over. `tickMsg{}`: a second has passed.
    - `shellEndedMsg{exit int; detached bool; err error}`: a shell has ended (the task of the shell sends it).
  - **The effects**, `type effect any`: `effDial{epoch int}`, `effHangUp{}`, `effWait{pause time.Duration; n int}`, `effRollouts{epoch, n int; service string}`, `effHistory{epoch, n int; zathras wire.ID}`, `effFollow{epoch int; zathras wire.ID; id int}`, `effUnfollow{}`, `effQuit{}`, `effFail{err error}`, `effShell{epoch int; zathras wire.ID}`.
  - `screenServices`, `screenService`, `screenZathras`; `connConnecting`, `connLive`, `connLost`; `askedNot`, `askedFor`, `askedHave`, `askedRefused`, `askedEnded`; `type logRow struct{ entry int; seq uint64; sub int; spans []span }`; `func logPrefix(width int) int`; `func refusal(err error) (*wire.Error, bool)`.
  - What a view has to give the model (Task 6 gives the real ones): `type shared struct`, `func (m *model) tooSmall() bool`, `servicesHeight() int`, `zathrasesHeight() int`, `logHeight() int`, `view() string`, and the constants `leastWidth = 50`, `leastHeight = 12`.
  - In the tests: `t0`, `fakeClock`, `idOf`, `longName`, `fixtureFleet`, `fixtureRollouts`, `fixtureSamples`, `fixtureLines`, `newTestModel`, `goLive`, `press`, `keyOfName`, `onService`, `onZathras`, `wantEffects`, and `scenes() []scene`.

**How the model works, in short:**
- **The fleet** is sorted when it comes (services by name, zathrases by name and then ID), whatever order draal sent it in, and what it says in words (names of zathrases, binaries, arguments, reasons, how a service ended) is capped at 4 KiB a field on the way (`capped`); the name of a service and the ID of a zathras are kept as they came, since draal is asked by them, and capped where they are drawn. What is chosen is a name (of a service) or an ID (of a zathras); when that is gone from the fleet, what is now at its place is chosen.
- **The connection.** `start` wants `effDial{1}`. `dialedMsg`, then the first `fleetMsg`: live. `lostMsg`: lost since now, `effHangUp` and `effWait{RetryFirst}`; `retryMsg`: `effDial` with the next epoch; `dialFailedMsg`: `effWait` with twice the pause, `RetryMax` at most. A key while a pause runs wants the dial at once. A `dialFailedMsg` with a `*wire.Error` of the code `unauthorized`, before any dial ever succeeded, wants `effFail`: the program ends with that error.
- **What a view holds.** Opening a service wants `effRollouts`, and again whenever the ID or the state of its last rollout changes in a fleet. Its answer is taken whatever view is in sight when it comes (the view of one of its zathrases, too), so that no list stays on "Asking draal …"; an answer to an older request than the newest is dropped (`TestRolloutsThatAreAnsweredOnAnotherView`, `TestAnOlderAnswerThatComesLastIsDropped`). Opening a zathras wants `effHistory` and `effFollow`; leaving it wants `effUnfollow`. The first fleet of a new connection, a zathras that is back in the fleet, and one whose time of connecting changed between two fleets want both again; `logBuffer.resume` keeps the lines there are from being taken twice.
- **The log** is rows: a line is one row, or up to four when it is longer than the terminal is wide. The end is followed until one scrolls; then the row one reads stays where it is while lines come.
- **The layout is the view's.** The model asks the view how many rows a list or the log has (`servicesHeight`, `zathrasesHeight`, `logHeight`) when it moves by a page and when it keeps what is chosen in sight, once at the end of every `update`.

- [ ] **Step 1: Add Bubble Tea, and write the failing tests**

```bash
nix develop .#bare --command go get charm.land/bubbletea/v2@v2.0.10
```

Three files of tests. The first is the fleet the tests draw, and how a test gets a model on a view:

`tui/fixture_test.go`:

```go
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
```

`tui/scenes_test.go`: every view and every odd state there is a sentence for, each a model. The test of the sizes below draws them all, as do the tests of the views in the next task.

```go
package tui

import (
	"errors"
	"slices"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// scene is a state of the terminal UI that the tests draw: a view, or
// one of the odd states of one.
type scene struct {
	name  string
	model func(w, h int) *model
}

// scenes returns every view and every odd state there is a sentence
// for.
func scenes() []scene {
	live := func(w, h int, f wire.Fleet) *model {
		m, _ := newTestModel(w, h)
		goLive(m, f)
		return m
	}
	return []scene{
		{"services", func(w, h int) *model { return live(w, h, fixtureFleet()) }},
		{"services-connecting", func(w, h int) *model { m, _ := newTestModel(w, h); return m }},
		{"services-no-answer", func(w, h int) *model {
			m, _ := newTestModel(w, h)
			m.update(dialFailedMsg{epoch: 1, err: errors.New("connecting to draal 3f9a2c1b: context deadline exceeded")})
			return m
		}},
		{"services-none", func(w, h int) *model { return live(w, h, wire.Fleet{Services: []wire.Service{}}) }},
		{"services-lost", func(w, h int) *model {
			m, clock := newTestModel(w, h)
			goLive(m, fixtureFleet())
			clock.at = t0.Add(42 * time.Second)
			m.update(lostMsg{epoch: 1})
			clock.at = t0.Add(time.Minute + 15*time.Second)
			m.update(tickMsg{})
			return m
		}},
		{"services-help", func(w, h int) *model { m := live(w, h, fixtureFleet()); press(m, "?"); return m }},
		{"service-rollout-under-way", func(w, h int) *model { m, _ := onService(w, h, "web"); return m }},
		{"service-halted", func(w, h int) *model { m, _ := onService(w, h, "worker"); press(m, "G"); return m }},
		{"service-no-zathras", func(w, h int) *model { m, _ := onService(w, h, "empty"); return m }},
		{"service-no-rollout", func(w, h int) *model { m, _ := onService(w, h, "fresh"); return m }},
		{"service-asking", func(w, h int) *model {
			m := live(w, h, fixtureFleet())
			press(m, "down", "down", "enter")
			return m
		}},
		{"service-refused", func(w, h int) *model {
			m := live(w, h, fixtureFleet())
			press(m, "down", "down", "enter")
			m.update(rolloutsMsg{epoch: 1, n: m.rolloutsN, service: "web", err: wire.Errorf(wire.CodeInternal, "reading the rollouts: the database is locked")})
			return m
		}},
		{"service-gone", func(w, h int) *model {
			m, _ := onService(w, h, "web")
			f := fixtureFleet()
			f.Services = slices.Delete(f.Services, 2, 3)
			m.update(fleetMsg{epoch: 1, fleet: f})
			return m
		}},
		{"service-lost", func(w, h int) *model {
			m, clock := onService(w, h, "web")
			m.update(lostMsg{epoch: 1})
			clock.at = t0.Add(3 * time.Second)
			m.update(tickMsg{})
			return m
		}},
		{"zathras", func(w, h int) *model { m, _ := onZathras(w, h, "web", 2); return m }},
		{"zathras-backoff", func(w, h int) *model { m, _ := onZathras(w, h, "web", 3); return m }},
		{"zathras-no-samples", func(w, h int) *model { m, _ := onZathras(w, h, "worker", 2); return m }},
		{"zathras-idle-no-log", func(w, h int) *model {
			m, _ := onService(w, h, "fresh")
			press(m, "down", "enter")
			m.update(historyMsg{epoch: 1, n: m.historyN, zathras: m.zathras})
			return m
		}},
		{"zathras-asking", func(w, h int) *model { m, _ := onService(w, h, "web"); press(m, "enter"); return m }},
		{"zathras-long-name", func(w, h int) *model { m, _ := onZathras(w, h, "worker", 4); return m }},
		{"zathras-gone", func(w, h int) *model {
			m, clock := onZathras(w, h, "web", 4)
			clock.at = t0.Add(10 * time.Second)
			m.update(logEndedMsg{epoch: 1, id: m.logID})
			f := fixtureFleet()
			f.Services[2].Zathrases = f.Services[2].Zathrases[:4]
			m.update(fleetMsg{epoch: 1, fleet: f})
			clock.at = t0.Add(25 * time.Second)
			m.update(tickMsg{})
			return m
		}},
		{"zathras-lost", func(w, h int) *model {
			m, clock := onZathras(w, h, "web", 2)
			m.update(lostMsg{epoch: 1, err: errors.New("the connection to draal ended")})
			m.update(retryMsg{n: m.waitN})
			m.update(dialFailedMsg{epoch: m.epoch, err: errors.New("connecting to draal 3f9a2c1b: context deadline exceeded")})
			clock.at = t0.Add(90 * time.Second)
			m.update(tickMsg{})
			return m
		}},
		{"zathras-scrolled", func(w, h int) *model { m, _ := onZathras(w, h, "web", 2); press(m, "pgup", "pgup"); return m }},
		{"zathras-notice", func(w, h int) *model { m, _ := onZathras(w, h, "web", 2); press(m, "s"); return m }},
		{"zathras-help", func(w, h int) *model { m, _ := onZathras(w, h, "web", 2); press(m, "?"); return m }},
	}
}
```

`tui/model_test.go`:

```go
package tui

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fables-for-robots/great-machine/wire"
)

// wantEffects fails the test when the effects are not those wanted.
func wantEffects(t *testing.T, what string, got []effect, want ...effect) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	// A request has a number, which counts up. Where a test does not
	// name it, it is not what the test is about.
	got = slices.Clone(got)
	for i := range min(len(got), len(want)) {
		switch w := want[i].(type) {
		case effRollouts:
			if g, ok := got[i].(effRollouts); ok && w.n == 0 {
				g.n = 0
				got[i] = g
			}
		case effHistory:
			if g, ok := got[i].(effHistory); ok && w.n == 0 {
				g.n = 0
				got[i] = g
			}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: the model wants %+v, want %+v", what, got, want)
	}
}

func TestTheFirstFleetMakesTheConnectionLive(t *testing.T) {
	clock := &fakeClock{at: t0}
	m := newModel(Config{Now: clock.now})
	wantEffects(t, "at the start", m.start(), effDial{epoch: 1})
	if m.conn != connConnecting || m.have {
		t.Errorf("before draal answered the connection is %v, and a fleet is there: %v", m.conn, m.have)
	}
	wantEffects(t, "dialed", m.update(dialedMsg{epoch: 1}))
	if m.conn != connConnecting {
		t.Errorf("dialed, and before the first fleet, the connection is %v", m.conn)
	}
	// The fleet comes in any order, and is kept in one: services by
	// name, zathrases by name and then by ID.
	f := fixtureFleet()
	slices.Reverse(f.Services)
	slices.Reverse(f.Services[1].Zathrases) // web
	wantEffects(t, "the first fleet", m.update(fleetMsg{epoch: 1, fleet: f}))
	if m.conn != connLive || !m.have || !m.fleetAt.Equal(t0) {
		t.Errorf("after the first fleet the connection is %v", m.conn)
	}
	var names []string
	for _, svc := range m.fleet.Services {
		names = append(names, svc.Name)
	}
	web := m.fleet.Services[2].Zathrases
	if strings.Join(names, " ") != "empty fresh web worker" || web[1].Name != web[2].Name || web[1].ID >= web[2].ID || web[0].Name != "web-7d9f8b6c4-2xkqp" {
		t.Errorf("the services are %v, and the zathrases of web %v %v %v", names, web[0].Name, web[1].ID[:2], web[2].ID[:2])
	}
	if m.svcName != "empty" || m.screen != screenServices {
		t.Errorf("the first service is not chosen: %q", m.svcName)
	}
	// A fleet of a connection that is not the one there is, is not read.
	m.update(fleetMsg{epoch: 0, fleet: wire.Fleet{}})
	m.update(fleetMsg{epoch: 2, fleet: wire.Fleet{}})
	if len(m.fleet.Services) != 4 {
		t.Error("a fleet of another connection was taken")
	}
}

// Every key of the contract's table, on every view.
func TestKeys(t *testing.T) {
	type state struct {
		screen screen
		chosen string // the service, or the first two characters of the zathras' ID
		help   bool
	}
	at := func(m *model) state {
		s := state{screen: m.screen, help: m.help}
		switch m.screen {
		case screenServices:
			s.chosen = m.svcName
		case screenService:
			s.chosen = string(m.zID[:min(2, len(m.zID))])
		case screenZathras:
			s.chosen = string(m.zathras[:2])
		}
		return s
	}
	services := func() *model { m, _ := newTestModel(80, 24); goLive(m, fixtureFleet()); press(m, "down"); return m } // fresh is chosen
	service := func() *model { m, _ := onService(80, 24, "web"); press(m, "down"); return m }                         // the second zathras
	zathras := func() *model { m, _ := onZathras(80, 24, "web", 1); return m }

	for _, tt := range []struct {
		on   string
		make func() *model
		key  string
		want state
		effs []effect
	}{
		// The services.
		{"services", services, "up", state{screenServices, "empty", false}, nil},
		{"services", services, "k", state{screenServices, "empty", false}, nil},
		{"services", services, "down", state{screenServices, "web", false}, nil},
		{"services", services, "j", state{screenServices, "web", false}, nil},
		{"services", services, "pgup", state{screenServices, "empty", false}, nil},
		{"services", services, "pgdown", state{screenServices, "worker", false}, nil},
		{"services", services, "g", state{screenServices, "empty", false}, nil},
		{"services", services, "G", state{screenServices, "worker", false}, nil},
		{"services", services, "enter", state{screenService, "10", false}, []effect{effRollouts{epoch: 1, service: "fresh"}}},
		{"services", services, "right", state{screenService, "10", false}, []effect{effRollouts{epoch: 1, service: "fresh"}}},
		{"services", services, "l", state{screenService, "10", false}, []effect{effRollouts{epoch: 1, service: "fresh"}}},
		{"services", services, "esc", state{screenServices, "fresh", false}, nil},
		{"services", services, "left", state{screenServices, "fresh", false}, nil},
		{"services", services, "h", state{screenServices, "fresh", false}, nil},
		{"services", services, "backspace", state{screenServices, "fresh", false}, nil},
		{"services", services, "s", state{screenServices, "fresh", false}, nil},
		{"services", services, "?", state{screenServices, "fresh", true}, nil},
		{"services", services, "q", state{screenServices, "fresh", false}, []effect{effQuit{}}},
		{"services", services, "ctrl+c", state{screenServices, "fresh", false}, []effect{effQuit{}}},
		{"services", services, "x", state{screenServices, "fresh", false}, nil},
		{"services", services, "tab", state{screenServices, "fresh", false}, nil},
		// A service.
		{"a service", service, "up", state{screenService, "20", false}, nil},
		{"a service", service, "k", state{screenService, "20", false}, nil},
		{"a service", service, "down", state{screenService, "22", false}, nil},
		{"a service", service, "j", state{screenService, "22", false}, nil},
		{"a service", service, "pgup", state{screenService, "20", false}, nil},
		{"a service", service, "pgdown", state{screenService, "24", false}, nil},
		{"a service", service, "g", state{screenService, "20", false}, nil},
		{"a service", service, "G", state{screenService, "24", false}, nil},
		{"a service", service, "enter", state{screenZathras, "21", false}, []effect{effHistory{epoch: 1, zathras: idOf(0x21)}, effFollow{epoch: 1, zathras: idOf(0x21), id: 1}}},
		{"a service", service, "right", state{screenZathras, "21", false}, []effect{effHistory{epoch: 1, zathras: idOf(0x21)}, effFollow{epoch: 1, zathras: idOf(0x21), id: 1}}},
		{"a service", service, "l", state{screenZathras, "21", false}, []effect{effHistory{epoch: 1, zathras: idOf(0x21)}, effFollow{epoch: 1, zathras: idOf(0x21), id: 1}}},
		{"a service", service, "esc", state{screenServices, "web", false}, nil},
		{"a service", service, "left", state{screenServices, "web", false}, nil},
		{"a service", service, "h", state{screenServices, "web", false}, nil},
		{"a service", service, "backspace", state{screenServices, "web", false}, nil},
		{"a service", service, "s", state{screenService, "21", false}, nil},
		{"a service", service, "?", state{screenService, "21", true}, nil},
		{"a service", service, "q", state{screenService, "21", false}, []effect{effQuit{}}},
		{"a service", service, "ctrl+c", state{screenService, "21", false}, []effect{effQuit{}}},
		// A zathras. The keys that move scroll its log: see TestLogScrolls.
		{"a zathras", zathras, "enter", state{screenZathras, "21", false}, nil},
		{"a zathras", zathras, "right", state{screenZathras, "21", false}, nil},
		{"a zathras", zathras, "l", state{screenZathras, "21", false}, nil},
		{"a zathras", zathras, "esc", state{screenService, "21", false}, []effect{effUnfollow{}}},
		{"a zathras", zathras, "left", state{screenService, "21", false}, []effect{effUnfollow{}}},
		{"a zathras", zathras, "h", state{screenService, "21", false}, []effect{effUnfollow{}}},
		{"a zathras", zathras, "backspace", state{screenService, "21", false}, []effect{effUnfollow{}}},
		{"a zathras", zathras, "?", state{screenZathras, "21", true}, nil},
		{"a zathras", zathras, "q", state{screenZathras, "21", false}, []effect{effQuit{}}},
		{"a zathras", zathras, "ctrl+c", state{screenZathras, "21", false}, []effect{effQuit{}}},
	} {
		m := tt.make()
		effs := press(m, tt.key)
		if got := at(m); got != tt.want {
			t.Errorf("%s, %s: now at %+v, want %+v", tt.on, tt.key, got, tt.want)
		}
		wantEffects(t, tt.on+", "+tt.key, effs, tt.effs...)
	}

	// The keys are said by any key, and q quits also then.
	m := services()
	press(m, "?")
	wantEffects(t, "a key while the keys are shown", press(m, "enter"))
	if m.help || m.screen != screenServices {
		t.Errorf("a key while the keys are shown: help %v, on %v; want the keys gone and nothing opened", m.help, m.screen)
	}
	press(m, "?")
	wantEffects(t, "q while the keys are shown", press(m, "q"), effQuit{})

	// s on a zathras: the place of the shell.
	m = zathras()
	wantEffects(t, "s on a zathras", press(m, "s"))
	if m.notice == "" {
		t.Error("s on a zathras says nothing")
	}
	press(m, "j")
	if m.notice != "" {
		t.Errorf("after the next key the notice is still %q", m.notice)
	}
	m.present = false
	press(m, "s")
	if !strings.Contains(m.notice, "not connected") {
		t.Errorf("s on a zathras that is gone says %q", m.notice)
	}
}

func TestOpeningWithNothingChosen(t *testing.T) {
	m, _ := newTestModel(80, 24)
	goLive(m, wire.Fleet{Services: []wire.Service{}})
	wantEffects(t, "enter without a service", press(m, "enter", "down", "up", "G", "g", "pgdown", "pgup"))
	if m.screen != screenServices || m.svcName != "" {
		t.Errorf("with no service, the model is on %v with %q chosen", m.screen, m.svcName)
	}
	m, _ = onService(80, 24, "empty")
	wantEffects(t, "enter without a zathras", press(m, "enter", "down", "up", "G", "g", "pgdown", "pgup"))
	if m.screen != screenService || m.zID != "" {
		t.Errorf("with no zathras, the model is on %v with %q chosen", m.screen, m.zID)
	}
}

// What is chosen stays chosen by what it is when the fleet changes
// around it, and when it is gone, what is at its place is chosen.
func TestWhatIsChosenStaysChosen(t *testing.T) {
	m, _ := newTestModel(80, 24)
	goLive(m, fixtureFleet())
	press(m, "down", "down") // web
	f := fixtureFleet()
	f.Services = append(f.Services, wire.Service{Name: "api"}, wire.Service{Name: "zeta"})
	m.update(fleetMsg{epoch: 1, fleet: f})
	if m.svcName != "web" || m.svcIdx != 3 {
		t.Errorf("with a service before it, %q is chosen at %d; want web at 3", m.svcName, m.svcIdx)
	}
	// web goes: what is at its place now is chosen.
	f = fixtureFleet()
	f.Services = slices.Delete(f.Services, 2, 3)
	f.Services = append(f.Services, wire.Service{Name: "api"})
	m.update(fleetMsg{epoch: 1, fleet: f})
	if m.svcName != "worker" || m.svcIdx != 3 {
		t.Errorf("with web gone, %q is chosen at %d; want worker at 3", m.svcName, m.svcIdx)
	}
	// Everything behind goes: the last is chosen.
	m.update(fleetMsg{epoch: 1, fleet: wire.Fleet{Services: []wire.Service{{Name: "api"}}}})
	if m.svcName != "api" || m.svcIdx != 0 {
		t.Errorf("with one service left, %q is chosen at %d", m.svcName, m.svcIdx)
	}
	m.update(fleetMsg{epoch: 1, fleet: wire.Fleet{}})
	if m.svcName != "" || m.svcIdx != 0 {
		t.Errorf("with no service left, %q is chosen at %d", m.svcName, m.svcIdx)
	}

	// The zathrases of a service: the second of the two of one name.
	m, _ = onService(80, 24, "web")
	press(m, "down", "down")
	if m.zID != idOf(0x22) {
		t.Fatalf("the third zathras is %s", m.zID[:2])
	}
	f = fixtureFleet()
	web := &f.Services[2]
	web.Zathrases = append([]wire.Zathras{{ID: idOf(0x1f), Name: "web-0"}}, web.Zathrases...)
	m.update(fleetMsg{epoch: 1, fleet: f})
	if m.zID != idOf(0x22) || m.zIdx != 3 {
		t.Errorf("with a zathras before it, %s is chosen at %d; want 22 at 3", m.zID[:2], m.zIdx)
	}
	// Its namesake goes: it is still the one chosen.
	web.Zathrases = slices.Delete(web.Zathrases, 2, 3)
	m.update(fleetMsg{epoch: 1, fleet: f})
	if m.zID != idOf(0x22) || m.zIdx != 2 {
		t.Errorf("with its namesake gone, %s is chosen at %d; want 22 at 2", m.zID[:2], m.zIdx)
	}
	// It goes: the one at its place is chosen.
	web.Zathrases = slices.Delete(web.Zathrases, 2, 3)
	m.update(fleetMsg{epoch: 1, fleet: f})
	if m.zID != idOf(0x23) || m.zIdx != 2 {
		t.Errorf("with it gone, %s is chosen at %d; want 23 at 2", m.zID[:2], m.zIdx)
	}
	// The service goes while its view is open, and is back: the view
	// stays, and what was chosen is chosen again.
	gone := fixtureFleet()
	gone.Services = slices.Delete(gone.Services, 2, 3)
	wantEffects(t, "the service goes", m.update(fleetMsg{epoch: 1, fleet: gone}))
	if _, ok := m.serviceNamed(m.service); ok || m.screen != screenService || m.service != "web" {
		t.Errorf("with its service gone, the model is on %v of %q", m.screen, m.service)
	}
	wantEffects(t, "keys on a service that is gone", press(m, "down", "enter", "G"))
	m.update(fleetMsg{epoch: 1, fleet: f})
	if m.zID != idOf(0x23) {
		t.Errorf("with its service back, %s is chosen; want 23", m.zID[:2])
	}
}

// A list that is longer than the terminal: what is chosen is in sight.
func TestWhatIsChosenIsInSight(t *testing.T) {
	f := wire.Fleet{}
	big := wire.Service{Name: "zzz-big"}
	for i := range 300 {
		f.Services = append(f.Services, wire.Service{Name: fmt.Sprintf("svc-%03d", i)})
		big.Zathrases = append(big.Zathrases, wire.Zathras{ID: idOf(i%256) + wire.ID(fmt.Sprint(i)), Name: fmt.Sprintf("big-%03d", i)})
	}
	f.Services = append(f.Services, big)
	m, _ := newTestModel(80, 24)
	goLive(m, f)
	inSight := func(what string, top, idx, h int) {
		t.Helper()
		if idx < top || idx >= top+h {
			t.Fatalf("%s: row %d is chosen, and rows %d to %d are in sight", what, idx, top, top+h-1)
		}
	}
	for _, key := range []string{"down", "down", "pgdown", "pgdown", "up", "G", "up", "pgup", "g", "pgdown", "G"} {
		press(m, key)
		inSight("services, "+key, m.svcTop, m.svcIdx, m.servicesHeight())
	}
	if m.svcName != "zzz-big" {
		t.Errorf("at the end of the services %q is chosen", m.svcName)
	}
	if m.svcTop != len(f.Services)-m.servicesHeight() {
		t.Errorf("at the end of %d services the first in sight is %d, and %d are in sight", len(f.Services), m.svcTop, m.servicesHeight())
	}
	press(m, "G", "enter")
	if svc, _ := m.serviceNamed(m.service); m.screen != screenService || len(svc.Zathrases) != 300 {
		t.Fatalf("the service with three hundred zathrases is not open: %q", m.service)
	}
	for _, key := range []string{"down", "pgdown", "pgdown", "up", "G", "up", "pgup", "g", "pgdown"} {
		press(m, key)
		inSight("zathrases, "+key, m.zTop, m.zIdx, m.zathrasesHeight())
	}
	// The terminal gets smaller: still in sight.
	press(m, "G")
	m.update(tea.WindowSizeMsg{Width: 60, Height: 12})
	inSight("zathrases, in a smaller terminal", m.zTop, m.zIdx, m.zathrasesHeight())
	// The fleet gets shorter: still in sight, and no empty rows at the end.
	big.Zathrases = big.Zathrases[:5]
	m.update(fleetMsg{epoch: 1, fleet: wire.Fleet{Services: []wire.Service{big}}})
	if m.zIdx != 4 || m.zTop != max(5-m.zathrasesHeight(), 0) {
		t.Errorf("with five zathrases left, %d is chosen and %d the first in sight", m.zIdx, m.zTop)
	}
}

// What a view holds, and when it is asked for again.
func TestServiceAsksForItsRolloutsAgainWhenTheLastOneChanges(t *testing.T) {
	m, _ := newTestModel(80, 24)
	goLive(m, fixtureFleet())
	press(m, "down", "down")
	wantEffects(t, "opening web", press(m, "enter"), effRollouts{epoch: 1, service: "web"})
	if m.rolloutsAre != askedFor {
		t.Errorf("asked for, the rollouts are %v", m.rolloutsAre)
	}
	m.update(rolloutsMsg{epoch: 1, n: m.rolloutsN, service: "worker", rollouts: fixtureRollouts("worker")})
	if m.rolloutsAre != askedFor {
		t.Error("the rollouts of another service were taken")
	}
	m.update(rolloutsMsg{epoch: 1, n: m.rolloutsN, service: "web", rollouts: fixtureRollouts("web")})
	if m.rolloutsAre != askedHave || len(m.rollouts) != 20 {
		t.Errorf("the rollouts are %v, %d of them", m.rolloutsAre, len(m.rollouts))
	}
	// The same rollout in the same state: nothing is asked.
	f := fixtureFleet()
	wantEffects(t, "the same fleet", m.update(fleetMsg{epoch: 1, fleet: f}))
	// Its counts change: nothing is asked.
	f.Services[2].Rollout.Activated = 3
	wantEffects(t, "other counts", m.update(fleetMsg{epoch: 1, fleet: f}))
	// Its state changes.
	f.Services[2].Rollout.State = wire.RolloutCompleted
	wantEffects(t, "another state", m.update(fleetMsg{epoch: 1, fleet: f}), effRollouts{epoch: 1, service: "web"})
	if m.rolloutsAre != askedHave || len(m.rollouts) != 20 {
		t.Error("while they are asked for again, the rollouts there were are gone")
	}
	wantEffects(t, "the same state again", m.update(fleetMsg{epoch: 1, fleet: f}))
	// Another rollout.
	f.Services[2].Rollout.ID, f.Services[2].Rollout.State = 32, wire.RolloutPreparing
	wantEffects(t, "another rollout", m.update(fleetMsg{epoch: 1, fleet: f}), effRollouts{epoch: 1, service: "web"})
	// A rollout of another service: nothing.
	f.Services[3].Rollout.ID = 33
	wantEffects(t, "a rollout of another service", m.update(fleetMsg{epoch: 1, fleet: f}))
	// On the view of a zathras nothing of rollouts is asked; back on the
	// service, they are asked for when the rollout has changed meanwhile.
	press(m, "enter")
	f.Services[2].Rollout.State = wire.RolloutActivating
	for _, e := range m.update(fleetMsg{epoch: 1, fleet: f}) {
		if _, is := e.(effRollouts); is {
			t.Error("the rollouts are asked for on the view of a zathras")
		}
	}
	wantEffects(t, "back on the service", press(m, "esc"), effUnfollow{}, effRollouts{epoch: 1, service: "web"})
	// draal refuses.
	m.update(rolloutsMsg{epoch: 1, n: m.rolloutsN, service: "web", err: wire.Errorf(wire.CodeInternal, "the database is \x1b[2Jlocked")})
	if m.rolloutsAre != askedRefused || m.rolloutsErr != "internal: the database is locked" {
		t.Errorf("refused, the rollouts are %v with %q", m.rolloutsAre, m.rolloutsErr)
	}
	// Left and opened again: asked again; another service: none of the
	// first one's rollouts are left.
	press(m, "esc")
	wantEffects(t, "opening web again", press(m, "enter"), effRollouts{epoch: 1, service: "web"})
	press(m, "esc", "down")
	wantEffects(t, "opening worker", press(m, "enter"), effRollouts{epoch: 1, service: "worker"})
	if len(m.rollouts) != 0 || m.rolloutsAre != askedFor || m.rolloutsErr != "" {
		t.Errorf("on worker the rollouts of web are still there: %d, %v, %q", len(m.rollouts), m.rolloutsAre, m.rolloutsErr)
	}
}

func TestZathrasHoldsItsSamplesAndItsLog(t *testing.T) {
	m, _ := onService(80, 24, "web")
	wantEffects(t, "opening a zathras", press(m, "enter"),
		effHistory{epoch: 1, zathras: idOf(0x20)}, effFollow{epoch: 1, zathras: idOf(0x20), id: 1})
	if m.history != askedFor || m.logIs != askedFor || len(m.samples) != 1 {
		t.Errorf("asked for: history %v, log %v, and %d samples, want the one of the fleet", m.history, m.logIs, len(m.samples))
	}
	m.update(historyMsg{epoch: 1, n: m.historyN, zathras: idOf(0x21), samples: fixtureSamples()})
	if m.history != askedFor {
		t.Error("the samples of another zathras were taken")
	}
	m.update(historyMsg{epoch: 1, n: m.historyN, zathras: idOf(0x20), samples: fixtureSamples()})
	if m.history != askedHave || len(m.samples) != 450 {
		t.Errorf("history %v with %d samples, want 450: the one of the fleet is the last of the history", m.history, len(m.samples))
	}
	// The samples of the fleets that follow are added, and the oldest go.
	f := fixtureFleet()
	f.Services[2].Zathrases[0].State.Sample = &wire.Sample{At: t0.Add(2 * time.Second), CPU: 3}
	wantEffects(t, "a fleet with a sample", m.update(fleetMsg{epoch: 1, fleet: f}))
	if n := len(m.samples); n != 451 || m.samples[n-1].CPU != 3 || m.seen.State.Sample.CPU != 3 {
		t.Errorf("%d samples, the last with %v cores", n, m.samples[n-1].CPU)
	}
	f.Services[2].Zathrases[0].State.Sample = &wire.Sample{At: t0.Add(4 * time.Second), CPU: 4}
	m.update(fleetMsg{epoch: 1, fleet: f})
	if n := len(m.samples); n != 451 || m.samples[n-1].CPU != 4 {
		t.Errorf("%d samples, the last with %v cores: want the oldest gone", n, m.samples[n-1].CPU)
	}
	// The log: until its first line it is asked for, and no more is
	// known of it: draal answers a log with its lines alone.
	if m.logIs != askedFor || len(m.rows) != 0 {
		t.Errorf("before its first line, the log is %v with %d rows", m.logIs, len(m.rows))
	}
	m.update(logMsg{epoch: 1, id: 0, lines: fixtureLines(5)})
	m.update(logMsg{epoch: 0, id: 1, lines: fixtureLines(5)})
	if len(m.rows) != 0 {
		t.Error("lines of another log were taken")
	}
	m.update(logMsg{epoch: 1, id: 1, lines: fixtureLines(5)})
	if len(m.rows) != 5 || len(m.log.entries) != 5 {
		t.Errorf("%d rows of %d lines", len(m.rows), len(m.log.entries))
	}
	// Left: the log is ended, and what is on its way is not taken by
	// the next view.
	wantEffects(t, "leaving", press(m, "esc"), effUnfollow{})
	press(m, "down")
	wantEffects(t, "opening the next", press(m, "enter"),
		effHistory{epoch: 1, zathras: idOf(0x21)}, effFollow{epoch: 1, zathras: idOf(0x21), id: 3})
	m.update(logMsg{epoch: 1, id: 1, lines: fixtureLines(5)})
	m.update(historyMsg{epoch: 1, n: m.historyN, zathras: idOf(0x20), samples: fixtureSamples()})
	if len(m.rows) != 0 || len(m.samples) != 1 || m.history != askedFor {
		t.Errorf("the next zathras has %d rows and %d samples of the one before", len(m.rows), len(m.samples))
	}
	// draal refuses the samples: a zathras that reports none has none.
	m.update(historyMsg{epoch: 1, n: m.historyN, zathras: idOf(0x21), err: wire.Errorf(wire.CodeUnavailable, "zathras 21 is not connected")})
	if m.history != askedRefused || m.histErr != "unavailable: zathras 21 is not connected" {
		t.Errorf("refused, the history is %v with %q", m.history, m.histErr)
	}
}

// Review Focus 4: a zathras that goes while its view is open, and comes
// back.
func TestZathrasGoesWhileItsViewIsOpenAndComesBack(t *testing.T) {
	m, clock := onZathras(80, 24, "web", 4)
	id := idOf(0x24)
	if m.zathras != id || !m.present || len(m.rows) == 0 {
		t.Fatalf("the view is of %s, present: %v, with %d rows", m.zathras[:2], m.present, len(m.rows))
	}
	samples := len(m.samples)

	// It goes: draal ends its log, and the next fleet is without it.
	clock.at = t0.Add(10 * time.Second)
	m.update(logEndedMsg{epoch: 1, id: m.logID})
	gone := fixtureFleet()
	web := &gone.Services[2]
	web.Zathrases = web.Zathrases[:4]
	wantEffects(t, "the fleet without it", m.update(fleetMsg{epoch: 1, fleet: gone}))
	if m.screen != screenZathras || m.zathras != id || m.present || !m.goneSince.Equal(clock.at) {
		t.Errorf("gone: on %v of %s, present: %v, gone since %v", m.screen, m.zathras[:2], m.present, m.goneSince)
	}
	// What it reported last, its samples and its log are kept.
	if m.seen.Name != "web-7d9f8b6c4-q5rvd" || m.seen.State.PID != 31000 || len(m.samples) != samples || len(m.log.entries) != logKept || m.logIs != askedEnded {
		t.Errorf("gone, the view has %q, %d samples, %d lines and a log that is %v", m.seen.Name, len(m.samples), len(m.log.entries), m.logIs)
	}
	if last := m.log.entries[len(m.log.entries)-1]; last.kind != entryNote || last.text != "The log has ended." {
		t.Errorf("the last row of the log is %+v", last)
	}
	// Nothing is asked of a zathras that is gone: not by a fleet, not by G.
	clock.at = t0.Add(12 * time.Second)
	wantEffects(t, "another fleet without it", m.update(fleetMsg{epoch: 1, fleet: gone}))
	wantEffects(t, "G on a zathras that is gone", press(m, "G"))
	if !m.goneSince.Equal(t0.Add(10 * time.Second)) {
		t.Errorf("gone since %v, want the time it was first missed", m.goneSince)
	}

	// It is back, with the ID it had: its samples and its log are asked
	// for again, and no line is there twice.
	clock.at = t0.Add(16 * time.Second)
	back := fixtureFleet()
	back.Services[2].Zathrases[4].Connected = clock.at
	back.Services[2].Zathrases[4].State.PID = 5
	logID := m.logID
	wantEffects(t, "the fleet with it again", m.update(fleetMsg{epoch: 1, fleet: back}),
		effHistory{epoch: 1, zathras: id}, effFollow{epoch: 1, zathras: id, id: logID + 1})
	if !m.present || m.seen.State.PID != 5 || m.logIs != askedFor {
		t.Errorf("back: present %v, pid %d, log %v", m.present, m.seen.State.PID, m.logIs)
	}
	again := append(fixtureLines(400), wire.LogLine{At: t0.Add(17 * time.Second), Stream: "stdout", Text: "back"})
	m.update(logMsg{epoch: 1, id: m.logID, lines: again})
	if got := len(m.log.entries); got != 400 {
		t.Errorf("%d rows are kept", got)
	}
	n := len(m.log.entries)
	if textOf(m.log.entries[n-1]) != "back" || m.log.entries[n-2].kind != entryNote || m.log.entries[n-3].kind != entryLine {
		t.Errorf("the end of the log is %q, %q, %q; want the last line of before, the note, and the new line",
			textOf(m.log.entries[n-3]), textOf(m.log.entries[n-2]), textOf(m.log.entries[n-1]))
	}
	wantEffects(t, "the fleet with it once more", m.update(fleetMsg{epoch: 1, fleet: back}))

	// It connects again between two fleets: it was never missed, and its
	// log has ended all the same.
	clock.at = t0.Add(30 * time.Second)
	back.Services[2].Zathrases[4].Connected = clock.at
	logID = m.logID
	wantEffects(t, "the fleet with it connected anew", m.update(fleetMsg{epoch: 1, fleet: back}),
		effHistory{epoch: 1, zathras: id}, effFollow{epoch: 1, zathras: id, id: logID + 1})

	// A log that ends while the zathras stays: G asks for it again.
	m.update(logEndedMsg{epoch: 1, id: m.logID, err: wire.Errorf(wire.CodeUnavailable, "the connection of zathras 24 failed")})
	if last := m.log.entries[len(m.log.entries)-1]; last.text != "draal does not send the log: the connection of zathras 24 failed" {
		t.Errorf("the last row of the log is %q", last.text)
	}
	logID = m.logID
	wantEffects(t, "G on a log that has ended", press(m, "G"), effFollow{epoch: 1, zathras: id, id: logID + 1})
	wantEffects(t, "G on a log that is followed", press(m, "G"))

	// Left while it is gone: back on its service, with what is at its
	// place chosen; nothing is to be ended.
	m.update(logEndedMsg{epoch: 1, id: m.logID})
	m.update(fleetMsg{epoch: 1, fleet: gone})
	wantEffects(t, "leaving a zathras that is gone", press(m, "esc"))
	if m.screen != screenService || m.zID != idOf(0x23) {
		t.Errorf("back on %v with %s chosen, want the service with 23", m.screen, m.zID[:min(2, len(m.zID))])
	}
}

// Review Focus 3: the connection is lost and is back, on every view:
// what was shown stays, a dial is tried again with pauses that grow, a
// key ends a pause, and what the view holds is asked for again.
func TestConnectionLostAndBack(t *testing.T) {
	for _, tt := range []struct {
		on    string
		make  func() (*model, *fakeClock)
		again func(m *model) []effect // what the view asks for when the connection is back
	}{
		{"services", func() (*model, *fakeClock) {
			m, c := newTestModel(80, 24)
			goLive(m, fixtureFleet())
			press(m, "down")
			return m, c
		},
			func(m *model) []effect { return nil }},
		{"a service", func() (*model, *fakeClock) { m, c := onService(80, 24, "web"); press(m, "down"); return m, c },
			func(m *model) []effect { return []effect{effRollouts{epoch: m.epoch, service: "web"}} }},
		{"a zathras", func() (*model, *fakeClock) { return onZathras(80, 24, "web", 1) },
			func(m *model) []effect {
				return []effect{effHistory{epoch: m.epoch, zathras: idOf(0x21)}, effFollow{epoch: m.epoch, zathras: idOf(0x21), id: m.logID}}
			}},
	} {
		m, clock := tt.make()
		screen, svc, zID, rollouts, samples := m.screen, m.svcName, m.zID, len(m.rollouts), len(m.samples)

		// Lost: the watch ended.
		clock.at = t0.Add(time.Minute)
		wantEffects(t, tt.on+": lost", m.update(lostMsg{epoch: 1}), effHangUp{}, effWait{pause: time.Second, n: 1})
		if m.conn != connLost || !m.since.Equal(clock.at) || !m.retryAt.Equal(clock.at.Add(time.Second)) {
			t.Errorf("%s: lost: the connection is %v since %v, and a dial is tried at %v", tt.on, m.conn, m.since, m.retryAt)
		}
		// What was shown stays.
		if m.screen != screen || m.svcName != svc || m.zID != zID || len(m.fleet.Services) != 4 || len(m.rollouts) != rollouts || len(m.samples) != samples {
			t.Errorf("%s: lost: the view is not what it was", tt.on)
		}
		if tt.on == "a zathras" && (len(m.log.entries) != logKept || m.logIs != askedEnded || m.log.entries[logKept-1].text != "The connection to draal was lost.") {
			t.Errorf("%s: lost: the log has %d lines and is %v", tt.on, len(m.log.entries), m.logIs)
		}
		// What is still on its way from the old connection is not read,
		// and a second end of it changes nothing.
		wantEffects(t, tt.on+": lost twice", m.update(lostMsg{epoch: 1, err: errors.New("stream closed")}))
		wantEffects(t, tt.on+": a fleet of the old connection", m.update(fleetMsg{epoch: 1, fleet: wire.Fleet{}}))
		m.update(logMsg{epoch: 1, id: m.logID, lines: fixtureLines(3)})
		if m.conn != connLost || len(m.fleet.Services) != 4 {
			t.Errorf("%s: the old connection was read after it was lost", tt.on)
		}

		// The pauses double up to the longest: 1s, then 2s, 4s, …, 30s.
		epoch := m.epoch
		for i, pause := range []time.Duration{2, 4, 8, 16, 30, 30} {
			clock.at = m.retryAt
			wantEffects(t, tt.on+": the pause is over", m.update(retryMsg{n: m.waitN}), effDial{epoch: epoch + 1 + i})
			if !m.retryAt.IsZero() {
				t.Errorf("%s: while a dial is under way, a pause ends at %v", tt.on, m.retryAt)
			}
			wantEffects(t, tt.on+": the dial fails", m.update(dialFailedMsg{epoch: m.epoch, err: errors.New("draal does not answer\x07")}),
				effWait{pause: pause * time.Second, n: 2 + i})
			if !m.retryAt.Equal(clock.at.Add(pause * time.Second)) {
				t.Errorf("%s: the pause of %ds ends at %v", tt.on, pause, m.retryAt)
			}
			if m.dialErr != "draal does not answer" || m.conn != connLost || !m.since.Equal(t0.Add(time.Minute)) {
				t.Errorf("%s: after a dial that failed: %q, %v since %v", tt.on, m.dialErr, m.conn, m.since)
			}
		}
		// A pause that is not the one that counts ends nothing.
		wantEffects(t, tt.on+": an old pause is over", m.update(retryMsg{n: m.waitN - 1}))
		// A key ends a pause at once, and does what it does.
		epoch = m.epoch
		wantEffects(t, tt.on+": a key in a pause", press(m, "?"), effDial{epoch: epoch + 1})
		if !m.help || !m.retryAt.IsZero() {
			t.Errorf("%s: a key in a pause: help %v, and the pause ends at %v", tt.on, m.help, m.retryAt)
		}
		// The pause that the key ended is over: nothing. And a key while
		// a dial is under way dials no second time.
		wantEffects(t, tt.on+": the pause the key ended is over", m.update(retryMsg{n: m.waitN}))
		wantEffects(t, tt.on+": a key while a dial is under way", press(m, "?"))

		// Back: Dial succeeds, and with the first fleet the connection is
		// live, on the view it was on, and what the view holds is asked
		// for again of the new connection.
		clock.at = clock.at.Add(time.Second)
		wantEffects(t, tt.on+": dialed", m.update(dialedMsg{epoch: m.epoch}))
		if m.conn != connLost {
			t.Errorf("%s: dialed, and before a fleet came, the connection is %v", tt.on, m.conn)
		}
		logID := m.logID
		effs := m.update(fleetMsg{epoch: m.epoch, fleet: fixtureFleet()})
		wantEffects(t, tt.on+": back", effs, tt.again(m)...)
		if m.conn != connLive || m.screen != screen || m.svcName != svc || m.zID != zID || m.dialErr != "" || m.pause != 0 {
			t.Errorf("%s: back: the connection is %v, on %v with %q and %q chosen", tt.on, m.conn, m.screen, m.svcName, m.zID)
		}
		if tt.on == "a zathras" {
			if m.logID != logID+1 {
				t.Errorf("the log that is followed anew has the number %d, and the one before had %d", m.logID, logID)
			}
			// The ring comes once more: no line is there twice.
			before := len(m.log.entries)
			m.update(logMsg{epoch: m.epoch, id: m.logID, lines: fixtureLines(400)})
			if len(m.log.entries) != before {
				t.Errorf("after the reconnect the log has %d rows, and had %d: lines were taken twice", len(m.log.entries), before)
			}
		}
		// Lost again: the first pause is the first one again.
		n := m.waitN + 1
		wantEffects(t, tt.on+": lost again", m.update(lostMsg{epoch: m.epoch}), effHangUp{}, effWait{pause: time.Second, n: n})
	}
}

// A view that is opened while the connection is lost asks for nothing,
// and for what it holds when the connection is back.
func TestViewsOpenedWhileTheConnectionIsLost(t *testing.T) {
	m, _ := newTestModel(80, 24)
	goLive(m, fixtureFleet())
	m.update(lostMsg{epoch: 1})
	m.update(retryMsg{n: m.waitN}) // a dial is under way: keys end no pause
	press(m, "down", "down")
	wantEffects(t, "opening a service without a connection", press(m, "enter"))
	if m.screen != screenService || m.rolloutsAre != askedNot {
		t.Errorf("on %v, with rollouts that are %v", m.screen, m.rolloutsAre)
	}
	wantEffects(t, "opening a zathras without a connection", press(m, "enter"))
	if m.screen != screenZathras || m.history != askedNot || m.logIs != askedNot {
		t.Errorf("on %v, with a history that is %v and a log that is %v", m.screen, m.history, m.logIs)
	}
	wantEffects(t, "s without a connection", press(m, "s"))
	if !strings.Contains(m.notice, "no connection") {
		t.Errorf("s without a connection says %q", m.notice)
	}
	wantEffects(t, "back without a connection", press(m, "esc"))
	press(m, "enter")
	wantEffects(t, "the connection is back", goLive(m, fixtureFleet()),
		effHistory{epoch: m.epoch, zathras: idOf(0x20)}, effFollow{epoch: m.epoch, zathras: idOf(0x20), id: m.logID})
	wantEffects(t, "back on the service", press(m, "esc"), effUnfollow{}, effRollouts{epoch: m.epoch, service: "web"})
}

// The rollouts of a service are asked for, and a zathras is opened
// before draal has answered: the answer is taken all the same, and back
// on the service the list is there. No state is left that says "asking"
// for ever.
func TestRolloutsThatAreAnsweredOnAnotherView(t *testing.T) {
	m, _ := newTestModel(80, 24)
	goLive(m, fixtureFleet())
	press(m, "down", "down")
	wantEffects(t, "opening web", press(m, "enter"), effRollouts{epoch: 1, n: 1, service: "web"})
	press(m, "enter") // a zathras, before the answer
	m.update(rolloutsMsg{epoch: 1, n: 1, service: "web", rollouts: fixtureRollouts("web")})
	wantEffects(t, "back on the service", press(m, "esc"), effUnfollow{})
	if m.rolloutsAre != askedHave || len(m.rollouts) != 20 {
		t.Errorf("back on the service the rollouts are %v, %d of them; want the answer that came meanwhile", m.rolloutsAre, len(m.rollouts))
	}
	// The same with an answer that never comes while the zathras is in
	// sight: the list is still asked for, and its answer still counts.
	m, _ = newTestModel(80, 24)
	goLive(m, fixtureFleet())
	press(m, "down", "down", "enter", "enter", "esc")
	if m.rolloutsAre != askedFor {
		t.Fatalf("the rollouts are %v", m.rolloutsAre)
	}
	m.update(rolloutsMsg{epoch: 1, n: m.rolloutsN, service: "web", rollouts: fixtureRollouts("web")})
	if m.rolloutsAre != askedHave {
		t.Errorf("the answer that came late was not taken: the rollouts are %v", m.rolloutsAre)
	}
	// An answer for a service that is not the open one any more is not
	// taken for the one that is.
	press(m, "esc", "down", "enter")
	m.update(rolloutsMsg{epoch: 1, n: m.rolloutsN - 1, service: "web", rollouts: fixtureRollouts("web")})
	m.update(rolloutsMsg{epoch: 1, n: m.rolloutsN, service: "web", rollouts: fixtureRollouts("web")})
	if m.service != "worker" || len(m.rollouts) != 0 {
		t.Errorf("on %s, %d rollouts of web were taken", m.service, len(m.rollouts))
	}
}

// Two requests are under way, and the answer to the older one comes
// last: it is dropped, for the rollouts and for the samples alike.
func TestAnOlderAnswerThatComesLastIsDropped(t *testing.T) {
	m, _ := newTestModel(80, 24)
	f := fixtureFleet()
	goLive(m, f)
	wantEffects(t, "opening web", press(m, "down", "down", "enter"), effRollouts{epoch: 1, n: 1, service: "web"})
	f.Services[2].Rollout.State = wire.RolloutCompleted
	wantEffects(t, "another state", m.update(fleetMsg{epoch: 1, fleet: f}), effRollouts{epoch: 1, n: 2, service: "web"})
	newer, older := fixtureRollouts("web"), fixtureRollouts("web")
	newer[0].State = wire.RolloutCompleted
	m.update(rolloutsMsg{epoch: 1, n: 2, service: "web", rollouts: newer})
	m.update(rolloutsMsg{epoch: 1, n: 1, service: "web", rollouts: older})
	m.update(rolloutsMsg{epoch: 1, n: 1, service: "web", err: wire.Errorf(wire.CodeInternal, "busy")})
	if m.rollouts[0].State != wire.RolloutCompleted || m.rolloutsAre != askedHave {
		t.Errorf("the list has rollout %d as %s (%v): an older answer was taken over a newer one", m.rollouts[0].ID, m.rollouts[0].State, m.rolloutsAre)
	}

	// The samples: asked for when the view is opened, and again when the
	// zathras has connected anew.
	wantEffects(t, "opening a zathras", press(m, "enter"),
		effHistory{epoch: 1, n: 1, zathras: idOf(0x20)}, effFollow{epoch: 1, zathras: idOf(0x20), id: 1})
	f.Services[2].Zathrases[0].Connected = t0
	wantEffects(t, "connected anew", m.update(fleetMsg{epoch: 1, fleet: f}),
		effHistory{epoch: 1, n: 2, zathras: idOf(0x20)}, effFollow{epoch: 1, zathras: idOf(0x20), id: 2})
	m.update(historyMsg{epoch: 1, n: 2, zathras: idOf(0x20), samples: fixtureSamples()})
	m.update(historyMsg{epoch: 1, n: 1, zathras: idOf(0x20), err: wire.Errorf(wire.CodeUnavailable, "zathras 20 is not connected")})
	if m.history != askedHave || m.histErr != "" || len(m.samples) != 450 {
		t.Errorf("the history is %v with %q and %d samples: an older answer was taken over a newer one", m.history, m.histErr, len(m.samples))
	}
}

// After a time in which the model was told nothing (a shell had the
// terminal), what the view that is open holds is asked for anew.
func TestAgainAsksForWhatTheViewHolds(t *testing.T) {
	m, _ := newTestModel(80, 24)
	goLive(m, fixtureFleet())
	wantEffects(t, "on the services", m.again())
	press(m, "down", "down", "enter")
	wantEffects(t, "on a service", m.again(), effRollouts{epoch: 1, service: "web"})
	press(m, "enter")
	m.update(logMsg{epoch: 1, id: m.logID, lines: fixtureLines(10)})
	id := m.logID
	wantEffects(t, "on a zathras", m.update(shellEndedMsg{}), effHistory{epoch: 1, zathras: idOf(0x20)}, effFollow{epoch: 1, zathras: idOf(0x20), id: id + 1})
	// What was on its way is not read, and the ring that comes anew
	// brings no line twice.
	m.update(logMsg{epoch: 1, id: id, lines: fixtureLines(10)})
	m.update(logMsg{epoch: 1, id: m.logID, lines: fixtureLines(10)})
	if len(m.log.entries) != 10 {
		t.Errorf("the log has %d lines, want the 10 it had", len(m.log.entries))
	}
	m.present = false
	wantEffects(t, "on a zathras that is gone", m.again())
	m.present = true
	m.update(lostMsg{epoch: 1})
	wantEffects(t, "without a connection", m.again())
}

// What draal refuses an operator at the start ends the terminal UI. A
// draal that does not answer is tried again.
func TestTheFirstDial(t *testing.T) {
	refused := fmt.Errorf("asking draal for the fleet: %w", wire.Errorf(wire.CodeUnauthorized, "this operator is not on the allowlist"))

	m, clock := newTestModel(80, 24)
	effs := m.update(dialFailedMsg{epoch: 1, err: refused})
	wantEffects(t, "refused at the first dial", effs, effFail{err: refused})
	if !errors.Is(m.fatal, refused) {
		t.Errorf("the model ends with %v", m.fatal)
	}

	// No answer: tried again after a second, then two, and the header
	// has why.
	m, clock = newTestModel(80, 24)
	wantEffects(t, "no answer", m.update(dialFailedMsg{epoch: 1, err: errors.New("connecting to draal 3f9a: timeout")}), effWait{pause: time.Second, n: 1})
	if m.conn != connConnecting || m.dialErr != "connecting to draal 3f9a: timeout" || !m.since.Equal(t0) {
		t.Errorf("no answer: the connection is %v since %v, with %q", m.conn, m.since, m.dialErr)
	}
	clock.at = t0.Add(time.Second)
	wantEffects(t, "the pause is over", m.update(retryMsg{n: 1}), effDial{epoch: 2})
	wantEffects(t, "the answer to the first dial comes late", m.update(dialFailedMsg{epoch: 1, err: refused}))
	wantEffects(t, "no answer again", m.update(dialFailedMsg{epoch: 2, err: errors.New("timeout")}), effWait{pause: 2 * time.Second, n: 2})
	// Refused at a later first dial: fatal all the same, as draal never
	// let this operator in.
	m.update(retryMsg{n: 2})
	wantEffects(t, "refused at the third dial", m.update(dialFailedMsg{epoch: 3, err: refused}), effFail{err: refused})

	// Refused after draal had let it in once (the allowlist was changed
	// while the terminal UI was open): it is said, and tried again.
	m, _ = newTestModel(80, 24)
	goLive(m, fixtureFleet())
	m.update(lostMsg{epoch: 1})
	m.update(retryMsg{n: m.waitN})
	wantEffects(t, "refused after it was let in", m.update(dialFailedMsg{epoch: m.epoch, err: refused}), effWait{pause: 2 * time.Second, n: 2})
	if m.fatal != nil || !strings.Contains(m.dialErr, "not on the allowlist") {
		t.Errorf("refused after it was let in: fatal %v, and the header has %q", m.fatal, m.dialErr)
	}

	// The times of a Config are what counts.
	m = newModel(Config{RetryFirst: 10 * time.Millisecond, RetryMax: 25 * time.Millisecond, Now: clock.now})
	m.start()
	for _, pause := range []time.Duration{10, 20, 25, 25} {
		effs := m.update(dialFailedMsg{epoch: m.epoch, err: errors.New("no")})
		wantEffects(t, "with other pauses", effs, effWait{pause: pause * time.Millisecond, n: m.waitN})
		m.update(retryMsg{n: m.waitN})
	}
}

// The log of a zathras: the end is followed until one scrolls, what one
// reads stays where it is while lines come, and G follows again.
func TestLogScrolls(t *testing.T) {
	m, _ := onZathras(80, 24, "web", 1)
	h := m.logHeight()
	if !m.follow || len(m.rows) <= h {
		t.Fatalf("at first the end is followed: %v, with %d rows and %d in sight", m.follow, len(m.rows), h)
	}
	last := func() int { return len(m.rows) - m.logHeight() }
	press(m, "up")
	if m.follow || m.logTop != last()-1 {
		t.Errorf("up: following %v, the first row in sight is %d of %d", m.follow, m.logTop, last())
	}
	press(m, "k", "pgup")
	if want := last() - 2 - (h - 1); m.logTop != want {
		t.Errorf("k and PgUp: the first row in sight is %d, want %d", m.logTop, want)
	}
	// Lines come: what is read stays in sight, by what it is.
	reading := m.rows[m.logTop]
	more := []wire.LogLine{{At: t0.Add(time.Second), Stream: "stdout", Text: "new 1"}, {At: t0.Add(2 * time.Second), Stream: "stdout", Text: "new 2"}}
	m.update(logMsg{epoch: 1, id: m.logID, lines: more})
	if now := m.rows[m.logTop]; m.follow || now.seq != reading.seq || now.sub != reading.sub {
		t.Errorf("after two more lines the first row in sight is of line %d, and was of line %d", now.seq, reading.seq)
	}
	// Down to the end follows again.
	press(m, "down", "j")
	if m.follow {
		t.Error("two rows down, and well before the end, the end is followed")
	}
	press(m, "pgdown", "pgdown", "pgdown")
	if !m.follow {
		t.Errorf("at the end, the end is not followed: the first row in sight is %d of %d", m.logTop, last())
	}
	press(m, "g")
	if m.follow || m.logTop != 0 {
		t.Errorf("g: following %v, the first row in sight is %d", m.follow, m.logTop)
	}
	press(m, "up", "pgup")
	if m.logTop != 0 {
		t.Errorf("up at the start: the first row in sight is %d", m.logTop)
	}
	// What is read is dropped by a flood: the oldest there is, is read.
	var flood []wire.LogLine
	for i := range 1000 {
		flood = append(flood, wire.LogLine{At: t0.Add(time.Minute + time.Duration(i)*time.Millisecond), Stream: "stdout", Text: fmt.Sprintf("flood %d", i)})
	}
	m.update(logMsg{epoch: 1, id: m.logID, lines: flood})
	if m.follow || m.logTop != 0 || len(m.log.entries) != logKept || len(m.rows) != logKept {
		t.Errorf("after a flood: following %v, the first row in sight is %d, of %d rows", m.follow, m.logTop, len(m.rows))
	}
	press(m, "G")
	if !m.follow {
		t.Error("G does not follow the end")
	}
	// A terminal that is narrower: the lines take more rows, and none is
	// wider than there is room.
	m.update(logMsg{epoch: 1, id: m.logID, lines: fixtureLines(100)[60:61]}) // already there: not taken
	m.update(logMsg{epoch: 1, id: m.logID, lines: []wire.LogLine{{At: t0.Add(time.Hour), Stream: "stderr", Text: strings.Repeat("0123456789", 1000)}}})
	for _, w := range []int{200, 80, 50, 61} {
		m.update(tea.WindowSizeMsg{Width: w, Height: 24})
		room := w - logPrefix(w)
		long := 0
		for _, r := range m.rows {
			if spansWidth(r.spans) > room {
				t.Fatalf("at %d columns a row is %d cells wide, and there is room for %d", w, spansWidth(r.spans), room)
			}
			if r.seq == m.log.entries[len(m.log.entries)-1].seq {
				long++
			}
		}
		if long != logRowsPerLine {
			t.Errorf("at %d columns a line of ten thousand characters takes %d rows, want %d", w, long, logRowsPerLine)
		}
		lastRow := m.rows[len(m.rows)-1]
		if got := lastRow.spans[len(lastRow.spans)-1].text; got != ellipsis {
			t.Errorf("at %d columns the last row of a line that is cut ends in %q", w, got)
		}
	}
	// A log that fits is not scrolled.
	m, _ = onZathras(80, 24, "web", 1)
	m.log, m.rows = newLogBuffer(), nil
	m.update(logMsg{epoch: 1, id: m.logID, lines: fixtureLines(2)})
	press(m, "up", "g", "pgup", "down")
	if !m.follow || m.logTop != 0 {
		t.Errorf("a log of two lines: following %v, the first row in sight is %d", m.follow, m.logTop)
	}
}

// Review Focus 5: a terminal that is small, or resized. In one that is
// too small the terminal UI says so in one line; at every size it draws
// without a panic, no more lines than there are rows, none wider than
// there are columns.
func TestEverySizeOfTerminal(t *testing.T) {
	check := func(what string, m *model, w, h int, keys ...string) {
		t.Helper()
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("%s at %d×%d: panic: %v", what, w, h, r)
			}
		}()
		m.update(tea.WindowSizeMsg{Width: w, Height: h})
		press(m, keys...)
		drawn := m.view()
		if found := unsafe(drawn); found != "" {
			t.Fatalf("%s at %d×%d: what is drawn has %s", what, w, h, found)
		}
		lines := strings.Split(plain(drawn), "\n")
		if len(lines) > max(h, 1) {
			t.Fatalf("%s at %d×%d: %d lines are drawn", what, w, h, len(lines))
		}
		// The widths are the renderer's, by graphemes and by characters:
		// what a terminal would count, not what this package counts.
		for i, line := range lines {
			if g, wc := ansi.StringWidth(line), ansi.StringWidthWc(line); g > w || wc > w {
				t.Fatalf("%s at %d×%d: line %d is %d cells wide by graphemes and %d by characters: %q", what, w, h, i, g, wc, line)
			}
		}
		if w < leastWidth || h < leastHeight {
			if len(lines) != 1 || (w >= 20 && !strings.Contains(lines[0], "Too small")) {
				t.Fatalf("%s at %d×%d: a terminal that is too small has %q", what, w, h, lines)
			}
		} else if len(lines) != h || !strings.Contains(lines[0], "great-machine") || !strings.Contains(lines[h-1], "quit") {
			t.Fatalf("%s at %d×%d: %d lines, from %q to %q; want the header first and the keys last", what, w, h, len(lines), lines[0], lines[len(lines)-1])
		}
	}
	// Every view and every odd state, in the terminals the contract
	// names and at the edges, with the keys that move.
	sizes := [][2]int{{80, 24}, {200, 60}, {20, 5}, {0, 0}, {1, 1}, {leastWidth, leastHeight}, {leastWidth - 1, 60}, {200, leastHeight - 1}, {59, 13}, {60, 15}, {109, 30}, {110, 30}, {1000, 300}}
	for _, sc := range scenes() {
		for _, size := range sizes {
			check(sc.name, sc.model(80, 24), size[0], size[1], "down", "pgdown", "up", "G", "g", "pgup")
		}
	}
	// Every size from 1×1 to 100×40, in a terminal that is resized from
	// one to the next: each of the three views, and the states that
	// draw the most.
	swept := []string{"services", "services-none", "services-lost", "service-rollout-under-way", "service-halted", "service-no-rollout",
		"zathras", "zathras-long-name", "zathras-gone", "zathras-help"}
	for _, sc := range scenes() {
		if !slices.Contains(swept, sc.name) {
			continue
		}
		m := sc.model(80, 24)
		for w := 1; w <= 100; w++ {
			for h := 1; h <= 40; h++ {
				check(sc.name, m, w, h)
			}
		}
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `nix develop .#bare --command go test ./tui/`
Expected: FAIL, the package does not build:

```
# github.com/fables-for-robots/great-machine/tui [github.com/fables-for-robots/great-machine/tui.test]
tui/scenes_test.go:15:24: undefined: model
tui/fixture_test.go:185:31: undefined: model
tui/fixture_test.go:187:7: undefined: newModel
tui/fixture_test.go:194:16: undefined: model
tui/fixture_test.go:194:39: undefined: effect
tui/fixture_test.go:217:15: undefined: model
tui/fixture_test.go:217:41: undefined: effect
tui/fixture_test.go:226:44: undefined: model
tui/fixture_test.go:239:51: undefined: model
tui/model_test.go:19:51: undefined: effect
tui/fixture_test.go:187:7: too many errors
FAIL	github.com/fables-for-robots/great-machine/tui [build failed]
FAIL
```

- [ ] **Step 3: Write the contract and the model**

`tui/tui.go`: the package, and what the contract has of it but `Run`, which is Task 7.

```go
// Package tui is the terminal UI of great-machine: what the web page
// shows, in a terminal. It shows every service and every connected
// zathras with its state, its figures and its log, followed live, and
// rolls nothing out.
//
// It talks to draal through an Operator, as the other operator commands
// do, and is three parts. The model is the state and a function of the
// messages it gets: it touches no terminal and no network, and says what
// it wants done as effects. The views are functions from the model to
// text. The program around them (Run) is Bubble Tea: it reads the
// terminal, draws the views, carries out the effects against the
// Operator, and tells the model what came of them.
//
// Nothing that draal or a zathras sent reaches the terminal as it came:
// see text.go.
package tui

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/fables-for-robots/great-machine/term"
	"github.com/fables-for-robots/great-machine/wire"
)

// Operator is what the terminal UI asks of draal. An *operator.Client is
// one, but for the type that its Shell returns: cli has the adapter.
type Operator interface {
	Watch(ctx context.Context) (<-chan wire.Fleet, error)
	Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error)
	History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error)
	Logs(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error)
	Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (Shell, error)
	Close() error
}

// Shell is a shell session: what term.Attach joins the terminal to, and
// Close, which ends it.
type Shell interface {
	term.Session
	Close() error
}

// Config configures the terminal UI.
type Config struct {
	// Dial connects to draal. It is called at the start, and again, with
	// pauses that grow, whenever the connection was lost. The terminal UI
	// closes what Dial returned.
	Dial func(ctx context.Context) (Operator, error)
	// In and Out are the terminal.
	In, Out *os.File
	// Draal is how the header names draal: the start of its endpoint ID.
	Draal string
	// Each of these has a default when it is zero:
	RetryFirst time.Duration    // the first pause before Dial is tried again; 1 second
	RetryMax   time.Duration    // the longest pause; 30 seconds
	Now        func() time.Time // time.Now
	Log        *slog.Logger     // nowhere: a terminal UI has no place to log to
}

// withDefaults returns cfg with the defaults in place of what is zero.
func (cfg Config) withDefaults() Config {
	if cfg.RetryFirst <= 0 {
		cfg.RetryFirst = time.Second
	}
	if cfg.RetryMax <= 0 {
		cfg.RetryMax = 30 * time.Second
	}
	if cfg.RetryMax < cfg.RetryFirst {
		cfg.RetryMax = cfg.RetryFirst
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	return cfg
}
```

`tui/model.go`:

```go
package tui

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/fables-for-robots/great-machine/wire"
)

// screen is one of the three views.
type screen int

const (
	// screenServices lists every service.
	screenServices screen = iota
	// screenService is one service: its zathrases and its rollouts.
	screenService
	// screenZathras is one zathras: its state, its figures and its log.
	screenZathras
)

// connState is the state of the connection to draal.
type connState int

const (
	// connConnecting: draal was never heard of yet.
	connConnecting connState = iota
	// connLive: a fleet has come over the connection there is.
	connLive
	// connLost: there was a connection, and it ended.
	connLost
)

// asked is how far something is that draal is asked for.
type asked int

const (
	// askedNot: it was not asked for: there is no connection.
	askedNot asked = iota
	// askedFor: draal was asked and has not answered.
	askedFor
	// askedHave: draal has answered.
	askedHave
	// askedRefused: draal has refused.
	askedRefused
	// askedEnded: of a log: it was followed and has ended.
	askedEnded
)

// The messages by which the program tells the model things. Each but
// tickMsg has the number of the connection it is of: the model counts
// the connections (epoch), and what is of an older one than the one it
// waits for is dropped unread.

// dialedMsg says that Dial has connected and the watch was begun.
type dialedMsg struct{ epoch int }

// dialFailedMsg says that Dial, or the beginning of the watch, failed.
type dialFailedMsg struct {
	epoch int
	err   error
}

// fleetMsg is a fleet of the watch.
type fleetMsg struct {
	epoch int
	fleet wire.Fleet
}

// lostMsg says that the connection is lost: the watch has ended, or a
// request failed with something that is no refusal of draal's.
type lostMsg struct {
	epoch int
	err   error // nil when the watch just ended
}

// rolloutsMsg answers effRollouts: the rollouts, or draal's refusal. n
// is the number of the request it answers.
type rolloutsMsg struct {
	epoch    int
	n        int
	service  string
	rollouts []wire.RolloutInfo
	err      error
}

// historyMsg answers effHistory: the samples, or draal's refusal. n is
// the number of the request it answers.
type historyMsg struct {
	epoch   int
	n       int
	zathras wire.ID
	samples []wire.Sample
	err     error
}

// logMsg is lines of the log that effFollow with this id asked for: at
// most logKept of them, and one message every logEvery at most. Until
// the first one, the model knows only that the log was asked for: draal
// answers a log with its lines alone, and a log without a line has not
// answered.
type logMsg struct {
	epoch int
	id    int
	lines []wire.LogLine
}

// logEndedMsg says that the log of this id has ended: err is draal's
// refusal, or nil when the lines just ended.
type logEndedMsg struct {
	epoch int
	id    int
	err   error
}

// retryMsg says that the pause effWait asked for with this number is
// over.
type retryMsg struct{ n int }

// tickMsg comes every second: the times that are drawn move on.
type tickMsg struct{}

// effect is something the model wants done. The program does it.
type effect any

// effDial asks for Dial and, on what it returns, Watch. Everything the
// program says of that connection has this epoch. It answers with
// dialedMsg and then fleetMsg after fleetMsg, or with dialFailedMsg; and
// with lostMsg when the watch ends.
type effDial struct{ epoch int }

// effHangUp asks for the connection there is to be closed, and
// everything that was asked of it to be ended.
type effHangUp struct{}

// effWait asks for retryMsg{n} after the pause.
type effWait struct {
	pause time.Duration
	n     int
}

// effRollouts asks draal for the rollouts of a service. Its answer has
// its number n: two requests may be under way, and their answers may
// come in any order.
type effRollouts struct {
	epoch   int
	n       int
	service string
}

// effHistory asks draal for the samples of a zathras. Its answer has its
// number n.
type effHistory struct {
	epoch   int
	n       int
	zathras wire.ID
}

// effFollow asks for the log of a zathras to be followed, in place of
// the one that is followed now. Its lines come as logMsg with this id.
type effFollow struct {
	epoch   int
	zathras wire.ID
	id      int
}

// effUnfollow asks for the log that is followed to be ended.
type effUnfollow struct{}

// effQuit ends the program: the user has quit.
type effQuit struct{}

// effFail ends the program with an error: draal refuses this operator.
type effFail struct{ err error }

// rolloutKey is what of a service's last rollout its list of rollouts
// was asked for: when it changes, the list is asked for again.
type rolloutKey struct {
	id    uint64
	state wire.RolloutState
}

// logRow is a row of the log as it is drawn: a line of the log is one
// row, or several when it is longer than the terminal is wide.
type logRow struct {
	entry int    // which of the buffer's entries, as they are now
	seq   uint64 // the number of that entry
	sub   int    // which row of its entry
	spans []span // of a line: what of it is in this row
}

// logRowsPerLine is how many rows one line of a log may take. What is
// longer ends in an ellipsis.
const logRowsPerLine = 4

// model is the state of the terminal UI. It is changed by update alone,
// which is a function of the model and a message: it reads no clock but
// the one it was given, and does nothing but return effects.
type model struct {
	cfg Config

	width, height int

	// The connection.
	conn       connState
	epoch      int           // the number of the connection that is dialed or there
	everDialed bool          // Dial has succeeded once
	since      time.Time     // when the connection was lost, or first asked for
	pause      time.Duration // the pause before the next dial; 0 before the first failure
	retryAt    time.Time     // when the pause is over; the zero time while a dial is under way
	waitN      int           // the number of the pause that counts
	dialErr    string        // why the last dial failed; clean
	fatal      error         // why the program ends, when it does not end by a key

	// The fleet, as it came last: services by name, zathrases by name
	// and then by ID.
	fleet   wire.Fleet
	have    bool      // a fleet has come, ever
	fleetAt time.Time // when the last one came

	screen screen
	help   bool
	notice string // one line that is said once: gone with the next key

	// screenServices: what is chosen, by name, and where it was.
	svcName string
	svcIdx  int
	svcTop  int

	// screenService: the service, and of its zathrases what is chosen.
	service      string
	zID          wire.ID
	zIdx         int
	zTop         int
	rollouts     []wire.RolloutInfo
	rolloutsAre  asked
	rolloutsErr  string // draal's refusal; clean
	rolloutsOf   rolloutKey
	rolloutsN    int       // the number of the request whose answer counts: the newest
	rolloutsFrom time.Time // when they came

	// screenZathras: the zathras, as it was seen last.
	zathras   wire.ID
	seen      wire.Zathras
	seenIn    string // its service
	present   bool   // it is in the fleet that came last
	goneSince time.Time
	samples   []wire.Sample
	history   asked
	historyN  int    // the number of the request whose answer counts: the newest
	histErr   string // draal's refusal; clean
	log       *logBuffer
	logID     int // the number of the log that is followed
	logIs     asked
	rows      []logRow
	follow    bool // the end of the log is kept in sight
	logTop    int  // the first row in sight, when follow is not set

	// shared is how the rows of the view that is open are shared out
	// among its parts. It is worked out once for a message, by whoever
	// asks first, and thrown away with the next.
	shared shared
}

// newModel returns the model of a terminal UI that has not begun.
func newModel(cfg Config) *model {
	return &model{cfg: cfg.withDefaults(), log: newLogBuffer(), follow: true}
}

// start is what the model wants at the beginning: the first dial.
func (m *model) start() []effect {
	m.conn, m.since = connConnecting, m.cfg.Now()
	return []effect{m.dial()}
}

// dial begins a new connection.
func (m *model) dial() effect {
	m.epoch++
	m.retryAt = time.Time{}
	return effDial{epoch: m.epoch}
}

// wait begins a pause, after which a dial is tried again.
func (m *model) wait() effect {
	m.waitN++
	m.retryAt = m.cfg.Now().Add(m.pause)
	return effWait{pause: m.pause, n: m.waitN}
}

// refusal returns the *wire.Error in err's chain: what draal answered,
// as against a connection that failed.
func refusal(err error) (*wire.Error, bool) {
	var e *wire.Error
	ok := errors.As(err, &e)
	return e, ok
}

// sortFleet returns the fleet in the order the terminal UI keeps it:
// services by name, zathrases by name and then by ID. draal sends it so;
// what is chosen must not depend on that. What it says in words is
// capped on the way (see capped), but for the name of a service and the
// ID of a zathras, which are what draal is asked by.
func sortFleet(f wire.Fleet) wire.Fleet {
	out := wire.Fleet{Services: slices.Clone(f.Services)}
	slices.SortStableFunc(out.Services, func(a, b wire.Service) int { return cmp.Compare(a.Name, b.Name) })
	for i := range out.Services {
		if r := out.Services[i].Rollout; r != nil {
			last := *r
			last.Reason, last.State = capped(last.Reason), wire.RolloutState(capped(string(last.State)))
			out.Services[i].Rollout = &last
		}
		zs := slices.Clone(out.Services[i].Zathrases)
		for j := range zs {
			zs[j].Name = capped(zs[j].Name)
			zs[j].State = cappedState(zs[j].State)
		}
		slices.SortStableFunc(zs, func(a, b wire.Zathras) int {
			return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
		})
		out.Services[i].Zathrases = zs
	}
	return out
}

// cappedState returns what a zathras reports with its words capped: its
// binary, its arguments (64 of them at most), and how it ended last.
func cappedState(st wire.State) wire.State {
	st.Binary = capped(st.Binary)
	if len(st.Args) > 0 {
		args := make([]string, 0, min(len(st.Args), 65))
		for i, a := range st.Args {
			if i == 64 {
				args = append(args, fmt.Sprintf("… (%d arguments more)", len(st.Args)-64))
				break
			}
			args = append(args, capped(a))
		}
		st.Args = args
	}
	if st.LastExit != nil {
		exit := *st.LastExit
		exit.Signal, exit.Error = capped(exit.Signal), capped(exit.Error)
		st.LastExit = &exit
	}
	return st
}

// serviceNamed returns the service of that name in the fleet, if it is
// there.
func (m *model) serviceNamed(name string) (wire.Service, bool) {
	for _, svc := range m.fleet.Services {
		if svc.Name == name {
			return svc, true
		}
	}
	return wire.Service{}, false
}

// find returns a zathras of the fleet and the name of its service.
func (m *model) find(id wire.ID) (wire.Zathras, string, bool) {
	for _, svc := range m.fleet.Services {
		for _, z := range svc.Zathrases {
			if z.ID == id {
				return z, svc.Name, true
			}
		}
	}
	return wire.Zathras{}, "", false
}

// update takes a message and returns what the model wants done.
func (m *model) update(msg tea.Msg) []effect {
	m.shared = shared{}
	effs := m.take(msg)
	m.shared = shared{}
	m.keepInSight()
	return effs
}

// take is update, but for keeping what is chosen in sight.
func (m *model) take(msg tea.Msg) []effect {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.key(msg.String())
	case tea.WindowSizeMsg:
		m.width, m.height = max(msg.Width, 0), max(msg.Height, 0)
		m.buildRows()
	case dialedMsg:
		if msg.epoch == m.epoch {
			m.everDialed = true
		}
	case dialFailedMsg:
		return m.dialFailed(msg)
	case fleetMsg:
		return m.newFleet(msg)
	case lostMsg:
		return m.lost(msg)
	case retryMsg:
		if msg.n == m.waitN && m.conn != connLive && !m.retryAt.IsZero() {
			return []effect{m.dial()}
		}
	case rolloutsMsg:
		m.newRollouts(msg)
	case historyMsg:
		m.newHistory(msg)
	case logMsg:
		if msg.epoch == m.epoch && msg.id == m.logID && m.screen == screenZathras {
			m.logIs = askedHave
			if m.log.add(msg.lines) > 0 {
				m.buildRows()
			}
		}
	case logEndedMsg:
		m.logEnded(msg)
	case shellEndedMsg:
		return m.shellEnded(msg)
	}
	return nil
}

// dialFailed takes the failure of a dial. What draal refuses an operator
// that it never let in ends the program; anything else is tried again
// after a pause, twice as long as the one before.
func (m *model) dialFailed(msg dialFailedMsg) []effect {
	if msg.epoch != m.epoch || m.conn == connLive {
		return nil
	}
	if e, ok := refusal(msg.err); ok && e.Code == wire.CodeUnauthorized && !m.everDialed {
		m.fatal = msg.err
		return []effect{effFail{err: msg.err}}
	}
	m.dialErr = ""
	if msg.err != nil {
		m.dialErr = clean(capped(msg.err.Error()))
	}
	m.pause = min(max(2*m.pause, m.cfg.RetryFirst), m.cfg.RetryMax)
	return []effect{m.wait()}
}

// lost takes the end of the connection: what is shown stays, and a dial
// is tried again after the first pause.
func (m *model) lost(msg lostMsg) []effect {
	if msg.epoch != m.epoch || !m.retryAt.IsZero() {
		return nil
	}
	if m.conn != connLost {
		m.conn, m.since = connLost, m.cfg.Now()
	}
	m.dialErr = ""
	if msg.err != nil {
		m.dialErr = clean(capped(msg.err.Error()))
	}
	// What was asked of the connection is ended with it.
	if m.rolloutsAre == askedFor {
		m.rolloutsAre = askedNot
	}
	if m.history == askedFor {
		m.history = askedNot
	}
	if m.logIs == askedFor || m.logIs == askedHave {
		m.logIs = askedEnded
		m.log.say("The connection to draal was lost.")
		m.buildRows()
	}
	m.epoch++ // nothing of the old connection is read from here on
	m.pause = m.cfg.RetryFirst
	return []effect{effHangUp{}, m.wait()}
}

// newFleet takes a fleet. The first one of a connection makes the
// connection live, and what the view that is open holds is asked for
// again.
func (m *model) newFleet(msg fleetMsg) []effect {
	if msg.epoch != m.epoch {
		return nil
	}
	now := m.cfg.Now()
	first := m.conn != connLive
	m.conn, m.pause, m.dialErr, m.retryAt = connLive, 0, "", time.Time{}
	m.fleet, m.have, m.fleetAt = sortFleet(msg.fleet), true, now
	m.choose()

	effs := m.rolloutsDue(first)
	if m.screen == screenZathras {
		z, in, ok := m.find(m.zathras)
		switch {
		case ok:
			// It is there. It is asked for its samples and its log when
			// the connection is new, when it was gone, and when it has
			// connected again between two fleets.
			again := first || !m.present || !z.Connected.Equal(m.seen.Connected)
			m.seen, m.seenIn, m.present = z, in, true
			if z.State.Sample != nil {
				m.samples = mergeSamples(m.samples, []wire.Sample{*z.State.Sample}, now)
			}
			if again {
				effs = append(effs, m.askZathras()...)
			}
		case m.present:
			m.present, m.goneSince = false, now
		}
	}
	return effs
}

// rolloutsDue asks for the rollouts of the service that is open when
// they are due: when they were not asked for over this connection (anew
// says that the connection is new), and when the last rollout of the
// service is another one, or in another state, than when they were
// asked for.
func (m *model) rolloutsDue(anew bool) []effect {
	if m.screen != screenService || m.conn != connLive {
		return nil
	}
	svc, ok := m.serviceNamed(m.service)
	if !ok || !anew && m.rolloutsAre != askedNot && keyOf(svc) == m.rolloutsOf {
		return nil
	}
	return []effect{m.askRollouts(svc)}
}

// keyOf returns what of a service's last rollout its list is asked for.
func keyOf(svc wire.Service) rolloutKey {
	if svc.Rollout == nil {
		return rolloutKey{}
	}
	return rolloutKey{id: svc.Rollout.ID, state: svc.Rollout.State}
}

// askRollouts asks for the rollouts of the service that is open.
func (m *model) askRollouts(svc wire.Service) effect {
	m.rolloutsOf = keyOf(svc)
	if m.rolloutsAre != askedHave {
		m.rolloutsAre = askedFor
	}
	m.rolloutsN++
	return effRollouts{epoch: m.epoch, n: m.rolloutsN, service: svc.Name}
}

// askZathras asks for what the view of a zathras holds: its samples
// once, and its log, followed. Lines the buffer has are not taken again.
func (m *model) askZathras() []effect {
	if m.history != askedHave {
		m.history = askedFor
	}
	m.logID++
	m.logIs = askedFor
	m.log.resume()
	m.historyN++
	return []effect{
		effHistory{epoch: m.epoch, n: m.historyN, zathras: m.zathras},
		effFollow{epoch: m.epoch, zathras: m.zathras, id: m.logID},
	}
}

// again asks anew for what the view that is open holds, as after a
// time in which the model was told nothing: the rollouts of a service,
// the samples and the log of a zathras. What was on its way is not
// read; no line of the log is taken twice.
func (m *model) again() []effect {
	if m.conn != connLive {
		return nil
	}
	switch m.screen {
	case screenService:
		return m.rolloutsDue(true)
	case screenZathras:
		if m.present {
			return m.askZathras()
		}
	}
	return nil
}

// choose keeps what is chosen in the lists chosen by what it is: a
// service by its name, a zathras by its ID. When that is gone, what is
// now at its place is chosen, or the last.
func (m *model) choose() {
	services := m.fleet.Services
	at := slices.IndexFunc(services, func(s wire.Service) bool { return s.Name == m.svcName })
	switch {
	case len(services) == 0:
		m.svcName, m.svcIdx = "", 0
	case at >= 0:
		m.svcIdx = at
	default:
		m.svcIdx = min(max(m.svcIdx, 0), len(services)-1)
		m.svcName = services[m.svcIdx].Name
	}

	svc, _ := m.serviceNamed(m.service)
	zs := svc.Zathrases
	at = slices.IndexFunc(zs, func(z wire.Zathras) bool { return z.ID == m.zID })
	switch {
	case len(zs) == 0:
		// The service has no zathras now, or is gone itself: what was
		// chosen is remembered, for when it is back.
		m.zIdx = 0
	case at >= 0:
		m.zIdx = at
	default:
		m.zIdx = min(max(m.zIdx, 0), len(zs)-1)
		m.zID = zs[m.zIdx].ID
	}
}

// newRollouts takes the answer to the newest request for the rollouts of
// the service that is open, whatever view is in sight when it comes: the
// view of one of its zathrases, too. An answer to an older request is
// dropped: it may come last, and says what was.
func (m *model) newRollouts(msg rolloutsMsg) {
	if msg.epoch != m.epoch || msg.service != m.service || msg.n != m.rolloutsN {
		return
	}
	if msg.err != nil {
		m.rolloutsAre, m.rolloutsErr = askedRefused, clean(capped(msg.err.Error()))
		return
	}
	list := slices.Clone(msg.rollouts)
	for i := range list {
		list[i].Reason, list[i].State = capped(list[i].Reason), wire.RolloutState(capped(string(list[i].State)))
	}
	m.rollouts, m.rolloutsAre, m.rolloutsErr, m.rolloutsFrom = list, askedHave, "", m.cfg.Now()
}

// newHistory takes the samples of the zathras that is open.
func (m *model) newHistory(msg historyMsg) {
	if msg.epoch != m.epoch || msg.zathras != m.zathras || msg.n != m.historyN || m.screen != screenZathras {
		return
	}
	if msg.err != nil {
		m.history, m.histErr = askedRefused, clean(capped(msg.err.Error()))
		return
	}
	m.samples, m.history, m.histErr = mergeSamples(m.samples, msg.samples, m.cfg.Now()), askedHave, ""
}

// logEnded takes the end of the log that is followed.
func (m *model) logEnded(msg logEndedMsg) {
	if msg.epoch != m.epoch || msg.id != m.logID || m.screen != screenZathras {
		return
	}
	m.logIs = askedEnded
	if e, ok := refusal(msg.err); ok {
		m.log.say("draal does not send the log: " + capped(e.Message))
	} else {
		m.log.say("The log has ended.")
	}
	m.buildRows()
}

// key takes a key by its name.
func (m *model) key(name string) []effect {
	if name == "q" || name == "ctrl+c" {
		return []effect{effQuit{}}
	}
	var effs []effect
	// A key ends a pause at once.
	if m.conn != connLive && !m.retryAt.IsZero() {
		effs = append(effs, m.dial())
	}
	m.notice = ""
	if m.help {
		m.help = false
		return effs
	}
	switch name {
	case "?":
		m.help = true
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup":
		m.move(-m.page())
	case "pgdown":
		m.move(m.page())
	case "g":
		m.moveTo(false)
	case "G":
		m.moveTo(true)
		if m.screen == screenZathras && m.logIs == askedEnded && m.present && m.conn == connLive {
			// G follows a log again, also one that has ended.
			m.logID++
			m.logIs = askedFor
			m.log.resume()
			effs = append(effs, effFollow{epoch: m.epoch, zathras: m.zathras, id: m.logID})
		}
	case "enter", "right", "l":
		effs = append(effs, m.open()...)
	case "esc", "left", "h", "backspace":
		effs = append(effs, m.back()...)
	case "s":
		if m.screen == screenZathras {
			effs = append(effs, m.shellKey()...)
		}
	}
	return effs
}

// shellKey is the key s on a zathras: a shell, when there is a
// container to open it in and a connection to ask over.
func (m *model) shellKey() []effect {
	switch {
	case !m.present:
		m.notice = "This zathras is not connected: there is no container to open a shell in."
		return nil
	case m.conn != connLive:
		m.notice = "There is no connection to draal: a shell cannot be opened now."
		return nil
	}
	return m.openShell()
}

// page returns how far PgUp and PgDn move: a screenful less one row.
func (m *model) page() int {
	if m.screen == screenZathras {
		return max(m.logHeight()-1, 1)
	}
	if m.screen == screenService {
		return max(m.zathrasesHeight()-1, 1)
	}
	return max(m.servicesHeight()-1, 1)
}

// move moves what is chosen in the list that is in sight by delta
// places, or scrolls the log by delta rows.
func (m *model) move(delta int) {
	switch m.screen {
	case screenServices:
		if n := len(m.fleet.Services); n > 0 {
			m.svcIdx = min(max(m.svcIdx+delta, 0), n-1)
			m.svcName = m.fleet.Services[m.svcIdx].Name
		}
	case screenService:
		svc, _ := m.serviceNamed(m.service)
		if n := len(svc.Zathrases); n > 0 {
			m.zIdx = min(max(m.zIdx+delta, 0), n-1)
			m.zID = svc.Zathrases[m.zIdx].ID
		}
	case screenZathras:
		last := max(len(m.rows)-m.logHeight(), 0)
		top := m.logTop
		if m.follow {
			top = last
		}
		top = min(max(top+delta, 0), last)
		m.logTop, m.follow = top, top >= last
	}
}

// moveTo moves to the start or to the end of the list that is in sight.
// The end of a log is followed.
func (m *model) moveTo(end bool) {
	if m.screen == screenZathras {
		m.logTop, m.follow = 0, end || len(m.rows) <= m.logHeight()
		return
	}
	if end {
		m.move(len(m.fleet.Services) + 1<<20)
	} else {
		m.move(-len(m.fleet.Services) - 1<<20)
	}
}

// open opens what is chosen: a service, or a zathras of it.
func (m *model) open() []effect {
	switch m.screen {
	case screenServices:
		svc, ok := m.serviceNamed(m.svcName)
		if !ok {
			return nil
		}
		m.screen = screenService
		if m.service != svc.Name {
			m.service, m.zID, m.zIdx, m.zTop = svc.Name, "", 0, 0
			m.rollouts, m.rolloutsAre, m.rolloutsErr, m.rolloutsOf = nil, askedNot, "", rolloutKey{}
		}
		m.choose()
		return m.rolloutsDue(true)
	case screenService:
		z, in, ok := m.find(m.zID)
		if !ok || in != m.service {
			return nil
		}
		m.screen = screenZathras
		m.zathras, m.seen, m.seenIn, m.present, m.goneSince = z.ID, z, in, true, time.Time{}
		m.samples, m.history, m.histErr = nil, askedNot, ""
		if z.State.Sample != nil {
			m.samples = mergeSamples(nil, []wire.Sample{*z.State.Sample}, m.cfg.Now())
		}
		m.log, m.logIs, m.rows, m.follow, m.logTop = newLogBuffer(), askedNot, nil, true, 0
		if m.conn == connLive {
			return m.askZathras()
		}
	}
	return nil
}

// back leaves the view that is open for the one it was opened from.
// What the view held is ended.
func (m *model) back() []effect {
	switch m.screen {
	case screenZathras:
		m.screen = screenService
		followed := m.logIs == askedFor || m.logIs == askedHave
		m.logID++ // lines that are on their way are not for the next log
		m.logIs = askedNot
		if m.present {
			m.zID = m.zathras
		}
		m.choose()
		var effs []effect
		if followed {
			effs = append(effs, effUnfollow{})
		}
		return append(effs, m.rolloutsDue(false)...)
	case screenService:
		m.screen = screenServices
		if _, ok := m.serviceNamed(m.service); ok {
			m.svcName = m.service
		}
		m.choose()
	}
	return nil
}

// keepInSight moves the lists so that what is chosen is in sight.
func (m *model) keepInSight() {
	switch m.screen {
	case screenServices:
		m.svcTop = inSight(m.svcTop, m.svcIdx, len(m.fleet.Services), max(m.servicesHeight(), 1))
	case screenService:
		svc, _ := m.serviceNamed(m.service)
		m.zTop = inSight(m.zTop, m.zIdx, len(svc.Zathrases), max(m.zathrasesHeight(), 1))
	case screenZathras:
		if last := max(len(m.rows)-m.logHeight(), 0); m.logTop > last {
			m.logTop = last
		}
	}
}

// inSight returns the first row of a list of n rows that shows h of
// them, moved from top as little as puts row idx in sight.
func inSight(top, idx, n, h int) int {
	if idx < top {
		top = idx
	}
	if idx >= top+h {
		top = idx - h + 1
	}
	return min(max(top, 0), max(n-h, 0))
}

// buildRows makes the rows of the log out of its lines, for the width
// the terminal has. A reader who does not follow the end keeps the row
// that was first in sight where it is.
func (m *model) buildRows() {
	var anchor logRow
	held := !m.follow && m.logTop < len(m.rows)
	if held {
		anchor = m.rows[m.logTop]
	}
	textWidth := m.width - logPrefix(m.width)
	rows := make([]logRow, 0, len(m.log.entries))
	for i, e := range m.log.entries {
		if e.kind != entryLine {
			rows = append(rows, logRow{entry: i, seq: e.seq})
			continue
		}
		parts, cut := wrapSpans(e.spans, textWidth, logRowsPerLine)
		if (cut || e.cut) && len(parts) > 0 && textWidth > 0 {
			// The last row ends in an ellipsis, in a cell of its own.
			var last []span
			if kept, _ := wrapSpans(parts[len(parts)-1], textWidth-1, 1); len(kept) > 0 {
				last = slices.Clone(kept[0])
			}
			parts[len(parts)-1] = append(last, span{text: ellipsis, look: look{dim: true}})
		}
		for sub, spans := range parts {
			rows = append(rows, logRow{entry: i, seq: e.seq, sub: sub, spans: spans})
		}
		if len(parts) == 0 {
			rows = append(rows, logRow{entry: i, seq: e.seq})
		}
	}
	m.rows = rows
	if !held {
		return
	}
	// The row that was first in sight, or what is left of its line, or,
	// when its line is gone, the oldest there is.
	m.logTop = 0
	for i, r := range rows {
		if r.seq == anchor.seq && r.sub <= anchor.sub {
			m.logTop = i
		}
		if r.seq > anchor.seq {
			break
		}
	}
}

// logPrefix returns how many cells stand before the text of a line of a
// log: the time and the stream, or the stream alone in a terminal that
// is narrow.
func logPrefix(width int) int {
	if width < 60 {
		return 3
	}
	return 12
}
```

`tui/shell.go`: the place of the shell, which a later task fills.

```go
package tui

import "github.com/fables-for-robots/great-machine/wire"

// The shell of a zathras comes with a later task. This file is its
// place: the effect by which the model asks for one, the message by
// which the program says how it ended, and what the key does until then.

// effShell asks the program for a shell on the zathras: to give the
// terminal to the session, and to take it back when the shell has ended
// or was left.
type effShell struct {
	epoch   int
	zathras wire.ID
}

// shellEndedMsg says how a shell ended: with an exit status, left with
// Ctrl-], or with an error.
type shellEndedMsg struct {
	exit     int
	detached bool
	err      error
}

// openShell is the key s on a zathras that is connected, with a
// connection to draal that is live.
func (m *model) openShell() []effect {
	m.notice = "The shell is not part of this terminal UI yet: `great-machine shell` opens one."
	return nil
}

// shellEnded takes the message that a shell has ended. While the shell
// had the terminal the model was told nothing, for as long as that was:
// what the view holds is asked for anew.
func (m *model) shellEnded(msg shellEndedMsg) []effect {
	return m.again()
}
```

`tui/view.go`, for now: the next task replaces this file with the views. The model asks it how many rows its lists and its log have, and the tests of this task draw with it.

```go
package tui

// What is drawn comes with the next task. Until then the model has the
// little of it that its own tests need: how much room the lists and the
// log have, and a view that says where one is.

import (
	"fmt"
	"strings"
)

const (
	// leastWidth and leastHeight are the smallest terminal the terminal
	// UI draws in. In a smaller one it says so, in one line.
	leastWidth  = 50
	leastHeight = 12
)

// shared is what of the view that is open is worked out once for a
// message. The views of the next task keep there how their rows are
// shared out among their parts.
type shared struct{}

// tooSmall says whether the terminal is smaller than the terminal UI
// can draw in.
func (m *model) tooSmall() bool {
	return m.width < leastWidth || m.height < leastHeight
}

// servicesHeight returns how many services of the list are in sight.
func (m *model) servicesHeight() int {
	return max(m.height-2, 1)
}

// zathrasesHeight returns how many zathrases of a service's table are
// in sight.
func (m *model) zathrasesHeight() int {
	return max(m.height-2, 1)
}

// logHeight returns how many rows of the log are in sight.
func (m *model) logHeight() int {
	return max(m.height-2, 1)
}

// view draws the model: as many lines as the terminal has rows at most,
// none wider than the terminal.
func (m *model) view() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	if m.tooSmall() {
		return cut(fmt.Sprintf("Too small: great-machine needs a terminal of %d×%d or more.", leastWidth, leastHeight), m.width)
	}
	where := []string{"great-machine"}
	if m.screen != screenServices {
		where = append(where, named(m.service))
	}
	if m.screen == screenZathras {
		where = append(where, named(m.seen.Name))
	}
	lines := make([]string, m.height)
	lines[0] = cut(" "+strings.Join(where, " › "), m.width)
	if m.notice != "" {
		lines[m.height-2] = cut(" "+m.notice, m.width)
	}
	lines[m.height-1] = cut(" ? keys  q quit", m.width)
	return strings.Join(lines, "\n")
}
```

```bash
nix develop .#bare --command go mod tidy
```

`go.mod` now has `charm.land/bubbletea/v2 v2.0.10` in its first `require` block.

- [ ] **Step 4: Run the tests to see them pass**

Run: `nix develop .#bare --command go test ./tui/`
Expected: `ok`, in about two seconds. `TestEverySizeOfTerminal` draws ten of the scenes at each of the 4000 sizes from 1×1 to 100×40, and every scene at thirteen sizes.

Run: `nix develop .#bare --command go test -race -count=20 -skip TestEverySizeOfTerminal ./tui/ && nix develop .#bare --command gofmt -l . && nix develop .#bare --command go vet ./...`
Expected: `ok`, and nothing from `gofmt` and `go vet`

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum tui/
git commit -m "TUI: the model: the fleet as it is held, the three views and the keys between them, the connection and its pauses, and what each view holds"
```
