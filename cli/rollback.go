package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// rollbackCommand is `great-machine rollback [--parallel N] [--detach] SERVICE`.
func rollbackCommand() *cli.Command {
	return &cli.Command{
		Name:      "rollback",
		Usage:     "roll the stable generation of a service out again",
		ArgsUsage: "SERVICE",
		Description: "The stable generation is the target of the last rollout that completed.\n" +
			"draal refuses when there is none, and when the last rollout completed.\n\n" +
			"The command follows the rollout and ends with 0 when it completed and\n" +
			"with 1 when it halted or was superseded. An interrupt ends the following\n" +
			"and not the rollout.",
		Flags: operatorFlags(parallelFlag(), detachFlag()),
		Action: func(c *cli.Context) error {
			args, err := arguments(c, 1, "SERVICE")
			if err != nil {
				return err
			}
			req := wire.RollbackRequest{}
			if req.Service, err = service(args[0]); err != nil {
				return err
			}
			if req.Parallel, err = parallel(c); err != nil {
				return err
			}
			client, _, err := connect(c.Context, c)
			if err != nil {
				return err
			}
			defer client.Close()
			p := newProgress(c.App.ErrWriter)
			defer p.close()
			return runRollback(c.Context, client, req, c.Bool("detach"), c.App.Writer, p)
		},
	}
}

// runRollback asks draal for the rollback and follows the rollout that
// comes of it, unless detach says not to.
func runRollback(ctx context.Context, client *operator.Client, req wire.RollbackRequest, detach bool, stdout io.Writer, p *progress) (err error) {
	defer func() {
		if err != nil {
			p.fail(err)
		}
	}()
	p.begin("rolling back")
	r, err := client.Rollback(ctx, req)
	if err != nil {
		return fmt.Errorf("rolling %s back: %w", req.Service, err)
	}
	p.done(fmt.Sprintf("generation %d, rollout #%d", r.Generation, r.ID))
	return watchRollout(ctx, client, r, detach, stdout, p)
}
