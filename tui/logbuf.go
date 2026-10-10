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
