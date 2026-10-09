package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// rolloutFleet is a fleet in which the last rollout of web, which has
// three zathrases, is r.
func rolloutFleet(r wire.RolloutInfo) wire.Fleet {
	r.Service = "web"
	return wire.Fleet{Services: []wire.Service{
		{Name: "api", Rollout: &wire.RolloutInfo{ID: 99, Service: "api", State: wire.RolloutHalted, Reason: "not ours"}},
		{Name: "web", Rollout: &r, Zathrases: make([]wire.Zathras, 3)},
	}}
}

// fleetsOf returns a channel that holds the fleets and, with closed, has
// ended behind them.
func fleetsOf(closed bool, fleets ...wire.Fleet) <-chan wire.Fleet {
	ch := make(chan wire.Fleet, len(fleets))
	for _, f := range fleets {
		ch <- f
	}
	if closed {
		close(ch)
	}
	return ch
}

func TestFollow(t *testing.T) {
	const hint = "rollout #13 of web goes on; look at it with: great-machine status web"
	before := rolloutFleet(wire.RolloutInfo{ID: 12, State: wire.RolloutCompleted})
	preparing := rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutPreparing, Prepared: 1})
	activating := rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutActivating, Prepared: 3, Activated: 1})

	tests := []struct {
		name   string
		fleets <-chan wire.Fleet
		err    string // "" for nil
		status int    // of a cli.ExitCoder; 0 for an error that is none
		output string
	}{
		{
			name: "completed",
			// The first fleet is older than the rollout, and is passed over.
			fleets: fleetsOf(false, before, wire.Fleet{}, preparing, activating,
				rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutCompleted})),
			output: "preparing: 3 zathrases (0.0s)\nactivating: 3 zathrases (0.0s)\n",
		},
		{
			name:   "completed before it was looked at",
			fleets: fleetsOf(false, rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutCompleted})),
		},
		{
			name: "halted",
			fleets: fleetsOf(false, preparing, activating,
				rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutHalted, Reason: "web-b: exited with status 1 after 0.2s"})),
			err:    "rollout #13 of web halted: web-b: exited with status 1 after 0.2s",
			output: "preparing: 3 zathrases (0.0s)\n",
		},
		{
			name:   "superseded",
			fleets: fleetsOf(false, preparing, rolloutFleet(wire.RolloutInfo{ID: 14, State: wire.RolloutPreparing})),
			err:    "rollout #13 of web was superseded by rollout #14",
		},
		{
			name:   "draal went away",
			fleets: fleetsOf(true, preparing),
			err:    "the connection to draal ended: " + hint,
		},
		{
			name:   "interrupted",
			fleets: nil, // set below: nothing comes, and the context is done
			err:    "interrupted: " + hint,
			status: 130,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.fleets == nil {
				tt.fleets = fleetsOf(false)
				cancel()
			}
			var out buffer
			err := follow(ctx, tt.fleets, "web", 13, plainProgress(&out))
			switch {
			case tt.err == "" && err != nil:
				t.Fatalf("follow returned %v, want nil", err)
			case tt.err != "" && (err == nil || err.Error() != tt.err):
				t.Fatalf("follow returned %v, want %q", err, tt.err)
			}
			var coder cli.ExitCoder
			if got := errors.As(err, &coder); got != (tt.status != 0) || got && coder.ExitCode() != tt.status {
				t.Errorf("follow returned %#v, want an error with the exit status %d", err, tt.status)
			}
			if got := out.String(); got != tt.output {
				t.Errorf("output:\n%s\nwant:\n%s", got, tt.output)
			}
		})
	}
}

// An interrupt in the middle of a step: the step says so, and the error
// is the hint with the status 130. The same goes for a watch that ended
// because the context is done.
func TestFollowInterrupted(t *testing.T) {
	for _, closed := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		fleets := make(chan wire.Fleet)
		var out buffer
		done := make(chan error, 1)
		go func() { done <- follow(ctx, fleets, "web", 13, plainProgress(&out)) }()
		fleets <- rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutActivating, Activated: 1})
		cancel()
		if closed {
			close(fleets)
		}
		err := <-done
		var coder cli.ExitCoder
		if !errors.As(err, &coder) || coder.ExitCode() != 130 || !strings.Contains(err.Error(), "great-machine status web") {
			t.Errorf("closed %v: follow returned %v, want the hint with the exit status 130", closed, err)
		}
		if got, want := out.String(), "activating: interrupted after 0.0s\n"; got != want {
			t.Errorf("closed %v: output %q, want %q", closed, got, want)
		}
	}
}
