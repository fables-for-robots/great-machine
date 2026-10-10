package cli

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// clock is a clock the test moves.
type clock struct {
	mu sync.Mutex
	at time.Time
}

func newClock() *clock { return &clock{at: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func TestProgressPlain(t *testing.T) {
	var out buffer
	c := newClock()
	p := &progress{out: &out, now: c.now, width: func() int { return 0 }}

	p.begin("reading the closure")
	c.advance(340 * time.Millisecond)
	p.done("42 paths")

	p.begin("connecting to draal")
	c.advance(1200 * time.Millisecond)
	p.done("")

	// A step that counts says where it is when it has moved, and every
	// five seconds at most.
	p.begin("pushing")
	p.count(0, 3)
	c.advance(6 * time.Second)
	p.add(1)
	c.advance(time.Second)
	p.add(1) // moved, but the last line is a second old
	c.advance(time.Second)
	p.count(2, 3) // and now two seconds
	c.advance(4 * time.Second)
	p.count(2, 3) // it has moved since 1/3 was said, five seconds ago: a line
	c.advance(time.Second)
	p.add(1) // moved, but the last line is a second old
	p.done("3 paths, 1.5 KiB")

	p.begin("preparing")
	c.advance(75 * time.Second)
	p.fail(errors.New("web-b: the store has no such path"))
	p.fail(errors.New("a second failure has no step to mark"))

	p.begin("activating")
	c.advance(2 * time.Second)
	p.fail(context.Canceled)

	// A step that is begun while another runs ends that one.
	p.begin("one")
	p.begin("two")
	p.finish("all of it")
	p.begin("nothing is shown after the end")
	p.close()

	want := strings.Join([]string{
		"reading the closure: 42 paths (0.3s)",
		"connecting to draal (1.2s)",
		"pushing: 1/3 after 6.0s",
		"pushing: 2/3 after 12.0s",
		"pushing: 3 paths, 1.5 KiB (13.0s)",
		"preparing: failed after 1m15s",
		"activating: interrupted after 2.0s",
		"one (0.0s)",
		"two (0.0s)",
		"total: all of it (1m31s)",
		"",
	}, "\n")
	if got := out.String(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// screen returns what a terminal shows after it was written text: its
// lines, and whether the cursor is at the start of a line that is empty.
// It knows the three things progress writes besides text: a carriage
// return, "erase to the end of the line" and a line feed.
func screen(t *testing.T, text string) (lines []string, fresh bool) {
	t.Helper()
	line, col := []rune{}, 0
	for len(text) > 0 {
		switch {
		case strings.HasPrefix(text, "\033[K"):
			line = line[:col]
			text = text[3:]
			continue
		case text[0] == '\r':
			col = 0
		case text[0] == '\n':
			lines = append(lines, string(line))
			line, col = []rune{}, 0
		case text[0] == '\033':
			t.Fatalf("an escape sequence the test does not know: %q", text)
		default:
			r := []rune(text)[0]
			if col < len(line) {
				line[col] = r
			} else {
				line = append(line, r)
			}
			col++
			text = text[len(string(r)):]
			continue
		}
		text = text[1:]
	}
	return lines, len(line) == 0 && col == 0
}

func TestProgressOnATerminal(t *testing.T) {
	var out buffer
	c := newClock()
	// every is 0: the step is redrawn when it changes, and by no ticker.
	width := 80
	p := &progress{out: &out, live: true, now: c.now, width: func() int { return width }}

	p.begin("reading the closure")
	c.advance(340 * time.Millisecond)
	p.done("42 paths")

	p.begin("pushing")
	p.count(0, 4)
	c.advance(2 * time.Second)
	p.add(1)
	lines, _ := screen(t, out.String()+"\n")
	if got, want := lines[len(lines)-1], "⠼ pushing                    2.0s  ██████░░░░░░░░░░░░░░░░░░  1/4"; got != want {
		t.Errorf("the running step:\n%q\nwant:\n%q", got, want)
	}
	p.add(3)
	p.done("4 paths, 2.0 MiB")

	// A line that is longer than the terminal is wide is cut: one that
	// wraps cannot be drawn over.
	width = 60
	p.begin("a step with a name that is longer than its column")
	p.count(1, 2)
	lines, _ = screen(t, out.String()+"\n")
	if got := []rune(lines[len(lines)-1]); len(got) != 59 {
		t.Errorf("the running step is %d wide on a terminal of 60: %q", len(got), string(got))
	}
	p.fail(errors.New("it failed"))

	p.begin("activating")
	c.advance(time.Minute)
	p.finish("rollout #13 completed")

	want := []string{
		"✓ reading the closure        0.3s  42 paths",
		"✓ pushing                    2.0s  4 paths, 2.0 MiB",
		"✗ a step with a name that is longer than its column   0.0s  failed",
		"✓ activating                1m00s",
		"  total                     1m02s  rollout #13 completed",
	}
	lines, fresh := screen(t, out.String())
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("the screen:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if !fresh {
		t.Error("the cursor is not at the start of a fresh line")
	}
}

// A show that is closed in the middle of a step, as after --detach or an
// error nobody marked, takes the line of that step off the terminal.
func TestProgressClosedInAStep(t *testing.T) {
	var out buffer
	p := &progress{out: &out, live: true, now: newClock().now, width: func() int { return 80 }, every: time.Millisecond}
	p.begin("connecting to draal")
	p.done("")
	p.begin("rolling out")
	// The ticker redraws the step by itself.
	before := len(out.String())
	waitFor(t, "the step to be redrawn", func() bool { return len(out.String()) > before })
	p.close()
	p.close()
	lines, fresh := screen(t, out.String())
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "✓ connecting to draal") || !fresh {
		t.Errorf("the screen: %q, fresh line: %v", lines, fresh)
	}
}
