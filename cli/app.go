// Package cli is the command line of great-machine: one command for
// draal, one for zathras, and the operator's.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/fables-for-robots/great-machine/tui"
	"github.com/urfave/cli/v2"
)

// Version is the release this program was built as. The linker sets it:
// -X github.com/fables-for-robots/great-machine/cli.Version=v0.1.0
var Version = "dev"

// Run runs the command line and returns the exit status: 0, 1 for a
// failure, 2 for a command line that was not valid. A failure is written
// to stderr as "great-machine: <error>". `shell` returns the exit status
// of the remote shell, and a command that was interrupted while it
// followed a rollout returns 130.
//
// The context of a command ends with the first SIGINT, SIGTERM or SIGHUP.
// From then on signals are the system's again, so a second one ends the
// process at once.
func Run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signalContext()
	defer stop()
	return run(ctx, newApp(stdout, stderr), args)
}

// signalContext returns the context of a command, which ends with the
// first of the signals, and a function that ends it. From then on signals
// are the system's again.
func signalContext() (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-ctx.Done()
		stop()
	}()
	return ctx, stop
}

// commands returns every command of the program.
func commands() []*cli.Command {
	return []*cli.Command{
		draalCommand(),
		zathrasCommand(),
		idCommand(),
		rolloutCommand(),
		rollbackCommand(),
		statusCommand(),
		logsCommand(),
		shellCommand(),
		tuiCommand(tuiDeps{in: os.Stdin, out: os.Stdout, run: tui.Run}),
		versionCommand(),
	}
}

// newApp returns the program with its commands, writing to stdout and
// stderr.
func newApp(stdout, stderr io.Writer) *cli.App {
	return &cli.App{
		Name:            "great-machine",
		Usage:           "roll a program built with Nix out to a group of pods",
		Writer:          stdout,
		ErrWriter:       stderr,
		HideHelpCommand: true,
		Commands:        commands(),
	}
}

// usage is an error that says the command line was not valid.
type usage struct{ err error }

func (u *usage) Error() string { return u.err.Error() }
func (u *usage) Unwrap() error { return u.err }

// usageError returns an error that makes Run return 2 instead of 1: the
// command line was not valid. A command returns it from its Action for a
// missing argument, one too many, a flag that is required and not set, or
// a flag whose value cannot be. The format is that of fmt.Errorf, %w
// included.
func usageError(format string, args ...any) error {
	return &usage{err: fmt.Errorf(format, args...)}
}

// run runs app and turns what it returns into an exit status. It is
// apart from Run so that a test can run an app with commands of its own.
func run(ctx context.Context, app *cli.App, args []string) int {
	// The library would print a flag it does not know and the whole help
	// by itself, and leave the process by itself for some errors. Both
	// are this function's to do.
	onUsage := func(_ *cli.Context, err error, _ bool) error { return usageError("%w", err) }
	app.OnUsageError = onUsage
	setOnUsage(app.Commands, onUsage)
	app.ExitErrHandler = func(*cli.Context, error) {}
	// Without a command the help is shown; a word that is no command is
	// not a reason to show it.
	app.Action = func(c *cli.Context) error {
		if c.Args().Present() {
			return usageError("unknown command %q", c.Args().First())
		}
		return cli.ShowAppHelp(c)
	}
	err := app.RunContext(ctx, args)
	if err == nil {
		return 0
	}
	// A command that chooses its exit status returns a cli.ExitCoder: the
	// status of the remote shell, or 130 after an interrupt. Its message
	// is printed like any other, and one that is empty is not printed.
	var coder cli.ExitCoder
	if errors.As(err, &coder) {
		if msg := coder.Error(); msg != "" {
			fmt.Fprintln(app.ErrWriter, "great-machine:", msg)
		}
		return coder.ExitCode()
	}
	fmt.Fprintln(app.ErrWriter, "great-machine:", err)
	var u *usage
	if errors.As(err, &u) {
		return 2
	}
	return 1
}

// setOnUsage makes every command, and the commands inside it, report a
// flag that could not be read through onUsage.
func setOnUsage(cmds []*cli.Command, onUsage cli.OnUsageErrorFunc) {
	for _, cmd := range cmds {
		cmd.OnUsageError = onUsage
		setOnUsage(cmd.Subcommands, onUsage)
	}
}
