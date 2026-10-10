// Command demo runs the terminal UI of great-machine over a made-up
// fleet, for developing the terminal UI: the real program of the package
// tui, and behind it the controller of the package demo, which invents
// four services, their zathrases, their figures and their logs, and
// moves them along.
//
//	go run ./tui/demo [--empty]
//
// With --empty there is no service at all, as draal has none when it was
// started for the first time.
//
// Everything it shows is made up but the shell: THE SHELL OF THE
// TERMINAL UI IS A REAL SHELL ON THE MACHINE THAT RUNS THE DEMO, as the
// user who runs it.
//
// It is not part of the program, and nothing of great-machine uses it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	xterm "golang.org/x/term"

	"github.com/fables-for-robots/great-machine/demo"
	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/tui"
	"github.com/fables-for-robots/great-machine/wire"
)

func main() {
	empty := flag.Bool("empty", false, "show a fleet without a service, as a draal that was just started")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "demo: %q is no argument of the demo\n", flag.Arg(0))
		os.Exit(2)
	}
	if !xterm.IsTerminal(int(os.Stdin.Fd())) || !xterm.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr, "demo: the terminal UI needs a terminal as its standard input and its standard output")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	if err := run(ctx, os.Stdin, os.Stdout, *empty, os.Stderr); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
}

// run runs the terminal UI on the terminal in and out, over the made-up
// fleet or, with empty, over a fleet without a service, until the user
// quits or ctx is done. It writes to errs what it is.
func run(ctx context.Context, in, out *os.File, empty bool, errs io.Writer) error {
	what := "a made-up fleet"
	if empty {
		what = "a fleet without a service"
	}
	fmt.Fprintf(errs, "demo: the shell of the terminal UI is a REAL SHELL ON THIS MACHINE, as the user who runs the demo\n")
	fmt.Fprintf(errs, "demo: %s behind the real terminal UI of great-machine\n", what)

	fleet := demo.New(empty)
	moving, stopMoving := context.WithCancel(context.Background())
	defer stopMoving()
	go fleet.Run(moving)

	return tui.Run(ctx, tui.Config{
		Dial:  func(context.Context) (tui.Operator, error) { return newOperator(fleet), nil },
		In:    in,
		Out:   out,
		Draal: "the demo",
	})
}

// operator is a draal.Controller as the tui.Operator of one connection:
// what an *operator.Client is over a real draal. Close ends what was
// asked of it, as closing a client does.
type operator struct {
	c    draal.Controller
	ctx  context.Context
	stop context.CancelFunc
}

// An operator is a tui.Operator.
var _ tui.Operator = (*operator)(nil)

// newOperator returns a connection to the controller.
func newOperator(c draal.Controller) *operator {
	ctx, stop := context.WithCancel(context.Background())
	return &operator{c: c, ctx: ctx, stop: stop}
}

// both returns a context that is done when ctx is, or when the
// connection is closed, and what frees it.
func (o *operator) both(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(o.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}

// Watch sends the fleet, now and whenever it changes, until ctx is done
// or the connection is closed.
func (o *operator) Watch(ctx context.Context) (<-chan wire.Fleet, error) {
	ctx, free := o.both(ctx)
	fleets, err := o.c.Watch(ctx)
	if err != nil {
		free()
		return nil, err
	}
	context.AfterFunc(ctx, free)
	return fleets, nil
}

// Rollouts returns the last rollouts of a service.
func (o *operator) Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error) {
	ctx, free := o.both(ctx)
	defer free()
	return o.c.Rollouts(ctx, service)
}

// History returns the samples of a zathras.
func (o *operator) History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error) {
	ctx, free := o.both(ctx)
	defer free()
	return o.c.History(ctx, zathras)
}

// Logs sends the log of a zathras, until ctx is done or the connection
// is closed.
func (o *operator) Logs(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error) {
	ctx, free := o.both(ctx)
	lines, err := o.c.Logs(ctx, zathras, follow)
	if err != nil {
		free()
		return nil, err
	}
	context.AfterFunc(ctx, free)
	return lines, nil
}

// Shell starts a shell: of the demo, a real one on this machine.
func (o *operator) Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (tui.Shell, error) {
	return o.c.Shell(ctx, zathras, req)
}

// Close ends the connection.
func (o *operator) Close() error {
	o.stop()
	return nil
}
