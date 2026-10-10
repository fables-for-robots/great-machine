### Task 6: The views

What is drawn, as functions from the model to text, with Lip Gloss: the header and the line of keys, the services, a service, a zathras, the keys, and every odd state as a sentence. A view is a list of lines; every line is made by `line` (or `chosen`), which cleans and cuts each piece of it, so that nothing gets into a line as it came and no line is wider than the terminal.

**Review Focus pinned here:** 2, by `TestNothingOfAZathrasReachesTheTerminalAsItCame` (a fleet whose every string is escape sequences, on every view); 3, by `TestLostConnectionIsSaid`; 4, by `TestOddStatesAreSentences` and `TestAbsurdFiguresAndMissingTimes` (no service yet, with what to do; a service without a zathras; one without a rollout, with the command that makes one; a halted one with its reason in full; a zathras without samples; one that is gone; a name of 253 characters; two zathrases of one name); 5, by `TestThreeHundredZathrases`, `TestWordsAreCapped`, `TestNoLineIsWiderByTheRenderersCounts`, and by `TestEverySizeOfTerminal` of Task 5, which from this task on draws these views. `FuzzViews` draws every view with whatever bytes in every field.

**Files:**
- Create: `tui/styles.go`, `tui/view_services.go`, `tui/view_service.go`, `tui/view_zathras.go`
- Modify: `tui/view.go` (replaced whole)
- Test: `tui/view_test.go`

**Interfaces:**
- Consumes: the model of Task 5 (its fields, and `shared`, which this task defines anew); Task 4 (`clean`, `named`, `cut`, `middle`, `pad`, `padLeft`, `wrap`, `width`, `cutClean`, `drawSpans`, `spark`, `largest`, `byteTop`, `coreTop`, `sparkWidest`, `stageOf`, `stages`, `phases`, `knownPhase`, `byPhase`, `phaseNote`, `rolloutOpen`, the entries of a log); `human`; Lip Gloss v2 (`lipgloss.NewStyle`, `Style.Render`, the colours `lipgloss.Red`, `Green`, `Yellow`, `Cyan`); the scenes and the fixture of Task 5's tests.
- Produces: `func (m *model) view() string`, `tooSmall`, `bodyHeight`, `servicesHeight`, `zathrasesHeight`, `logHeight`, `type shared struct` (how the rows of a view are shared out, kept from one message to the next); `type seg struct{ text string; st lipgloss.Style }`, `func line(w int, segs ...seg) string`, `func chosen(w int, segs ...seg) string`, `plainText`, `dim`, `bold`, `sentences`; `whenWords`, `sinceWords`. In the tests: `screenOf(m) []string`, `wantScreen`, `wantSaid`, `sceneNamed`, `bigFleet`.

**What is drawn:**
- **The frame.** The first line, in reverse: where one is (`great-machine › web › web-7d9f8b6c4-2xkqp`), draal as `Config.Draal` names it (from 70 columns on), and the connection: `◌ connecting`, `● live`, or `✖ lost for 1m30s`. While there is no connection and a fleet is shown, a banner under it says since when, that what is shown is what draal sent last and how long ago, and when draal is asked again. The last line is the keys that work here; in a narrow terminal the least needed go first, and `q quit` never.
- **Colour.** Four of the terminal's own sixteen colours (green, yellow, red, cyan: SGR 32, 33, 31, 36), and bold, faint, underline and reverse. No colour by number or by parts, so the terminal UI is light or dark as the terminal is. **Every state has a sign and a word besides its colour**: `● running`, `◐ stopping`, `✖ backoff`, `○ idle`; `▶` under way, `✔ completed`, `✖ halted`, `– superseded`; the streams of a log are `o`, `e`, `z`. With `NO_COLOR` the renderer of Bubble Tea leaves the colours out (Task 7 tests that), and the signs, the words, bold and reverse stay.
- **The services**: two lines each. **A service**: its line; the rollout that is under way or halted, with draal's counts and where every zathras stands by its own report (a halted one with its reason in full); the table of its zathrases; its last rollouts. The table scrolls with what is chosen kept in sight, and has an ID column and longer cells from 110 columns on. In a narrower terminal a name is cut in its middle, and **two zathrases of one name are told apart by the start of their IDs** (`web-…jwz·21ab`).
- **A zathras**: its state in full (its name whole, also one of 253 characters), six sparklines with the value now and the scale said beside each, and its log. The log has its title and three rows at least; where the terminal has no room, the sparklines give way from the last. **The title of the log says what is known of it**: `no line has come yet` from when it is asked for until its first line (draal answers a log with its lines alone: a log that is followed and empty cannot be told from one draal has not got to), then `followed live`, or `ended`.
- **What is not known is not quoted**: a last exit without a time has no "at", a zathras without a time of connecting says `draal does not say since when`, a halted rollout without a time of its end says nothing of how long it took. And a scale is a number whatever the figure: a rate of 1e308 has the top `1048576.0 TiB/s`.
- **No line is wider than the terminal as the renderer counts**, by graphemes and by characters: `view` cuts a line that is, which only a name in a script this package counts otherwise can make (see Task 4).
- **In a terminal smaller than 50×12**: one line, `Too small: great-machine needs a terminal of 50×12 or more.`

- [ ] **Step 1: Write the failing tests**

Create `tui/view_test.go`:

```go
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
		{"zathras-notice", []string{"The shell is not part of this terminal UI yet"}},
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
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `nix develop .#bare --command go test ./tui/`
Expected: FAIL: the view of Task 5 draws a header and nothing else. The first of many lines:

```
--- FAIL: TestServicesViewAt80x24 (0.00s)
    view_test.go:77: the services, line 1:
         got " great-machine"
        want " great-machine                                          draal 3f9a2c1b · ● live"
    view_test.go:77: the services, line 2:
         got ""
        want " 4 services, 12 zathrases connected"
    view_test.go:77: the services, line 4:
         got ""
        want "▸ empty   stable generation 2   no zathras connected"
    view_test.go:77: the services, line 5:
         got ""
…
FAIL	github.com/fables-for-robots/great-machine/tui	2.509s
FAIL
```

- [ ] **Step 3: Write the views**

Create `tui/styles.go`: the looks, and the one way a line is made.

```go
package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/fables-for-robots/great-machine/wire"
)

// The looks of the terminal UI. Its colours are four of the terminal's
// own sixteen (30 to 37, which a light terminal has dark and a dark one
// light), and bold, faint and reverse, which every terminal has: it is
// light or dark as the terminal is, and with NO_COLOR the renderer
// leaves the colours out and the rest stays. Nothing is said by colour
// alone: what is coloured has a sign and a word too.
var (
	stPlain = lipgloss.NewStyle()
	stBold  = lipgloss.NewStyle().Bold(true)
	stDim   = lipgloss.NewStyle().Faint(true)
	stBar   = lipgloss.NewStyle().Reverse(true)
	stTitle = lipgloss.NewStyle().Reverse(true).Bold(true)
	stOK    = lipgloss.NewStyle().Foreground(lipgloss.Green)
	stWarn  = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	stBad   = lipgloss.NewStyle().Foreground(lipgloss.Red).Bold(true)
	stBusy  = lipgloss.NewStyle().Foreground(lipgloss.Cyan)
	stHead  = lipgloss.NewStyle().Faint(true).Underline(true)
)

// seg is a piece of a line: text, which may be anything, and its look.
type seg struct {
	text string
	st   lipgloss.Style
}

// line draws the pieces side by side in at most w cells. Every piece is
// cleaned and cut on the way: nothing gets into a line but through
// here, and no line is wider than it may be.
func line(w int, segs ...seg) string {
	var b strings.Builder
	for _, s := range segs {
		if w <= 0 {
			break
		}
		text := cut(s.text, w)
		if text == "" {
			continue
		}
		w -= width(text)
		b.WriteString(s.st.Render(text))
	}
	return b.String()
}

// chosen draws the pieces as the row that is chosen: in reverse, across
// all of w cells, whatever the looks of the pieces.
func chosen(w int, segs ...seg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(clean(s.text))
	}
	return stBar.Render(pad(b.String(), w))
}

// plainText is a piece without a look.
func plainText(text string) seg { return seg{text, stPlain} }

// dim is a piece that matters less.
func dim(text string) seg { return seg{text, stDim} }

// bold is a piece that matters more.
func bold(text string) seg { return seg{text, stBold} }

// phaseSign returns the sign and the look of a phase.
func phaseSign(p wire.Phase) (string, lipgloss.Style) {
	switch knownPhase(p) {
	case wire.PhaseRunning:
		return "●", stOK
	case wire.PhaseStopping:
		return "◐", stWarn
	case wire.PhaseBackoff:
		return "✖", stBad
	}
	return "○", stDim
}

// rolloutSign returns the sign and the look of a state of a rollout.
func rolloutSign(s wire.RolloutState) (string, lipgloss.Style) {
	switch s {
	case wire.RolloutPreparing, wire.RolloutActivating:
		return "▶", stBusy
	case wire.RolloutCompleted:
		return "✔", stOK
	case wire.RolloutHalted:
		return "✖", stBad
	}
	return "–", stDim
}
```

**Replace** `tui/view.go` with:

```go
package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fables-for-robots/great-machine/human"
)

// What is drawn: the model as text. A view is a list of lines; view
// puts the header above the one that is open and the keys below it.

const (
	// leastWidth and leastHeight are the smallest terminal the terminal
	// UI draws in. In a smaller one it says so, in one line.
	leastWidth  = 50
	leastHeight = 12
)

// shared is how the rows of a view are shared out among its parts, kept
// from when it was worked out to the next message: the model asks for
// it when it keeps what is chosen in sight, and the view draws by it.
type shared struct {
	service  *serviceParts
	serviceH int // the rows service was worked out for
	zathras  *zathrasParts
	zathrasH int // the rows zathras was worked out for
}

// tooSmall says whether the terminal is smaller than the terminal UI
// can draw in.
func (m *model) tooSmall() bool {
	return m.width < leastWidth || m.height < leastHeight
}

// view draws the model: as many lines as the terminal has rows at most,
// none wider than the terminal.
func (m *model) view() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	if m.tooSmall() {
		return line(m.width, plainText(fmt.Sprintf("Too small: great-machine needs a terminal of %d×%d or more.", leastWidth, leastHeight)))
	}
	lines := []string{m.header()}
	lines = append(lines, m.banner()...)
	body := m.body(m.bodyHeight())
	if len(body) > m.bodyHeight() {
		body = body[:m.bodyHeight()]
	}
	lines = append(lines, body...)
	for len(lines) < m.height-1-len(m.noticeLines()) {
		lines = append(lines, "")
	}
	lines = append(lines, m.noticeLines()...)
	lines = append(lines, m.keys())
	for i, l := range lines {
		// Every line is made by line and its like, which keep to the
		// width as this package counts it (runeWidth). The renderer
		// counts by graphemes or by characters, as the terminal has it,
		// and for some scripts either count is another than ours. No
		// line is wider than the terminal by any of the three: one that
		// is, by the renderer's counts, is cut here.
		for to := m.width; to >= 0; to-- {
			g, wc := ansi.StringWidth(l), ansi.StringWidthWc(l)
			if g <= m.width && wc <= m.width {
				break
			}
			// Cut by graphemes, to as many cells fewer as the larger
			// count is over.
			to = min(to, g-(max(g, wc)-m.width))
			l = ansi.Truncate(l, max(to, 0), "")
		}
		lines[i] = l
	}
	return strings.Join(lines[:min(len(lines), m.height)], "\n")
}

// bodyHeight returns how many rows the view that is open has: the
// terminal without the header, the keys, and what is said between.
func (m *model) bodyHeight() int {
	return max(m.height-2-len(m.banner())-len(m.noticeLines()), 0)
}

// body draws the view that is open in at most h lines.
func (m *model) body(h int) []string {
	switch {
	case m.help:
		return m.helpView(h)
	case !m.have:
		return m.connecting()
	}
	switch m.screen {
	case screenService:
		return m.serviceView(h)
	case screenZathras:
		return m.zathrasView(h)
	}
	return m.servicesView(h)
}

// header draws the first line: where one is, draal, and the state of
// the connection.
func (m *model) header() string {
	conn := m.connWords()
	if m.cfg.Draal != "" && m.width >= 70 {
		conn = "draal " + cut(m.cfg.Draal, 12) + " · " + conn
	}
	conn = " " + cut(conn, m.width/2) + " "
	left := m.width - width(conn)
	return stTitle.Render(pad(" "+m.where(left-2), left)) + stBar.Render(conn)
}

// where says where one is, in at most w cells: the program, the service
// and the zathras. Where there is no room for all of it, the names give
// way, the one of the zathras first, and never the start of the line.
func (m *model) where(w int) string {
	const root, sep = "great-machine", " › "
	service, zathras := named(m.service), named(m.seen.Name)
	switch m.screen {
	case screenServices:
		return cut(root, w)
	case screenService:
		return cut(root+sep+middle(service, max(w-width(root+sep), 8)), w)
	}
	rest := w - width(root+sep+sep)
	service = middle(service, max(rest-8, 8))
	return cut(root+sep+service+sep+middle(zathras, max(rest-width(service), 8)), w)
}

// connWords says the state of the connection, in a sign and words.
func (m *model) connWords() string {
	now := m.cfg.Now()
	switch m.conn {
	case connLive:
		return "● live"
	case connLost:
		return "✖ lost for " + human.Age(now.Sub(m.since))
	}
	return "◌ connecting"
}

// retryWords says when draal is asked again.
func (m *model) retryWords() string {
	if m.retryAt.IsZero() {
		return "Asking draal now."
	}
	until := human.Until(m.retryAt, m.cfg.Now())
	if until == "now" {
		return "Asking draal again now."
	}
	return "Asking draal again " + until + "; any key asks now."
}

// banner is what stands under the header while there is no connection
// and a fleet is shown all the same: that it is old, since when, and
// when draal is asked again.
func (m *model) banner() []string {
	if m.conn == connLive || !m.have || m.tooSmall() {
		return nil
	}
	text := "✖ The connection to draal is lost since " + human.Clock(m.since) + ". What is shown is what draal sent last, " +
		human.Ago(m.fleetAt, m.cfg.Now()) + ". " + m.retryWords()
	if m.dialErr != "" {
		text += " (" + m.dialErr + ")"
	}
	var out []string
	for _, l := range wrap(text, m.width-2, 3) {
		out = append(out, line(m.width, seg{" " + l, stBad}))
	}
	return out
}

// noticeLines is the one line that is said once, when there is one.
func (m *model) noticeLines() []string {
	if m.notice == "" || m.tooSmall() {
		return nil
	}
	return []string{line(m.width, seg{" " + m.notice, stWarn})}
}

// keys draws the last line: the keys that work here. In a terminal
// that has no room for all of them, the ones that are said first stay:
// how to quit, the key that says the rest, and the way back.
func (m *model) keys() string {
	type key struct {
		key, does string
		rank      int // the lower, the longer it stays
	}
	var keys []key
	switch {
	case m.help:
		keys = []key{{"any key", "back", 1}, {"q", "quit", 0}}
	case m.screen == screenServices:
		keys = []key{{"↑↓", "move", 3}, {"enter", "open", 2}, {"?", "keys", 1}, {"q", "quit", 0}}
	case m.screen == screenService:
		keys = []key{{"↑↓", "move", 4}, {"enter", "open", 3}, {"esc", "back", 2}, {"?", "keys", 1}, {"q", "quit", 0}}
	default:
		keys = []key{{"↑↓", "scroll the log", 5}, {"G", "follow it", 4}, {"s", "shell", 3}, {"esc", "back", 2}, {"?", "keys", 1}, {"q", "quit", 0}}
	}
	room, most := m.width-1, -1
	for rank := 0; rank < len(keys); rank++ {
		for _, k := range keys {
			if k.rank == rank {
				room -= width(k.key) + 1 + width(k.does) + 2
			}
		}
		if room+2 < 0 {
			break
		}
		most = rank
	}
	segs := []seg{plainText(" ")}
	for _, k := range keys {
		if k.rank > most {
			continue
		}
		if len(segs) > 1 {
			segs = append(segs, dim("  "))
		}
		segs = append(segs, bold(k.key), dim(" "+k.does))
	}
	return line(m.width, segs...)
}

// connecting is the view before the first fleet: whom the terminal UI
// asks, and why there is nothing yet.
func (m *model) connecting() []string {
	draal := "draal"
	if m.cfg.Draal != "" {
		draal += " " + m.cfg.Draal
	}
	out := []string{"", line(m.width, plainText(" Connecting to "+draal+" …"))}
	if m.dialErr == "" {
		return out
	}
	out = append(out, "")
	for _, l := range wrap("draal does not answer: "+m.dialErr, m.width-2, 4) {
		out = append(out, line(m.width, seg{" " + l, stBad}))
	}
	return append(out, line(m.width, dim(" "+m.retryWords())))
}

// helpView is the keys, and what the signs mean.
func (m *model) helpView(h int) []string {
	rows := [][2]string{
		{"↑ ↓  k j", "move in a list; scroll a log"},
		{"Enter  →  l", "open what is chosen"},
		{"Esc  ←  h  Backspace", "back"},
		{"PgUp PgDn", "a page up, a page down"},
		{"g  G", "the start, the end; in a log G also follows it again"},
		{"s", "on a zathras: open a shell"},
		{"?", "these keys"},
		{"q  Ctrl-C", "quit"},
	}
	out := []string{"", line(m.width, bold(" Keys")), ""}
	for _, r := range rows {
		out = append(out, line(m.width, plainText("   "), bold(pad(r[0], 22)), plainText(r[1])))
	}
	if h < len(out)+7 {
		return out
	}
	sign := func(s string, st lipgloss.Style, words string) []seg {
		return []seg{{s, st}, plainText(" " + words + "   ")}
	}
	phases := []seg{plainText("   ")}
	phases = append(phases, sign("●", stOK, "running")...)
	phases = append(phases, sign("◐", stWarn, "stopping")...)
	phases = append(phases, sign("✖", stBad, "backoff")...)
	phases = append(phases, sign("○", stDim, "idle")...)
	rollouts := []seg{plainText("   ")}
	rollouts = append(rollouts, sign("▶", stBusy, "under way")...)
	rollouts = append(rollouts, sign("✔", stOK, "completed")...)
	rollouts = append(rollouts, sign("✖", stBad, "halted")...)
	rollouts = append(rollouts, sign("–", stDim, "superseded")...)
	streams := []seg{plainText("   ")}
	streams = append(streams, sign("o", stDim, "standard output")...)
	streams = append(streams, sign("e", stBad, "standard error")...)
	streams = append(streams, sign("z", stBusy, "zathras itself")...)
	return append(out, "",
		line(m.width, bold(" Signs")), "",
		line(m.width, phases...),
		line(m.width, rollouts...),
		line(m.width, streams...),
	)
}

// sentences draws text as lines of words, with a margin, in the look
// given: what a view says when it has nothing to show.
func sentences(w int, st lipgloss.Style, text string) []string {
	var out []string
	for _, l := range wrap(text, w-2, 0) {
		out = append(out, line(w, seg{" " + l, st}))
	}
	return out
}
```

Create `tui/view_services.go`:

```go
package tui

import (
	"fmt"
	"time"

	"github.com/fables-for-robots/great-machine/human"
	"github.com/fables-for-robots/great-machine/wire"
)

// The view of the services: every service with its stable generation,
// its zathrases by phase, and its last rollout and how far it is. A
// service is two lines and an empty one.

// serviceRows is how many rows one service takes in the list.
const serviceRows = 3

// servicesHeight returns how many services of the list are in sight.
func (m *model) servicesHeight() int {
	return max((m.bodyHeight()-2+1)/serviceRows, 1)
}

// servicesView draws the services in at most h lines.
func (m *model) servicesView(h int) []string {
	services := m.fleet.Services
	if len(services) == 0 {
		// Review Focus 4: no service yet, and what to do.
		out := []string{"", line(m.width, bold(" draal knows no service yet."))}
		out = append(out, sentences(m.width, stPlain, "A service is there as soon as a zathras of it is connected or something was rolled out to it. Start a zathras and name its service:")...)
		out = append(out, "", line(m.width, plainText("   great-machine zathras --draal ID --service NAME")), "")
		return append(out, sentences(m.width, stDim, "ID is the endpoint ID that draal logged when it started. The zathras is shown here as soon as it has registered.")...)
	}
	zathrases := 0
	nameWidth := 0
	for _, svc := range services {
		zathrases += len(svc.Zathrases)
		nameWidth = max(nameWidth, width(named(svc.Name)))
	}
	nameWidth = min(nameWidth, 30, m.width/3)
	summary := human.Count(len(services), "service", "services") + ", " + human.Count(zathrases, "zathras", "zathrases") + " connected"
	shown := m.servicesHeight()
	if len(services) > shown {
		summary += fmt.Sprintf(" · %d to %d are in sight", m.svcTop+1, min(m.svcTop+shown, len(services)))
	}
	out := []string{line(m.width, dim(" "+summary)), ""}
	now := m.cfg.Now()
	for i := m.svcTop; i < min(m.svcTop+shown, len(services)); i++ {
		svc := services[i]
		stable := "no stable generation"
		if svc.Stable > 0 {
			stable = "stable generation " + human.Generation(svc.Stable)
		}
		first := []seg{plainText("  "), bold(pad(middle(named(svc.Name), nameWidth), nameWidth)), dim("  " + pad(stable, 22))}
		first = append(first, phaseStrip(svc.Zathrases)...)
		if i == m.svcIdx {
			first[0] = plainText("▸ ")
			out = append(out, chosen(m.width, first...))
		} else {
			out = append(out, line(m.width, first...))
		}
		second := append([]seg{plainText(pad("", nameWidth+4))}, rolloutWords(svc, now)...)
		out = append(out, line(m.width, second...), "")
	}
	return out
}

// phaseStrip says how many zathrases are in each phase.
func phaseStrip(zs []wire.Zathras) []seg {
	if len(zs) == 0 {
		return []seg{dim("no zathras connected")}
	}
	count := byPhase(zs)
	var out []seg
	for _, p := range phases {
		if count[p] == 0 {
			continue
		}
		sign, st := phaseSign(p)
		out = append(out, seg{sign, st}, plainText(fmt.Sprintf(" %d %s  ", count[p], p)))
	}
	return out
}

// rolloutWords says the last rollout of a service in a line: what it
// is, how far, and since when. The counts are draal's, of the zathrases
// that are connected.
func rolloutWords(svc wire.Service, now time.Time) []seg {
	r := svc.Rollout
	if r == nil {
		return []seg{dim("nothing was rolled out yet")}
	}
	sign, st := rolloutSign(r.State)
	what := fmt.Sprintf(" rollout %d to generation %s: ", r.ID, human.Generation(r.Generation))
	// When it ended, where draal says so.
	ended := ""
	if !r.Finished.IsZero() {
		ended = " " + human.Ago(r.Finished, now)
	}
	switch r.State {
	case wire.RolloutPreparing:
		return []seg{{sign, st}, plainText(what), {"preparing", st},
			plainText(fmt.Sprintf(", %d of %d prepared, started %s", r.Prepared, len(svc.Zathrases), human.Ago(r.Created, now)))}
	case wire.RolloutActivating:
		return []seg{{sign, st}, plainText(what), {"activating", st},
			plainText(fmt.Sprintf(", %d of %d activated, started %s", r.Activated, len(svc.Zathrases), human.Ago(r.Created, now)))}
	case wire.RolloutHalted:
		return []seg{{sign, st}, plainText(what), {"halted", st}, plainText(ended + ": " + r.Reason)}
	}
	return []seg{{sign, st}, plainText(what), {string(r.State), st}, plainText(ended)}
}
```

Create `tui/view_service.go`:

```go
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/fables-for-robots/great-machine/human"
	"github.com/fables-for-robots/great-machine/wire"
)

// The view of a service: what it is in a line, the rollout under way or
// halted with where every zathras stands in it, its zathrases as a
// table, and its last rollouts.

// serviceParts is how the rows of the view of a service are shared out.
type serviceParts struct {
	top   []string // the line of the service and what is said of its rollout
	table int      // how many zathrases of the table are in sight
	last  int      // how many rows the last rollouts have, with their title
}

// zathrasesHeight returns how many zathrases of a service's table are
// in sight.
func (m *model) zathrasesHeight() int {
	svc, ok := m.serviceNamed(m.service)
	if !ok {
		return 1
	}
	return max(m.shareService(svc, m.bodyHeight()).table, 1)
}

// shareService shares h rows out among the parts of the view: the top
// as it is, as long as it leaves room; then the table, as many rows as
// it has zathrases, but for five that are left to the last rollouts
// while the table has three; and the last rollouts get the rest.
func (m *model) shareService(svc wire.Service, h int) serviceParts {
	if m.shared.service != nil && m.shared.serviceH == h {
		return *m.shared.service
	}
	p := serviceParts{top: m.serviceTop(svc)}
	rows := max(len(svc.Zathrases), 1)
	if spare := h - 4 - 3; len(p.top) > spare { // the table's head and three rows, a line between
		p.top = p.top[:max(spare, 1)]
	}
	room := h - len(p.top) - 2 // an empty line, and the head of the table
	p.table = min(rows, max(room-5, 3), max(room, 1))
	if rest := room - p.table - 1; rest >= 2 {
		p.last = rest
	}
	m.shared.service, m.shared.serviceH = &p, h
	return p
}

// serviceView draws a service in at most h lines.
func (m *model) serviceView(h int) []string {
	svc, ok := m.serviceNamed(m.service)
	if !ok {
		// Review Focus 4: the service went while its view was open.
		out := []string{"", line(m.width, bold(" draal knows no service of this name any more: "), plainText(named(m.service)))}
		out = append(out, sentences(m.width, stPlain, "A service is there as long as a zathras of it is connected or something was rolled out to it. The view is drawn again when it is back; Esc goes to the services.")...)
		return out
	}
	p := m.shareService(svc, h)
	out := append([]string{}, p.top...)
	out = append(out, "")
	out = append(out, m.zathrasTable(svc, p.table)...)
	if p.last > 0 {
		out = append(out, "")
		out = append(out, m.lastRollouts(p.last)...)
	}
	return out
}

// serviceTop draws the line of the service, and under it the rollout
// that is under way or halted, or that there was none.
func (m *model) serviceTop(svc wire.Service) []string {
	now := m.cfg.Now()
	stable := "no stable generation"
	if svc.Stable > 0 {
		stable = "stable generation " + human.Generation(svc.Stable)
	}
	first := []seg{plainText(" "), bold(middle(named(svc.Name), m.width/3)), dim(" · " + stable + " · ")}
	first = append(first, phaseStrip(svc.Zathrases)...)
	out := []string{line(m.width, first...)}

	r := svc.Rollout
	if r == nil {
		// Review Focus 4: a service without a rollout, and the command
		// that makes one.
		out = append(out, "", line(m.width, bold(" Nothing was rolled out to this service yet.")))
		out = append(out, sentences(m.width, stPlain, "Its zathrases are connected and run nothing. A rollout gives them a generation: on the machine that has the binary in its Nix store, as an operator of this draal,")...)
		return append(out, line(m.width, plainText("   great-machine rollout "+named(svc.Name)+" /nix/store/…/bin/…")))
	}
	if !rolloutOpen(r) {
		return out
	}
	sign, st := rolloutSign(r.State)
	parallel := "all of them at a time"
	if r.Parallel > 0 {
		parallel = fmt.Sprintf("%d at a time", r.Parallel)
	}
	head := []seg{plainText(" "), {sign, st}, bold(fmt.Sprintf(" Rollout %d to generation %s", r.ID, human.Generation(r.Generation))), plainText(": "), {string(r.State), st}}
	if r.State == wire.RolloutHalted && !r.Finished.IsZero() {
		head = append(head, plainText(" "+human.Ago(r.Finished, now)+", after "+human.Took(r.Finished.Sub(r.Created))))
	}
	out = append(out, "", line(m.width, head...),
		line(m.width, dim("   "+parallel+", started "+whenWords(r.Created, now)+" by the operator "+human.ShortID(r.CreatedBy))))
	if r.State == wire.RolloutHalted {
		// The reason, in full.
		for i, l := range wrap(r.Reason, m.width-9, 0) {
			label := "   why  "
			if i > 0 {
				label = "        "
			}
			out = append(out, line(m.width, dim(label), seg{l, stBad}))
		}
	}
	out = append(out, line(m.width, dim("   draal counts "),
		plainText(fmt.Sprintf("%d prepared, %d activated, %d awaited", r.Prepared, r.Activated, r.Awaited)),
		dim(" of "+human.Count(len(svc.Zathrases), "zathras", "zathrases")+" connected")))
	if len(svc.Zathrases) == 0 {
		return append(out, line(m.width, dim("   No zathras of the service is connected, so none has a part in this rollout now.")))
	}
	// Where every zathras stands, by its own report.
	names := map[stage][]string{}
	for _, z := range svc.Zathrases {
		s := stageOf(z.State, r.Generation)
		names[s] = append(names[s], m.nameIn(svc, z, 24))
	}
	for _, s := range stages {
		if len(names[s]) == 0 {
			continue
		}
		out = append(out, line(m.width, dim("   "+pad(s.String(), 13)), bold(pad(fmt.Sprint(len(names[s])), 4)), plainText(strings.Join(names[s], ", "))))
	}
	return out
}

// nameIn returns the name of a zathras in at most w cells, as it is
// told apart among the zathrases of its service: one whose name another
// one has too is named with the start of its ID.
func (m *model) nameIn(svc wire.Service, z wire.Zathras, w int) string {
	twice := false
	for _, other := range svc.Zathrases {
		twice = twice || other.Name == z.Name && other.ID != z.ID
	}
	if !twice || w < 12 {
		return middle(named(z.Name), w)
	}
	id, _ := cutClean(clean(capped(string(z.ID))), 4)
	return middle(named(z.Name), w-5) + "·" + id
}

// zathrasTable draws the table of a service's zathrases: its head and
// rows of them, from the first in sight.
func (m *model) zathrasTable(svc wire.Service, rows int) []string {
	if len(svc.Zathrases) == 0 {
		// Review Focus 4: a service without a zathras.
		text := "No zathras of this service is connected. draal keeps the service because something was rolled out to it; a zathras that is started with --service " + named(svc.Name) + " gets its stable generation"
		if svc.Stable > 0 {
			text += ", " + human.Generation(svc.Stable)
		}
		return sentences(m.width, stPlain, text+".")
	}
	now := m.cfg.Now()
	wide := m.width >= 110
	// The columns: what is left of the width is the name's.
	const phaseW, genW, restartsW, cpuW = 16, 10, 8, 5
	exitW, memW, idW := 10, 10, 0
	if wide {
		exitW, memW, idW = 26, 24, 8
	}
	fixed := 2 + 1 + phaseW + 1 + genW + 1 + restartsW + 1 + exitW + 1 + cpuW + 1 + memW
	if idW > 0 {
		fixed += idW + 1
	}
	nameW := max(m.width-fixed, 8)

	head := []seg{{"  " + pad("NAME", nameW), stDim}}
	if idW > 0 {
		head = append(head, dim(" "+pad("ID", idW)))
	}
	head = append(head, dim(" "+pad("PHASE", phaseW)+" "+pad("GENERATION", genW)+" "+padLeft("RESTARTS", restartsW)+" "+pad("LAST EXIT", exitW)+" "+padLeft("CPU", cpuW)+" "+padLeft("MEMORY", memW)))
	out := []string{line(m.width, head...)}

	for i := m.zTop; i < min(m.zTop+rows, len(svc.Zathrases)); i++ {
		z := svc.Zathrases[i]
		st := z.State
		sign, signSt := phaseSign(st.Phase)
		phase := string(knownPhase(st.Phase))
		switch knownPhase(st.Phase) {
		case wire.PhaseRunning:
			if st.RunningFor > 0 {
				phase += " " + human.Age(st.RunningFor)
			}
		case wire.PhaseBackoff:
			if until := human.Until(st.RestartAt, now); until != human.None {
				phase += " " + strings.TrimPrefix(until, "in ")
			}
		}
		gen := human.Generation(st.Generation)
		if st.Generation != 0 && st.Generation == svc.Stable {
			gen += " stable"
		}
		exit := human.Exit(st.LastExit)
		if wide && st.LastExit != nil {
			exit += ", " + human.Ago(st.LastExit.At, now)
		} else if st.LastExit != nil && st.LastExit.Signal != "" {
			exit = st.LastExit.Signal
		}
		cpu, memory := "no", "figures"
		if s := st.Sample; s != nil {
			cpu, memory = human.CPU(s.CPU), human.Bytes(s.Memory)
			if wide && s.MemoryLimit > 0 {
				memory += " of " + human.Bytes(s.MemoryLimit)
			}
		}
		row := []seg{plainText("  " + pad(m.nameIn(svc, z, nameW), nameW))}
		if idW > 0 {
			row = append(row, dim(" "+pad(human.ShortID(z.ID), idW)))
		}
		row = append(row,
			plainText(" "), seg{sign, signSt}, plainText(" "+pad(phase, phaseW-2)),
			plainText(" "+pad(gen, genW)),
			plainText(" "+padLeft(fmt.Sprint(st.Restarts), restartsW)),
			plainText(" "+pad(exit, exitW)),
			plainText(" "+padLeft(cpu, cpuW)),
			plainText(" "+padLeft(memory, memW)),
		)
		if i == m.zIdx {
			row[0].text = "▸ " + row[0].text[2:]
			out = append(out, chosen(m.width, row...))
		} else {
			out = append(out, line(m.width, row...))
		}
	}
	return out
}

// lastRollouts draws the last rollouts of the service in h lines, of
// which the first is their title.
func (m *model) lastRollouts(h int) []string {
	title := []seg{bold(" Last rollouts")}
	var body []string
	switch {
	case m.rolloutsAre == askedNot:
		body = sentences(m.width, stDim, "Not asked for: there is no connection to draal.")
	case m.rolloutsAre == askedFor:
		body = sentences(m.width, stDim, "Asking draal …")
	case m.rolloutsAre == askedRefused:
		body = sentences(m.width, stBad, "draal does not list them: "+m.rolloutsErr)
	case len(m.rollouts) == 0:
		body = sentences(m.width, stDim, "draal has no rollout of this service on record.")
	default:
		now := m.cfg.Now()
		shown := min(len(m.rollouts), h-1)
		if shown < len(m.rollouts) {
			title = append(title, dim(fmt.Sprintf(" · the newest %d of %d", shown, len(m.rollouts))))
		}
		for _, r := range m.rollouts[:shown] {
			sign, st := rolloutSign(r.State)
			took := ""
			switch {
			case !r.Finished.IsZero():
				took = "took " + human.Took(r.Finished.Sub(r.Created))
			case r.State.UnderWay():
				took = "under way"
			}
			body = append(body, line(m.width,
				plainText("   "+padLeft(fmt.Sprintf("#%d", r.ID), 5)+" "), seg{sign, st}, seg{" " + pad(string(r.State), 10), st},
				plainText(" to "+pad(human.Generation(r.Generation), 5)),
				dim(" "+pad(human.Ago(r.Created, now), 8)+" "+pad(took, 12)+" "), seg{r.Reason, stPlain}))
		}
	}
	out := append([]string{line(m.width, title...)}, body...)
	return out[:min(len(out), h)]
}

// whenWords says when a time was: on the clock when it is of the last
// day, with its day when it is older, and by how long ago.
func whenWords(t, now time.Time) string {
	switch {
	case t.IsZero():
		return human.None
	case now.Sub(t) < 24*time.Hour && t.Sub(now) < 24*time.Hour:
		return human.Clock(t) + " (" + human.Ago(t, now) + ")"
	}
	return sinceWords(t, now)
}

// sinceWords says since when a time is, on the clock and by how long.
func sinceWords(t, now time.Time) string {
	if t.IsZero() {
		return human.None
	}
	return human.DateTime(t) + " (" + human.Ago(t, now) + ")"
}
```

Create `tui/view_zathras.go`:

```go
package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/fables-for-robots/great-machine/human"
	"github.com/fables-for-robots/great-machine/wire"
)

// The view of a zathras: its state in full, its figures over the last
// fifteen minutes as sparklines with the value now, and its log.

// zathrasParts is how the rows of the view of a zathras are shared out.
type zathrasParts struct {
	state   []string // its state
	figures []string // its sparklines, or why there are none
	log     int      // how many rows of the log are in sight
}

// logHeight returns how many rows of the log are in sight.
func (m *model) logHeight() int {
	return max(m.shareZathras(m.bodyHeight()).log, 1)
}

// shareZathras shares h rows out among the parts of the view. The log
// has its title and three rows at least; where the terminal has no
// room for everything, the figures give way first, the last of them
// first, and then the end of the state.
func (m *model) shareZathras(h int) zathrasParts {
	if m.shared.zathras != nil && m.shared.zathrasH == h {
		return *m.shared.zathras
	}
	p := zathrasParts{state: m.stateLines(), figures: m.figureLines()}
	const logLeast = 1 + 3
	if over := len(p.state) + 1 + len(p.figures) + 1 + logLeast - h; over > 0 {
		// The figures give way from their end; their title alone is none.
		p.figures = p.figures[:max(len(p.figures)-over, 0)]
		if len(p.figures) == 1 && m.hasFigures() {
			p.figures = nil
		}
	}
	if over := len(p.state) + 1 + logLeast - h; len(p.figures) == 0 && over > 0 {
		p.state = p.state[:max(len(p.state)-over, 1)]
	}
	used := len(p.state) + 1 + 1 // an empty line, and the title of the log
	if len(p.figures) > 0 {
		used += len(p.figures) + 1
	}
	p.log = max(h-used, 0)
	m.shared.zathras, m.shared.zathrasH = &p, h
	return p
}

// zathrasView draws a zathras in at most h lines.
func (m *model) zathrasView(h int) []string {
	p := m.shareZathras(h)
	out := append([]string{}, p.state...)
	if len(p.figures) > 0 {
		out = append(out, "")
		out = append(out, p.figures...)
	}
	out = append(out, "", m.logTitle())
	return append(out, m.logLines(p.log)...)
}

// stateLines draws the state of the zathras in full, as it reported it
// last.
func (m *model) stateLines() []string {
	now := m.cfg.Now()
	z, st := m.seen, m.seen.State
	w := m.width
	var out []string
	if !m.present {
		// Review Focus 4: the zathras went while its view was open.
		text := "✖ This zathras is not connected to draal any more, since " + human.Clock(m.goneSince) + " (" + human.Ago(m.goneSince, now) +
			"). Below is what it reported last. Nothing is asked of it now; the view carries on when it is back."
		for _, l := range wrap(text, w-2, 3) {
			out = append(out, line(w, seg{" " + l, stBad}))
		}
	}
	// Its name, in full: also one of 253 characters.
	for _, l := range wrap(named(z.Name), w-2, 4) {
		out = append(out, line(w, plainText(" "), bold(l)))
	}
	sign, signSt := phaseSign(st.Phase)
	phase := []seg{plainText(" "), {sign, signSt}, {" " + string(knownPhase(st.Phase)), signSt}}
	switch note := phaseNote(st, now); {
	case note == "" || st.Generation == 0:
	case knownPhase(st.Phase) == wire.PhaseRunning:
		phase = append(phase, plainText(" "+note))
	default:
		phase = append(phase, plainText(", "+note))
	}
	stable := uint64(0)
	if svc, ok := m.serviceNamed(m.seenIn); ok {
		stable = svc.Stable
	}
	generation := "generation " + human.Generation(st.Generation)
	switch {
	case st.Generation == 0:
		phase = append(phase, dim(" · "), plainText("no generation: nothing was activated on it"))
	case st.Generation == stable:
		phase = append(phase, dim(" · "), plainText(generation), dim(", the stable one of "+named(m.seenIn)))
	case stable == 0:
		phase = append(phase, dim(" · "), plainText(generation), dim("; "+named(m.seenIn)+" has no stable one"))
	default:
		phase = append(phase, dim(" · "), plainText(generation), dim(", not the stable one of "+named(m.seenIn)+" ("+human.Generation(stable)+")"))
	}
	out = append(out, line(w, phase...))
	out = append(out, line(w, dim(" id         "), plainText(capped(string(z.ID)))))

	field := func(label, value string, rows int) {
		for i, l := range wrap(value, w-13, rows) {
			if i > 0 {
				label = ""
			}
			out = append(out, line(w, dim(" "+pad(label, 11)), plainText(l)))
		}
	}
	if st.Binary != "" {
		field("runs", strings.Join(append([]string{st.Binary}, st.Args...), " "), 3)
	}
	process := "not running"
	if st.PID != 0 {
		process = fmt.Sprintf("pid %d", st.PID)
	}
	field("process", process+", "+human.Count(int(min(st.Restarts, 1<<31)), "restart", "restarts")+" since this zathras started", 1)
	if e := st.LastExit; e != nil {
		// What is known of it, and no more: a zathras says when it was
		// and of which generation, and one that does not is not quoted.
		text := human.Exit(e)
		if !e.At.IsZero() {
			text += " at " + whenWords(e.At, now)
		}
		text += ", after " + human.Took(e.Ran)
		if e.Generation != 0 {
			text += ", of generation " + human.Generation(e.Generation)
		}
		if e.Error != "" {
			text += ": " + e.Error
		}
		field("last exit", text, 3)
	} else {
		field("last exit", "none: the service has not ended since this zathras started", 1)
	}
	connected := "since " + whenWords(z.Connected, now)
	if z.Connected.IsZero() {
		connected = "draal does not say since when"
	}
	if len(st.Prepared) > 0 {
		parts := make([]string, len(st.Prepared))
		for i, g := range st.Prepared {
			parts[i] = human.Generation(g)
		}
		connected += " · prepared " + strings.Join(parts, ", ")
	} else {
		connected += " · nothing prepared"
	}
	if st.Preparing != 0 {
		connected += " · preparing " + human.Generation(st.Preparing)
	}
	field("connected", connected, 2)
	return out
}

// hasFigures says whether the figures are sparklines, under a title, as
// against a sentence that says why there are none.
func (m *model) hasFigures() bool {
	return len(m.samples) > 0
}

// figureLines draws the figures of the container: a sparkline for each
// with the value now and the scale, or a sentence that says why there
// are none.
func (m *model) figureLines() []string {
	w := m.width
	if len(m.samples) == 0 {
		// Review Focus 4: a zathras without samples.
		switch {
		case m.history == askedFor:
			return sentences(w, stDim, "Figures: asking draal …")
		case m.history == askedRefused:
			return sentences(w, stBad, "Figures: draal does not send them: "+m.histErr)
		case m.history == askedNot && !m.present:
			return sentences(w, stDim, "Figures: they were not received before this zathras went.")
		case m.history == askedNot:
			return sentences(w, stDim, "Figures: not asked for: there is no connection to draal.")
		}
		return sentences(w, stPlain, "This zathras reports no figures. zathras reads them from cgroup v2 on Linux; where that is missing it sends none, and there is nothing to draw. Its state, its log and its shell do not depend on them.")
	}
	now := m.samples[len(m.samples)-1]
	const labelW, valueW, scaleW = 11, 11, 24
	sparkW := min(max(w-1-labelW-1-valueW-2-2-scaleW, 8), sparkWidest)
	row := func(label, value string, pick func(wire.Sample) float64, top float64, scale string) string {
		return line(w, dim(" "+pad(label, labelW)), plainText(" "+padLeft(value, valueW)+"  "),
			seg{spark(m.samples, pick, sparkW, top), stBusy}, dim("  "+scale))
	}
	cpu := func(s wire.Sample) float64 { return s.CPU }
	memory := func(s wire.Sample) float64 { return float64(s.Memory) }
	cpuTop := coreTop(largest(m.samples, cpu))
	memTop, memScale := byteTop(largest(m.samples, memory)), ""
	if now.MemoryLimit > 0 {
		memTop = float64(now.MemoryLimit)
		memScale = "to " + human.Bytes(now.MemoryLimit) + ", its limit"
	} else {
		memScale = "to " + human.Bytes(uint64(memTop)) + "; no limit"
	}
	rate := func(label string, pick func(wire.Sample) float64) string {
		top := byteTop(largest(m.samples, pick))
		return row(label, human.Rate(pick(now)), pick, top, "to "+human.Rate(top))
	}
	return []string{
		line(w, bold(" Figures of the container"), dim(" · the last 15 minutes, to "+human.Clock(now.At)+" ("+human.Ago(now.At, m.cfg.Now())+")")),
		row("CPU", human.CPU(now.CPU), cpu, cpuTop, "to "+human.CPU(cpuTop)+" of one core"),
		row("memory", human.Bytes(now.Memory), memory, memTop, memScale),
		rate("disk read", func(s wire.Sample) float64 { return s.DiskRead }),
		rate("disk write", func(s wire.Sample) float64 { return s.DiskWrite }),
		rate("net in", func(s wire.Sample) float64 { return s.NetRx }),
		rate("net out", func(s wire.Sample) float64 { return s.NetTx }),
	}
}

// logTitle draws the line above the log: what it is, and where one is
// in it.
func (m *model) logTitle() string {
	what := ""
	switch m.logIs {
	case askedNot:
		what = "not asked for: there is no connection to draal"
		if !m.present {
			what = "not asked for: the zathras is not connected"
		}
	case askedFor:
		// draal answers a log with its lines alone: until the first one
		// there is no telling a log that is followed and empty from one
		// that draal has not got to yet.
		what = "no line has come yet"
	case askedEnded:
		what = "ended"
		if m.present && m.conn == connLive {
			what = "ended; G asks for it again"
		}
	default:
		what = "followed live"
	}
	segs := []seg{bold(" Log"), dim(" · " + what)}
	if h := m.logHeight(); !m.follow && len(m.rows) > h {
		segs = append(segs, seg{fmt.Sprintf(" · rows %d to %d of %d; G goes to the end", m.logTop+1, min(m.logTop+h, len(m.rows)), len(m.rows)), stWarn})
	}
	return line(m.width, segs...)
}

// logLines draws h rows of the log: the end of it, or from the row one
// has scrolled to.
func (m *model) logLines(h int) []string {
	if h <= 0 {
		return nil
	}
	if len(m.rows) == 0 {
		return nil
	}
	top := m.logTop
	if m.follow {
		top = max(len(m.rows)-h, 0)
	}
	prefix := logPrefix(m.width)
	var out []string
	for _, r := range m.rows[top:min(top+h, len(m.rows))] {
		if r.entry >= len(m.log.entries) {
			continue
		}
		e := m.log.entries[r.entry]
		switch e.kind {
		case entryLost:
			out = append(out, line(m.width, seg{fmt.Sprintf("   … %s lost here: the log grew faster than it was read …", human.Count(int(min(e.lost, 1<<31)), "line was", "lines were")), stWarn}))
			continue
		case entryNote:
			out = append(out, line(m.width, dim("   — "+e.text)))
			continue
		}
		var b strings.Builder
		if r.sub > 0 {
			b.WriteString(strings.Repeat(" ", prefix))
		} else {
			b.WriteString(" ")
			if prefix > 3 {
				b.WriteString(line(9, dim(human.Clock(e.at)+" ")))
			}
			letter, st := streamSign(e.stream)
			b.WriteString(line(2, seg{letter + " ", st}))
		}
		b.WriteString(drawSpans(r.spans))
		out = append(out, b.String())
	}
	return out
}

// streamSign returns the letter and the look of a stream of a log.
func streamSign(stream string) (string, lipgloss.Style) {
	switch stream {
	case "stderr":
		return "e", stBad
	case "zathras":
		return "z", stBusy
	case "stdout":
		return "o", stDim
	}
	return "?", stDim
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `nix develop .#bare --command go test ./tui/`
Expected: `ok`, in about nine seconds: `TestEverySizeOfTerminal` of Task 5 now draws the real views at every size.

Run: `nix develop .#bare --command go test ./tui/ -run XXX -fuzz FuzzViews -fuzztime 60s`
Expected: ends with `PASS`: every view, with whatever bytes in every field, is text and SGR, and no line is wider than the terminal.

Run: `nix develop .#bare --command gofmt -l . && nix develop .#bare --command go vet ./...`
Expected: no output

- [ ] **Step 5: Measure a fleet of three hundred zathrases and a flood**

Run: `nix develop .#bare --command go test ./tui/ -run XXX -bench . -benchtime 2s`
Expected: three lines like these (an Apple M-series laptop; the numbers are what was measured when this plan was written):

```
BenchmarkThreeHundredZathrases/80x24-14         	    4155	    572965 ns/op
BenchmarkThreeHundredZathrases/200x60-14        	    2047	   1241333 ns/op
BenchmarkLogFlood-14                            	    1784	   1483858 ns/op
```

A fleet of three hundred zathrases that comes and is drawn costs 0.6 ms in a terminal of 80×24 and 1.2 ms in one of 200×60: what is in sight is drawn, not what the fleet has. A batch of four hundred lines of a flood, read, wrapped and drawn at 160×48, costs 1.5 ms; at twenty batches a second that is 3% of one core. A number ten times these on another machine is still no trouble; a hundred times is a view that draws what is not in sight.

- [ ] **Step 6: Look at it**

What can only be seen. The demo is Task 7; until then, print a scene. Put this file at `tui/look_test.go`, run it, look, and **delete it** (it is no test):

```go
package tui

import (
	"fmt"
	"os"
	"testing"
)

func TestLook(t *testing.T) {
	for _, sc := range scenes() {
		if sc.name == os.Getenv("SCENE") {
			fmt.Println(sc.model(80, 24).view())
		}
	}
}
```

Run, in a terminal: `SCENE=service-rollout-under-way nix develop .#bare --command go test ./tui/ -run TestLook -v`, and the same with `SCENE=zathras`, `SCENE=services-lost`, `SCENE=service-halted`, `SCENE=zathras-gone`.

What must be seen, on a dark terminal and on a light one: the header as a bar in reverse; the row that is chosen as a bar in reverse; `●` and `completed` in green, `✖`, `halted`, `backoff` and the banner of a lost connection in red and bold, `◐` in yellow, `▶` and the sparklines in cyan; the labels of the fields faint; every word readable on both grounds. Then `rm tui/look_test.go`.

- [ ] **Step 7: Commit**

```bash
git add tui/
git commit -m "TUI: the views: the services, a service, a zathras, the keys, and every odd state as a sentence"
```
