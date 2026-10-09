package zathras

import (
	"io"
	"slices"
	"testing"

	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/testservice/build"
	"github.com/fables-for-robots/great-machine/wire"
)

func logs(follow bool) *wire.Request {
	return &wire.Request{Logs: &wire.LogsRequest{Follow: follow}}
}

// has says whether lines holds a line of stream with text.
func has(lines []wire.LogLine, stream, text string) bool {
	return slices.ContainsFunc(lines, func(l wire.LogLine) bool { return l.Stream == stream && l.Text == text })
}

func TestLogs(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	if _, err := l.call(activate(1, build.Binary(t), "--stdout", "hello from the service", "--stderr", "and a warning")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the lines of the service to be in the ring", func() bool {
		lines := w.ring.Lines()
		return has(lines, logring.Stdout, "hello from the service") && has(lines, logring.Stderr, "and a warning")
	})
	w.ring.Add(logring.Zathras, "the last line")

	s := l.open(logs(false))
	var got []wire.LogLine
	for {
		resp, err := l.read(s)
		if err == io.EOF {
			break // the agent ends the stream after the last line
		}
		if err != nil || resp.Line == nil {
			t.Fatalf("logs answered %+v, %v, want a line", resp, err)
		}
		got = append(got, *resp.Line)
	}
	if !has(got, logring.Stdout, "hello from the service") || !has(got, logring.Stderr, "and a warning") {
		t.Errorf("the lines of the service are not among %+v", got)
	}
	if last := got[len(got)-1]; last.Text != "the last line" || last.Stream != logring.Zathras || last.At.IsZero() {
		t.Errorf("the last line is %+v, want the last of the ring, with its time", last)
	}
	s.Close()
	w.idle()
}

func TestLogsOfAnEmptyRing(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	s := l.open(logs(false))
	if resp, err := l.read(s); err != io.EOF {
		t.Fatalf("logs answered %+v, %v, want the end of the stream", resp, err)
	}
}

func TestLogsFollow(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	w.ring.Add(logring.Stdout, "one")
	w.ring.Add(logring.Stdout, "two")

	s := l.open(logs(true))
	for _, want := range []string{"one", "two"} {
		resp, err := l.read(s)
		if err != nil || resp.Line == nil || resp.Line.Text != want {
			t.Fatalf("logs answered %+v, %v, want the line %q", resp, err, want)
		}
	}
	// The stream goes on, and a line that is written later arrives.
	for _, want := range []string{"three", "four"} {
		w.ring.Add(logring.Stderr, want)
		resp, err := l.read(s)
		if err != nil || resp.Line == nil || resp.Line.Text != want || resp.Line.Stream != logring.Stderr {
			t.Fatalf("logs answered %+v, %v, want the line %q of stderr", resp, err, want)
		}
	}

	// draal closes the stream, and the follower is gone.
	s.Close()
	w.idle()
	w.ring.Add(logring.Stdout, "five") // to nobody
}

func TestLogsFollowEndsWithTheConnection(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	w.ring.Add(logring.Stdout, "one")
	s := l.open(logs(true))
	if resp, err := l.read(s); err != nil || resp.Line == nil {
		t.Fatalf("logs answered %+v, %v, want a line", resp, err)
	}
	eventually(t, "the follower to be counted", func() bool { return w.agent.handlers.Load() == 1 })
	l.conn.Close()
	w.idle()
}
