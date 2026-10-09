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
