package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

var statusNow = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func TestFormatFleet(t *testing.T) {
	want := strings.Join([]string{
		"SERVICE  STABLE  ZATHRASES                     LAST ROLLOUT",
		"api      -       none                          -",
		"web      7       2 running, 1 backoff, 1 idle  #13 activating 2/4",
		"",
	}, "\n")
	if got := formatFleet(testFleet(statusNow)); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if got, want := formatFleet(wire.Fleet{}), "no services: no zathras is connected and nothing was rolled out\n"; got != want {
		t.Errorf("an empty fleet: got %q, want %q", got, want)
	}
}

func TestRolloutLine(t *testing.T) {
	zathrases := make([]wire.Zathras, 5)
	tests := []struct {
		rollout *wire.RolloutInfo
		want    string
	}{
		{nil, "-"},
		{&wire.RolloutInfo{ID: 12, State: wire.RolloutCompleted}, "#12 completed"},
		{&wire.RolloutInfo{ID: 13, State: wire.RolloutPreparing, Prepared: 1}, "#13 preparing 1/5"},
		{&wire.RolloutInfo{ID: 13, State: wire.RolloutActivating, Prepared: 5, Activated: 2}, "#13 activating 2/5"},
		{&wire.RolloutInfo{ID: 13, State: wire.RolloutHalted, Reason: "web-b: exited with status 1"}, "#13 halted: web-b: exited with status 1"},
		{&wire.RolloutInfo{ID: 11, State: wire.RolloutSuperseded}, "#11 superseded"},
	}
	for _, tt := range tests {
		if got := rolloutLine(wire.Service{Rollout: tt.rollout, Zathrases: zathrases}); got != tt.want {
			t.Errorf("got %q, want %q", got, tt.want)
		}
	}
}

func TestFormatService(t *testing.T) {
	want := strings.Join([]string{
		"web: stable generation 7, last rollout #13 activating 2/4",
		"rollout #13: to generation 8, 1 at a time, begun 1m35s ago",
		"NAME   ID        PHASE    GENERATION  SINCE          RESTARTS  CPU   MEMORY",
		"web-a  a1a1a1a1  running  8           up 3m12s       0         0.25  120.5 MiB of 512.0 MiB",
		"web-b  b2b2b2b2  running  8           up 31s         0         1.50  2.0 KiB",
		"web-c  c3c3c3c3  backoff  7           restart in 4s  3         -     -",
		"web-d  d4d4d4d4  idle     -           -              0         -     -",
		"",
	}, "\n")
	got, err := formatService(testFleet(statusNow), "web", statusNow)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	want = "api: stable generation -, last rollout -\nno zathras is connected\n"
	if got, err = formatService(testFleet(statusNow), "api", statusNow); err != nil || got != want {
		t.Errorf("a service without zathrases: got %q, %v; want %q", got, err, want)
	}

	_, err = formatService(testFleet(statusNow), "wbe", statusNow)
	if err == nil || err.Error() != `no service "wbe"; there are: api, web` {
		t.Errorf("an unknown service: got %v", err)
	}
}

func TestSince(t *testing.T) {
	tests := []struct {
		state wire.State
		want  string
	}{
		{wire.State{Phase: wire.PhaseRunning, RunningFor: 42 * time.Second}, "up 42s"},
		{wire.State{Phase: wire.PhaseRunning, RunningFor: 5*time.Hour + 3*time.Minute}, "up 5h03m"},
		{wire.State{Phase: wire.PhaseRunning, RunningFor: 52 * time.Hour}, "up 2d04h"},
		{wire.State{Phase: wire.PhaseBackoff, RestartAt: statusNow.Add(90 * time.Second)}, "restart in 1m30s"},
		{wire.State{Phase: wire.PhaseBackoff, RestartAt: statusNow.Add(-time.Second)}, "restart now"},
		{wire.State{Phase: wire.PhaseStopping}, "-"},
		{wire.State{Phase: wire.PhaseIdle}, "-"},
	}
	for _, tt := range tests {
		if got := since(tt.state, statusNow); got != tt.want {
			t.Errorf("%+v: got %q, want %q", tt.state, got, tt.want)
		}
	}
}

// TestRunStatus asks a draal for the fleet, over the protocol.
func TestRunStatus(t *testing.T) {
	fake := &fakeDraal{fleets: []wire.Fleet{testFleet(statusNow)}}
	client := fake.start(t)

	var out buffer
	if err := runStatus(context.Background(), client, "", &out, statusNow); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != formatFleet(testFleet(statusNow)) {
		t.Errorf("without a service:\n%s", got)
	}

	out = buffer{}
	if err := runStatus(context.Background(), client, "web", &out, statusNow); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "web-c  c3c3c3c3  backoff  7           restart in 4s  3") {
		t.Errorf("with a service:\n%s", got)
	}

	err := runStatus(context.Background(), client, "db", &out, statusNow)
	if err == nil || !strings.Contains(err.Error(), `no service "db"`) {
		t.Errorf("an unknown service: got %v", err)
	}
}
