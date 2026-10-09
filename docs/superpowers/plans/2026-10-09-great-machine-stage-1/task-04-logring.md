### Task 4: logring, the ring of the last lines

**Files:**
- Create: `logring/logring.go` (the ring, `Add`, `Lines`, `Follow`)
- Create: `logring/writer.go` (`Writer`: cuts what a service writes into lines)
- Test: `logring/logring_test.go`
- Test: `logring/writer_test.go`

**Interfaces:**
- Consumes: from `wire` (Task 2), nothing but this type:

  ```go
  // LogLine is a line of the ring. One with Lost set has no text: it says
  // that so many lines were lost before the line that follows it.
  type LogLine struct {
  	At     time.Time `json:"at"`
  	Stream string    `json:"stream"` // "stdout", "stderr" or "zathras"
  	Text   string    `json:"text,omitempty"`
  	Lost   uint64    `json:"lost,omitempty"`
  }
  ```

- Produces, exactly as the plan's Interfaces section has it:

  ```go
  package logring

  const (
  	Stdout  = "stdout"
  	Stderr  = "stderr"
  	Zathras = "zathras"
  )

  type Ring struct{ /* unexported */ }

  func New(size, maxLine int) *Ring
  func (r *Ring) SetClock(now func() time.Time)
  func (r *Ring) Add(stream, text string)
  func (r *Ring) Writer(stream string) *Writer
  func (r *Ring) Lines() []wire.LogLine
  func (r *Ring) Follow(ctx context.Context, buffer int) ([]wire.LogLine, <-chan wire.LogLine)

  type Writer struct{ /* unexported */ }

  func (w *Writer) Write(p []byte) (int, error) // never fails
  func (w *Writer) Flush()
  ```

  What the contract leaves open, and this task settles:
  - `New` takes a `size` below 1 as 1 and a `maxLine` below 4 as 4 (the longest character of UTF-8 has four bytes, so a cut never has to fall inside one).
  - A run of bytes that is no valid UTF-8 becomes **one** U+FFFD (`strings.ToValidUTF8`). U+FFFD is three bytes long, so a text can grow by this; it is cut into lines of at most `maxLine` bytes after it was made valid.
  - Every line that one `Add` makes has the same time.
  - A `Writer` adds a line that is exactly `maxLine` bytes long when its last byte is written, and the line feed that follows makes no empty line behind it.
  - The line that tells a follower of lost lines has the time of the last line that was lost. A follower can be told twice in a row (the first count was taken when more were lost behind it); the counts add up.
  - `Follow` takes a `buffer` below 1 as 1, and allocates room for `buffer` lines at once.

This is Spec section 7.4. **Review Focus 1 is pinned here**: the tests `TestAddMakesValidLinesOfAtMostMaxLine`, `TestWriterMakesValidUTF8`, `TestWriterMegabyteWithoutLineFeed`, `TestWriterMegabyteOfNoise` and `TestWriterMegabyteOfLineFeeds` say so in their comments.

The tests are in the package itself (`package logring`), because two of them look at what the writer holds back.

- [ ] **Step 1: Write the failing test of the ring and its followers**

Create `logring/logring_test.go`:

```go
package logring

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/fables-for-robots/great-machine/wire"
)

// clock is a clock for a ring: every reading is a second after the one
// before.
type clock struct{ t time.Time }

func (c *clock) now() time.Time {
	c.t = c.t.Add(time.Second)
	return c.t
}

var epoch = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// texts returns the texts of lines.
func texts(lines []wire.LogLine) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.Text)
	}
	return out
}

// equal fails the test unless got is want.
func equal(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %d lines %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d is %q, want %q (all: %q)", i, got[i], want[i], got)
		}
	}
}

// read reads one line of a follower, and fails the test when none comes.
func read(t *testing.T, ch <-chan wire.LogLine) wire.LogLine {
	t.Helper()
	select {
	case l, ok := <-ch:
		if !ok {
			t.Fatal("the channel was closed")
		}
		return l
	case <-time.After(10 * time.Second):
		t.Fatal("no line within 10 seconds")
	}
	return wire.LogLine{}
}

func TestLinesOldestFirstWithStreamAndTime(t *testing.T) {
	r := New(10, 100)
	c := &clock{t: epoch}
	r.SetClock(c.now)
	r.Add(Stdout, "one")
	r.Add(Stderr, "two")
	r.Add(Zathras, "three")
	r.Add(Stdout, "")

	want := []wire.LogLine{
		{At: epoch.Add(1 * time.Second), Stream: Stdout, Text: "one"},
		{At: epoch.Add(2 * time.Second), Stream: Stderr, Text: "two"},
		{At: epoch.Add(3 * time.Second), Stream: Zathras, Text: "three"},
		{At: epoch.Add(4 * time.Second), Stream: Stdout, Text: ""},
	}
	got := r.Lines()
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d is %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestRingKeepsTheLast(t *testing.T) {
	r := New(3, 100)
	if got := r.Lines(); len(got) != 0 {
		t.Fatalf("a new ring has %d lines", len(got))
	}
	for i := 1; i <= 7; i++ {
		r.Add(Stdout, fmt.Sprint(i))
	}
	equal(t, texts(r.Lines()), []string{"5", "6", "7"})

	// What Lines returned is the caller's.
	lines := r.Lines()
	lines[0].Text = "changed"
	equal(t, texts(r.Lines()), []string{"5", "6", "7"})
}

// Review Focus 1: bytes that are no valid UTF-8 become U+FFFD, and a text
// of more than maxLine bytes becomes several lines, none of which ends
// inside a character.
func TestAddMakesValidLinesOfAtMostMaxLine(t *testing.T) {
	tests := []struct {
		name    string
		maxLine int
		text    string
		want    []string
	}{
		{"valid", 16, "zażółć", []string{"zażółć"}},
		{"one bad byte", 16, "a\xffb", []string{"a�b"}},
		{"a cut-off character", 16, "caf\xc3", []string{"caf�"}},
		{"only bad bytes", 16, "\xff\xfe\xfd", []string{"�"}},
		{"exactly maxLine", 4, "abcd", []string{"abcd"}},
		{"longer than maxLine", 4, "abcdefghij", []string{"abcd", "efgh", "ij"}},
		{"the cut would fall inside a character", 4, "abcé", []string{"abc", "é"}},
		{"characters of three bytes", 4, "€€€", []string{"€", "€", "€"}},
		{"bad bytes grow beyond maxLine", 4, "ab\xffcd", []string{"ab", "�c", "d"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(100, tt.maxLine)
			r.Add(Stdout, tt.text)
			got := texts(r.Lines())
			equal(t, got, tt.want)
			for _, text := range got {
				if !utf8.ValidString(text) {
					t.Errorf("%q is no valid UTF-8", text)
				}
				if len(text) > tt.maxLine {
					t.Errorf("%q is longer than %d bytes", text, tt.maxLine)
				}
			}
		})
	}
}

func TestFollowSnapshotThenEveryLaterLineOnce(t *testing.T) {
	r := New(10, 100)
	r.Add(Stdout, "before 1")
	r.Add(Stderr, "before 2")

	snapshot, ch := r.Follow(t.Context(), 16)
	equal(t, texts(snapshot), []string{"before 1", "before 2"})

	r.Add(Stdout, "after 1")
	r.Add(Stderr, "after 2")
	if l := read(t, ch); l.Text != "after 1" || l.Stream != Stdout || l.Lost != 0 {
		t.Errorf("got %+v, want after 1 of stdout", l)
	}
	if l := read(t, ch); l.Text != "after 2" || l.Stream != Stderr || l.Lost != 0 {
		t.Errorf("got %+v, want after 2 of stderr", l)
	}
	select {
	case l := <-ch:
		t.Errorf("a line nobody added: %+v", l)
	default:
	}
}

// Lines are added while Follow is called: every line is in the snapshot
// or comes on the channel, in order, none twice. Run it with -race.
func TestFollowLosesAndDoublesNothingWhileLinesAreAdded(t *testing.T) {
	const total = 5000
	for round := range 20 {
		r := New(total, 100)
		started := make(chan struct{})
		go func() {
			for i := range total {
				if i == round*50 {
					close(started)
				}
				r.Add(Stdout, fmt.Sprint(i))
			}
		}()
		<-started
		snapshot, ch := r.Follow(t.Context(), total)

		next := 0
		check := func(l wire.LogLine) {
			if l.Lost != 0 {
				t.Fatalf("round %d: lost %d lines with room for all", round, l.Lost)
			}
			if l.Text != fmt.Sprint(next) {
				t.Fatalf("round %d: got line %q, want %d", round, l.Text, next)
			}
			next++
		}
		for _, l := range snapshot {
			check(l)
		}
		for next < total {
			check(read(t, ch))
		}
	}
}

func TestFollowChannelClosesWhenTheContextIsDone(t *testing.T) {
	r := New(10, 100)
	ctx, cancel := context.WithCancel(t.Context())
	_, ch := r.Follow(ctx, 4)
	r.Add(Stdout, "one")
	if l := read(t, ch); l.Text != "one" {
		t.Fatalf("got %+v", l)
	}
	cancel()
	select {
	case l, ok := <-ch:
		if ok {
			t.Fatalf("got %+v, want the channel closed", l)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the channel was not closed within 10 seconds")
	}

	// The ring has forgotten the follower, and goes on without it.
	deadline := time.Now().Add(10 * time.Second)
	for {
		r.mu.Lock()
		n := len(r.followers)
		r.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the ring still has %d followers", n)
		}
		time.Sleep(time.Millisecond)
	}
	r.Add(Stdout, "two")
}

func TestSlowFollowerIsToldHowManyLinesItLost(t *testing.T) {
	const buffer, total = 4, 20
	r := New(100, 100)
	c := &clock{t: epoch}
	r.SetClock(c.now)
	_, ch := r.Follow(t.Context(), buffer)

	// Nobody reads while these are added. The follower may hold the
	// first line in its hand already, or a count of lost lines that more
	// are lost after; of the others only the last four have room.
	for i := 1; i <= total; i++ {
		r.Add(Stdout, fmt.Sprint(i))
	}

	next := 1 // the line that is to come
	markers := 0
	for next <= total {
		l := read(t, ch)
		if l.Lost > 0 {
			markers++
			if l.Stream != Zathras || l.Text != "" {
				t.Errorf("the line that tells of lost lines is %+v, want stream zathras and no text", l)
			}
			if l.At.IsZero() {
				t.Errorf("the line that tells of lost lines has no time")
			}
			next += int(l.Lost)
			continue
		}
		if l.Text != fmt.Sprint(next) {
			t.Fatalf("got line %q, want %d: lines were lost and not told", l.Text, next)
		}
		next++
	}
	if next != total+1 {
		t.Errorf("lines read and lines told lost come to %d, want %d", next-1, total)
	}
	if markers == 0 {
		t.Errorf("never told of lost lines")
	}

	// It goes on with real lines.
	r.Add(Stderr, "and on")
	if l := read(t, ch); l.Text != "and on" || l.Stream != Stderr || l.Lost != 0 {
		t.Errorf("got %+v, want the line that was added", l)
	}
}

func TestManyFollowers(t *testing.T) {
	const followers, total = 25, 200
	r := New(10, 100)
	ctx, cancel := context.WithCancel(t.Context())
	errs := make(chan error, followers)
	chans := make([]<-chan wire.LogLine, followers)
	for i := range followers {
		_, chans[i] = r.Follow(ctx, total)
	}
	for i := range followers {
		go func() {
			for want := range total {
				select {
				case l := <-chans[i]:
					if l.Text != fmt.Sprint(want) {
						errs <- fmt.Errorf("follower %d got %+v, want line %d", i, l, want)
						return
					}
				case <-time.After(10 * time.Second):
					errs <- fmt.Errorf("follower %d got no line %d", i, want)
					return
				}
			}
			errs <- nil
		}()
	}
	for i := range total {
		r.Add(Stdout, fmt.Sprint(i))
	}
	for range followers {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
	cancel()
	for i, ch := range chans {
		select {
		case _, ok := <-ch:
			if ok {
				t.Errorf("follower %d got a line nobody added", i)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("the channel of follower %d was not closed", i)
		}
	}
}

func TestAddSplitsKeepTheTime(t *testing.T) {
	r := New(10, 4)
	c := &clock{t: epoch}
	r.SetClock(c.now)
	r.Add(Stdout, strings.Repeat("x", 10))
	lines := r.Lines()
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	for i, l := range lines {
		if !l.At.Equal(epoch.Add(time.Second)) || l.Stream != Stdout {
			t.Errorf("line %d is %+v, want the time and the stream of the one Add", i, l)
		}
	}
}
```

- [ ] **Step 2: Run the test to see it fail**

Run: `go test ./logring/`
Expected: FAIL — the package does not build: `undefined: New`, `undefined: Stdout`, and more of the kind.

- [ ] **Step 3: Write the ring**

Create `logring/logring.go`:

```go
// Package logring keeps the last lines a service wrote, each with its time
// and its stream, and hands them to those who follow the log.
package logring

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/fables-for-robots/great-machine/wire"
)

// The streams of a line.
const (
	// Stdout is the standard output of the service.
	Stdout = "stdout"
	// Stderr is the standard error of the service.
	Stderr = "stderr"
	// Zathras is what zathras itself says: starts, exits, lost lines.
	Zathras = "zathras"
)

// Ring holds the last lines. It is safe for concurrent use.
type Ring struct {
	maxLine int

	mu        sync.Mutex
	now       func() time.Time
	lines     queue
	followers map[*follower]struct{}
}

// New returns a ring of the last size lines, of at most maxLine bytes
// each. great-machine uses New(400, 16<<10). A size below 1 is taken as 1,
// and a maxLine below 4, the length of the longest character, as 4.
func New(size, maxLine int) *Ring {
	if size < 1 {
		size = 1
	}
	if maxLine < utf8.UTFMax {
		maxLine = utf8.UTFMax
	}
	return &Ring{
		maxLine:   maxLine,
		now:       time.Now,
		lines:     newQueue(size),
		followers: make(map[*follower]struct{}),
	}
}

// SetClock replaces time.Now, for tests. Call it before anything is added.
func (r *Ring) SetClock(now func() time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = now
}

// Add adds text as one line of stream. Bytes that are no valid UTF-8
// become U+FFFD, and a text of more than maxLine bytes becomes several
// lines.
func (r *Ring) Add(stream, text string) {
	text = strings.ToValidUTF8(text, "�")
	r.mu.Lock()
	defer r.mu.Unlock()
	at := r.now()
	for len(text) > r.maxLine {
		// The cut may not fall inside a character. text is valid UTF-8
		// and maxLine is at least the longest character, so cut stays
		// above zero.
		cut := r.maxLine
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		r.add(wire.LogLine{At: at, Stream: stream, Text: strings.Clone(text[:cut])})
		text = text[cut:]
	}
	r.add(wire.LogLine{At: at, Stream: stream, Text: text})
}

// add puts one line in the ring and hands it to every follower. The
// caller holds r.mu.
func (r *Ring) add(l wire.LogLine) {
	r.lines.push(l)
	for f := range r.followers {
		if dropped, full := f.pending.push(l); full {
			f.lost++
			f.lostAt = dropped.At
		}
		select {
		case f.wake <- struct{}{}:
		default:
		}
	}
}

// Lines returns the lines of the ring, oldest first.
func (r *Ring) Lines() []wire.LogLine {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lines.all()
}

// follower is one call of Follow. Its fields but wake are guarded by the
// ring's mutex.
type follower struct {
	pending queue         // the lines that wait to be read
	lost    uint64        // how many were dropped from pending and not told yet
	lostAt  time.Time     // the time of the last of them
	wake    chan struct{} // has room for one value; gets one when a line was added
}

// Follow returns the lines of the ring and a channel of the lines that
// follow, with none between them lost and none twice. The channel is
// closed when ctx is done. A reader that lets more than buffer lines wait
// loses the oldest of them, and reads a line with Lost set, stream
// Zathras and no text in their place.
func (r *Ring) Follow(ctx context.Context, buffer int) ([]wire.LogLine, <-chan wire.LogLine) {
	if buffer < 1 {
		buffer = 1
	}
	f := &follower{pending: newQueue(buffer), wake: make(chan struct{}, 1)}
	out := make(chan wire.LogLine)

	// The snapshot is taken and the follower added under one lock, so a
	// line is in the one or goes to the other.
	r.mu.Lock()
	snapshot := r.lines.all()
	r.followers[f] = struct{}{}
	r.mu.Unlock()

	go r.feed(ctx, f, out)
	return snapshot, out
}

// feed moves the lines that wait for f to out until ctx is done.
func (r *Ring) feed(ctx context.Context, f *follower, out chan<- wire.LogLine) {
	defer close(out)
	defer func() {
		r.mu.Lock()
		delete(r.followers, f)
		r.mu.Unlock()
	}()
	for {
		l, ok := r.next(f)
		if !ok {
			select {
			case <-f.wake:
				continue
			case <-ctx.Done():
				return
			}
		}
		select {
		case out <- l:
		case <-ctx.Done():
			return
		}
	}
}

// next returns the next line for f: the count of the lines it lost, if it
// lost any, before the oldest line that waits.
func (r *Ring) next(f *follower) (wire.LogLine, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if f.lost > 0 {
		l := wire.LogLine{At: f.lostAt, Stream: Zathras, Lost: f.lost}
		f.lost = 0
		return l, true
	}
	return f.pending.pop()
}

// queue is a ring of lines of a fixed size.
type queue struct {
	items []wire.LogLine
	head  int // the index of the oldest
	n     int // how many there are
}

func newQueue(size int) queue {
	return queue{items: make([]wire.LogLine, size)}
}

// push adds l as the newest. When the queue is full the oldest makes
// room, and is returned with true.
func (q *queue) push(l wire.LogLine) (dropped wire.LogLine, full bool) {
	if q.n == len(q.items) {
		dropped = q.items[q.head]
		q.items[q.head] = l
		q.head = (q.head + 1) % len(q.items)
		return dropped, true
	}
	q.items[(q.head+q.n)%len(q.items)] = l
	q.n++
	return wire.LogLine{}, false
}

// pop takes the oldest out, and returns false when there is none.
func (q *queue) pop() (wire.LogLine, bool) {
	if q.n == 0 {
		return wire.LogLine{}, false
	}
	l := q.items[q.head]
	q.items[q.head] = wire.LogLine{}
	q.head = (q.head + 1) % len(q.items)
	q.n--
	return l, true
}

// all returns a copy of the lines, oldest first.
func (q *queue) all() []wire.LogLine {
	out := make([]wire.LogLine, 0, q.n)
	for i := range q.n {
		out = append(out, q.items[(q.head+i)%len(q.items)])
	}
	return out
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test -race -count=3 ./logring/`
Expected: PASS (`ok  	github.com/fables-for-robots/great-machine/logring`)

- [ ] **Step 5: Commit**

```bash
git add logring/
git commit -m "Logring: the ring of the last lines and its followers"
```

- [ ] **Step 6: Write the failing test of the writer**

Create `logring/writer_test.go`:

```go
package logring

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestWriterCutsAtLineFeeds(t *testing.T) {
	tests := []struct {
		name   string
		writes []string
		want   []string // before Flush
		after  []string // after Flush
	}{
		{"one line", []string{"one\n"}, []string{"one"}, []string{"one"}},
		{"two lines in one write", []string{"one\ntwo\n"}, []string{"one", "two"}, []string{"one", "two"}},
		{"a line over three writes", []string{"on", "e t", "wo\n"}, []string{"one two"}, []string{"one two"}},
		{"the line feed in a write of its own", []string{"one", "\n"}, []string{"one"}, []string{"one"}},
		{"an empty line", []string{"one\n\ntwo\n"}, []string{"one", "", "two"}, []string{"one", "", "two"}},
		{"carriage return and line feed", []string{"one\r\ntwo\r\n"}, []string{"one", "two"}, []string{"one", "two"}},
		{"carriage return and line feed apart", []string{"one\r", "\ntwo\n"}, []string{"one", "two"}, []string{"one", "two"}},
		{"a carriage return inside a line stays", []string{"10%\r20%\n"}, []string{"10%\r20%"}, []string{"10%\r20%"}},
		{"only one carriage return is dropped", []string{"one\r\r\n"}, []string{"one\r"}, []string{"one\r"}},
		{"a partial line waits for Flush", []string{"one\ntw", "o"}, []string{"one"}, []string{"one", "two"}},
		{"nothing written", nil, nil, nil},
		{"an empty write", []string{""}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(100, 100)
			w := r.Writer(Stdout)
			for _, s := range tt.writes {
				n, err := w.Write([]byte(s))
				if n != len(s) || err != nil {
					t.Fatalf("Write(%q) = %d, %v", s, n, err)
				}
			}
			equal(t, texts(r.Lines()), tt.want)
			w.Flush()
			equal(t, texts(r.Lines()), tt.after)
			w.Flush()
			equal(t, texts(r.Lines()), tt.after)
			for _, l := range r.Lines() {
				if l.Stream != Stdout {
					t.Errorf("stream %q, want stdout", l.Stream)
				}
			}
		})
	}
}

func TestTwoWritersDoNotMixTheirPartialLines(t *testing.T) {
	r := New(100, 100)
	out, errw := r.Writer(Stdout), r.Writer(Stderr)
	out.Write([]byte("out "))
	errw.Write([]byte("err "))
	out.Write([]byte("one\nout "))
	errw.Write([]byte("one\n"))
	out.Write([]byte("two\n"))
	errw.Write([]byte("err two"))
	errw.Flush()

	lines := r.Lines()
	equal(t, texts(lines), []string{"out one", "err one", "out two", "err two"})
	streams := []string{Stdout, Stderr, Stdout, Stderr}
	for i, l := range lines {
		if l.Stream != streams[i] {
			t.Errorf("line %d (%q) is of %q, want %q", i, l.Text, l.Stream, streams[i])
		}
	}
}

func TestWritersFromManyGoroutines(t *testing.T) {
	const writers, each = 8, 200
	r := New(writers*each, 100)
	var wg sync.WaitGroup
	for i := range writers {
		w := r.Writer(fmt.Sprint(i))
		wg.Go(func() {
			for j := range each {
				// Each line in two writes, so that partial lines are
				// held back all the time.
				fmt.Fprintf(w, "writer %d ", i)
				fmt.Fprintf(w, "line %d\n", j)
			}
		})
	}
	wg.Wait()
	next := make(map[string]int)
	for _, l := range r.Lines() {
		want := fmt.Sprintf("writer %s line %d", l.Stream, next[l.Stream])
		if l.Text != want {
			t.Fatalf("got %q, want %q", l.Text, want)
		}
		next[l.Stream]++
	}
	for i := range writers {
		if n := next[fmt.Sprint(i)]; n != each {
			t.Errorf("writer %d has %d lines, want %d", i, n, each)
		}
	}
}

// Review Focus 1: output that is no valid UTF-8. Every text in the ring
// is valid UTF-8, a character that is split across two writes comes out
// whole, and a cut at maxLine does not fall inside a character.
func TestWriterMakesValidUTF8(t *testing.T) {
	tests := []struct {
		name    string
		maxLine int
		writes  []string
		want    []string // after Flush
	}{
		{"a bad byte", 100, []string{"a\xffb\n"}, []string{"a�b"}},
		{"a character split across two writes", 100, []string{"caf\xc3", "\xa9\n"}, []string{"café"}},
		{"a character of four bytes, a byte at a time", 100, []string{"\xf0", "\x9f", "\x98", "\x80", "\n"}, []string{"😀"}},
		{"half a character before the line feed", 100, []string{"caf\xc3\n"}, []string{"caf�"}},
		{"half a character at Flush", 100, []string{"caf\xc3"}, []string{"caf�"}},
		{"the cut at maxLine would fall inside a character", 8, []string{"aaaaaaaé\n"}, []string{"aaaaaaa", "é"}},
		{"the cut would fall inside a character that comes in two writes", 8, []string{"aaaaaaa\xc3", "\xa9bc\n"}, []string{"aaaaaaa", "ébc"}},
		{"characters of three bytes without a line feed", 8, []string{"€€€€€"}, []string{"€€", "€€", "€"}},
		{"exactly maxLine and a line feed is one line", 8, []string{"12345678\n", "next\n"}, []string{"12345678", "next"}},
		{"exactly maxLine, carriage return, line feed", 8, []string{"12345678\r\n", "next\n"}, []string{"12345678", "next"}},
		{"a carriage return where the cut is", 8, []string{"1234567\r\n", "next\n"}, []string{"1234567", "next"}},
		{"longer than maxLine", 8, []string{"1234567890\n"}, []string{"12345678", "90"}},
		{"bad bytes that grow beyond maxLine", 8, []string{"abcdef\xff\xfe\n"}, []string{"abcdef", "�"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(100, tt.maxLine)
			w := r.Writer(Stdout)
			for _, s := range tt.writes {
				w.Write([]byte(s))
			}
			w.Flush()
			got := texts(r.Lines())
			equal(t, got, tt.want)
			for _, text := range got {
				if !utf8.ValidString(text) {
					t.Errorf("%q is no valid UTF-8", text)
				}
				if len(text) > tt.maxLine {
					t.Errorf("%q is longer than %d bytes", text, tt.maxLine)
				}
			}
		})
	}
}

// Review Focus 1: megabytes without a line feed. They become lines of at
// most maxLine bytes, and the writer never holds more than maxLine bytes
// back, whether they come in one write or in many.
func TestWriterMegabyteWithoutLineFeed(t *testing.T) {
	const size, maxLine = 400, 16 << 10
	megabyte := bytes.Repeat([]byte("x"), 1<<20)
	for _, step := range []int{len(megabyte), 64 << 10, 4096, 1000, 7} {
		t.Run(fmt.Sprintf("writes of %d bytes", step), func(t *testing.T) {
			r := New(size, maxLine)
			w := r.Writer(Stdout)
			for rest := megabyte; len(rest) > 0; {
				n := min(step, len(rest))
				w.Write(rest[:n])
				rest = rest[n:]
				if len(w.buf) >= maxLine || cap(w.buf) > 2*maxLine {
					t.Fatalf("the writer holds %d bytes back in a buffer of %d, with maxLine %d", len(w.buf), cap(w.buf), maxLine)
				}
			}
			w.Flush()
			lines := r.Lines()
			if want := (1 << 20) / maxLine; len(lines) != want {
				t.Errorf("got %d lines, want %d", len(lines), want)
			}
			total := 0
			for _, l := range lines {
				if len(l.Text) > maxLine {
					t.Fatalf("a line of %d bytes", len(l.Text))
				}
				total += len(l.Text)
			}
			if total != len(megabyte) {
				t.Errorf("the lines hold %d bytes, want %d", total, len(megabyte))
			}
		})
	}
}

// Review Focus 1: a megabyte that is no text at all. Whatever the bytes
// are, the ring holds valid UTF-8 in lines of at most maxLine bytes, and
// never more than size lines.
func TestWriterMegabyteOfNoise(t *testing.T) {
	const size, maxLine = 400, 16 << 10
	rnd := rand.New(rand.NewPCG(1, 2))
	noise := make([]byte, 1<<20)
	for i := range noise {
		noise[i] = byte(rnd.UintN(256))
		if noise[i] == '\n' {
			noise[i] = 0xff // no line feed, and more bytes that are no UTF-8
		}
	}
	r := New(size, maxLine)
	w := r.Writer(Stderr)
	for rest := noise; len(rest) > 0; {
		n := min(1+rnd.IntN(40000), len(rest))
		w.Write(rest[:n])
		rest = rest[n:]
		if len(w.buf) >= maxLine {
			t.Fatalf("the writer holds %d bytes back, with maxLine %d", len(w.buf), maxLine)
		}
	}
	w.Flush()
	lines := r.Lines()
	if len(lines) == 0 || len(lines) > size {
		t.Fatalf("the ring has %d lines, want 1 to %d", len(lines), size)
	}
	for _, l := range lines {
		if !utf8.ValidString(l.Text) {
			t.Fatalf("a line that is no valid UTF-8")
		}
		if len(l.Text) > maxLine {
			t.Fatalf("a line of %d bytes", len(l.Text))
		}
	}
}

// Review Focus 1: a megabyte of line feeds alone is a million empty
// lines, of which the ring keeps the last size, and a follower is told
// how many it lost.
func TestWriterMegabyteOfLineFeeds(t *testing.T) {
	const size, maxLine = 400, 16 << 10
	r := New(size, maxLine)
	_, ch := r.Follow(t.Context(), 8)
	w := r.Writer(Stdout)
	w.Write([]byte(strings.Repeat("\n", 1<<20)))
	w.Flush()

	lines := r.Lines()
	if len(lines) != size {
		t.Fatalf("the ring has %d lines, want %d", len(lines), size)
	}
	for _, l := range lines {
		if l.Text != "" || l.Stream != Stdout {
			t.Fatalf("got %+v, want an empty line of stdout", l)
		}
	}
	if len(r.lines.items) != size {
		t.Errorf("the ring has room for %d lines, want %d", len(r.lines.items), size)
	}

	// The follower reads empty lines and the count of those it lost,
	// which together are every line that was added.
	seen := 0
	for seen < 1<<20 {
		l := read(t, ch)
		if l.Text != "" {
			t.Fatalf("got %+v, want an empty line", l)
		}
		if l.Lost > 0 {
			seen += int(l.Lost)
		} else {
			seen++
		}
	}
	if seen != 1<<20 {
		t.Errorf("lines read and lines told lost come to %d, want %d", seen, 1<<20)
	}
}
```

- [ ] **Step 7: Run the test to see it fail**

Run: `go test ./logring/`
Expected: FAIL — the package does not build: `r.Writer undefined (type *Ring has no field or method Writer)`

- [ ] **Step 8: Write the writer**

Create `logring/writer.go`:

```go
package logring

import (
	"bytes"
	"sync"
	"unicode/utf8"
)

// Writer returns a writer that adds what is written to it, cut at line
// feeds, as lines of stream. What has no line feed yet is held back until
// one comes, until it is maxLine bytes long, or until Flush. A carriage
// return before the line feed is dropped.
func (r *Ring) Writer(stream string) *Writer {
	return &Writer{ring: r, stream: stream}
}

// Writer cuts what is written to it into lines of one stream of a ring.
// It is safe for concurrent use.
type Writer struct {
	ring   *Ring
	stream string

	mu  sync.Mutex
	buf []byte // what has no line feed yet: fewer than maxLine bytes between calls
	// cut says that the beginning of the line in buf was added already,
	// because it was maxLine bytes long.
	cut bool
}

// Write adds the lines that p completes and holds the rest back. It never
// fails.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		// Never look at more than fits in a line: buf stays below
		// maxLine however much is written at once.
		chunk := p
		if room := w.ring.maxLine - len(w.buf); len(chunk) > room {
			chunk = chunk[:room]
		}
		if i := bytes.IndexByte(chunk, '\n'); i >= 0 {
			w.buf = append(w.buf, chunk[:i]...)
			p = p[i+1:]
			w.endLine()
			continue
		}
		w.buf = append(w.buf, chunk...)
		p = p[len(chunk):]
		if len(w.buf) >= w.ring.maxLine {
			w.cutLine()
		}
	}
	return n, nil
}

// endLine adds buf as a line: a line feed was read.
func (w *Writer) endLine() {
	line := bytes.TrimSuffix(w.buf, []byte{'\r'})
	// A line of exactly maxLine bytes was added when it was cut. The
	// line feed behind it ends that line and is no empty line.
	if len(line) > 0 || !w.cut {
		w.ring.Add(w.stream, string(line))
	}
	w.buf = w.buf[:0]
	w.cut = false
}

// cutLine adds the beginning of buf, which is maxLine bytes long and has
// no line feed, as a line. It holds back a carriage return at the end,
// which a line feed may follow, and the beginning of a character whose
// rest was not written yet, so that no character is cut in two.
func (w *Writer) cutLine() {
	cut := len(w.buf)
	if w.buf[cut-1] == '\r' {
		cut--
	} else {
		for i := cut - 1; i >= 0 && i > cut-utf8.UTFMax; i-- {
			if utf8.RuneStart(w.buf[i]) {
				if !utf8.FullRune(w.buf[i:cut]) {
					cut = i
				}
				break
			}
		}
	}
	w.ring.Add(w.stream, string(w.buf[:cut]))
	w.buf = w.buf[:copy(w.buf, w.buf[cut:])]
	w.cut = true
}

// Flush adds what is held back as a line, if anything is.
func (w *Writer) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.ring.Add(w.stream, string(w.buf))
		w.buf = w.buf[:0]
	}
	w.cut = false
}
```

- [ ] **Step 9: Run the tests to see them pass**

Run: `go vet ./logring/ && go test -race -count=3 ./logring/`
Expected: PASS. The whole package takes a few seconds with the race detector; `TestWriterMegabyteOfLineFeeds` adds a million lines.

- [ ] **Step 10: Commit**

```bash
git add logring/
git commit -m "Logring: a writer that cuts output into lines of valid UTF-8"
```
