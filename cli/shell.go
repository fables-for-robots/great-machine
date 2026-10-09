package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/term"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// shellCommand is `great-machine shell SERVICE [ZATHRAS]`.
func shellCommand() *cli.Command {
	return &cli.Command{
		Name:      "shell",
		Usage:     "a shell in the container of a zathras",
		ArgsUsage: "SERVICE [ZATHRAS]",
		Description: "ZATHRAS is a name or the beginning of one, or of an endpoint ID. It may\n" +
			"be left out when the service has one zathras.\n\n" +
			"Ctrl-] ends the session: the remote shell is hung up, as when its\n" +
			"terminal is closed, and what it runs ends with it. With a standard input\n" +
			"that is no terminal (echo uptime | great-machine shell web) the input is\n" +
			"passed on and the shell ends with it. The exit status is that of the shell.",
		Flags: operatorFlags(),
		Action: func(c *cli.Context) error {
			t, err := readTarget(c)
			if err != nil {
				return err
			}
			client, _, err := connect(c.Context, c)
			if err != nil {
				return err
			}
			defer client.Close()
			return runShell(c.Context, client, t, os.Stdin, os.Stdout, c.App.ErrWriter)
		},
	}
}

// runShell joins stdin and stdout to a shell of the zathras that t names.
// What it returns for a shell that ended with a status other than 0 makes
// that status the command's.
func runShell(ctx context.Context, client *operator.Client, t target, stdin *os.File, stdout, stderr io.Writer) error {
	z, err := t.resolve(ctx, client)
	if err != nil {
		return err
	}
	cols, rows := term.Size(stdin)
	sh, err := client.Shell(ctx, z.ID, wire.ShellRequest{Zathras: z.ID, Cols: cols, Rows: rows, Term: os.Getenv("TERM")})
	if err != nil {
		return fmt.Errorf("a shell on %s: %w", z.Name, err)
	}
	defer sh.Close()
	exit, detached, err := term.Attach(ctx, stdin, stdout, sh)
	return shellResult(ctx, z.Name, exit, detached, err, stderr)
}

// shellResult turns what term.Attach returned into what the command
// returns.
func shellResult(ctx context.Context, name string, exit int, detached bool, err error, stderr io.Writer) error {
	switch {
	case detached:
		// The line of the shell's prompt is left as it is.
		fmt.Fprintln(stderr, "\ndetached")
		return nil
	case err != nil && ctx.Err() != nil:
		return interrupted("")
	case err != nil:
		return fmt.Errorf("the shell on %s: %w", name, err)
	case exit < 0:
		return fmt.Errorf("the shell on %s was ended by a signal", name)
	case exit != 0:
		return cli.Exit("", exit)
	}
	return nil
}
