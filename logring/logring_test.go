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
