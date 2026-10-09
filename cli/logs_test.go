package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/wire"
)

func testLines() []wire.LogLine {
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	return []wire.LogLine{
		{At: at, Stream: logring.Zathras, Text: "started generation 8: /nix/store/abc-web/bin/web (pid 41)"},
		{At: at.Add(120 * time.Millisecond), Stream: logring.Stdout, Text: "listening on :8080"},
		{At: at.Add(1500 * time.Millisecond), Stream: logring.Stderr, Text: "cannot reach the database"},
		{At: at.Add(2 * time.Second), Stream: logring.Zathras, Lost: 12},
		{At: at.Add(2 * time.Second), Stream: logring.Zathras, Lost: 1},
		{At: at.Add(3 * time.Second), Stream: logring.Stdout, Text: ""},
	}
}

const testLinesText = "" +
	"14:00:00.000 --  started generation 8: /nix/store/abc-web/bin/web (pid 41)\n" +
	"14:00:00.120 out listening on :8080\n" +
	"14:00:01.500 err cannot reach the database\n" +
	"14:00:02.000 --  … 12 lines lost\n" +
	"14:00:02.000 --  … 1 line lost\n" +
	"14:00:03.000 out \n"

func TestFormatLogLine(t *testing.T) {
	zone := time.FixedZone("CEST", 2*3600)
	var b strings.Builder
	for _, line := range testLines() {
		b.WriteString(formatLogLine(line, zone) + "\n")
	}
	if got := b.String(); got != testLinesText {
		t.Errorf("got:\n%s\nwant:\n%s", got, testLinesText)
	}
}

// TestRunLogs asks a draal for the log of a zathras, over the protocol.
func TestRunLogs(t *testing.T) {
	zone := time.FixedZone("CEST", 2*3600)
	fake := &fakeDraal{fleets: []wire.Fleet{testFleet(statusNow)}, lines: testLines()}
	client := fake.start(t)

	t.Run("the ring", func(t *testing.T) {
		var out buffer
		if err := runLogs(context.Background(), client, target{service: "web", zathras: "web-b"}, false, &out, zone); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); got != testLinesText {
			t.Errorf("got:\n%s\nwant:\n%s", got, testLinesText)
		}
		asked := fake.asked()
		last := asked[len(asked)-1].Logs
		if last == nil || last.Zathras != testFleet(statusNow).Services[1].Zathrases[1].ID || last.Follow {
			t.Errorf("draal was asked %+v, want the log of web-b by its ID, without follow", last)
		}
	})

	t.Run("following ends with an interrupt, and well", func(t *testing.T) {
		var out buffer
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- runLogs(ctx, client, target{service: "web", zathras: "web-b"}, true, &out, zone) }()
		waitFor(t, "the lines", func() bool { return out.String() == testLinesText })
		select {
		case err := <-done:
			t.Fatalf("runLogs returned %v while it was to follow", err)
		default:
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("runLogs returned %v after the interrupt, want nil", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("runLogs did not return after the interrupt")
		}
	})

	t.Run("a name that names no zathras", func(t *testing.T) {
		var out buffer
		err := runLogs(context.Background(), client, target{service: "web", zathras: "nobody"}, false, &out, zone)
		if err == nil {
			t.Fatal("runLogs returned nil")
		}
		if out.String() != "" {
			t.Errorf("something was printed: %q", out.String())
		}
	})
}
