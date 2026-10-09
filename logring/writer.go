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
