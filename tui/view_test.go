package tui

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fables-for-robots/great-machine/wire"
)

// screenOf returns what a person reads of the model in its terminal:
// the lines, without their colours and without the spaces at their
// ends.
func screenOf(m *model) []string {
	lines := strings.Split(plain(m.view()), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return lines
}

// wantScreen fails the test when the model is not drawn as wanted, and
// says which lines differ.
func wantScreen(t *testing.T, what string, m *model, want []string) {
	t.Helper()
	got := screenOf(m)
	for i := range max(len(got), len(want)) {
		var g, w string
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			t.Errorf("%s, line %d:\n got %q\nwant %q", what, i+1, g, w)
		}
	}
}

// sceneNamed returns the model of a scene in a terminal of the size.
func sceneNamed(t *testing.T, name string, w, h int) *model {
	t.Helper()
	for _, sc := range scenes() {
		if sc.name == name {
			return sc.model(w, h)
		}
	}
	t.Fatalf("there is no scene %s", name)
	return nil
}

// wantSaid fails the test when the model, drawn, does not say all of
// the sentences. A sentence may be broken over lines.
func wantSaid(t *testing.T, what string, m *model, sentences ...string) {
	t.Helper()
	said := strings.Join(strings.Fields(strings.Join(screenOf(m), " ")), " ")
	for _, s := range sentences {
		if !strings.Contains(said, s) {
			t.Errorf("%s: it does not say %q. It says:\n%s", what, s, strings.Join(screenOf(m), "\n"))
		}
	}
}

// The three views in a terminal of 80 columns and 24 rows, as a person
// reads them. Every state has its sign and its word: none is said by
// colour alone.
func TestServicesViewAt80x24(t *testing.T) {
	wantScreen(t, "the services", sceneNamed(t, "services", 80, 24), []string{
		" great-machine                                          draal 3f9a2c1b · ● live",
		" 4 services, 12 zathrases connected",
		"",
		"▸ empty   stable generation 2   no zathras connected",
		"          ✔ rollout 2 to generation 2: completed 1d ago",
		"",
		"  fresh   no stable generation  ○ 2 idle",
		"          nothing was rolled out yet",
		"",
		"  web     stable generation 20  ● 3 running  ◐ 1 stopping  ✖ 1 backoff",
		"          ▶ rollout 31 to generation 21: activating, 1 of 5 activated, started …",
		"",
		"  worker  stable generation 3   ● 3 running  ✖ 1 backoff  ○ 1 idle",
		"          ✖ rollout 30 to generation 4: halted 25m ago: worker-1: exited with s…",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		" ↑↓ move  enter open  ? keys  q quit",
	})
}

func TestServiceViewAt80x24(t *testing.T) {
	wantScreen(t, "a service", sceneNamed(t, "service-rollout-under-way", 80, 24), []string{
		" great-machine › web                                    draal 3f9a2c1b · ● live",
		" web · stable generation 20 · ● 3 running  ◐ 1 stopping  ✖ 1 backoff",
		"",
		" ▶ Rollout 31 to generation 21: activating",
		"   2 at a time, started 11:59:46 (14s ago) by the operator eeab12cd",
		"   draal counts 5 prepared, 1 activated, 1 awaited of 5 zathrases connected",
		"   not prepared 1   web-7d9f8b6c4-q5rvd",
		"   preparing    1   web-7d9f8b6c4-8hjwz·22ab",
		"   prepared     1   web-7d9f8b6c4-8hjwz·21ab",
		"   on it        1   web-7d9f8b6c4-2xkqp",
		"   ended on it  1   web-7d9f8b6c4-mn4tc",
		"",
		"  NAME          PHASE            GENERATION RESTARTS LAST EXIT    CPU     MEMORY",
		"▸ web-7d…-2xkqp ● running 9s     21                0 -            25%  200.0 MiB",
		"  web-…jwz·21ab ◐ stopping       20 stable         0 -            40%  220.0 MiB",
		"  web-…jwz·22ab ● running 46m12s 20 stable         0 -            55%  240.0 MiB",
		"  web-7d…-mn4tc ✖ backoff 6s     21                4 exit 1       70%  260.0 MiB",
		"  web-7d…-q5rvd ● running 46m00s 20 stable         0 SIGKILL      85%  280.0 MiB",
		"",
		" Last rollouts · the newest 3 of 20",
		"     #31 ▶ activating to 21    14s ago  under way",
		"     #30 ✔ completed  to 20    47m ago  took 1m07s",
		"     #29 – superseded to 19    1h ago   took 1m14s",
		" ↑↓ move  enter open  esc back  ? keys  q quit",
	})
}

func TestZathrasViewAt80x24(t *testing.T) {
	wantScreen(t, "a zathras", sceneNamed(t, "zathras", 80, 24), []string{
		" great-machine › web › web-7d9f8b6c4-8hjwz              draal 3f9a2c1b · ● live",
		" web-7d9f8b6c4-8hjwz",
		" ● running for 46m12s · generation 20, the stable one of web",
		" id         22ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd",
		" runs       /nix/store/kzrvqcn6yd2p8bqlhbqm8wl5bh7f0r1s-web-1.4.20/bin/web",
		"            --listen :8080 --db postgres://db.internal/web --workers 8",
		" process    pid 977, 0 restarts since this zathras started",
		" last exit  none: the service has not ended since this zathras started",
		" connected  since 01:00:00 (11h ago) · prepared 20 · preparing 21",
		"",
		" Figures of the container · the last 15 minutes, to 12:00:00 (now)",
		" CPU                8.3%  ▂▃▃▃▃▃▃▂▂▂▁▁▁▁▂▂▂▃▃▃▃▃▃▅▅▂▁▁  to 100% of one core",
		" memory        254.0 MiB  ▃▄▄▄▄▄▄▄▄▄▃▄▄▄▄▄▄▄▄▃▄▄▄▄▄▄▄▄  to 512.0 MiB, its limit",
		" disk read         0 B/s  ▁▁▆▁▁▁▁▁▆▁▁▁▁▁▆▁▁▁▁▆▁▁▁▁▁▆▁▁  to 8.0 MiB/s",
		" disk write    4.4 KiB/s  ▄▄▅▅▅▅▄▄▃▂▁▁▁▂▂▃▄▄▅▅▅▄▄▃▃▂▁▁  to 64.0 KiB/s",
		" net in       20.8 KiB/s  ▁▁▁▁▁▁▁▁▁▁▁▁▇▇▇▁▁▁▁▁▁▁▁▁▁▁▁▁  to 4.0 MiB/s",
		" net out      10.4 KiB/s  ▁▁▁▁▁▁▁▁▁▁▁▁▅▅▅▁▁▁▁▁▁▁▁▁▁▁▁▁  to 2.0 MiB/s",
		"",
		" Log · followed live",
		" 11:59:59 o 12:00:00.000 INFO  http: GET /api/orders/396 200 1.2ms",
		" 11:59:59 o 12:00:00.000 INFO  http: GET /api/orders/397 200 1.2ms",
		" 11:59:59 o 12:00:00.000 INFO  http: GET /api/orders/398 200 1.2ms",
		" 12:00:00 o 12:00:00.000 INFO  http: GET /api/orders/399 200 1.2ms",
		" ↑↓ scroll the log  G follow it  s shell  esc back  ? keys  q quit",
	})
}

// Review Focus 4: nothing to show. Every odd state is a sentence, and
// says what to do where there is something to do.
func TestOddStatesAreSentences(t *testing.T) {
	for _, tt := range []struct {
		scene string
		says  []string
	}{
		{"services-connecting", []string{"Connecting to draal 3f9a2c1b …", "◌ connecting"}},
		{"services-no-answer", []string{"draal does not answer: connecting to draal 3f9a2c1b: context deadline exceeded", "Asking draal again in 1s; any key asks now."}},
		{"services-none", []string{"draal knows no service yet.", "Start a zathras and name its service:", "great-machine zathras --draal ID --service NAME"}},
		{"service-no-zathras", []string{"No zathras of this service is connected.", "a zathras that is started with --service empty gets its stable generation, 2.", "#2 ✔ completed"}},
		{"service-no-rollout", []string{"Nothing was rolled out to this service yet.", "great-machine rollout fresh /nix/store/…/bin/…", "draal has no rollout of this service on record.", "fresh-0", "no figures"}},
		{"service-halted", []string{"✖ Rollout 30 to generation 4: halted 25m ago, after 21.0s", "why worker-1: exited with status 2 after 1.3s", "ended on it 1 worker-1", "draal counts 5 prepared, 0 activated, 0 awaited"}},
		{"service-asking", []string{"Last rollouts", "Asking draal …"}},
		{"service-refused", []string{"draal does not list them: internal: reading the rollouts: the database is locked"}},
		{"service-gone", []string{"draal knows no service of this name any more: web", "Esc goes to the services."}},
		{"zathras-no-samples", []string{"This zathras reports no figures.", "Its state, its log and its shell do not depend on them.", "Log · followed live"}},
		{"zathras-idle-no-log", []string{"○ idle · no generation: nothing was activated on it", "not running, 0 restarts", "nothing prepared", "Log · no line has come yet"}},
		{"zathras-asking", []string{"Figures", "Log · no line has come yet"}},
		{"zathras-gone", []string{"✖ This zathras is not connected to draal any more, since 12:00:10 (15s ago).", "Below is what it reported last.", "pid 31000", "Log · ended", "— The log has ended."}},
		{"zathras-backoff", []string{"✖ backoff, starts again in 6s · generation 21, not the stable one of web (20)", "exit 1 at 11:59:57 (3s ago), after 2.3s, of generation 21"}},
		{"zathras-notice", []string{"The shell ended with status 3."}},
		{"zathras-no-shell", []string{"No shell was opened: zathras 22ab12cd is not connected"}},
		{"zathras-opening-shell", []string{"Opening a shell on web-7d9f8b6c4-8hjwz … esc gives it up"}},
		{"zathras-scrolled", []string{"G goes to the end"}},
		{"zathras-help", []string{"on a zathras: open a shell", "q Ctrl-C quit", "● running ◐ stopping ✖ backoff ○ idle"}},
	} {
		wantSaid(t, tt.scene, sceneNamed(t, tt.scene, 80, 24), tt.says...)
	}

	// A halted rollout has its reason in full, however long it is.
	m, _ := onService(80, 24, "worker")
	f := fixtureFleet()
	reason := "worker-1: could not be started: fork/exec /nix/store/kzrvqcn6yd2p8bqlhbqm8wl5bh7f0r1s-worker-1.4.4/bin/worker: exec format error, and that is the whole of a reason that is longer than two lines of a terminal are wide"
	f.Services[3].Rollout.Reason = reason
	m.update(fleetMsg{epoch: 1, fleet: f})
	wantSaid(t, "a long reason", m, reason)

	// A name of 253 characters: whole on the view of its zathras, and
	// with both its ends in a table.
	m = sceneNamed(t, "zathras-long-name", 80, 24)
	if all := strings.Join(screenOf(m), ""); !strings.Contains(strings.ReplaceAll(all, " ", ""), longName) {
		t.Errorf("the view of the zathras does not have its name of 253 characters whole:\n%s", strings.Join(screenOf(m), "\n"))
	}
	wantSaid(t, "the long name in the table", sceneNamed(t, "service-halted", 80, 24), "▸ worker…ternal ● running 2d02h")
	wantSaid(t, "the long name in a wide table", sceneNamed(t, "service-halted", 160, 40), "▸ worker-batch-eu-central-1a.…de-0123456789abcdeinternal 34ab12cd ● running 2d02h")

	// Two zathrases of one name are told apart by their IDs.
	wantSaid(t, "two of one name", sceneNamed(t, "service-rollout-under-way", 80, 24), "web-…jwz·21ab ◐ stopping", "web-…jwz·22ab ● running 46m12s", "preparing 1 web-7d9f8b6c4-8hjwz·22ab")
}

// Review Focus 3: while the connection is lost, the header says so and
// for how long, and what is shown is marked as old.
func TestLostConnectionIsSaid(t *testing.T) {
	m := sceneNamed(t, "services-lost", 80, 24)
	wantSaid(t, "lost", m, "✖ lost for 33s", "✖ The connection to draal is lost since 12:00:42. What is shown is what draal sent last, 1m ago.",
		"4 services, 12 zathrases connected", "web stable generation 20")
	m = sceneNamed(t, "zathras-lost", 80, 24)
	wantSaid(t, "lost on a zathras", m, "✖ lost for 1m30s", "Asking draal again", "(connecting to draal 3f9a2c1b: context deadline exceeded)", "pid 977", "Log · ended", "— The connection to draal was lost.")
}

// Review Focus 2: whatever draal and the zathrases sent, what is drawn
// is text and colours of the terminal UI's own making.
func TestNothingOfAZathrasReachesTheTerminalAsItCame(t *testing.T) {
	const evil = "\x1b]0;pwned\x07\x1b[2J\x1b[5;5H\x1b[?1049l\u202e\u009b31m\x00\x08\r"
	f := fixtureFleet()
	for i := range f.Services {
		svc := &f.Services[i]
		if svc.Rollout != nil {
			svc.Rollout.Reason = evil + "reason" + evil
			svc.Rollout.State += wire.RolloutState(evil)
			svc.Rollout.CreatedBy += wire.ID(evil)
		}
		for j := range svc.Zathrases {
			z := &svc.Zathrases[j]
			z.Name = evil + z.Name + evil
			z.State.Binary += evil
			z.State.Args = append(z.State.Args, evil)
			z.State.Phase += wire.Phase(evil)
			z.State.LastExit = &wire.Exit{Signal: "SIG" + evil, Error: evil + "error", At: t0}
		}
	}
	// A service and a zathras whose very names are sequences.
	f.Services = append(f.Services, wire.Service{Name: evil + "svc", Zathrases: []wire.Zathras{{ID: wire.ID(evil + "id"), Name: evil}}})
	rollouts := fixtureRollouts("web")
	for i := range rollouts {
		rollouts[i].Reason, rollouts[i].State = evil+"why", rollouts[i].State+wire.RolloutState(evil)
	}
	lines := []wire.LogLine{
		{At: t0, Stream: evil, Text: evil + "a line" + evil},
		{At: t0, Stream: "stdout", Text: strings.Repeat("\x1b", 4096)},
		{At: t0, Stream: "stdout", Text: strings.Repeat(evil, 2000)},
		{At: t0, Stream: "stdout", Text: "half a title \x1b]0;pw"},
		{At: t0, Stream: "stdout", Text: "ned\x07 the rest of it"},
		{At: t0, Lost: 7},
	}
	check := func(what string, m *model) {
		t.Helper()
		for _, size := range [][2]int{{80, 24}, {200, 60}, {50, 12}} {
			m.update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			drawn := m.view()
			if found := unsafe(drawn); found != "" {
				t.Errorf("%s at %d×%d: what is drawn has %s", what, size[0], size[1], found)
			}
			if strings.Contains(drawn, "\x1b]") || strings.Contains(drawn, "\x1b[2J") || strings.Contains(drawn, "\x1b[5;5H") {
				t.Errorf("%s at %d×%d: a sequence of the fleet is in what is drawn", what, size[0], size[1])
			}
		}
	}
	m, _ := newTestModel(80, 24)
	m.update(dialFailedMsg{epoch: 1, err: errors.New(evil + "no answer")})
	check("a dial that failed", m)
	m.update(retryMsg{n: 1})
	goLive(m, f)
	for range len(f.Services) {
		check("the services", m)
		press(m, "enter")
		m.update(rolloutsMsg{epoch: m.epoch, n: m.rolloutsN, service: m.service, rollouts: rollouts})
		check("a service", m)
		m.update(rolloutsMsg{epoch: m.epoch, n: m.rolloutsN, service: m.service, err: wire.Errorf(wire.CodeInternal, "%s", evil+"refused")})
		check("a service whose rollouts are refused", m)
		svc, _ := m.serviceNamed(m.service)
		for range len(svc.Zathrases) {
			press(m, "enter")
			m.update(logMsg{epoch: m.epoch, id: m.logID, lines: lines})
			m.update(historyMsg{epoch: m.epoch, n: m.historyN, zathras: m.zathras, err: wire.Errorf(wire.CodeUnavailable, "%s", evil+"refused")})
			check("a zathras", m)
			m.update(logEndedMsg{epoch: m.epoch, id: m.logID, err: wire.Errorf(wire.CodeUnavailable, "%s", evil+"ended")})
			press(m, "?")
			check("the keys", m)
			press(m, "?", "esc", "down")
		}
		press(m, "esc", "down")
	}
	m.update(lostMsg{epoch: m.epoch, err: errors.New(evil + "lost")})
	check("a lost connection", m)
	// And the text is still there to be read.
	m, _ = newTestModel(80, 24)
	goLive(m, f)
	press(m, "down", "down", "down", "enter", "enter")
	m.update(logMsg{epoch: m.epoch, id: m.logID, lines: lines})
	wantSaid(t, "the text of a hostile zathras", m, "web-7d9f8b6c4-2xkqp", "half a title", "ned the rest of it", "7 lines were lost here")
	// A name of which nothing is left is said to be none.
	press(m, "esc", "esc", "g", "enter")
	wantSaid(t, "a zathras whose name is a sequence", m, "▸ (no name)")
}

// The colours are the terminal's own: of its sixteen, bold, faint,
// underline and reverse, and nothing else, whatever a log asks for.
func TestColoursAreTheTerminalsOwn(t *testing.T) {
	sgr := regexp.MustCompile("\x1b\\[([0-9;]*)m")
	allowed := func(n string) bool {
		switch n {
		case "", "0", "1", "2", "3", "4", "7", "22", "23", "24", "27", "39", "49":
			return true
		}
		var c int
		fmt.Sscan(n, &c)
		return c >= 30 && c <= 37 || c >= 40 && c <= 47 || c >= 90 && c <= 97 || c >= 100 && c <= 107
	}
	coloured := false
	for _, sc := range scenes() {
		m := sc.model(120, 40)
		if m.screen == screenZathras {
			m.update(logMsg{epoch: m.epoch, id: m.logID, lines: []wire.LogLine{
				{At: t0.Add(time.Hour), Stream: "stdout", Text: "\x1b[38;5;196mred of 256\x1b[0m \x1b[38;2;10;200;30mgreen by its parts\x1b[0m \x1b[48;5;21mon blue\x1b[0m \x1b[5;9;53mblinking\x1b[0m"},
			}})
		}
		for _, match := range sgr.FindAllStringSubmatch(m.view(), -1) {
			coloured = true
			for _, n := range strings.Split(match[1], ";") {
				if !allowed(n) {
					t.Fatalf("%s: the sequence %q is drawn, which is none of the terminal's own sixteen colours", sc.name, match[0])
				}
			}
		}
	}
	if !coloured {
		t.Error("nothing is drawn in colour")
	}
}

// bigFleet returns a fleet of one service with n zathrases, in the
// middle of a rollout.
func bigFleet(n int) wire.Fleet {
	big := wire.Service{Name: "big", Stable: 3, Rollout: &wire.RolloutInfo{ID: 9, Service: "big", Generation: 4, Parallel: 10, State: wire.RolloutActivating, Created: t0.Add(-time.Minute), Prepared: n, Activated: n / 2}}
	for i := range n {
		st := wire.State{Generation: 3 + uint64(i%2), Phase: wire.PhaseRunning, PID: 100 + i, RunningFor: time.Duration(i) * time.Minute, Prepared: []uint64{3, 4}, Sample: sampleOf(0.3, 200<<20, 512<<20)}
		big.Zathrases = append(big.Zathrases, wire.Zathras{ID: wire.ID(fmt.Sprintf("%04x", i) + strings.Repeat("0", 60)), Name: fmt.Sprintf("big-7d9f8b6c4-%05d", i), Connected: t0.Add(-time.Hour), State: st})
	}
	return wire.Fleet{Services: []wire.Service{big}}
}

// Review Focus 5: a fleet of some hundred zathrases. What is chosen is
// in sight, wherever in the list it is.
func TestThreeHundredZathrases(t *testing.T) {
	m, _ := newTestModel(80, 24)
	goLive(m, bigFleet(300))
	wantSaid(t, "the services", m, "1 service, 300 zathrases connected", "● 300 running", "activating, 150 of 300 activated")
	press(m, "enter")
	seen := map[string]bool{}
	for i := range 300 {
		name := fmt.Sprintf("big-7d…-%05d", i)
		rows := slices.DeleteFunc(screenOf(m), func(l string) bool { return !strings.HasPrefix(l, "▸ ") })
		if len(rows) != 1 || !strings.HasPrefix(rows[0], "▸ "+name) {
			t.Fatalf("with zathras %d chosen, the chosen rows in sight are %q", i, rows)
		}
		seen[rows[0]] = true
		press(m, "down")
	}
	if len(seen) != 300 {
		t.Errorf("%d zathrases were in sight as the chosen one, want 300", len(seen))
	}
	// The part of each stage is said in a line, however many are in it.
	wantSaid(t, "the stages", m, "prepared 150", "on it 150")
	if lines := screenOf(m); len(lines) != 24 {
		t.Errorf("%d lines are drawn", len(lines))
	}
}

// BenchmarkThreeHundredZathrases measures what a fleet of three hundred
// zathrases costs: a fleet that comes and is drawn, on the view of
// their service.
func BenchmarkThreeHundredZathrases(b *testing.B) {
	for _, size := range [][2]int{{80, 24}, {200, 60}} {
		b.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(b *testing.B) {
			m, _ := newTestModel(size[0], size[1])
			f := bigFleet(300)
			goLive(m, f)
			press(m, "enter", "pgdown")
			for b.Loop() {
				m.update(fleetMsg{epoch: 1, fleet: f})
				_ = m.view()
			}
		})
	}
}

// BenchmarkLogFlood measures what a batch of a flood costs on the view
// of a zathras: four hundred lines that come, and are drawn.
func BenchmarkLogFlood(b *testing.B) {
	m, _ := onZathras(160, 48, "web", 1)
	at := t0
	for b.Loop() {
		lines := fixtureLines(400)
		for i := range lines {
			at = at.Add(time.Millisecond)
			lines[i].At = at
		}
		m.update(logMsg{epoch: 1, id: m.logID, lines: lines})
		_ = m.view()
	}
}

// What draal or a zathras says in words is capped where it comes in: a
// megabyte in a field is not cleaned and wrapped with every message,
// and the cut is said.
func TestWordsAreCapped(t *testing.T) {
	huge := strings.Repeat("word ", 1<<18)
	f := fixtureFleet()
	web := &f.Services[2]
	web.Rollout.State, web.Rollout.Reason = wire.RolloutHalted, huge
	for i := range web.Zathrases {
		z := &web.Zathrases[i]
		z.Name, z.State.Binary = huge, huge
		z.State.Args = append(slices.Repeat([]string{"-v"}, 1000), huge)
		z.State.LastExit = &wire.Exit{Signal: huge, Error: huge, At: t0}
	}
	m, _ := newTestModel(200, 60)
	goLive(m, f)
	most := fieldCap + 40
	for _, svc := range m.fleet.Services {
		if svc.Rollout != nil && len(svc.Rollout.Reason) > most {
			t.Errorf("a reason of %d bytes is kept", len(svc.Rollout.Reason))
		}
		for _, z := range svc.Zathrases {
			st := z.State
			if len(z.Name) > most || len(st.Binary) > most || len(st.Args) > 65 || st.LastExit != nil && (len(st.LastExit.Error) > most || len(st.LastExit.Signal) > most) {
				t.Errorf("of %s… a name of %d bytes, a binary of %d and %d arguments are kept", z.Name[:10], len(z.Name), len(st.Binary), len(st.Args))
			}
		}
	}
	press(m, "down", "down", "enter")
	m.update(rolloutsMsg{epoch: 1, n: m.rolloutsN, service: "web", rollouts: []wire.RolloutInfo{{ID: 1, State: wire.RolloutHalted, Reason: huge}}})
	if len(m.rollouts[0].Reason) > most {
		t.Errorf("a reason of %d bytes is kept of a rollout of the list", len(m.rollouts[0].Reason))
	}
	wantSaid(t, "a reason of a megabyte", m, "… (1306624 bytes more)")
	press(m, "enter")
	if args := m.seen.State.Args; len(args) != 65 || args[64] != "… (937 arguments more)" {
		t.Errorf("of 1001 arguments %d are kept, the last %q", len(args), args[len(args)-1])
	}
	if header := screenOf(m)[0]; strings.Count(header, "bytes more") != 1 {
		t.Errorf("what was capped is capped again: %q", header)
	}
	m.update(dialFailedMsg{epoch: 1})
	m.update(lostMsg{epoch: 1, err: errors.New(huge)})
	if len(m.dialErr) > most {
		t.Errorf("an error of %d bytes is kept", len(m.dialErr))
	}
}

// Figures that are no figures of a container still have a scale that
// can be read, and what a zathras or draal does not say is not quoted.
// The generations a zathras says it has prepared are a list that
// neither wire nor draal bounds: the view names 64 of them and says how
// many more there are, and what it draws is made of those alone.
func TestPreparedGenerationsAreKeptToTheLimit(t *testing.T) {
	prepared := make([]uint64, 1<<20)
	for i := range prepared {
		prepared[i] = uint64(i + 1)
	}
	words := preparedWords(prepared)
	if n := strings.Count(words, ", ") + 1; n > 65 || !strings.HasSuffix(words, ", … (1048512 generations more)") {
		t.Errorf("of a million generations the words are %d bytes in %d parts, ending %q; want 64 named and the count of the rest", len(words), n, words[max(len(words)-40, 0):])
	}
	if len(words) > 64*21+40 {
		t.Errorf("of a million generations the words are %d bytes", len(words))
	}
	if words := preparedWords(prepared[:64]); !strings.HasSuffix(words, ", 63, 64") {
		t.Errorf("of 64 generations the words end %q; want all of them", words[max(len(words)-20, 0):])
	}

	m, _ := onZathras(400, 40, "web", 0)
	f := fixtureFleet()
	for i := range f.Services {
		for j := range f.Services[i].Zathrases {
			if f.Services[i].Zathrases[j].ID == m.zathras {
				f.Services[i].Zathrases[j].State.Prepared = prepared
			}
		}
	}
	m.update(fleetMsg{epoch: m.epoch, fleet: f})
	wantSaid(t, "a zathras with a million prepared generations", m, "prepared 1, 2, 3,", " 63, 64, … (1048512 generations more)")
}

func TestAbsurdFiguresAndMissingTimes(t *testing.T) {
	m, _ := onZathras(120, 40, "web", 2)
	absurd := wire.Sample{At: t0.Add(2 * time.Second), CPU: 1e300, Memory: 1<<64 - 1, DiskRead: 1e308, DiskWrite: math.MaxFloat64, NetRx: math.Inf(1), NetTx: math.NaN()}
	f := fixtureFleet()
	z := &f.Services[2].Zathrases[2]
	z.State.Sample = &absurd
	z.Connected = time.Time{}
	z.State.LastExit = &wire.Exit{Status: 3}
	m.update(fleetMsg{epoch: 1, fleet: f})
	screen := strings.Join(screenOf(m), "\n")
	for _, bad := range []string{"to -", "Inf", "NaN", " at -", "since -", "of generation -"} {
		if strings.Contains(screen, bad) {
			t.Errorf("the view says %q:\n%s", bad, screen)
		}
	}
	wantSaid(t, "absurd figures", m, "to 1048576.0 TiB/s", "to 100000000000000%", "last exit exit 3, after 0.0s", "connected draal does not say since when")

	// A rollout that is halted and has no time of its end: nothing is
	// said of how long it took.
	m, _ = onService(120, 40, "worker")
	f = fixtureFleet()
	f.Services[3].Rollout.Finished = time.Time{}
	m.update(fleetMsg{epoch: 1, fleet: f})
	list := fixtureRollouts("worker")
	list[0].Finished = time.Time{}
	m.update(rolloutsMsg{epoch: 1, n: m.rolloutsN, service: "worker", rollouts: list})
	screen = strings.Join(screenOf(m), "\n")
	if strings.Contains(screen, "took 0.0s") || strings.Contains(screen, "halted -") || strings.Contains(screen, "#30 ✖ halted to 4 26m ago under way") {
		t.Errorf("a halted rollout without a time of its end:\n%s", screen)
	}
	wantSaid(t, "a halted rollout without a time of its end", m, "✖ Rollout 30 to generation 4: halted", "why worker-1: exited with status 2 after 1.3s")
	press(m, "esc")
	wantSaid(t, "the same among the services", m, "rollout 30 to generation 4: halted: worker-1: exited")
}

// The width this package counts (runeWidth) is the renderer's for the
// scripts the fixture has. For some others it is not: a spacing mark of
// Devanagari or Myanmar and a Hangul initial with its vowel count
// another number of cells by graphemes, by characters, and here. A
// column may then stand a cell off; but no line is wider than the
// terminal by either of the renderer's counts, which is what a terminal
// would wrap.
func TestNoLineIsWiderByTheRenderersCounts(t *testing.T) {
	names := []string{"सेवा-कार्यकर्ता-किंवा", "မြန်မာ-ဝန်ဆောင်မှု", "\u1100\u1161\u1102\u1161-한국어", "กำลังทำงาน-ผู้ปฏิบัติงาน", "👨\u200d👩\u200d👧-🇩🇪-👋🏽"}
	f := fixtureFleet()
	for i := range f.Services {
		for j := range f.Services[i].Zathrases {
			z := &f.Services[i].Zathrases[j]
			name := names[(i+j)%len(names)]
			z.Name = strings.Repeat(name+".", 1+j*3)
			z.State.Args = []string{name, name}
		}
		if r := f.Services[i].Rollout; r != nil {
			r.Reason = strings.Repeat(names[i%len(names)]+" ", 20)
		}
	}
	var lines []wire.LogLine
	for i, name := range names {
		lines = append(lines, wire.LogLine{At: t0.Add(time.Duration(i) * time.Second), Stream: "stdout", Text: strings.Repeat(name+" ", 30)})
	}
	check := func(what string, m *model) {
		t.Helper()
		for w := leastWidth; w <= 140; w += 3 {
			m.update(tea.WindowSizeMsg{Width: w, Height: 30})
			for i, l := range strings.Split(m.view(), "\n") {
				if g, wc := ansi.StringWidth(l), ansi.StringWidthWc(l); g > w || wc > w {
					t.Fatalf("%s at %d columns: line %d is %d cells wide by graphemes and %d by characters: %q", what, w, i, g, wc, plain(l))
				}
			}
		}
	}
	m, _ := newTestModel(80, 30)
	goLive(m, f)
	check("the services", m)
	for range len(f.Services) {
		press(m, "enter")
		check("a service", m)
		svc, _ := m.serviceNamed(m.service)
		for range len(svc.Zathrases) {
			press(m, "enter")
			m.update(logMsg{epoch: 1, id: m.logID, lines: lines})
			check("a zathras", m)
			press(m, "esc", "down")
		}
		press(m, "esc", "down")
	}
}

// FuzzViews draws every view with whatever bytes in every field that
// draal and a zathras fill: what is drawn is text and SGR, no more
// lines than rows, and no line wider than the terminal as the renderer
// counts.
func FuzzViews(f *testing.F) {
	for _, tt := range hostile {
		if len(tt.in) < 4096 {
			f.Add(tt.in, tt.in)
		}
	}
	f.Add("注文ｗ🚀\u0301\u0e31", "a\tb\x1b[31mc")
	f.Fuzz(func(t *testing.T, evil, text string) {
		fl := fixtureFleet()
		for i := range fl.Services {
			svc := &fl.Services[i]
			svc.Name = evil + svc.Name
			if svc.Rollout != nil {
				svc.Rollout.Reason = evil
				svc.Rollout.State = wire.RolloutHalted
				svc.Rollout.CreatedBy = wire.ID(evil)
			}
			for j := range svc.Zathrases {
				z := &svc.Zathrases[j]
				z.Name = evil + z.Name + evil
				z.ID += wire.ID(evil)
				z.State.Binary = evil
				z.State.Args = []string{evil, text}
				z.State.Phase += wire.Phase(evil)
				z.State.LastExit = &wire.Exit{Signal: evil, Error: evil, At: t0}
			}
		}
		check := func(what string, m *model) {
			t.Helper()
			for _, size := range [][2]int{{80, 24}, {50, 12}, {137, 41}} {
				m.update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				drawn := m.view()
				if found := unsafe(drawn); found != "" {
					t.Fatalf("%s at %d×%d: %s", what, size[0], size[1], found)
				}
				lines := strings.Split(drawn, "\n")
				if len(lines) > size[1] {
					t.Fatalf("%s at %d×%d: %d lines", what, size[0], size[1], len(lines))
				}
				for i, l := range lines {
					if g, wc := ansi.StringWidth(l), ansi.StringWidthWc(l); g > size[0] || wc > size[0] {
						t.Fatalf("%s at %d×%d: line %d is %d cells wide by graphemes and %d by characters: %q", what, size[0], size[1], i, g, wc, l)
					}
				}
			}
		}
		m, _ := newTestModel(80, 24)
		goLive(m, fl)
		check("services", m)
		press(m, "down", "down", "enter")
		m.update(rolloutsMsg{epoch: m.epoch, n: m.rolloutsN, service: m.service, rollouts: []wire.RolloutInfo{{ID: 1, State: wire.RolloutState(evil), Reason: evil + text}}})
		check("service", m)
		press(m, "enter")
		m.update(logMsg{epoch: m.epoch, id: m.logID, lines: []wire.LogLine{{At: t0, Stream: evil, Text: text}, {At: t0, Stream: "stdout", Text: evil + text + evil}}})
		check("zathras", m)
		m.update(logEndedMsg{epoch: m.epoch, id: m.logID, err: wire.Errorf(wire.CodeInternal, "%s", evil)})
		m.update(lostMsg{epoch: m.epoch, err: wire.Errorf(wire.CodeInternal, "%s", text)})
		check("lost", m)
	})
}
