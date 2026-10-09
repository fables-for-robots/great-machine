package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/wire"
)

// follow shows the rollout id of a service, out of the fleets draal
// sends, until the rollout has ended. It returns nil when the rollout
// completed, and an error that says what became of it otherwise: it
// halted, with the reason; it was superseded, with the rollout that did
// so; draal went away; or ctx is done, which is an interrupt and status
// 130. The last two do not end the rollout, and the error says how to
// look at it.
//
// A fleet that does not have the rollout yet is passed over: draal
// answers a rollout before it sends the fleet that shows it.
func follow(ctx context.Context, fleets <-chan wire.Fleet, service string, id uint64, p *progress) error {
	goesOn := fmt.Sprintf("rollout #%d of %s goes on; look at it with: great-machine status %s", id, service, service)
	interrupt := func() error {
		p.fail(context.Canceled)
		return interrupted("interrupted: %s", goesOn)
	}

	// phase is the step that is shown: "", preparing or activating.
	phase := ""
	zathrases := 0
	enter := func(next string) {
		if phase == next {
			return
		}
		if phase != "" {
			p.done(fmt.Sprintf("%d zathrases", zathrases))
		}
		if next != "" {
			p.begin(next)
		}
		phase = next
	}

	for {
		var f wire.Fleet
		select {
		case <-ctx.Done():
			return interrupt()
		case next, ok := <-fleets:
			if !ok {
				// The watch ends with ctx as well as with draal.
				if ctx.Err() != nil {
					return interrupt()
				}
				return fmt.Errorf("the connection to draal ended: %s", goesOn)
			}
			f = next
		}

		var r *wire.RolloutInfo
		for _, s := range f.Services {
			if s.Name == service {
				r, zathrases = s.Rollout, len(s.Zathrases)
			}
		}
		switch {
		case r == nil || r.ID < id:
			continue
		case r.ID > id:
			return fmt.Errorf("rollout #%d of %s was superseded by rollout #%d", id, service, r.ID)
		}
		switch r.State {
		case wire.RolloutPreparing:
			enter("preparing")
			p.count(uint64(r.Prepared), uint64(zathrases))
		case wire.RolloutActivating:
			enter("activating")
			p.count(uint64(r.Activated), uint64(zathrases))
		case wire.RolloutCompleted:
			enter("")
			return nil
		case wire.RolloutHalted:
			return fmt.Errorf("rollout #%d of %s halted: %s", id, service, r.Reason)
		case wire.RolloutSuperseded:
			return fmt.Errorf("rollout #%d of %s was superseded", id, service)
		default:
			return fmt.Errorf("rollout #%d of %s is in a state this program does not know: %q", id, service, r.State)
		}
	}
}

// watchRollout is what rollout and rollback do once draal has answered
// with the rollout: with detach they print its number and leave, and
// otherwise they follow it to its end.
func watchRollout(ctx context.Context, client *operator.Client, r wire.RolloutInfo, detach bool, stdout io.Writer, p *progress) error {
	if detach {
		p.close()
		_, err := fmt.Fprintf(stdout, "rollout #%d of %s to generation %d was started; look at it with: great-machine status %s\n",
			r.ID, r.Service, r.Generation, r.Service)
		return err
	}
	watching, cancel := context.WithCancel(ctx)
	defer cancel()
	fleets, err := client.Watch(watching)
	if err != nil {
		return fmt.Errorf("watching rollout #%d of %s, which goes on: %w", r.ID, r.Service, err)
	}
	if err := follow(ctx, fleets, r.Service, r.ID, p); err != nil {
		return err
	}
	p.finish(fmt.Sprintf("rollout #%d completed: %s runs generation %d", r.ID, r.Service, r.Generation))
	return nil
}
