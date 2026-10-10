package cli

import (
	"context"
	"fmt"
	"net/netip"
	"os"

	"github.com/fables-for-robots/great-machine/human"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/tui"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
	xterm "golang.org/x/term"
)

// tuiDeps is what `great-machine tui` works with. The tests give it a
// terminal and a draal of their own.
type tuiDeps struct {
	in, out *os.File                                        // the terminal
	run     func(ctx context.Context, cfg tui.Config) error // tui.Run
	// addrs is for tests: where draal is, as node.DialConfig has it. No
	// flag sets it.
	addrs []netip.AddrPort
}

// tuiCommand is `great-machine tui`.
func tuiCommand(deps tuiDeps) *cli.Command {
	return &cli.Command{
		Name:  "tui",
		Usage: "the fleet in this terminal: services, zathrases, figures, logs, and a shell",
		Description: "Shows what the web page shows, and follows it: every service, the\n" +
			"zathrases of one and how far a rollout is, and of a zathras its state, its\n" +
			"figures and its log. It rolls nothing out. The key ? says the keys; s on a\n" +
			"zathras opens a shell in its container, which Ctrl-] leaves; q quits.\n" +
			"While a shell is being opened Esc gives it up, and nothing else that is\n" +
			"typed then is acted on or passed on to the shell.\n\n" +
			"It needs a terminal as its standard input and its standard output. When\n" +
			"the connection to draal is lost it says so, keeps what it showed, and\n" +
			"asks again by itself.",
		Flags: operatorFlags(),
		Action: func(c *cli.Context) error {
			if err := noArguments(c); err != nil {
				return err
			}
			s, err := readOperator(c)
			if err != nil {
				return err
			}
			if !xterm.IsTerminal(int(deps.in.Fd())) || !xterm.IsTerminal(int(deps.out.Fd())) {
				return usageError("tui needs a terminal as its standard input and its standard output; status and logs print the same without one")
			}
			s.addrs = deps.addrs
			return runTUI(c.Context, s, deps)
		},
	}
}

// runTUI runs the terminal UI on the terminal of deps, as the operator
// the settings name, until the user quits. What draal refuses at the
// first dial is returned, with the terminal as it was; a context that
// is done makes the status 130, with what Run had to say beyond that.
func runTUI(ctx context.Context, s operatorSettings, deps tuiDeps) error {
	// The key is read once, and before the terminal is taken: a file
	// that cannot be read is no reason to dial again and again.
	k, err := node.LoadOrCreateKey(s.key)
	if err != nil {
		return fmt.Errorf("the operator's key: %w", err)
	}
	err = deps.run(ctx, tui.Config{
		Dial: func(ctx context.Context) (tui.Operator, error) {
			client, err := s.dial(ctx, k)
			if err != nil {
				return nil, err
			}
			return tuiOperator{client}, nil
		},
		In:    deps.in,
		Out:   deps.out,
		Draal: human.ShortID(s.draal),
	})
	if err != nil && ctx.Err() != nil {
		// The status is that of an interrupt. What Run says beyond the
		// end of its context is said: that the terminal UI had to be cut
		// off, that the terminal may not be as it was, or a failure that
		// came with the end.
		if err == ctx.Err() || err == context.Cause(ctx) {
			return interrupted("")
		}
		return interrupted("%v", err)
	}
	return err
}

// tuiOperator is an *operator.Client as the terminal UI asks for one.
// The two differ in one thing: the client's Shell returns its own type,
// and the terminal UI asks for the interface, so that its tests and its
// demo can give it sessions of their own.
type tuiOperator struct{ *operator.Client }

// A tuiOperator is a tui.Operator.
var _ tui.Operator = tuiOperator{}

// Shell asks for a shell in the container of a zathras. A shell that
// was refused is no session at all: not one that holds a nil pointer.
func (o tuiOperator) Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (tui.Shell, error) {
	sh, err := o.Client.Shell(ctx, zathras, req)
	if err != nil {
		return nil, err
	}
	return sh, nil
}
