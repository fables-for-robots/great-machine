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
