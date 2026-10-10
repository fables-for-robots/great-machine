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
