package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/fables-for-robots/great-machine/human"
	xterm "golang.org/x/term"
)

// progress shows on standard error what a rollout is doing, as
// jaccard-store shows a push. Every step leaves a line: its name, the
// time it took and what came of it. On a terminal the line of the running
// step is redrawn in place, with a bar when the step counts what it does.
// Elsewhere a step is one plain line when it ends, and a step that counts
// says where it is every few seconds, so that a log shows it moving.
//
// Its methods may be called from several goroutines.
type progress struct {
	out   io.Writer
	live  bool       // out is a terminal
	width func() int // of the terminal; 0 for unknown
	now   func() time.Time
	every time.Duration // between two redrawings on a terminal; 0 for none

	mu      sync.Mutex
	began   time.Time // when the first step began; zero before it
	step    *step     // the running one; nil between steps
	frame   int
	closed  bool
	stop    chan struct{} // closed to end the redrawing; nil when none runs
	stopped chan struct{} // closed when the redrawing has ended
}

// step is the running step.
type step struct {
	name        string
	start       time.Time
	counts      bool // it says how far it is
	done, total uint64
	said        time.Time // when a line was last written for it off a terminal
	saidDone    uint64
}

const (
	// liveEvery is how often the running step is redrawn on a terminal,
	// and plainEvery how often at most it gets a line elsewhere.
	liveEvery  = 100 * time.Millisecond
	plainEvery = 5 * time.Second
	// nameWidth and timeWidth are the columns a line on a terminal begins
	// with: what is done and for how long.
	nameWidth = 24
	timeWidth = 6
	barWidth  = 24
)

// newProgress returns the progress of a command that writes to out. It
// draws when out is a terminal.
func newProgress(out io.Writer) *progress {
	p := &progress{out: out, now: time.Now, width: func() int { return 0 }}
	f, ok := out.(*os.File)
	if !ok || !xterm.IsTerminal(int(f.Fd())) || os.Getenv("TERM") == "dumb" {
		return p
	}
	p.live, p.every = true, liveEvery
	p.width = func() int {
		w, _, err := xterm.GetSize(int(f.Fd()))
		if err != nil || w <= 0 {
			return 80
		}
		return w
	}
	return p
}

// begin starts a step. A step that is running ends first, with nothing to
// say for itself.
func (p *progress) begin(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.end("")
	now := p.now()
	if p.began.IsZero() {
		p.began = now
	}
	p.step = &step{name: name, start: now, said: now}
	p.draw(now)
	if p.live && p.every > 0 && p.stop == nil {
		p.stop, p.stopped = make(chan struct{}), make(chan struct{})
		go p.run(p.stop, p.stopped)
	}
}

// count says how far the running step is: done of total.
func (p *progress) count(done, total uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.step == nil {
		return
	}
	p.step.counts, p.step.done, p.step.total = true, done, total
	p.draw(p.now())
}

// add says that the running step has done n more.
func (p *progress) add(n uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.step == nil {
		return
	}
	p.step.done += n
	p.draw(p.now())
}

// done ends the running step and leaves its line: the time it took and
// what came of it.
func (p *progress) done(summary string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.end(summary)
}

// end is done with p.mu held.
func (p *progress) end(summary string) {
	s := p.step
	if s == nil {
		return
	}
	p.step = nil
	p.leave("✓", s.name, p.now().Sub(s.start), summary)
}

// fail marks the running step as the one err came in, and leaves its
// line. Without a running step it does nothing.
func (p *progress) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.step
	if s == nil {
		return
	}
	p.step = nil
	how := "failed"
	if errors.Is(err, context.Canceled) {
		how = "interrupted"
	}
	d := p.now().Sub(s.start)
	if p.live {
		fmt.Fprintf(p.out, "\r\033[K%s\n", liveLine("✗", s.name, human.Took(d), how))
		return
	}
	fmt.Fprintf(p.out, "%s: %s after %s\n", s.name, how, human.Took(d))
}

// finish ends the show with a last line: what came of it all, and the
// time of all steps together.
func (p *progress) finish(summary string) {
	p.halt()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.end("")
	if p.began.IsZero() {
		return
	}
	p.leave(" ", "total", p.now().Sub(p.began), summary)
	p.began = time.Time{}
}

// close ends the show without a word. The line of a step nobody ended is
// taken off the terminal. It may be called more than once, and nothing is
// shown after it.
func (p *progress) close() {
	p.halt()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.step != nil && p.live {
		fmt.Fprint(p.out, "\r\033[K")
	}
	p.step = nil
}

// halt ends the redrawing, and with it the taking of new steps.
func (p *progress) halt() {
	p.mu.Lock()
	stop, stopped := p.stop, p.stopped
	p.stop, p.closed = nil, true
	p.mu.Unlock()
	if stop != nil {
		close(stop)
		<-stopped
	}
}

// run redraws the running step now and then until stop is closed, so that
// its time moves while nothing else does.
func (p *progress) run(stop <-chan struct{}, stopped chan<- struct{}) {
	defer close(stopped)
	t := time.NewTicker(p.every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			p.mu.Lock()
			if p.step != nil {
				p.draw(p.now())
			}
			p.mu.Unlock()
		}
	}
}

// leave writes the line a step leaves behind. p.mu is held.
func (p *progress) leave(mark, name string, d time.Duration, summary string) {
	if p.live {
		fmt.Fprintf(p.out, "\r\033[K%s\n", liveLine(mark, name, human.Took(d), summary))
		return
	}
	if summary == "" {
		fmt.Fprintf(p.out, "%s (%s)\n", name, human.Took(d))
		return
	}
	fmt.Fprintf(p.out, "%s: %s (%s)\n", name, summary, human.Took(d))
}

// draw shows the running step: over its line on a terminal, and elsewhere
// as a line of its own when it counts, has moved, and the last line is
// plainEvery old. p.mu is held.
func (p *progress) draw(now time.Time) {
	s := p.step
	if s == nil {
		return
	}
	if p.live {
		p.frame++
		spinner := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
		rest := ""
		if s.counts {
			rest = fmt.Sprintf("%s  %d/%d", bar(s.done, s.total), s.done, s.total)
		}
		line := liveLine(string(spinner[p.frame%len(spinner)]), s.name, human.Took(now.Sub(s.start)), rest)
		fmt.Fprintf(p.out, "\r\033[K%s", fit(line, p.width()))
		return
	}
	if !s.counts || s.done == s.saidDone || now.Sub(s.said) < plainEvery {
		return
	}
	s.said, s.saidDone = now, s.done
	fmt.Fprintf(p.out, "%s: %d/%d after %s\n", s.name, s.done, s.total, human.Took(now.Sub(s.start)))
}

// liveLine is a line on a terminal: a mark, what is done, for how long,
// and what there is to say of it.
func liveLine(mark, name, time, rest string) string {
	line := fmt.Sprintf("%s %-*s %*s", mark, nameWidth, name, timeWidth, time)
	if rest != "" {
		line += "  " + rest
	}
	return line
}

// bar draws how much of total is done.
func bar(done, total uint64) string {
	filled := barWidth
	if done < total {
		filled = int(done * barWidth / total)
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
}

// fit cuts a line that is redrawn in place to what fits a terminal of
// width columns: a line that wraps cannot be drawn over.
func fit(line string, width int) string {
	if width <= 1 || utf8.RuneCountInString(line) < width {
		return line
	}
	return string([]rune(line)[:width-1])
}
