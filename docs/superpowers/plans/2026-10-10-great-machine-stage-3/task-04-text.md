### Task 4: Text that is safe for a terminal, and what the terminal UI writes

Everything the views are made of that is no view: each a pure function, with a table test. Five cycles: what is written of a number and a time (a package `human`, which `cli` uses too); the one function through which every string of draal and of a zathras goes before it is drawn; sparklines; where a zathras stands in a rollout; and the buffer of a log.

**Review Focus 2 is pinned here**, by `TestCleanLeavesNothingATerminalActsOn`, `TestCleanOfThesePieces`, `FuzzClean`, `TestDrawSpansWritesItsOwnSequences` (`tui/text_test.go`) and `TestLogBufferReadsALineWhenItComes` (`tui/logbuf_test.go`). Their helper `unsafe` is the check the later tasks use on everything that is drawn.

**Files:**
- Create: `human/human.go`, `human/human_test.go`
- Delete: `cli/human.go` (its three functions move to `human`)
- Modify: `cli/progress.go`, `cli/rollout.go`, `cli/status.go` (call `human.Took`, `human.Age`, `human.Bytes`, `human.Generation`), `cli/progress_test.go` (`TestByteSize` moves to `human`)
- Create: `tui/text.go`, `tui/spark.go`, `tui/rollout.go`, `tui/logbuf.go`
- Test: `tui/text_test.go`, `tui/spark_test.go`, `tui/rollout_test.go`, `tui/logbuf_test.go`
- Modify: `go.mod`, `go.sum` (Lip Gloss v2.0.6 and `x/ansi` v0.11.8, as direct requirements)

**Interfaces:**
- Consumes: `wire` (`Sample`, `State`, `Exit`, `LogLine`, `RolloutInfo`, `Zathras`, `ID`, `Phase`, `RolloutState`).
- Produces, package `human` (every function is total: None, `"-"`, stands where a value is missing, and no length of time is below zero):
  - `const None = "-"`
  - `func Took(d time.Duration) string` (`1.3s`, `3m12s`), `func Age(d time.Duration) string` (`42s`, `3m12s`, `5h03m`, `2d04h`)
  - `func Bytes(n uint64) string` (`120.5 MiB`), `func Rate(perSecond float64) string` (`22.4 KiB/s`), `func CPU(cores float64) string` (`75%`, `4.2%`), `func Percent(share float64) string`
  - `func Ago(t, now time.Time) string` (`now`, `47s ago`), `func Until(t, now time.Time) string` (`in 6s`, `now`), `func Clock(t time.Time) string` (`22:14:41`, local), `func DateTime(t time.Time) string`
  - `func ShortID(id wire.ID) string` (8 characters, as `great-machine status` prints), `func StoreName(path string) string`, `func Exit(e *wire.Exit) string` (`exit 3`, `killed by SIGKILL`, `did not start`), `func Count(n int, one, many string) string`, `func Generation(n uint64) string`
- Produces, package `tui` (unexported; the later tasks of the package use them):
  - `func clean(s string) string`: `s` as it may be drawn. **Every string of draal or a zathras goes through it** (or through `parseLine`) before it is drawn. `func named(s string) string`: a name, capped and clean, or `(no name)`. `func capped(s string) string`: `s`, or its first `fieldCap` (4 KiB) bytes and ` … (N bytes more)`; it is applied where words of draal or a zathras come in, so that a megabyte in a field is not cleaned and wrapped with every message.
  - `func width(s string) int`, `func cut(s string, w int) string` (ends in `…`), `func middle(s string, w int) string` (keeps both ends), `func pad(s string, w int) string`, `func padLeft(s string, w int) string`, `func wrap(s string, w, rows int) []string`; `cutClean`, `tailClean` for text that is clean already; `const ellipsis = "…"`
  - `type look struct{ fg, bg uint8; bold, dim, italic, underline, inverse bool }` (colours 1 to 16 for the terminal's 0 to 15; 0 is its default), `type span struct{ text string; look look }`
  - `func parseLine(line string, limit int) (spans []span, cut bool)`, `func spansWidth(spans []span) int`, `func wrapSpans(spans []span, w, rows int) (out [][]span, cut bool)`, `func drawSpans(spans []span) string`
  - `func mergeSamples(have, more []wire.Sample, now time.Time) []wire.Sample` (a sample more than `sampleAhead`, an hour, ahead of `now` is left out), `func spark(samples []wire.Sample, pick func(wire.Sample) float64, width int, top float64) string`, `func largest(samples []wire.Sample, pick func(wire.Sample) float64) float64`, `func byteTop(most float64) float64`, `func coreTop(most float64) float64` (both are numbers whatever the figure: `byteMost`, `coreMost` at most), `const sparkWidest = 150`
  - `type stage int` (`stageWaiting`, `stagePreparing`, `stagePrepared`, `stageOn`, `stageFailed`; `String`), `var stages []stage`, `func stageOf(st wire.State, target uint64) stage`, `var phases []wire.Phase`, `func knownPhase(p wire.Phase) wire.Phase`, `func byPhase(zs []wire.Zathras) map[wire.Phase]int`, `func phaseNote(st wire.State, now time.Time) string`, `func rolloutOpen(r *wire.RolloutInfo) bool`
  - `type logBuffer` with `entries []entry`, `func newLogBuffer() *logBuffer`, `add(lines []wire.LogLine) int`, `say(text string)`, `resume()` (the log is asked for again: of the lines that come first, those that are there are not taken twice); `type entry struct{ seq uint64; kind entryKind; at time.Time; stream string; spans []span; cut bool; lost uint64; text string }`; `entryLine`, `entryLost`, `entryNote`; `type pending struct{ keep int; lines []wire.LogLine }` with `add`, `take`; `const logKept = 400`, `logCells = 2000`, `logEvery = 50 * time.Millisecond`
  - In the tests, for every later test of the package: `func plain(s string) string` (what is drawn, without its escape sequences) and `func unsafe(s string) string` (the first thing in `s` that a terminal would act on, or `""`).

**What was decided, and why:**
- **The dependencies.** `charm.land/lipgloss/v2` v2.0.6 draws the colours. `github.com/charmbracelet/x/ansi` v0.11.8 is required by Lip Gloss and by Bubble Tea already; it becomes a direct requirement because `tui` counts the width of a character with `ansi.StringWidth`, the function the renderer counts with, and the tests take the escape sequences out of what is drawn with `ansi.Strip`, which is none of the code under test. **Bubbles is not used**: its viewport scrolls a string that would have to be cleaned, wrapped and cut by this package first, its table cuts cells by rules of its own, and the lists here are a loop over the rows in sight; every component would stand between a hostile byte and the terminal without doing what is needed.
- **`cli` and `tui` write alike.** A `tui` cannot import `cli` (which will import it), so `took`, `age`, `byteSize` and `generation` of `cli` move to `human`, unchanged, and both import it. What `cli` prints does not change.
- **What `clean` does with what is not printable** is the comment at the top of `tui/text.go`. In short: escape sequences out, whole or cut off; tabs to spaces; line ends to one space; other control characters out; invalid UTF-8 and unassigned characters to U+FFFD; **the bidi marks and overrides, the zero-width characters, variation selectors and the skin tones of emoji are left out**, so that no line shows another order than it has and every terminal counts the same width; a regional indicator is its letter; a character keeps three combining marks at most.
- **256 colours and RGB of a log line are drawn as the nearest of the terminal's sixteen**: the terminal UI writes none but those (Global Constraints).
- **A log line is read when it comes**, into at most 2000 cells: a megabyte costs what is kept of it.
- **The width `tui` counts is its own** (`runeWidth`: 2 for the wide characters, 0 for every combining mark, else what `ansi.StringWidth` says of the character alone). It is the renderer's for Latin, CJK, emoji and the like, which `TestCleanOfThesePieces` checks for its pieces. It is not for every script: a spacing mark of Devanagari or Myanmar and a Hangul initial with its vowel count another number of cells by graphemes, by characters, and here, and the renderer itself counts by one or the other as the terminal reports. So a column may stand a cell off in such a name; no sequence can form of it, and Task 6 cuts every line that is wider than the terminal by either of the renderer's counts.
- **After a log is asked for again** (`resume`), a line is skipped as one that is there when its time is before the newest held, or is that time and the line itself (stream and text) is held with it: a line over 16 KiB is several lines of one time in the ring, and the later ones are new. Behind the first new line, and behind 400 lines (the ring has no more), everything is new, whatever its time: a zathras whose clock was set back loses 400 lines at most, not its log.
- **The times of samples are the zathras' own.** The window ends at its newest sample, so a clock that is ahead or behind shows its figures all the same; a sample more than an hour ahead of the terminal UI's clock is left out, so that one wrong sample cannot be the newest for a day and empty the window before it. A zathras whose clock is more than an hour ahead has no sparklines.

- [ ] **Step 1: Write the failing test of `human`**

Create `human/human_test.go`:

```go
package human

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

func TestTookAndAge(t *testing.T) {
	for _, tt := range []struct {
		d         time.Duration
		took, age string
	}{
		{-5 * time.Second, "0.0s", "0s"},
		{0, "0.0s", "0s"},
		{1349 * time.Millisecond, "1.3s", "1s"},
		{42 * time.Second, "42.0s", "42s"},
		{59999 * time.Millisecond, "59.9s", "59s"},
		{time.Minute, "1m00s", "1m00s"},
		{3*time.Minute + 12*time.Second, "3m12s", "3m12s"},
		{5*time.Hour + 3*time.Minute, "5h03m", "5h03m"},
		{52 * time.Hour, "2d04h", "2d04h"},
		{math.MaxInt64, "106751d23h", "106751d23h"},
		{math.MinInt64, "0.0s", "0s"},
	} {
		if got := Took(tt.d); got != tt.took {
			t.Errorf("Took(%d) = %q, want %q", tt.d, got, tt.took)
		}
		if got := Age(tt.d); got != tt.age {
			t.Errorf("Age(%d) = %q, want %q", tt.d, got, tt.age)
		}
	}
}

func TestBytes(t *testing.T) {
	for _, tt := range []struct {
		n    uint64
		want string
	}{
		{0, "0 B"}, {1023, "1023 B"}, {1536, "1.5 KiB"}, {126353408, "120.5 MiB"}, {3 << 30, "3.0 GiB"},
		{5 << 40, "5.0 TiB"}, {math.MaxUint64, "16777216.0 TiB"},
	} {
		if got := Bytes(tt.n); got != tt.want {
			t.Errorf("Bytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestRateCPUAndPercent(t *testing.T) {
	for _, tt := range []struct {
		v                  float64
		rate, cpu, percent string
	}{
		{0, "0 B/s", "0.0%", "0%"},
		{0.042, "0 B/s", "4.2%", "4%"},
		{0.75, "1 B/s", "75%", "75%"},
		{2.5, "3 B/s", "250%", "250%"},
		{22937.6, "22.4 KiB/s", "2293760%", "2293760%"},
		{-1, None, None, None}, // a counter that started again
		{math.NaN(), None, None, None},
		{math.Inf(1), None, None, None},
		{math.Inf(-1), None, None, None},
	} {
		if got := Rate(tt.v); got != tt.rate {
			t.Errorf("Rate(%v) = %q, want %q", tt.v, got, tt.rate)
		}
		if got := CPU(tt.v); got != tt.cpu {
			t.Errorf("CPU(%v) = %q, want %q", tt.v, got, tt.cpu)
		}
		if got := Percent(tt.v); got != tt.percent {
			t.Errorf("Percent(%v) = %q, want %q", tt.v, got, tt.percent)
		}
	}
	if got := Rate(1e300); got != "16777216.0 TiB/s" {
		t.Errorf("Rate(1e300) = %q", got)
	}
}

func TestAgoAndUntil(t *testing.T) {
	now := time.Date(2026, 10, 9, 22, 14, 41, 0, time.UTC)
	for _, tt := range []struct {
		t          time.Time
		ago, until string
	}{
		{time.Time{}, None, None},
		{now, "now", "now"},
		{now.Add(-999 * time.Millisecond), "now", "now"},
		{now.Add(-47 * time.Second), "47s ago", "now"},
		{now.Add(-200 * time.Second), "3m ago", "now"},
		{now.Add(-3 * time.Hour), "3h ago", "now"},
		{now.Add(-80 * time.Hour), "3d ago", "now"},
		{now.Add(6 * time.Second), "now", "in 6s"}, // a clock that is ahead
		{now.Add(90 * time.Minute), "now", "in 1h"},
	} {
		if got := Ago(tt.t, now); got != tt.ago {
			t.Errorf("Ago(%v) = %q, want %q", tt.t, got, tt.ago)
		}
		if got := Until(tt.t, now); got != tt.until {
			t.Errorf("Until(%v) = %q, want %q", tt.t, got, tt.until)
		}
	}
}

func TestClockAndDateTime(t *testing.T) {
	at := time.Date(2026, 10, 9, 22, 4, 1, 999, time.Local)
	if got := Clock(at); got != "22:04:01" {
		t.Errorf("Clock = %q", got)
	}
	if got := DateTime(at); got != "2026-10-09 22:04:01" {
		t.Errorf("DateTime = %q", got)
	}
	// On the clock of the reader, whatever zone the time came in.
	if got := Clock(at.UTC()); got != "22:04:01" {
		t.Errorf("Clock of the same time in UTC = %q", got)
	}
	if Clock(time.Time{}) != None || DateTime(time.Time{}) != None {
		t.Errorf("the zero time is %q and %q, want %q", Clock(time.Time{}), DateTime(time.Time{}), None)
	}
}

func TestShortIDAndStoreName(t *testing.T) {
	id := wire.ID(strings.Repeat("ab12", 16))
	if got := ShortID(id); got != "ab12ab12" {
		t.Errorf("ShortID = %q", got)
	}
	if ShortID("") != None || ShortID("abc") != "abc" {
		t.Errorf("ShortID of nothing = %q, of abc = %q", ShortID(""), ShortID("abc"))
	}
	for path, want := range map[string]string{
		"": None,
		"/nix/store/kzrvqcn6yd2p8bqlhbqm8wl5bh7f0r1s-web-1.4.20/bin/web": "web-1.4.20/bin/web",
		"/nix/store/kzrvqcn6yd2p8bqlhbqm8wl5bh7f0r1s-web-1.4.20":         "web-1.4.20",
		"/nix/store/short-web/bin/web":                                   "/nix/store/short-web/bin/web",
		"/nix/store/KZRVQCN6YD2P8BQLHBQM8WL5BH7F0R1S-web/bin/web":        "/nix/store/KZRVQCN6YD2P8BQLHBQM8WL5BH7F0R1S-web/bin/web",
		"/nix/store/kzrvqcn6yd2p8bqlhbqm8wl5bh7f0r1s-":                   "/nix/store/kzrvqcn6yd2p8bqlhbqm8wl5bh7f0r1s-",
		"/usr/bin/env": "/usr/bin/env",
	} {
		if got := StoreName(path); got != want {
			t.Errorf("StoreName(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestExitCountAndGeneration(t *testing.T) {
	for _, tt := range []struct {
		e    *wire.Exit
		want string
	}{
		{nil, None},
		{&wire.Exit{Status: 0}, "exit 0"},
		{&wire.Exit{Status: 3}, "exit 3"},
		{&wire.Exit{Status: -1, Signal: "SIGKILL"}, "killed by SIGKILL"},
		{&wire.Exit{Status: -1, Error: "exec format error"}, "did not start"},
		{&wire.Exit{Status: -1}, "exit -1"},
	} {
		if got := Exit(tt.e); got != tt.want {
			t.Errorf("Exit(%+v) = %q, want %q", tt.e, got, tt.want)
		}
	}
	if got := Count(1, "zathras", "zathrases") + ", " + Count(0, "zathras", "zathrases") + ", " + Count(3, "zathras", "zathrases"); got != "1 zathras, 0 zathrases, 3 zathrases" {
		t.Errorf("Count = %q", got)
	}
	if Generation(0) != None || Generation(20) != "20" {
		t.Errorf("Generation(0) = %q, Generation(20) = %q", Generation(0), Generation(20))
	}
}
```

- [ ] **Step 2: Run the test to see it fail**

Run: `nix develop .#bare --command go test ./human/`
Expected: FAIL, the package does not build:

```
# github.com/fables-for-robots/great-machine/human [github.com/fables-for-robots/great-machine/human.test]
human/human_test.go:29:13: undefined: Took
human/human_test.go:32:13: undefined: Age
human/human_test.go:46:13: undefined: Bytes
human/human_test.go:62:8: undefined: None
human/human_test.go:63:16: undefined: None
human/human_test.go:64:17: undefined: None
human/human_test.go:65:18: undefined: None
human/human_test.go:67:13: undefined: Rate
human/human_test.go:70:13: undefined: CPU
human/human_test.go:73:13: undefined: Percent
human/human_test.go:73:13: too many errors
FAIL	github.com/fables-for-robots/great-machine/human [build failed]
FAIL
```

- [ ] **Step 3: Write `human`, and have `cli` use it**

Create `human/human.go`. `Took`, `Age` and `Bytes` are `took`, `age` and `byteSize` of `cli/human.go`, and `Generation` is `generation` of `cli/status.go`, each with its body unchanged:

```go
// Package human writes numbers, times and names as a person reads them:
// what the command line and the terminal UI print of a duration, a size,
// a rate, a time, an endpoint ID, a store path and an exit.
//
// Every function is total: whatever it is given, it returns words for a
// person. What is missing (the zero time, a figure that is no number) is
// written as None, and a length of time is never below zero.
package human

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// None stands where a value is missing.
const None = "-"

// Took writes how long a step took: tenths of a second up to a minute,
// which is where they matter, and whole seconds or minutes beyond.
func Took(d time.Duration) string {
	switch {
	case d < 0:
		return "0.0s"
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Truncate(100*time.Millisecond).Seconds())
	}
	return Age(d)
}

// Age writes for how long something has been so, in two units at most:
// 42s, 3m12s, 5h03m, 2d04h. A time below zero, which two clocks that
// differ give, is 0s.
func Age(d time.Duration) string {
	s := int64(max(d, 0) / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	case s < 24*3600:
		return fmt.Sprintf("%dh%02dm", s/3600, s/60%60)
	}
	return fmt.Sprintf("%dd%02dh", s/(24*3600), s/3600%24)
}

// Bytes writes a number of bytes with a binary unit: 512 B, 1.5 KiB,
// 120.5 MiB.
func Bytes(n uint64) string {
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n) / 1024
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// figure says whether v is a figure there is: a number, and not below
// zero. A rate below zero is what a counter that started again gives.
func figure(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

// Rate writes bytes per second: 22.4 KiB/s. A rate that is no figure is
// None.
func Rate(perSecond float64) string {
	if !figure(perSecond) {
		return None
	}
	if perSecond >= math.MaxUint64 {
		return Bytes(math.MaxUint64) + "/s"
	}
	return Bytes(uint64(perSecond+0.5)) + "/s"
}

// CPU writes cores as a percentage of one core: 0.75 is 75%, 2.5 is
// 250%, and less than a tenth of a core has a decimal: 4.2%.
func CPU(cores float64) string {
	if !figure(cores) {
		return None
	}
	p := cores * 100
	if p >= 10 {
		return strconv.FormatFloat(p, 'f', 0, 64) + "%"
	}
	return strconv.FormatFloat(p, 'f', 1, 64) + "%"
}

// Percent writes a share, 0 to 1, as a whole percentage.
func Percent(share float64) string {
	if !figure(share) {
		return None
	}
	return strconv.FormatFloat(share*100, 'f', 0, 64) + "%"
}

// coarse writes a length of time in its largest unit alone: 47s, 3m, 2h,
// 3d.
func coarse(d time.Duration) string {
	s := int64(d / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm", s/60)
	case s < 24*3600:
		return fmt.Sprintf("%dh", s/3600)
	}
	return fmt.Sprintf("%dd", s/(24*3600))
}

// Ago writes how long ago t was by the clock of now: "now", "47s ago",
// "3m ago". The zero time is None, and a time in the future, which a
// clock that is ahead gives, is "now".
func Ago(t, now time.Time) string {
	if t.IsZero() {
		return None
	}
	d := now.Sub(t)
	if d < time.Second {
		return "now"
	}
	return coarse(d) + " ago"
}

// Until writes how long it is until t: "in 6s", or "now" when it has
// come. The zero time is None.
func Until(t, now time.Time) string {
	if t.IsZero() {
		return None
	}
	d := t.Sub(now)
	if d < time.Second {
		return "now"
	}
	return "in " + coarse(d)
}

// Clock writes the time of day of t on the clock of the reader:
// 22:14:41. The zero time is None.
func Clock(t time.Time) string {
	if t.IsZero() {
		return None
	}
	return t.Local().Format("15:04:05")
}

// DateTime writes the day and the time of t on the clock of the reader:
// 2026-10-09 22:14:41. The zero time is None.
func DateTime(t time.Time) string {
	if t.IsZero() {
		return None
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// ShortID writes the first 8 characters of an endpoint ID, which is what
// `great-machine status` prints of one. An empty ID is None.
func ShortID(id wire.ID) string {
	switch {
	case id == "":
		return None
	case len(id) > 8:
		return string(id[:8])
	}
	return string(id)
}

// StoreName writes a path of the Nix store without the store and the
// hash: /nix/store/kzrv…-web-1.4.20/bin/web is web-1.4.20/bin/web. Any
// other path is itself, and an empty one is None.
func StoreName(path string) string {
	if path == "" {
		return None
	}
	const store, hash = "/nix/store/", 32
	rest, ok := strings.CutPrefix(path, store)
	if !ok || len(rest) < hash+2 || rest[hash] != '-' {
		return path
	}
	for i := range hash {
		if c := rest[i]; (c < '0' || c > '9') && (c < 'a' || c > 'z') {
			return path
		}
	}
	return rest[hash+1:]
}

// Exit writes how a service ended: "exit 3", "killed by SIGKILL", "did
// not start". No exit is None.
func Exit(e *wire.Exit) string {
	switch {
	case e == nil:
		return None
	case e.Error != "":
		return "did not start"
	case e.Signal != "":
		return "killed by " + e.Signal
	}
	return "exit " + strconv.Itoa(e.Status)
}

// Count writes a number with its noun: "1 zathras", "3 zathrases".
func Count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// Generation writes the number of a generation, or None for none (0).
func Generation(n uint64) string {
	if n == 0 {
		return None
	}
	return strconv.FormatUint(n, 10)
}
```

Then `cli` uses the package. The calls are changed in place:

```bash
git rm -q cli/human.go
sed -i.bak -e 's/ took(/ human.Took(/g' cli/progress.go
sed -i.bak -e 's/ byteSize(/ human.Bytes(/g' cli/rollout.go
sed -i.bak -e 's/ age(/ human.Age(/g' -e 's/ byteSize(/ human.Bytes(/g' -e 's/\([ {]\)generation(/\1human.Generation(/g' cli/status.go
rm cli/*.bak
```

and four things by hand:

1. In `cli/status.go`, delete the function `generation` with its comment. The command has just turned its first line into `func human.Generation(n uint64) string {`; the seven lines to delete are

```go
// generation writes the number of a generation, or - for none.
func human.Generation(n uint64) string {
	if n == 0 {
		return "-"
	}
	return strconv.FormatUint(n, 10)
}
```

   (`strconv` stays imported: the CPU of a zathras is written with it.)
2. In `cli/status.go` and in `cli/rollout.go`, add the import `"github.com/fables-for-robots/great-machine/human"` among the imports of the module, in the order of the alphabet: in `status.go` before `…/operator`, in `rollout.go` behind `…/closure`.
3. In `cli/progress.go`, the last group of the imports becomes

```go
	"github.com/fables-for-robots/great-machine/human"
	xterm "golang.org/x/term"
)
```

4. In `cli/progress_test.go`, delete `TestByteSize`, the last function of the file (its cases are in `human/human_test.go`).

- [ ] **Step 4: Run the tests to see them pass**

Run: `nix develop .#bare --command go test ./human/ ./cli/`
Expected: PASS (`ok` for both)

Run: `nix develop .#bare --command gofmt -l human cli`
Expected: no output

- [ ] **Step 5: Commit**

```bash
git add -A human cli
git commit -m "Human: what the command line writes of a duration and a size is a package, with what the terminal UI writes of a rate, a time, an ID, a path and an exit"
```

- [ ] **Step 6: Add Lip Gloss, and write the failing test of the text**

The two modules are added at exact versions. `go get` writes them as indirect until something imports them; `go mod tidy` in Step 8 makes them direct.

```bash
nix develop .#bare --command go get charm.land/lipgloss/v2@v2.0.6 github.com/charmbracelet/x/ansi@v0.11.8
```

Create `tui/text_test.go`. The escapes in its strings (`\u200b`, `\u0301`, …) are written as escapes on purpose: an editor that turns them into the characters makes a file nobody can read, and Go refuses a byte order mark in a source file.

```go
package tui

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// plain returns what a person reads of something that was drawn: the
// text without the escape sequences that colour it. Every test of what
// is drawn compares this.
func plain(s string) string {
	return ansi.Strip(s)
}

// unsafe returns the first thing in s that a terminal would do
// something with, or "" when s is text, line feeds and SGR sequences
// (ESC [ numbers m) alone. It is the check of Review Focus 2: what the
// terminal UI draws moves no cursor, sets no title and clears nothing.
func unsafe(s string) string {
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n <= 1:
			return fmt.Sprintf("the byte %#x at %d, which is no UTF-8", s[i], i)
		case r == 0x1b:
			j := i + 1
			if j >= len(s) || s[j] != '[' {
				return fmt.Sprintf("an escape sequence that is no SGR at %d: %q", i, s[i:min(len(s), i+12)])
			}
			for j++; j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == ';'); j++ {
			}
			if j >= len(s) || s[j] != 'm' {
				return fmt.Sprintf("an escape sequence that is no SGR at %d: %q", i, s[i:min(len(s), i+12)])
			}
			i = j + 1
			continue
		case r == '\n':
		case r < 0x20, r >= 0x7f && r <= 0x9f:
			return fmt.Sprintf("the control character %#x at %d", r, i)
		case unicode.Is(unicode.Cf, r):
			return fmt.Sprintf("the format character %U at %d", r, i)
		}
		i += n
	}
	return ""
}

// The worst that a zathras can write, and what is left of it.
var hostile = []struct {
	name, in, want string
}{
	{"plain text", "GET /api/orders 200", "GET /api/orders 200"},
	{"a title, ended by BEL", "a\x1b]0;pwned\x07b", "ab"},
	{"a title, ended by ST", "a\x1b]2;pwned\x1b\\b", "ab"},
	{"the clipboard", "a\x1b]52;c;cm0gLXJmIH4=\x07b", "ab"},
	{"a link", "\x1b]8;;http://evil.example\x1b\\click\x1b]8;;\x1b\\", "click"},
	{"a cursor move", "a\x1b[10;10Hb", "ab"},
	{"clearing the screen", "a\x1b[2J\x1b[H\x1b[3Jb", "ab"},
	{"leaving the alternate screen", "a\x1b[?1049l\x1b[?25hb", "ab"},
	{"a scroll region and a reset", "a\x1b[1;5r\x1bcb", "ab"},
	{"asking the terminal to answer", "a\x1b[6n\x1b[c\x1b]10;?\x07b", "ab"},
	{"a colour", "\x1b[1;31mERROR\x1b[0m failed", "ERROR failed"},
	{"a sequence with intermediates", "a\x1b[ qb\x1b[!pc", "abc"},
	{"a sequence cut off at the end", "abc\x1b[31", "abc"},
	{"an introducer at the end", "abc\x1b[", "abc"},
	{"an ESC at the end", "abc\x1b", "abc"},
	{"a title cut off at the end", "abc\x1b]0;half a tit", "abc"},
	{"the rest of a title that the buffer cut", "le\x07 and text", "le and text"},
	{"the rest of a colour that the buffer cut", "1;31mERROR\x1b[0m", "1;31mERROR"},
	{"nothing but ESC", strings.Repeat("\x1b", 1000), ""},
	{"ESC before a sequence", "\x1b\x1b[31mx", "x"},
	{"a sequence that another one interrupts", "\x1b[12\x1b[31mx", "x"},
	{"a title that a sequence interrupts", "\x1b]0;t\x1b[31mx", "x"},
	{"a line feed inside a sequence", "\x1b[1\n2Hx", " 2Hx"},
	{"a title ended by CAN", "\x1b]0;t\x18x", "x"},
	{"DCS", "a\x1bPq#0;2;0;0;0#0~~\x1b\\b", "ab"},
	{"APC, as Kitty's pictures", "a\x1b_Gf=100;AAAA\x1b\\b", "ab"},
	{"PM and SOS", "a\x1b^secret\x1b\\b\x1bXmore\x1b\\c", "abc"},
	{"DCS without an end", "a\x1bP1$r never ended", "a"},
	{"a character set", "a\x1b(0b\x1b)Bc", "abc"},
	{"saving the cursor, the alignment test", "a\x1b7\x1b8\x1b#8b", "ab"},
	{"C1: a control sequence", "a\u009b31mb\u009b2Jc", "abc"},
	{"C1: a title with its end", "a\u009d0;t\u009cb", "ab"},
	{"C1: a title without its end", "a\u009d0;t", "a"},
	{"C1: DCS, SOS, PM, APC", "a\u0090x\x1b\\b\u0098x\x07c\u009ex\x07d\u009fx\x07e", "abcde"},
	{"C1: the others", "a\u0080\u0084\u0099b", "ab"},
	{"C1: next line", "a\u0085b", "a b"},
	{"C0", "a\x00b\x07c\x08d\x7fe\x0ef\x0fg\x1ah", "abcdefgh"},
	{"a carriage return and a line feed", "a\r\nb", "a b"},
	{"line feeds", "a\n\n\nb\vc\fd", "a b c d"},
	{"a carriage return, which would write over the line", "the truth\rthe lie", "the truth the lie"},
	{"a backspace, which would write over a character", "rm -rf /\x08\x08\x08\x08\x08\x08\x08\x08ls      ", "rm -rf /ls      "},
	{"a tab", "a\tb", "a       b"},
	{"a tab at the start", "\tb", "        b"},
	{"a tab at a stop", "12345678\tb", "12345678        b"},
	{"a tab behind a wide character", "注\tb", "注      b"},
	{"a byte that is no UTF-8", "a\xffb", "a�b"},
	{"a character cut off", "a\xe2\x82", "a��"},
	{"C1 as a byte", "a\x9b31mb", "a�31mb"},
	{"an overlong ESC", "a\xc0\x9b[2Jb", "a��[2Jb"},
	{"a bidi override", "abc\u202edef\u202c", "abcdef"},
	{"bidi isolates and marks", "\u2066a\u2069\u200eb\u200f\u061cc", "abc"},
	{"zero-width characters", "a\u200bb\u200cc\u200dd\xef\xbb\xbfe\u00adf\u2060g", "abcdefg"},
	{"a family of emoji", "👨\u200d👩\u200d👧", "👨👩👧"},
	{"a variation selector", "✔\ufe0f ok", "✔ ok"},
	{"a skin tone", "👋\U0001f3fd", "👋"},
	{"tag characters", "a\U000e0041\U000e007fb", "ab"},
	{"an accent", "cafe\u0301", "cafe\u0301"},
	{"a hundred accents", "a" + strings.Repeat("\u0301", 100) + "b", "a\u0301\u0301\u0301b"},
	{"an accent on nothing", "\u0301a", "a"},
	{"an accent behind a sequence", "\x1b[31m\u0301a", "a"},
	{"spaces of every kind", "a\u00a0b\u3000c\u2003d\u2028e\u2029f", "a b c d e f"},
	{"a character nobody assigned", "a\u0378b", "a�b"},
	{"a noncharacter", "a\uffffb", "a�b"},
	{"private use", "\ue0b0 main", "\ue0b0 main"},
	{"wide characters", "注文 ｗｉｄｅ 🚀", "注文 ｗｉｄｅ 🚀"},
	{"a flag", "🇩🇪 and 🇪", "DE and E"},
	{"an accent behind something that was taken out", "a\x1b[0m\u0301b\x07\u0301c\u200b\u0301", "abc\u0301"},
	{"a megabyte of titles", strings.Repeat("\x1b]0;x\x07", 1<<17) + "end", "end"},
	{"a megabyte of cursor moves", strings.Repeat("\x1b[9999;9999H", 1<<17) + "end", "end"},
	{"the empty text", "", ""},
}

// Review Focus 2: bytes of a zathras on the operator's terminal. Nothing
// but printable text is left of them, whatever they were.
func TestCleanLeavesNothingATerminalActsOn(t *testing.T) {
	for _, tt := range hostile {
		got := clean(tt.in)
		if got != tt.want {
			t.Errorf("%s: clean(%.60q) = %.80q, want %.80q", tt.name, tt.in, got, tt.want)
		}
		if what := unsafe(got); what != "" {
			t.Errorf("%s: what is left has %s", tt.name, what)
		}
		if again := clean(got); again != got {
			t.Errorf("%s: cleaned twice it is %.80q, and once %.80q", tt.name, again, got)
		}
	}
}

// Whatever is made of these pieces: the result is safe, and cleaning it
// again changes nothing. And for these pieces, which are what the logs
// of the fixture and the demo are made of, the width this package
// counts is the renderer's, by characters as by graphemes. That is not
// so for every script: see TestNoLineIsWiderByTheRenderersCounts.
func TestCleanOfThesePieces(t *testing.T) {
	pieces := []string{
		"\x1b", "[", "]", "P", "_", "^", "X", "\\", "0", "1", "3", "8", ";", ":", "?", "m", "H", "J", " ", "!",
		"\x07", "\x18", "\x00", "\x08", "\t", "\n", "\r", "\x7f", "\u009b", "\u009d", "\u009c", "\u0090", "\x9b", "\xff", "\xe2", "\x82",
		"a", "Z", "é", "e\u0301", "\u0301", "注", "ｗ", "🚀", "👨", "\u200d", "\ufe0f", "\U0001f3fd", "\u202e", "\u00a0", "\u3000", "한", "ก", "\u0e31", "🇩", "🇪",
	}
	rng := rand.New(rand.NewPCG(3, 3))
	for range 20000 {
		var b strings.Builder
		for range 1 + rng.IntN(24) {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		in := b.String()
		got := clean(in)
		if what := unsafe(got); what != "" {
			t.Fatalf("clean(%q) = %q has %s", in, got, what)
		}
		if again := clean(got); again != got {
			t.Fatalf("clean(%q) = %q, and cleaned again %q", in, got, again)
		}
		if w, g, wc := width(got), ansi.StringWidth(got), ansi.StringWidthWc(got); w != g || w != wc {
			t.Fatalf("clean(%q) = %q is %d cells wide, %d by graphemes and %d by characters", in, got, w, g, wc)
		}
		spans, _ := parseLine(in, 1<<20)
		var text strings.Builder
		for _, sp := range spans {
			text.WriteString(sp.text)
		}
		if text.String() != got {
			t.Fatalf("the pieces of %q are %q, and clean says %q", in, text.String(), got)
		}
		if drawn := drawSpans(spans); unsafe(drawn) != "" || plain(drawn) != got {
			t.Fatalf("%q is drawn as %q: %s", in, drawn, unsafe(drawn))
		}
	}
}

func TestWidthCutAndPad(t *testing.T) {
	for _, tt := range []struct {
		in    string
		width int
	}{
		{"", 0}, {"abc", 3}, {"注文", 4}, {"ｗｉｄｅ", 8}, {"cafe\u0301", 4}, {"🚀", 2}, {"a\tb", 9},
		{"\x1b[31mred\x1b[0m", 3}, {"👨\u200d👩\u200d👧", 6},
	} {
		if got := width(tt.in); got != tt.width {
			t.Errorf("width(%q) = %d, want %d", tt.in, got, tt.width)
		}
	}
	for _, tt := range []struct {
		in                        string
		w                         int
		cut, middle, pad, padLeft string
	}{
		{"web-0", 8, "web-0", "web-0", "web-0   ", "   web-0"},
		{"web-7d9f8b6c4-2xkqp", 10, "web-7d9f8…", "web-7…xkqp", "web-7d9f8…", "web-7d9f8…"},
		{"web-7d9f8b6c4-2xkqp", 4, "web…", "web…", "web…", "web…"},
		{"web", 1, "…", "…", "…", "…"},
		{"web", 0, "", "", "", ""},
		{"web", -3, "", "", "", ""},
		{"注文を受け付けました", 7, "注文を…", "注文…た", "注文を…", "注文を…"},
		{"注文", 3, "注…", "注…", "注…", "注…"},
		{"a\x1b]0;t\x07b\x1b[2J", 5, "ab", "ab", "ab   ", "   ab"},
		{"cafe\u0301s au lait", 6, "cafe\u0301s…", "caf…it", "cafe\u0301s…", "cafe\u0301s…"},
	} {
		if got := cut(tt.in, tt.w); got != tt.cut {
			t.Errorf("cut(%q, %d) = %q, want %q", tt.in, tt.w, got, tt.cut)
		}
		if got := middle(tt.in, tt.w); got != tt.middle {
			t.Errorf("middle(%q, %d) = %q, want %q", tt.in, tt.w, got, tt.middle)
		}
		if got := pad(tt.in, tt.w); got != tt.pad {
			t.Errorf("pad(%q, %d) = %q, want %q", tt.in, tt.w, got, tt.pad)
		}
		if got := padLeft(tt.in, tt.w); got != tt.padLeft {
			t.Errorf("padLeft(%q, %d) = %q, want %q", tt.in, tt.w, got, tt.padLeft)
		}
		for _, got := range []string{cut(tt.in, tt.w), middle(tt.in, tt.w), pad(tt.in, tt.w), padLeft(tt.in, tt.w)} {
			if width(got) > max(tt.w, 0) {
				t.Errorf("%q is wider than %d", got, tt.w)
			}
		}
	}
	// A wide character that does not fit leaves its cell empty: the
	// padded text is as wide as was asked for.
	if got := pad("注文", 3); width(got) != 3 {
		t.Errorf("pad(注文, 3) = %q, %d cells wide", got, width(got))
	}
}

func TestWrap(t *testing.T) {
	reason := "web-7d9f8b6c4-8hjwz: could not be started: fork/exec /nix/store/abc-web/bin/web: exec format error"
	for _, tt := range []struct {
		in      string
		w, rows int
		want    []string
	}{
		{"", 10, 0, nil},
		{"one", 10, 0, []string{"one"}},
		{reason, 40, 0, []string{"web-7d9f8b6c4-8hjwz: could not be", "started: fork/exec", "/nix/store/abc-web/bin/web: exec format", "error"}},
		{reason, 40, 2, []string{"web-7d9f8b6c4-8hjwz: could not be", "started: fork/exec…"}},
		{"abcdefghijklmnop", 5, 0, []string{"abcde", "fghij", "klmno", "p"}},
		{"ab cdefghijklmnop q", 5, 0, []string{"ab", "cdefg", "hijkl", "mnop", "q"}},
		{"a\nb\x1b[2Jc  d", 10, 0, []string{"a bc d"}},
		{"注文を受け付けました", 5, 0, []string{"注文", "を受", "け付", "けま", "した"}},
		{"注文", 1, 0, nil},
		{"abc", 0, 0, nil},
		{"one two three", 5, 1, []string{"one…"}},
		{"12345 12345", 5, 1, []string{"1234…"}},
	} {
		got := wrap(tt.in, tt.w, tt.rows)
		if !slices.Equal(got, tt.want) {
			t.Errorf("wrap(%q, %d, %d) = %q, want %q", tt.in, tt.w, tt.rows, got, tt.want)
		}
		for _, line := range got {
			if width(line) > tt.w {
				t.Errorf("wrap(%q, %d, %d): %q is wider than %d", tt.in, tt.w, tt.rows, line, tt.w)
			}
		}
	}
}

func TestParseLineReadsColours(t *testing.T) {
	red, green := uint8(1+1), uint8(2+1)
	for _, tt := range []struct {
		name, in string
		want     []span
	}{
		{"no colour", "plain", []span{{"plain", look{}}}},
		{"the colours of a service", "\x1b[2m12:00:00\x1b[0m \x1b[1;31mERROR\x1b[0m failed", []span{
			{"12:00:00", look{dim: true}}, {" ", look{}}, {"ERROR", look{fg: red, bold: true}}, {" failed", look{}},
		}},
		{"ESC [ m is a reset", "\x1b[32mok\x1b[m done", []span{{"ok", look{fg: green}}, {" done", look{}}}},
		{"an empty number is 0", "\x1b[32mok\x1b[;1mbold", []span{{"ok", look{fg: green}}, {"bold", look{bold: true}}}},
		{"a look that is never reset ends with the line", "\x1b[32mok", []span{{"ok", look{fg: green}}}},
		{"the bright colours and the grounds", "\x1b[91;44ma\x1b[39;102mb\x1b[49mc", []span{
			{"a", look{fg: 9 + 1, bg: 4 + 1}}, {"b", look{bg: 10 + 1}}, {"c", look{}},
		}},
		{"what is switched off again", "\x1b[1;2;3;4;7ma\x1b[22mb\x1b[23;24;27mc", []span{
			{"a", look{bold: true, dim: true, italic: true, underline: true, inverse: true}},
			{"b", look{italic: true, underline: true, inverse: true}}, {"c", look{}},
		}},
		{"one of 256: the sixteen are themselves", "\x1b[38;5;1ma\x1b[48;5;10mb", []span{{"a", look{fg: red}}, {"b", look{fg: red, bg: 10 + 1}}}},
		{"one of 256: the cube is the nearest of sixteen", "\x1b[38;5;196ma\x1b[38;5;28mb\x1b[38;5;240mc", []span{
			{"a", look{fg: 9 + 1}}, {"b", look{fg: green}}, {"c", look{fg: 8 + 1}},
		}},
		{"RGB is the nearest of sixteen", "\x1b[38;2;255;135;0ma\x1b[38;2;0;0;128mb\x1b[48;2;250;250;250mc", []span{
			{"a", look{fg: 11 + 1}}, {"b", look{fg: 4 + 1}}, {"c", look{fg: 4 + 1, bg: 15 + 1}},
		}},
		{"with colons", "\x1b[38:5:196ma", []span{{"a", look{fg: 9 + 1}}}},
		{"a colour of 256 that is cut off", "\x1b[38;5ma\x1b[1;38;2;1;2mb", []span{{"a", look{}}, {"b", look{bold: true}}}},
		{"numbers nobody knows", "\x1b[5;53;999;73ma", []span{{"a", look{}}}},
		{"numbers too large and too many", "\x1b[99999ma\x1b[" + strings.Repeat("1;", 40) + "mb", []span{{"ab", look{}}}},
		{"a private sequence that ends in m", "\x1b[?1ma\x1b[>4;2mb", []span{{"ab", look{}}}},
		{"a sequence that is no colour between", "\x1b[31ma\x1b[2Kb\x1b]0;t\x07c", []span{{"abc", look{fg: red}}}},
		{"C1", "\u009b31ma", []span{{"a", look{fg: red}}}},
		{"nothing printable", "\x1b[31m\x1b[0m", nil},
	} {
		got, cut := parseLine(tt.in, 1000)
		// Pieces of one look that follow each other are one piece to the reader.
		var merged []span
		for _, sp := range got {
			if n := len(merged); n > 0 && merged[n-1].look == sp.look {
				merged[n-1].text += sp.text
			} else {
				merged = append(merged, sp)
			}
		}
		if !slices.Equal(merged, tt.want) || cut {
			t.Errorf("%s: parseLine(%q) = %+v (cut: %v), want %+v", tt.name, tt.in, merged, cut, tt.want)
		}
	}
}

// A line as long as can be costs what is drawn of it, and no more is
// kept of it.
func TestParseLineStopsAtItsLimit(t *testing.T) {
	long := "\x1b[32m" + strings.Repeat("0123456789", 1<<20/10)
	spans, cut := parseLine(long, 200)
	if !cut || spansWidth(spans) != 200 || len(spans) != 1 || spans[0].look.fg != 3 {
		t.Errorf("a megabyte in 200 cells: %d cells in %d pieces, cut: %v", spansWidth(spans), len(spans), cut)
	}
	spans, cut = parseLine(strings.Repeat("注", 1000), 201)
	if !cut || spansWidth(spans) != 200 {
		t.Errorf("wide characters in 201 cells: %d cells, cut: %v; want 200, as half a character is none", spansWidth(spans), cut)
	}
	spans, cut = parseLine("exactly", 7)
	if cut || spansWidth(spans) != 7 {
		t.Errorf("a line of 7 cells in 7: %d cells, cut: %v", spansWidth(spans), cut)
	}
	if spans, cut = parseLine("exactly\x1b[0m\x1b]0;t\x07", 7); cut || spansWidth(spans) != 7 {
		t.Errorf("a line of 7 cells and sequences behind them in 7: %d cells, cut: %v", spansWidth(spans), cut)
	}
	if spans, cut = parseLine("anything", 0); !cut || len(spans) != 0 {
		t.Errorf("a line in 0 cells: %+v, cut: %v", spans, cut)
	}
}

func TestWrapSpans(t *testing.T) {
	red := look{fg: 2}
	texts := func(rows [][]span) []string {
		var out []string
		for _, row := range rows {
			var b strings.Builder
			for _, sp := range row {
				b.WriteString(sp.text)
			}
			out = append(out, b.String())
		}
		return out
	}
	for _, tt := range []struct {
		name    string
		in      []span
		w, rows int
		want    []string
		cut     bool
	}{
		{"nothing is one empty row", nil, 10, 4, []string{""}, false},
		{"what fits", []span{{"abc", red}, {"def", look{}}}, 10, 4, []string{"abcdef"}, false},
		{"what fills the row", []span{{"abcde", red}}, 5, 4, []string{"abcde"}, false},
		{"over two rows, through a piece", []span{{"abc", red}, {"defgh", look{}}}, 5, 4, []string{"abcde", "fgh"}, false},
		{"more rows than there may be", []span{{"abcdefghijkl", red}}, 5, 2, []string{"abcde", "fghij"}, true},
		{"exactly the rows there may be", []span{{"abcdefghij", red}}, 5, 2, []string{"abcde", "fghij"}, false},
		{"a wide character does not straddle", []span{{"ab注文", red}}, 3, 4, []string{"ab", "注", "文"}, false},
		{"a wide character and one cell", []span{{"注", red}}, 1, 4, nil, true},
		{"no room", []span{{"a", red}}, 0, 4, nil, true},
	} {
		got, cut := wrapSpans(tt.in, tt.w, tt.rows)
		if !slices.Equal(texts(got), tt.want) || cut != tt.cut {
			t.Errorf("%s: %q (cut: %v), want %q (cut: %v)", tt.name, texts(got), cut, tt.want, tt.cut)
		}
	}
	rows, _ := wrapSpans([]span{{"abc", red}, {"defgh", look{}}}, 5, 4)
	if len(rows) != 2 || len(rows[0]) != 2 || rows[0][0] != (span{"abc", red}) || rows[0][1] != (span{"de", look{}}) || rows[1][0] != (span{"fgh", look{}}) {
		t.Errorf("the pieces keep their looks over a break: %+v", rows)
	}
}

// Review Focus 2: a line of a log is drawn in colours the terminal UI
// makes, and nothing of the line's own sequences is in what is drawn.
func TestDrawSpansWritesItsOwnSequences(t *testing.T) {
	line := "\x1b]0;pwned\x07\x1b[2J\x1b[1;31mERROR\x1b[0m \x1b[38;5;196mdisk\x1b[0m \x1b[5;10Hfull\x1b[?1049l"
	spans, _ := parseLine(line, 1000)
	drawn := drawSpans(spans)
	if got := plain(drawn); got != "ERROR disk full" {
		t.Errorf("drawn, a person reads %q", got)
	}
	if what := unsafe(drawn); what != "" {
		t.Errorf("what is drawn has %s: %q", what, drawn)
	}
	if !strings.Contains(drawn, "\x1b[") || strings.Contains(drawn, "pwned") || strings.Contains(drawn, "38;5") || strings.Contains(drawn, "2J") {
		t.Errorf("what is drawn is %q: want colours of the sixteen, and nothing of the title", drawn)
	}
	if got := drawSpans([]span{{"plain", look{}}}); got != "plain" {
		t.Errorf("text without a look is drawn as %q", got)
	}
	// Every colour of the sixteen is one of the terminal's palette: 30
	// to 37 and 90 to 97, never a colour by its number or its parts.
	for c := range uint8(16) {
		drawn := drawSpans([]span{{"x", look{fg: c + 1}}})
		want := fmt.Sprintf("\x1b[%dm", 30+int(c))
		if c >= 8 {
			want = fmt.Sprintf("\x1b[%dm", 90+int(c)-8)
		}
		if !strings.HasPrefix(drawn, want) || plain(drawn) != "x" {
			t.Errorf("colour %d is drawn as %q, want it to begin with %q", c, drawn, want)
		}
	}
}

func FuzzClean(f *testing.F) {
	for _, tt := range hostile {
		if len(tt.in) < 4096 {
			f.Add(tt.in)
		}
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := clean(in)
		if what := unsafe(got); what != "" {
			t.Fatalf("clean(%q) = %q has %s", in, got, what)
		}
		if again := clean(got); again != got {
			t.Fatalf("clean(%q) = %q, and cleaned again %q", in, got, again)
		}
		spans, _ := parseLine(in, 1<<20)
		if drawn := drawSpans(spans); unsafe(drawn) != "" || plain(drawn) != got {
			t.Fatalf("%q is drawn as %q: %s", in, drawn, unsafe(drawn))
		}
	})
}
```

- [ ] **Step 7: Run the test to see it fail**

Run: `nix develop .#bare --command go test ./tui/`
Expected: FAIL, the package does not build:

```
# github.com/fables-for-robots/great-machine/tui [github.com/fables-for-robots/great-machine/tui.test]
tui/text_test.go:135:10: undefined: clean
tui/text_test.go:142:15: undefined: clean
tui/text_test.go:166:10: undefined: clean
tui/text_test.go:170:15: undefined: clean
tui/text_test.go:173:18: undefined: width
tui/text_test.go:176:15: undefined: parseLine
tui/text_test.go:184:15: undefined: drawSpans
tui/text_test.go:198:13: undefined: width
tui/text_test.go:218:13: undefined: cut
tui/text_test.go:221:13: undefined: middle
tui/text_test.go:221:13: too many errors
FAIL	github.com/fables-for-robots/great-machine/tui [build failed]
FAIL
```

- [ ] **Step 8: Write the implementation**

Create `tui/text.go`:

```go
package tui

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Everything draal or a zathras sent is text of someone else: a name, a
// phase, a reason, a refusal, a line of a log. None of it reaches the
// terminal as it came. All of it goes through scan, which lets printable
// characters through and nothing else: no escape sequence, no control
// character. A line of a log keeps its colours by being read: scan
// reports what its SGR sequences ask for, and the terminal UI draws the
// text again in colours of its own making.
//
// What scan lets through, and what it makes of the rest:
//
//   - An escape sequence is taken out whole: CSI (ESC [ … final), the
//     strings OSC, DCS, SOS, PM and APC up to their terminator (BEL, ST,
//     CAN, SUB) or, without one, to the end of the text, and any other
//     sequence of ESC, intermediates and a final byte. One that is cut
//     off is taken out as far as it goes, and a lone ESC by itself. The
//     C1 forms of the same (U+009B, U+0090, U+009D, …) likewise.
//   - A tab is spaces up to the next column that is a multiple of 8.
//   - A line feed, a carriage return and their like are one space: the
//     words of two lines stay two words.
//   - Every other control character (C0, DEL, C1) is left out.
//   - A byte that is no UTF-8 is drawn as U+FFFD, as is a character that
//     Unicode has not assigned.
//   - Characters that draw nothing and move things are left out: the
//     bidi marks and overrides, with which a line could show another
//     order than it has, the zero-width space and joiners, the byte
//     order mark, the soft hyphen, the tag characters (all of the class
//     Cf), the variation selectors and the skin tones of emoji. Without
//     them a family of emoji is its members side by side, and every
//     terminal counts the same width for what is left.
//   - A regional indicator is its letter: two of them are a flag in one
//     terminal and two boxes in another, of a width nobody agrees on.
//   - A combining mark is kept on the character before it, three at
//     most: a letter can have its accents, and cannot be buried under a
//     hundred. One with nothing before it, or behind something that was
//     taken out, is left out.
//   - Any kind of space is the space.

// marksKept is how many combining marks a character keeps.
const marksKept = 3

// tabStop is how far apart the columns are that a tab moves to.
const tabStop = 8

// look is how a piece of text is drawn. The zero look is the terminal's
// own.
type look struct {
	fg, bg    uint8 // 1 to 16 for the terminal's colours 0 to 15; 0 for its default
	bold      bool
	dim       bool
	italic    bool
	underline bool
	inverse   bool
}

// span is a piece of text with one look. Its text is clean: it went
// through scan.
type span struct {
	text string
	look look
}

// runeWidth returns how many cells a printable character takes: 2 for
// the wide ones, 0 for a mark that sits on the character before it. It
// is the width the renderer counts for the characters of the scripts
// the terminal UI is tested with; where a script joins its characters
// in ways this does not know, a column may stand a cell off, and that
// is all: the renderer puts every character into a cell of its own
// making.
func runeWidth(r rune) int {
	switch {
	case r < 0x7f:
		return 1
	case unicode.Is(unicode.M, r):
		return 0
	}
	return min(ansi.StringWidth(string(r)), 2)
}

// dropped says whether r is a character that draws nothing and is left
// out: see the list above.
func dropped(r rune) bool {
	switch {
	case unicode.Is(unicode.Cf, r):
		return true
	case r >= 0xfe00 && r <= 0xfe0f, r >= 0xe0100 && r <= 0xe01ef: // variation selectors
		return true
	case r >= 0x1f3fb && r <= 0x1f3ff: // skin tones
		return true
	}
	return false
}

// scanner is one run of scan.
type scanner struct {
	limit int // cells after which it stops; below 0 for never
	col   int // cells so far
	marks int // marks on the last character
	base  bool
	space bool // the last thing written was the space of a line's end
	run   strings.Builder
	text  func(string)
	sgr   func(params []int)
}

// full says whether the limit is reached.
func (sc *scanner) full() bool {
	return sc.limit >= 0 && sc.col >= sc.limit
}

// put writes a character of the given width, if there is room for it.
func (sc *scanner) put(r rune, width int) {
	if sc.limit >= 0 && sc.col+width > sc.limit {
		sc.col = sc.limit
		return
	}
	sc.run.WriteRune(r)
	sc.col += width
	sc.marks, sc.base, sc.space = 0, true, false
}

// flush hands the text gathered so far to the caller.
func (sc *scanner) flush() {
	if sc.run.Len() > 0 {
		sc.text(sc.run.String())
		sc.run.Reset()
	}
}

// scan reads s as the terminal would, and calls text with every run of
// what is printable and, if sgr is set, sgr with the numbers of every
// SGR sequence, in order. It stops when it has let limit cells through;
// a limit below zero is none. Its time is linear in what it reads.
func scan(s string, limit int, text func(string), sgr func(params []int)) {
	sc := scanner{limit: limit, text: text, sgr: sgr}
	for i := 0; i < len(s) && !sc.full(); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n <= 1 {
			sc.put(utf8.RuneError, 1)
			i++
			continue
		}
		i += n
		switch {
		case r == 0x1b:
			i = sc.escape(s, i)
			sc.base = false
		case r == 0x9b:
			i = sc.csi(s, i)
			sc.base = false
		case r == 0x90, r == 0x98, r == 0x9d, r == 0x9e, r == 0x9f:
			i = endOfString(s, i)
			sc.base = false
		case r == '\t':
			for range tabStop - sc.col%tabStop {
				sc.put(' ', 1)
			}
		case r == '\n', r == '\r', r == '\v', r == '\f', r == 0x85, r == 0x2028, r == 0x2029:
			if !sc.space {
				sc.put(' ', 1)
				sc.space = true
			}
		case r < 0x20, r >= 0x7f && r <= 0x9f:
			// A control character: nothing.
			sc.base = false
		case dropped(r):
		case r >= 0x1f1e6 && r <= 0x1f1ff:
			sc.put('A'+r-0x1f1e6, 1) // a regional indicator: its letter
		case unicode.Is(unicode.Zs, r):
			sc.put(' ', 1)
		case unicode.Is(unicode.Co, r):
			sc.put(r, 1) // private use: the glyphs of a font, one cell each
		case !unicode.IsPrint(r):
			sc.put(utf8.RuneError, 1)
		default:
			w := runeWidth(r)
			if w > 0 {
				sc.put(r, w)
			} else if sc.base && sc.marks < marksKept {
				sc.run.WriteRune(r)
				sc.marks++
			}
		}
	}
	sc.flush()
}

// escape reads what follows an ESC at s[i:] and returns where the text
// goes on.
func (sc *scanner) escape(s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[':
		return sc.csi(s, i+1)
	case ']', 'P', 'X', '^', '_':
		return endOfString(s, i+1)
	}
	// Any other sequence: intermediates and one final byte.
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
		i++
	}
	return i
}

// csi reads a control sequence whose introducer ended before s[i:], and
// returns where the text goes on. One that sets colours (its final byte
// is m) is reported; one that is cut off is taken out as far as it goes.
func (sc *scanner) csi(s string, i int) int {
	start := i
	for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3f {
		i++
	}
	params := s[start:i]
	intermediates := false
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
		intermediates = true
	}
	if i >= len(s) || s[i] < 0x40 || s[i] > 0x7e {
		return i
	}
	if s[i] == 'm' && !intermediates && sc.sgr != nil {
		if numbers, ok := sgrNumbers(params); ok {
			sc.flush()
			sc.sgr(numbers)
		}
	}
	return i + 1
}

// sgrNumbers reads the parameters of an SGR sequence: numbers with ; or
// : between them, an empty one being 0. More than 32 numbers, or
// anything else among them, is no SGR sequence the terminal UI reads.
func sgrNumbers(params string) ([]int, bool) {
	numbers := make([]int, 0, 4)
	n, digits := 0, 0
	for i := 0; i <= len(params); i++ {
		switch {
		case i == len(params) || params[i] == ';' || params[i] == ':':
			if len(numbers) == 32 {
				return nil, false
			}
			numbers = append(numbers, n)
			n, digits = 0, 0
		case params[i] >= '0' && params[i] <= '9':
			if digits++; digits > 4 {
				return nil, false
			}
			n = n*10 + int(params[i]-'0')
		default:
			return nil, false
		}
	}
	return numbers, true
}

// endOfString returns where the text goes on after a string sequence
// (OSC, DCS, SOS, PM, APC) whose introducer ended before s[i:]: behind
// its terminator, at an ESC that begins something else, or at the end.
func endOfString(s string, i int) int {
	for ; i < len(s); i++ {
		switch s[i] {
		case 0x07, 0x18, 0x1a:
			return i + 1
		case 0x1b:
			if i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
			return i
		case 0xc2:
			if i+1 < len(s) && s[i+1] == 0x9c { // ST as a C1 character
				return i + 2
			}
		}
	}
	return i
}

// clean returns s as it may be drawn: see scan.
func clean(s string) string {
	// Most of what is drawn is plain already.
	plain := true
	for i := 0; i < len(s) && plain; i++ {
		plain = s[i] >= 0x20 && s[i] < 0x7f
	}
	if plain {
		return s
	}
	var b strings.Builder
	scan(s, -1, func(t string) { b.WriteString(t) }, nil)
	return b.String()
}

// fieldCap is how many bytes are kept of something draal or a zathras
// said in words: a name, a reason, an argument, a refusal. What a
// person reads of one is some lines; a megabyte of it would be cleaned
// and wrapped anew with every message.
const fieldCap = 4 << 10

// capped returns s, or its first fieldCap bytes and how many more there
// were. It is applied where the words come in; what is drawn of them
// still goes through clean.
func capped(s string) string {
	if len(s) <= fieldCap || len(s) <= fieldCap+48 && strings.HasSuffix(s, " bytes more)") {
		return s // short, or capped before
	}
	keep := fieldCap
	for keep > 0 && !utf8.RuneStart(s[keep]) {
		keep--
	}
	return s[:keep] + fmt.Sprintf(" … (%d bytes more)", len(s)-keep)
}

// named returns a name, clean, or words that say that there is none: a
// name of which nothing printable is left would be a hole in a line.
func named(s string) string {
	if s = clean(capped(s)); strings.TrimSpace(s) != "" {
		return s
	}
	return "(no name)"
}

// width returns how many cells s takes once it is clean.
func width(s string) int {
	n := 0
	for _, r := range clean(s) {
		n += runeWidth(r)
	}
	return n
}

// cutClean returns the start of the clean text s that fits into w
// cells, and how many cells it takes.
func cutClean(s string, w int) (string, int) {
	n := 0
	for i, r := range s {
		rw := runeWidth(r)
		if n+rw > w {
			return s[:i], n
		}
		n += rw
	}
	return s, n
}

// tailClean returns the end of the clean text s that fits into w cells,
// and how many cells it takes. It begins with a character, not with the
// marks of one that was cut away.
func tailClean(s string, w int) (string, int) {
	n, from := 0, len(s)
	for i := len(s); i > 0; {
		r, size := utf8.DecodeLastRuneInString(s[:i])
		rw := runeWidth(r)
		if n+rw > w {
			break
		}
		i -= size
		n += rw
		if rw > 0 {
			from = i
		}
	}
	return s[from:], n
}

// ellipsis stands where text was cut.
const ellipsis = "…"

// cut returns s, clean, in at most w cells: what is longer ends in an
// ellipsis.
func cut(s string, w int) string {
	s = clean(s)
	if w <= 0 {
		return ""
	}
	if all, _ := cutClean(s, w); len(all) == len(s) {
		return s
	}
	head, _ := cutClean(s, w-1)
	return head + ellipsis
}

// middle returns s, clean, in at most w cells: of what is longer the
// start and the end are kept, with an ellipsis between them. The names
// of the zathrases of one service differ at their ends.
func middle(s string, w int) string {
	s = clean(s)
	if w <= 0 {
		return ""
	}
	if all, _ := cutClean(s, w); len(all) == len(s) {
		return s
	}
	if w < 5 {
		head, _ := cutClean(s, w-1)
		return head + ellipsis
	}
	tail, back := tailClean(s, (w-1)/2)
	head, _ := cutClean(s, w-1-back)
	return head + ellipsis + tail
}

// pad returns s, clean, in exactly w cells: cut as cut does, or with
// spaces behind it.
func pad(s string, w int) string {
	s = cut(s, w)
	if n := w - width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// padLeft returns s, clean, in exactly w cells: cut as cut does, or with
// spaces before it.
func padLeft(s string, w int) string {
	s = cut(s, w)
	if n := w - width(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

// wrap returns s, clean, as lines of at most w cells, broken at spaces
// where there are any, and at most rows of them: what does not fit ends
// in an ellipsis. No rows is no limit.
func wrap(s string, w, rows int) []string {
	if w <= 0 {
		return nil
	}
	var lines []string
	line, used := "", 0
	end := func() {
		lines = append(lines, line)
		line, used = "", 0
	}
	for _, word := range strings.Fields(clean(s)) {
		ww := width(word)
		if used > 0 && used+1+ww > w {
			end()
		}
		if used > 0 {
			line += " "
			used++
		}
		for ww > w-used { // a word longer than a line is broken
			head, n := cutClean(word, w-used)
			if n == 0 && used > 0 {
				end()
				continue
			}
			if n == 0 {
				// A wide character and one cell: nothing fits, ever.
				return lines
			}
			line += head
			word, ww = word[len(head):], ww-n
			end()
		}
		line += word
		used += ww
	}
	if used > 0 {
		end()
	}
	if rows > 0 && len(lines) > rows {
		lines = lines[:rows]
		last, _ := cutClean(lines[rows-1], w-1)
		lines[rows-1] = last + ellipsis
	}
	return lines
}

// apply returns the look after an SGR sequence with these numbers.
func (l look) apply(numbers []int) look {
	for i := 0; i < len(numbers); i++ {
		switch n := numbers[i]; {
		case n == 0:
			l = look{}
		case n == 1:
			l.bold = true
		case n == 2:
			l.dim = true
		case n == 3:
			l.italic = true
		case n == 4:
			l.underline = true
		case n == 7:
			l.inverse = true
		case n == 22:
			l.bold, l.dim = false, false
		case n == 23:
			l.italic = false
		case n == 24:
			l.underline = false
		case n == 27:
			l.inverse = false
		case n >= 30 && n <= 37:
			l.fg = uint8(n - 30 + 1)
		case n == 39:
			l.fg = 0
		case n >= 40 && n <= 47:
			l.bg = uint8(n - 40 + 1)
		case n == 49:
			l.bg = 0
		case n >= 90 && n <= 97:
			l.fg = uint8(n - 90 + 8 + 1)
		case n >= 100 && n <= 107:
			l.bg = uint8(n - 100 + 8 + 1)
		case n == 38 || n == 48:
			// 38;5;N is one of 256 colours, 38;2;R;G;B one of all: the
			// terminal UI draws the nearest of the terminal's sixteen.
			var c uint8
			switch {
			case i+2 < len(numbers) && numbers[i+1] == 5:
				c = nearest256(numbers[i+2])
				i += 2
			case i+4 < len(numbers) && numbers[i+1] == 2:
				c = nearestRGB(numbers[i+2], numbers[i+3], numbers[i+4])
				i += 4
			default:
				// No form the terminal UI knows: the rest of the
				// sequence is not guessed at.
				return l
			}
			if n == 38 {
				l.fg = c
			} else {
				l.bg = c
			}
		}
		// Any other number (blinking, fonts, …) changes nothing here.
	}
	return l
}

// cube is the steps of the colour cube of the 256 colours.
var cube = [6]int{0, 95, 135, 175, 215, 255}

// nearest256 returns the one of the terminal's sixteen colours, as a
// look has them, that is nearest to the colour n of 256.
func nearest256(n int) uint8 {
	switch {
	case n < 0 || n > 255:
		return 0
	case n < 16:
		return uint8(n + 1)
	case n < 232:
		c := n - 16
		return nearestRGB(cube[c/36], cube[c/6%6], cube[c%6])
	}
	grey := 8 + (n-232)*10
	return nearestRGB(grey, grey, grey)
}

// nearestRGB returns the one of the terminal's sixteen colours, as a
// look has them, that is nearest to a colour by its parts: the colour
// whose parts are on where these are above the middle, the bright one of
// it where one of them is near the top, and the greys by how light they
// are.
func nearestRGB(r, g, b int) uint8 {
	r, g, b = min(max(r, 0), 255), min(max(g, 0), 255), min(max(b, 0), 255)
	top, bottom := max(r, g, b), min(r, g, b)
	if top-bottom < 32 { // a grey
		switch {
		case top < 64:
			return 0 + 1 // black
		case top < 128:
			return 8 + 1 // bright black
		case top < 208:
			return 7 + 1 // white
		}
		return 15 + 1 // bright white
	}
	half := top / 2
	c := 0
	if r > half {
		c |= 1
	}
	if g > half {
		c |= 2
	}
	if b > half {
		c |= 4
	}
	if top >= 224 {
		c += 8
	}
	return uint8(c + 1)
}

// parseLine reads a line of a log into its pieces: the printable text
// of at most limit cells, each piece with the look its SGR sequences
// asked for. cut says that the line had more than limit cells.
func parseLine(line string, limit int) (spans []span, cut bool) {
	var l look
	// One cell more than the limit is asked for, to know of a line that
	// is longer.
	scan(line, limit+1, func(t string) {
		spans = append(spans, span{text: t, look: l})
	}, func(numbers []int) {
		l = l.apply(numbers)
	})
	total := 0
	for i := range spans {
		head, n := cutClean(spans[i].text, limit-total)
		if len(head) < len(spans[i].text) {
			spans[i].text = head
			spans = spans[:i+1]
			if head == "" {
				spans = spans[:i]
			}
			return spans, true
		}
		total += n
	}
	return spans, false
}

// spansWidth returns how many cells the pieces take.
func spansWidth(spans []span) int {
	n := 0
	for _, sp := range spans {
		for _, r := range sp.text {
			n += runeWidth(r)
		}
	}
	return n
}

// wrapSpans returns the pieces as rows of at most w cells, broken where
// a row is full, and at most rows of them. cut says that there was more.
func wrapSpans(spans []span, w, rows int) (out [][]span, cut bool) {
	if w <= 0 || rows <= 0 {
		return nil, len(spans) > 0
	}
	var row []span
	used := 0
	for _, sp := range spans {
		text := sp.text
		for text != "" {
			head, n := cutClean(text, w-used)
			if head != "" {
				row = append(row, span{text: head, look: sp.look})
				used += n
				text = text[len(head):]
			}
			if text == "" {
				break
			}
			if used == 0 {
				return out, true // a wide character and one cell
			}
			out = append(out, row)
			row, used = nil, 0
			if len(out) == rows {
				return out, true
			}
		}
	}
	if len(row) > 0 || len(out) == 0 {
		out = append(out, row)
	}
	return out, false
}

// colours are the terminal's sixteen, as Lip Gloss names them.
var colours = [16]ansi.BasicColor{
	lipgloss.Black, lipgloss.Red, lipgloss.Green, lipgloss.Yellow,
	lipgloss.Blue, lipgloss.Magenta, lipgloss.Cyan, lipgloss.White,
	lipgloss.BrightBlack, lipgloss.BrightRed, lipgloss.BrightGreen, lipgloss.BrightYellow,
	lipgloss.BrightBlue, lipgloss.BrightMagenta, lipgloss.BrightCyan, lipgloss.BrightWhite,
}

// style returns the look as a style of Lip Gloss, in the terminal's own
// colours.
func (l look) style() lipgloss.Style {
	st := lipgloss.NewStyle()
	if l.fg > 0 && l.fg <= 16 {
		st = st.Foreground(colours[l.fg-1])
	}
	if l.bg > 0 && l.bg <= 16 {
		st = st.Background(colours[l.bg-1])
	}
	return st.Bold(l.bold).Faint(l.dim).Italic(l.italic).Underline(l.underline).Reverse(l.inverse)
}

// drawSpans draws the pieces, each in its look. Nothing of what it
// returns but the text of the pieces is of the line they were read
// from: the escape sequences are those Lip Gloss writes.
func drawSpans(spans []span) string {
	var b strings.Builder
	for _, sp := range spans {
		if sp.look == (look{}) {
			b.WriteString(sp.text)
			continue
		}
		b.WriteString(sp.look.style().Render(sp.text))
	}
	return b.String()
}
```

```bash
nix develop .#bare --command go mod tidy
```

`go.mod` now has, in its first `require` block, `charm.land/lipgloss/v2 v2.0.6` and `github.com/charmbracelet/x/ansi v0.11.8`.

- [ ] **Step 9: Run the tests to see them pass**

Run: `nix develop .#bare --command go test ./tui/ -v -run 'Clean|Width|Wrap|ParseLine|DrawSpans'`
Expected: PASS: `TestCleanLeavesNothingATerminalActsOn`, `TestCleanOfThesePieces`, `TestWidthCutAndPad`, `TestWrap`, `TestParseLineReadsColours`, `TestParseLineStopsAtItsLimit`, `TestWrapSpans`, `TestDrawSpansWritesItsOwnSequences`, `FuzzClean` (its seeds)

And half a minute of fuzzing, which finds nothing (about a million inputs on a laptop):

Run: `nix develop .#bare --command go test ./tui/ -run XXX -fuzz FuzzClean -fuzztime 30s`
Expected: ends with `PASS`. Should it find something, it writes the input to `tui/testdata/fuzz/FuzzClean/`: that is a hole in `scan`, to be closed before anything is drawn with it.

- [ ] **Step 10: Commit**

```bash
git add go.mod go.sum tui/text.go tui/text_test.go
git commit -m "TUI: text of a zathras is drawn through one function that lets printable characters through and nothing else, and a line of a log keeps its colours by being read"
```

- [ ] **Step 11: Write the failing test of the sparklines**

Create `tui/spark_test.go`:

```go
package tui

import (
	"math"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// sampleStart is where the samples of the tests begin.
var sampleStart = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// cpuAt returns a sample of so many cores, so many seconds after
// sampleStart.
func cpuAt(seconds int, cores float64) wire.Sample {
	return wire.Sample{At: sampleStart.Add(time.Duration(seconds) * time.Second), CPU: cores}
}

func cpuOf(s wire.Sample) float64 { return s.CPU }

func TestMergeSamples(t *testing.T) {
	seconds := func(samples []wire.Sample) []int {
		var out []int
		for _, s := range samples {
			out = append(out, int(s.At.Sub(sampleStart)/time.Second))
		}
		return out
	}
	equal := func(a, b []int) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	if got := mergeSamples(nil, nil, sampleStart.Add(time.Hour)); got != nil {
		t.Errorf("nothing and nothing is %v", got)
	}
	// In the order of time, whatever order they came in.
	got := mergeSamples([]wire.Sample{cpuAt(4, 1), cpuAt(0, 1)}, []wire.Sample{cpuAt(2, 1), cpuAt(6, 1)}, sampleStart.Add(time.Hour))
	if !equal(seconds(got), []int{0, 2, 4, 6}) {
		t.Errorf("merged: %v", seconds(got))
	}
	// The sample of the fleet is the last one of the history again: one
	// for each time, and the later one stands.
	got = mergeSamples([]wire.Sample{cpuAt(0, 1), cpuAt(2, 1)}, []wire.Sample{cpuAt(2, 7), cpuAt(4, 1)}, sampleStart.Add(time.Hour))
	if !equal(seconds(got), []int{0, 2, 4}) || got[1].CPU != 7 {
		t.Errorf("merged with one of the same time: %v, the second with %v cores", seconds(got), got[1].CPU)
	}
	// None older than the window before the newest; one at its edge stays.
	got = mergeSamples([]wire.Sample{cpuAt(0, 1), cpuAt(2, 1), cpuAt(4, 1)}, []wire.Sample{cpuAt(902, 1)}, sampleStart.Add(time.Hour))
	if !equal(seconds(got), []int{2, 4, 902}) {
		t.Errorf("merged over the window: %v", seconds(got))
	}
	// A sample without a time is none.
	got = mergeSamples([]wire.Sample{{CPU: 3}}, []wire.Sample{cpuAt(2, 1), {}}, sampleStart.Add(time.Hour))
	if !equal(seconds(got), []int{2}) {
		t.Errorf("merged with samples without a time: %v", seconds(got))
	}
	// What was given is not changed.
	have := []wire.Sample{cpuAt(0, 1), cpuAt(2, 1)}
	mergeSamples(have, []wire.Sample{cpuAt(2000, 1)}, sampleStart.Add(time.Hour))
	if len(have) != 2 || have[0].CPU != 1 {
		t.Errorf("the samples that were given are now %v", have)
	}
}

// The times of samples are the zathras' own. A clock that is ahead by
// less than sampleAhead has its window; a sample further ahead is none,
// and cannot empty the window of the others.
func TestMergeSamplesOfAClockThatIsAhead(t *testing.T) {
	now := sampleStart.Add(20 * time.Second)
	var have []wire.Sample
	for i := range 10 {
		have = append(have, cpuAt(2*i, 1))
	}
	got := mergeSamples(have, []wire.Sample{{At: now.Add(2 * time.Hour), CPU: 9}, {At: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), CPU: 9}}, now)
	if len(got) != 10 || got[9].CPU != 1 {
		t.Errorf("with a sample two hours ahead and one of the year 9999, %d samples are kept, the newest with %v cores; want the ten there were", len(got), got[len(got)-1].CPU)
	}
	// A zathras whose clock is half an hour ahead: its samples are its
	// figures, windowed by its own newest.
	var ahead []wire.Sample
	for i := range 600 {
		ahead = append(ahead, wire.Sample{At: now.Add(30*time.Minute + time.Duration(2*i)*time.Second - 20*time.Minute), CPU: 2})
	}
	got = mergeSamples(nil, ahead, now)
	if len(got) != 451 || !got[len(got)-1].At.Equal(ahead[599].At) {
		t.Errorf("of a clock half an hour ahead %d samples are kept, want the 451 of its last fifteen minutes", len(got))
	}
}

func TestSpark(t *testing.T) {
	// One sample every 2 seconds for 15 minutes, drawn in 15 cells: a
	// minute each.
	var steady, rising, restarted, paused []wire.Sample
	for i := range 450 {
		steady = append(steady, cpuAt(2*i, 0.5))
		rising = append(rising, cpuAt(2*i, float64(i)/450))
		// The counter of the network starts again in the eighth minute:
		// a rate below zero, which is no figure.
		rate := 100.0
		if i/30 == 7 {
			rate = -4e9
		}
		restarted = append(restarted, wire.Sample{At: sampleStart.Add(time.Duration(2*i) * time.Second), NetRx: rate})
		// Nothing was sampled from the fourth to the sixth minute.
		if i/30 < 3 || i/30 > 5 {
			paused = append(paused, cpuAt(2*i, 0.5))
		}
	}
	netOf := func(s wire.Sample) float64 { return s.NetRx }
	for _, tt := range []struct {
		name    string
		samples []wire.Sample
		pick    func(wire.Sample) float64
		width   int
		top     float64
		want    string
	}{
		{"no samples", nil, cpuOf, 8, 1, "        "},
		{"no width", steady, cpuOf, 0, 1, ""},
		{"a steady half", steady, cpuOf, 15, 1, "▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄"},
		{"rising", rising, cpuOf, 15, 1, "▁▂▂▃▃▄▄▅▅▆▆▇▇██"},
		{"above the top is the top", steady, cpuOf, 5, 0.25, "█████"},
		{"zero is the lowest block, and no gap", []wire.Sample{cpuAt(0, 0), cpuAt(450, 0), cpuAt(898, 0)}, cpuOf, 3, 1, "▁▁▁"},
		{"a top of zero", steady, cpuOf, 3, 0, "▁▁▁"},
		{"a top that is no number", steady, cpuOf, 3, math.NaN(), "▁▁▁"},
		{"a gap is a gap", paused, cpuOf, 15, 1, "▄▄▄   ▄▄▄▄▄▄▄▄▄"},
		{"a counter that started again is a gap, never below the line", restarted, netOf, 15, 100, "███████ ███████"},
		{"a zathras that has just come: its samples are at the right", steady[:60], cpuOf, 15, 1, "             ▄▄"},
		{"one sample", steady[:1], cpuOf, 4, 1, "   ▄"},
	} {
		got := spark(tt.samples, tt.pick, tt.width, tt.top)
		if got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
		if width(got) != tt.width {
			t.Errorf("%s: %d cells wide, want %d", tt.name, width(got), tt.width)
		}
	}
	// Whatever the figures are, a sparkline is blocks and spaces.
	odd := []wire.Sample{cpuAt(0, math.NaN()), cpuAt(2, math.Inf(1)), cpuAt(4, math.Inf(-1)), cpuAt(6, -0.0), cpuAt(8, 1e308), cpuAt(10, -1)}
	for _, r := range spark(odd, cpuOf, sparkWidest, 1) {
		if r != ' ' && (r < '▁' || r > '█') {
			t.Errorf("a sparkline of figures that are none has %q in it", r)
		}
	}
}

func TestTopsOfTheScales(t *testing.T) {
	for _, tt := range []struct{ most, bytes, cores float64 }{
		{0, 1024, 0.1},
		{-5, 1024, 0.1},
		{math.NaN(), 1024, 0.1},
		{0.05, 1024, 0.1},
		{0.1, 1024, 0.1},
		{0.12, 1024, 0.2},
		{0.3, 1024, 0.5},
		{0.75, 1024, 1},
		{1, 1024, 1},
		{2.5, 1024, 5},
		{1025, 2048, 2000},
		{300 << 20, 512 << 20, 500000000},
		{512 << 20, 512 << 20, 1000000000},
		// Figures that are none of a container: the top is the largest
		// there is, and a number.
		{1e308, byteMost, coreMost},
		{math.MaxFloat64, byteMost, coreMost},
		{math.Inf(1), 1024, 0.1},
	} {
		if got := byteTop(tt.most); got != tt.bytes {
			t.Errorf("byteTop(%v) = %v, want %v", tt.most, got, tt.bytes)
		}
		if got := coreTop(tt.most); got != tt.cores {
			t.Errorf("coreTop(%v) = %v, want %v", tt.most, got, tt.cores)
		}
	}
	samples := []wire.Sample{cpuAt(0, 0.2), cpuAt(2, math.NaN()), cpuAt(4, 0.7), cpuAt(6, -3)}
	if got := largest(samples, cpuOf); got != 0.7 {
		t.Errorf("largest = %v, want 0.7", got)
	}
	if got := largest(nil, cpuOf); got != 0 {
		t.Errorf("largest of nothing = %v", got)
	}
}
```

- [ ] **Step 12: Run the test to see it fail**

Run: `nix develop .#bare --command go test ./tui/`
Expected: FAIL, the package does not build:

```
# github.com/fables-for-robots/great-machine/tui [github.com/fables-for-robots/great-machine/tui.test]
tui/spark_test.go:41:12: undefined: mergeSamples
tui/spark_test.go:45:9: undefined: mergeSamples
tui/spark_test.go:51:8: undefined: mergeSamples
tui/spark_test.go:56:8: undefined: mergeSamples
tui/spark_test.go:61:8: undefined: mergeSamples
tui/spark_test.go:67:2: undefined: mergeSamples
tui/spark_test.go:82:9: undefined: mergeSamples
tui/spark_test.go:92:8: undefined: mergeSamples
tui/spark_test.go:139:10: undefined: spark
tui/spark_test.go:149:20: undefined: spark
tui/spark_test.go:149:20: too many errors
FAIL	github.com/fables-for-robots/great-machine/tui [build failed]
FAIL
```

- [ ] **Step 13: Write the implementation**

Create `tui/spark.go`:

```go
package tui

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

const (
	// window is how much of the past the sparklines show: what draal
	// keeps of a zathras.
	window = 15 * time.Minute
	// sparkWidest is how many cells a sparkline has at most: with more, a
	// cell would be less than the time between two samples and a pause
	// of the sampling, and every other cell a gap that is none.
	sparkWidest = 150
	// sampleAhead is how far ahead of the terminal UI's clock the time
	// of a sample may be.
	sampleAhead = time.Hour
	// byteMost and coreMost are the largest tops a scale has: figures
	// beyond them are no figures of a container, and are drawn as the
	// top.
	byteMost = 1 << 60
	coreMost = 1e12
)

// mergeSamples returns the samples a zathras view keeps: those it has
// and those that came, in the order of time, one for each time (a later
// one of the same time takes the place of the earlier), and none older
// than the window before the newest. A sample without a time is left
// out.
//
// The times are the zathras' own, and its clock may differ from the
// one of the terminal UI, which now is. A zathras whose clock is ahead
// by up to sampleAhead has its figures all the same: the window ends at
// its newest sample. A sample further ahead than that is left out: one
// such sample, of a clock that jumped or of a zathras that is wrong,
// would be the newest for as long as it is ahead, and the window before
// it would be empty.
func mergeSamples(have, more []wire.Sample, now time.Time) []wire.Sample {
	latest := now.Add(sampleAhead)
	all := make([]wire.Sample, 0, len(have)+len(more))
	for _, list := range [][]wire.Sample{have, more} {
		for _, s := range list {
			if !s.At.IsZero() && !s.At.After(latest) {
				all = append(all, s)
			}
		}
	}
	if len(all) == 0 {
		return nil
	}
	slices.SortStableFunc(all, func(a, b wire.Sample) int { return a.At.Compare(b.At) })
	out := all[:0]
	for _, s := range all {
		if n := len(out); n > 0 && out[n-1].At.Equal(s.At) {
			out[n-1] = s
			continue
		}
		out = append(out, s)
	}
	from := out[len(out)-1].At.Add(-window)
	first, _ := slices.BinarySearchFunc(out, from, func(s wire.Sample, t time.Time) int { return cmp.Compare(s.At.Compare(t), 0) })
	return slices.Clone(out[first:])
}

// figure says whether v is a figure a sparkline draws: a number that is
// not below zero. A rate below zero is what a counter that started
// again gives (the service was restarted): it is no figure, and the
// line has a gap there, never a block below the line.
func figure(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

// largest returns the largest figure of the samples; 0 when there is
// none.
func largest(samples []wire.Sample, pick func(wire.Sample) float64) float64 {
	top := 0.0
	for _, s := range samples {
		if v := pick(s); figure(v) && v > top {
			top = v
		}
	}
	return top
}

// byteTop returns the top of a sparkline of bytes, or of bytes a second,
// whose largest figure is most: the power of two at or above it, so
// that the scale is said in whole binary units, and 1 KiB at least.
func byteTop(most float64) float64 {
	if !figure(most) || most <= 1024 {
		return 1024
	}
	return min(math.Exp2(math.Ceil(math.Log2(most))), byteMost)
}

// coreTop returns the top of a sparkline of cores whose largest figure
// is most: 1, 2 or 5 times a power of ten at or above it, and a tenth
// of a core at least.
func coreTop(most float64) float64 {
	if !figure(most) || most <= 0.1 {
		return 0.1
	}
	if most >= coreMost {
		return coreMost
	}
	power := math.Pow(10, math.Floor(math.Log10(most)))
	for _, k := range []float64{1, 2, 5} {
		if k*power >= most*(1-1e-12) {
			// 0.1 + 0.2 is not 0.3: the top is rounded to what it stands for.
			return math.Round(k*power*1e9) / 1e9
		}
	}
	return math.Round(10*power*1e9) / 1e9
}

// blocks are what a sparkline is drawn with, from the lowest. The lowest
// is a figure of zero: a cell without a figure is empty.
var blocks = []rune("▁▂▃▄▅▆▇█")

// spark draws one figure of the samples as a line of width cells: the
// window that ends at the newest sample, each cell a part of it, with
// the block for the largest figure of that part on a scale from 0 to
// top. A cell in whose part there is no figure is a space: a gap is a
// gap. What is above top is drawn as the top.
func spark(samples []wire.Sample, pick func(wire.Sample) float64, width int, top float64) string {
	if width <= 0 {
		return ""
	}
	cells := make([]float64, width)
	for i := range cells {
		cells[i] = -1
	}
	if len(samples) > 0 {
		to := samples[len(samples)-1].At
		for _, s := range samples {
			v, back := pick(s), to.Sub(s.At)
			if !figure(v) || back < 0 || back >= window {
				continue
			}
			i := width - 1 - int(int64(back)*int64(width)/int64(window))
			cells[i] = max(cells[i], v)
		}
	}
	var b strings.Builder
	for _, v := range cells {
		switch {
		case v < 0:
			b.WriteByte(' ')
		case !figure(top) || top <= 0 || v <= 0:
			b.WriteRune(blocks[0])
		default:
			level := int(math.Ceil(v/top*float64(len(blocks)))) - 1
			b.WriteRune(blocks[min(max(level, 0), len(blocks)-1)])
		}
	}
	return b.String()
}
```

- [ ] **Step 14: Run the tests to see them pass**

Run: `nix develop .#bare --command go test ./tui/ -v -run 'MergeSamples|Spark|Tops'`
Expected: PASS: `TestMergeSamples`, `TestMergeSamplesOfAClockThatIsAhead`, `TestSpark`, `TestTopsOfTheScales`

- [ ] **Step 15: Commit**

```bash
git add tui/spark.go tui/spark_test.go
git commit -m "TUI: sparklines, with a gap where samples are missing and a scale that is said"
```

- [ ] **Step 16: Write the failing test of where a zathras stands in a rollout**

Create `tui/rollout_test.go`:

```go
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
```

- [ ] **Step 17: Run the test to see it fail**

Run: `nix develop .#bare --command go test ./tui/`
Expected: FAIL, the package does not build:

```
# github.com/fables-for-robots/great-machine/tui [github.com/fables-for-robots/great-machine/tui.test]
tui/rollout_test.go:15:10: undefined: stage
tui/rollout_test.go:17:124: undefined: stageWaiting
tui/rollout_test.go:18:123: undefined: stagePreparing
tui/rollout_test.go:19:112: undefined: stagePrepared
tui/rollout_test.go:20:104: undefined: stageOn
tui/rollout_test.go:21:84: undefined: stageOn
tui/rollout_test.go:22:89: undefined: stageFailed
tui/rollout_test.go:23:85: undefined: stageWaiting
tui/rollout_test.go:24:97: undefined: stagePrepared
tui/rollout_test.go:27:121: undefined: stageOn
tui/rollout_test.go:27:121: too many errors
FAIL	github.com/fables-for-robots/great-machine/tui [build failed]
FAIL
```

- [ ] **Step 18: Write the implementation**

Create `tui/rollout.go`:

```go
package tui

import (
	"slices"
	"time"

	"github.com/fables-for-robots/great-machine/human"
	"github.com/fables-for-robots/great-machine/wire"
)

// What the terminal UI works out from the fleet about a rollout. It
// reads what a zathras reports and nothing else: the fleet has no record
// of a rollout for each zathras, so where a zathras stands in a rollout
// is what its own state says about the rollout's generation. The web
// page reads the same (ui/src/state/rollout.ts), and the two agree.

// stage is where a zathras stands on the way to the generation of a
// rollout.
type stage int

// The stages, in the order of a rollout.
const (
	// stageWaiting: it has not prepared the generation and is not
	// preparing it.
	stageWaiting stage = iota
	// stagePreparing: it is making the closure of the generation present.
	stagePreparing
	// stagePrepared: it has the closure, and runs something else or
	// nothing.
	stagePrepared
	// stageOn: it has taken the generation as its own. Whether draal
	// counts it as activated the terminal UI cannot tell: that is a
	// matter of how long it has run, by a time draal is configured with.
	stageOn
	// stageFailed: it has taken the generation as its own, and the
	// service has ended and waits to be started again.
	stageFailed
)

// stages are all of them, in order.
var stages = []stage{stageWaiting, stagePreparing, stagePrepared, stageOn, stageFailed}

// String returns the words the terminal UI has for a stage.
func (s stage) String() string {
	switch s {
	case stagePreparing:
		return "preparing"
	case stagePrepared:
		return "prepared"
	case stageOn:
		return "on it"
	case stageFailed:
		return "ended on it"
	}
	return "not prepared"
}

// stageOf returns where a zathras that reports st stands on the way to
// the generation target.
//
// One that has taken the generation as its own is on it whatever its
// phase, but for backoff: also one that reports itself idle on it, which
// no zathras does. One that is on another generation, an older or a
// newer one (a rollback goes to an older one), stands where its
// preparing and its prepared put it. And one that prepares the
// generation while it lists it as prepared is preparing: it is fetching
// it once more.
func stageOf(st wire.State, target uint64) stage {
	switch {
	case target == 0:
		return stageWaiting
	case st.Generation == target && st.Phase == wire.PhaseBackoff:
		return stageFailed
	case st.Generation == target:
		return stageOn
	case st.Preparing == target:
		return stagePreparing
	case slices.Contains(st.Prepared, target):
		return stagePrepared
	}
	return stageWaiting
}

// phases are the phases in the order the terminal UI lists them.
var phases = []wire.Phase{wire.PhaseRunning, wire.PhaseStopping, wire.PhaseBackoff, wire.PhaseIdle}

// knownPhase returns the phase, or idle for one the terminal UI does not
// know: it runs nothing that could be named.
func knownPhase(p wire.Phase) wire.Phase {
	switch p {
	case wire.PhaseRunning, wire.PhaseStopping, wire.PhaseBackoff:
		return p
	}
	return wire.PhaseIdle
}

// byPhase counts zathrases by phase.
func byPhase(zs []wire.Zathras) map[wire.Phase]int {
	count := map[wire.Phase]int{}
	for _, z := range zs {
		count[knownPhase(z.State.Phase)]++
	}
	return count
}

// phaseNote returns what there is to say of a phase besides its name:
// for how long, or until when. It is empty when the state says nothing
// of it.
func phaseNote(st wire.State, now time.Time) string {
	switch knownPhase(st.Phase) {
	case wire.PhaseRunning:
		if st.RunningFor <= 0 {
			return ""
		}
		return "for " + human.Age(st.RunningFor)
	case wire.PhaseBackoff:
		switch until := human.Until(st.RestartAt, now); until {
		case human.None:
			return ""
		case "now":
			return "starts again now"
		default:
			return "starts again " + until
		}
	case wire.PhaseStopping:
		return "told to end"
	}
	if st.Generation == 0 {
		return "has no generation"
	}
	return ""
}

// rolloutOpen says whether a rollout is one to show with where every
// zathras stands in it: under way, or halted.
func rolloutOpen(r *wire.RolloutInfo) bool {
	return r != nil && (r.State.UnderWay() || r.State == wire.RolloutHalted)
}
```

- [ ] **Step 19: Run the tests to see them pass**

Run: `nix develop .#bare --command go test ./tui/ -v -run 'StageOf|Phases|RolloutOpen'`
Expected: PASS: `TestStageOf` (with the three cases the tests of the page lacked), `TestPhases`, `TestRolloutOpen`

- [ ] **Step 20: Commit**

```bash
git add tui/rollout.go tui/rollout_test.go
git commit -m "TUI: where a zathras stands in a rollout by its own report"
```

- [ ] **Step 21: Write the failing test of the buffer of a log**

Create `tui/logbuf_test.go`:

```go
package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// logStart is where the lines of the tests begin.
var logStart = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// lineAt returns the n-th line of a log: a millisecond after the one
// before it.
func lineAt(n int) wire.LogLine {
	return wire.LogLine{At: logStart.Add(time.Duration(n) * time.Millisecond), Stream: "stdout", Text: fmt.Sprintf("line %d", n)}
}

// linesFrom returns the lines from to to, the last one left out.
func linesFrom(from, to int) []wire.LogLine {
	var out []wire.LogLine
	for n := from; n < to; n++ {
		out = append(out, lineAt(n))
	}
	return out
}

// textOf returns what a row says.
func textOf(e entry) string {
	switch e.kind {
	case entryLost:
		return fmt.Sprintf("lost %d", e.lost)
	case entryNote:
		return "note: " + e.text
	}
	var b strings.Builder
	for _, sp := range e.spans {
		b.WriteString(sp.text)
	}
	return b.String()
}

func TestLogBufferKeepsTheNewestLines(t *testing.T) {
	b := newLogBuffer()
	if got := b.add(linesFrom(0, 300)); got != 300 || len(b.entries) != 300 {
		t.Fatalf("300 lines: %d added, %d kept", got, len(b.entries))
	}
	b.add(linesFrom(300, 1000))
	if len(b.entries) != logKept || textOf(b.entries[0]) != "line 600" || textOf(b.entries[logKept-1]) != "line 999" {
		t.Errorf("after a thousand lines %d are kept, from %q to %q; want the last %d", len(b.entries), textOf(b.entries[0]), textOf(b.entries[len(b.entries)-1]), logKept)
	}
	// Every row has a number of its own, in order.
	for i := 1; i < len(b.entries); i++ {
		if b.entries[i].seq != b.entries[i-1].seq+1 {
			t.Fatalf("row %d has the number %d behind %d", i, b.entries[i].seq, b.entries[i-1].seq)
		}
	}
	if e := b.entries[0]; e.kind != entryLine || e.stream != "stdout" || !e.at.Equal(logStart.Add(600*time.Millisecond)) || e.cut {
		t.Errorf("the first row is %+v", e)
	}
}

func TestLogBufferHasTheLostMarkerAsARowOfItsOwn(t *testing.T) {
	b := newLogBuffer()
	b.add(linesFrom(0, 2))
	b.add([]wire.LogLine{{At: lineAt(2).At, Lost: 1200}, lineAt(3)})
	var got []string
	for _, e := range b.entries {
		got = append(got, textOf(e))
	}
	if strings.Join(got, "|") != "line 0|line 1|lost 1200|line 3" {
		t.Errorf("the rows are %q", got)
	}
}

func TestLogBufferTakesNoLineTwiceAfterAReconnect(t *testing.T) {
	b := newLogBuffer()
	b.add(linesFrom(0, 50))
	// The log is asked for again: the ring comes once more, and ten
	// lines that are new.
	b.resume()
	if got := b.add(linesFrom(0, 60)); got != 10 {
		t.Errorf("%d lines were added of a ring that repeats 50, want 10", got)
	}
	if len(b.entries) != 60 || textOf(b.entries[49]) != "line 49" || textOf(b.entries[50]) != "line 50" {
		t.Errorf("%d rows; around the seam %q and %q", len(b.entries), textOf(b.entries[49]), textOf(b.entries[50]))
	}
	// And again, with nothing new, and with a lost marker of the past.
	b.resume()
	if got := b.add(append([]wire.LogLine{{At: lineAt(5).At, Lost: 9}}, linesFrom(0, 60)...)); got != 0 {
		t.Errorf("%d rows were added of a ring that has nothing new", got)
	}
	// A buffer that was never resumed takes lines of any time: a zathras
	// whose clock was set back.
	fresh := newLogBuffer()
	fresh.add(linesFrom(10, 12))
	if got := fresh.add(linesFrom(0, 2)); got != 2 {
		t.Errorf("%d lines of an earlier time were added to a buffer that was not resumed, want 2", got)
	}
	// A buffer that is empty when it is resumed takes everything.
	empty := newLogBuffer()
	empty.resume()
	if got := empty.add(linesFrom(0, 3)); got != 3 {
		t.Errorf("%d lines were added to an empty buffer that was resumed, want 3", got)
	}
}

// A line of more than 16 KiB is several lines of one time in the ring.
// When the first of them was taken and the log is asked for again, the
// others are new: a line is one that is there only when it is there.
func TestLogBufferTakesALineOfTheNewestTimeThatIsNew(t *testing.T) {
	b := newLogBuffer()
	at := lineAt(7).At
	b.add([]wire.LogLine{lineAt(6), {At: at, Stream: "stdout", Text: "part 1"}, {At: at, Lost: 3}})
	b.resume()
	got := b.add([]wire.LogLine{
		lineAt(6),
		{At: at, Stream: "stdout", Text: "part 1"},
		{At: at, Lost: 3},
		{At: at, Stream: "stderr", Text: "part 1"}, // of another stream: another line
		{At: at, Stream: "stdout", Text: "part 2"},
		{At: at, Stream: "stdout", Text: "part 1"}, // behind a new line everything is new
	})
	var rows []string
	for _, e := range b.entries {
		rows = append(rows, textOf(e))
	}
	if got != 3 || strings.Join(rows, "|") != "line 6|part 1|lost 3|part 1|part 2|part 1" {
		t.Errorf("%d rows were added, and the buffer has %q", got, rows)
	}
}

// A zathras whose clock was set back writes lines of an earlier time
// than the newest there is. After the log is asked for again they are
// taken for the ring once more, but not for ever: the ring has logKept
// lines, and behind that many everything is new.
func TestLogBufferOfAClockThatWasSetBack(t *testing.T) {
	b := newLogBuffer()
	b.add(linesFrom(1000, 1010))
	b.resume()
	if got := b.add(linesFrom(0, logKept)); got != 0 {
		t.Errorf("%d of the first %d lines of an earlier time were added, want them taken for the ring", got, logKept)
	}
	if got := b.add(linesFrom(logKept, logKept+5)); got != 5 {
		t.Errorf("%d of five lines behind %d were added, want all: the ring has no more than that", got, logKept)
	}
	// And a line that is new ends it at once.
	b = newLogBuffer()
	b.add(linesFrom(1000, 1010))
	b.resume()
	if got := b.add(append(linesFrom(1005, 1012), linesFrom(0, 3)...)); got != 5 {
		t.Errorf("%d rows were added, want the two that are new and the three behind them", got)
	}
}

func TestLogBufferSaysAThingOnce(t *testing.T) {
	b := newLogBuffer()
	b.add(linesFrom(0, 1))
	b.say("the log ended")
	b.say("the log ended")
	b.say("the log ended: \x1b]0;t\x07the zathras is gone")
	if len(b.entries) != 3 || textOf(b.entries[1]) != "note: the log ended" || textOf(b.entries[2]) != "note: the log ended: the zathras is gone" {
		t.Errorf("the rows are %+v", b.entries)
	}
	for range 1000 {
		b.say("a")
		b.say("b")
	}
	if len(b.entries) != logKept {
		t.Errorf("%d rows are kept of 2000 notes", len(b.entries))
	}
}

// Review Focus 2: a line is read when it comes, and what is kept of it
// is clean, and bounded however long it was.
func TestLogBufferReadsALineWhenItComes(t *testing.T) {
	b := newLogBuffer()
	b.add([]wire.LogLine{
		{At: lineAt(0).At, Stream: "std\x1b[2Jerr", Text: "\x1b]0;pwned\x07\x1b[1;31mERROR\x1b[0m disk\tfull"},
		{At: lineAt(1).At, Stream: "stdout", Text: strings.Repeat("0123456789", 1<<20/10)},
		{At: lineAt(2).At, Stream: "stdout", Text: strings.Repeat("\x1b", 16<<10)},
		{At: lineAt(3).At, Stream: "stdout"},
	})
	first := b.entries[0]
	if textOf(first) != "ERROR disk      full" || first.stream != "stderr" || len(first.spans) != 2 || !first.spans[0].look.bold || first.spans[0].look.fg != 2 {
		t.Errorf("the first row is %+v", first)
	}
	if long := b.entries[1]; spansWidth(long.spans) != logCells || !long.cut {
		t.Errorf("of a megabyte %d cells are kept (cut: %v), want %d", spansWidth(long.spans), long.cut, logCells)
	}
	if esc := b.entries[2]; len(esc.spans) != 0 || esc.cut {
		t.Errorf("a line of nothing but ESC is %+v", esc)
	}
	if blank := b.entries[3]; blank.kind != entryLine || len(blank.spans) != 0 {
		t.Errorf("an empty line is %+v", blank)
	}
}

// Review Focus 5: of a flood, what waits for the model is bounded, and
// it is the end of it.
func TestPendingKeepsTheEndOfAFlood(t *testing.T) {
	p := pending{keep: logKept}
	if got := p.take(); len(got) != 0 {
		t.Errorf("nothing waits, and take returns %d lines", len(got))
	}
	for n := range 250 {
		p.add(lineAt(n))
	}
	if got := p.take(); len(got) != 250 || got[0].Text != "line 0" {
		t.Errorf("250 lines wait, and take returns %d from %q", len(got), got[0].Text)
	}
	for n := range 10000 {
		p.add(lineAt(n))
		if len(p.lines) > 2*logKept {
			t.Fatalf("%d lines wait", len(p.lines))
		}
	}
	got := p.take()
	if len(got) != logKept || got[0].Text != "line 9600" || got[logKept-1].Text != "line 9999" {
		t.Errorf("of ten thousand lines take returns %d, from %q to %q", len(got), got[0].Text, got[len(got)-1].Text)
	}
	if len(p.take()) != 0 {
		t.Error("what was taken waits again")
	}
}
```

- [ ] **Step 22: Run the test to see it fail**

Run: `nix develop .#bare --command go test ./tui/`
Expected: FAIL, the package does not build:

```
# github.com/fables-for-robots/great-machine/tui [github.com/fables-for-robots/great-machine/tui.test]
tui/logbuf_test.go:31:15: undefined: entry
tui/logbuf_test.go:33:7: undefined: entryLost
tui/logbuf_test.go:35:7: undefined: entryNote
tui/logbuf_test.go:46:7: undefined: newLogBuffer
tui/logbuf_test.go:51:23: undefined: logKept
tui/logbuf_test.go:52:158: undefined: logKept
tui/logbuf_test.go:60:34: undefined: entryLine
tui/logbuf_test.go:66:7: undefined: newLogBuffer
tui/logbuf_test.go:79:7: undefined: newLogBuffer
tui/logbuf_test.go:97:11: undefined: newLogBuffer
tui/logbuf_test.go:97:11: too many errors
FAIL	github.com/fables-for-robots/great-machine/tui [build failed]
FAIL
```

- [ ] **Step 23: Write the implementation**

Create `tui/logbuf.go`:

```go
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

const (
	// logKept is how many lines the terminal UI keeps of a log: as many
	// as the ring of a zathras.
	logKept = 400
	// logCells is how many cells are kept of one line. A line of a ring
	// is 16 KiB at most; what is drawn of it is some rows of the
	// terminal, and no more is read.
	logCells = 2000
	// logEvery is the least time between two batches of lines the
	// program hands the model: twenty a second, however fast the lines
	// come. The model is drawn once for each.
	logEvery = 50 * time.Millisecond
)

// entryKind is what a row of a log is.
type entryKind int

const (
	// entryLine is a line of the service or of zathras.
	entryLine entryKind = iota
	// entryLost says that so many lines were lost before the next: draal
	// said so.
	entryLost
	// entryNote is something the terminal UI says: that the log ended,
	// for one.
	entryNote
)

// entry is a row of a log as it is kept: read once, when it came.
type entry struct {
	// seq is a number no other row of the buffer has. Rows are told
	// apart by it when the buffer has moved under a reader.
	seq    uint64
	kind   entryKind
	at     time.Time
	stream string // of a line: clean
	spans  []span // of a line: its text, at most logCells cells of it
	cut    bool   // of a line: it had more cells than are kept
	lost   uint64 // of a lost marker
	text   string // of a note: clean
}

// logBuffer is the log of one zathras: at most keep rows, the oldest
// going first.
type logBuffer struct {
	keep    int
	entries []entry
	seq     uint64
	// newest is the time of the newest line taken. After resume, the
	// lines that come first are the ring once more: repeated is the
	// time up to which they may be a repetition of what is there, held
	// is what is there of that very time, and repeats is how many lines
	// may still be of the ring.
	newest   time.Time
	repeated time.Time
	held     map[string]bool
	repeats  int
}

// newLogBuffer returns an empty buffer of logKept rows.
func newLogBuffer() *logBuffer {
	return &logBuffer{keep: logKept}
}

// add takes lines and lost markers as they came, and returns how many
// rows it added. A lost marker is a row of its own.
func (b *logBuffer) add(lines []wire.LogLine) int {
	added := 0
	for _, l := range lines {
		e := entry{at: l.At}
		if l.Lost > 0 {
			e.kind, e.lost = entryLost, l.Lost
		} else {
			e.stream = clean(capped(l.Stream))
			e.spans, e.cut = parseLine(l.Text, logCells)
		}
		// After resume the ring is sent once more: what is there is not
		// taken twice. A line is one that is there when its time is
		// before the newest that is there, or is that time and the line
		// is among those held of it: a line of the same time that is
		// not, is new (a line longer than the ring takes is several
		// lines of one time). The ring is logKept lines and comes first:
		// behind the first line that is new, and behind logKept lines,
		// everything is new, whatever its time says. (What is held of the
		// newest time is a set, not a count: a service that wrote the
		// same text twice in one nanosecond, of which one was taken
		// before, has the other left out.)
		if b.repeats > 0 {
			b.repeats--
			if !l.At.IsZero() && (l.At.Before(b.repeated) || l.At.Equal(b.repeated) && b.held[e.same()]) {
				continue
			}
			b.repeats = 0
		}
		if l.At.After(b.newest) {
			b.newest = l.At
		}
		e.seq = b.seq
		b.seq++
		b.entries = append(b.entries, e)
		added++
	}
	b.trim()
	return added
}

// say adds a row of the terminal UI's own. One that says what the row
// before it said is not added.
func (b *logBuffer) say(text string) {
	text = clean(text)
	if n := len(b.entries); n > 0 && b.entries[n-1].kind == entryNote && b.entries[n-1].text == text {
		return
	}
	b.entries = append(b.entries, entry{seq: b.seq, kind: entryNote, text: text})
	b.seq++
	b.trim()
}

// trim drops the oldest rows over the bound.
func (b *logBuffer) trim() {
	if over := len(b.entries) - b.keep; over > 0 {
		b.entries = append(b.entries[:0], b.entries[over:]...)
	}
}

// same returns what tells a row from the others of its time.
func (e entry) same() string {
	if e.kind == entryLost {
		return fmt.Sprintf("lost %d", e.lost)
	}
	var s strings.Builder
	s.WriteString(e.stream)
	s.WriteByte(0)
	for _, sp := range e.spans {
		s.WriteString(sp.text)
	}
	return s.String()
}

// resume says that the log is asked for again: the lines that come
// first are the ring, and those the buffer has from before are left
// out.
func (b *logBuffer) resume() {
	b.repeated, b.held, b.repeats = b.newest, map[string]bool{}, 0
	if b.newest.IsZero() {
		return
	}
	b.repeats = logKept
	for _, e := range b.entries {
		if e.kind != entryNote && e.at.Equal(b.newest) {
			b.held[e.same()] = true
		}
	}
}

// pending is the lines of a followed log that the program has read and
// not yet handed to the model. A flood is read as fast as it comes, and
// handed on at most every logEvery; what waits is bounded, and of a
// flood the end is what is kept: the model keeps no more than that
// either.
type pending struct {
	keep  int
	lines []wire.LogLine
}

// add takes a line. Over twice the bound the oldest half goes.
func (p *pending) add(l wire.LogLine) {
	p.lines = append(p.lines, l)
	if len(p.lines) > 2*p.keep {
		p.lines = append(p.lines[:0], p.lines[len(p.lines)-p.keep:]...)
	}
}

// take returns what waits, the newest keep of it, and empties p.
func (p *pending) take() []wire.LogLine {
	lines := p.lines
	if len(lines) > p.keep {
		lines = lines[len(lines)-p.keep:]
	}
	p.lines = nil
	return lines
}
```

- [ ] **Step 24: Run the tests to see them pass**

Run: `nix develop .#bare --command go test ./tui/ -v -run 'LogBuffer|Pending'`
Expected: PASS: `TestLogBufferKeepsTheNewestLines`, `TestLogBufferHasTheLostMarkerAsARowOfItsOwn`, `TestLogBufferTakesNoLineTwiceAfterAReconnect`, `TestLogBufferTakesALineOfTheNewestTimeThatIsNew`, `TestLogBufferOfAClockThatWasSetBack`, `TestLogBufferSaysAThingOnce`, `TestLogBufferReadsALineWhenItComes`, `TestPendingKeepsTheEndOfAFlood`

- [ ] **Step 25: Commit**

```bash
git add tui/logbuf.go tui/logbuf_test.go
git commit -m "TUI: the buffer of a log: 400 lines, the lost marker a row of its own, no line twice after a reconnect, and what waits of a flood is bounded"
```

- [ ] **Step 26: Run everything of the task many times**

Run: `nix develop .#bare --command go test -race -count=50 ./human/ ./tui/ && nix develop .#bare --command go test ./cli/ && nix develop .#bare --command gofmt -l . && nix develop .#bare --command go vet ./...`
Expected: `ok` for the three packages, and nothing from `gofmt` and `go vet`
