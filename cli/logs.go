package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// logsCommand is `great-machine logs [-f] SERVICE [ZATHRAS]`.
func logsCommand() *cli.Command {
	return &cli.Command{
		Name:      "logs",
		Usage:     "print the last 400 lines of a zathras' service, and follow with -f",
		ArgsUsage: "SERVICE [ZATHRAS]",
		Description: "ZATHRAS is a name or the beginning of one, or of an endpoint ID. It may\n" +
			"be left out when the service has one zathras.\n\n" +
			"A line is its time, its stream (out, err, or -- for what zathras says\n" +
			"itself) and its text.",
		Flags: operatorFlags(&cli.BoolFlag{Name: "follow", Aliases: []string{"f"}, EnvVars: []string{"GREAT_MACHINE_FOLLOW"},
			Usage: "go on with every new line, until interrupted"}),
		Action: func(c *cli.Context) error {
			cfg, err := readTarget(c)
			if err != nil {
				return err
			}
			client, _, err := connect(c.Context, c)
			if err != nil {
				return err
			}
			defer client.Close()
			return runLogs(c.Context, client, cfg, c.Bool("follow"), c.App.Writer, time.Local)
		},
	}
}

// target is the zathras a command is about: SERVICE [ZATHRAS].
type target struct {
	service string
	zathras string // a name, the beginning of one or of an ID, or ""
}

// readTarget reads SERVICE [ZATHRAS].
func readTarget(c *cli.Context) (target, error) {
	args, err := arguments(c, 1, "SERVICE", "ZATHRAS")
	if err != nil {
		return target{}, err
	}
	name, err := service(args[0])
	if err != nil {
		return target{}, err
	}
	t := target{service: name}
	if len(args) == 2 {
		t.zathras = args[1]
	}
	return t, nil
}

// resolve finds the zathras that t names in the fleet as it is now.
func (t target) resolve(ctx context.Context, client *operator.Client) (wire.Zathras, error) {
	f, err := fleet(ctx, client)
	if err != nil {
		return wire.Zathras{}, err
	}
	return operator.Resolve(f, t.service, t.zathras)
}

// runLogs prints the ring of a zathras, and with follow what follows,
// until ctx is done. An interrupt is how following ends, and no error.
func runLogs(ctx context.Context, client *operator.Client, t target, follow bool, stdout io.Writer, zone *time.Location) error {
	z, err := t.resolve(ctx, client)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines, err := client.Logs(ctx, z.ID, follow)
	if err != nil {
		return fmt.Errorf("the log of %s: %w", z.Name, err)
	}
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				if follow && ctx.Err() == nil {
					return fmt.Errorf("the log of %s ended: the zathras or draal has gone", z.Name)
				}
				return nil
			}
			if _, err := fmt.Fprintln(stdout, formatLogLine(line, zone)); err != nil {
				return err
			}
		case <-ctx.Done():
			if follow {
				return nil
			}
			return errors.New("interrupted")
		}
	}
}

// formatLogLine writes a line of the ring as its time in zone, its stream
// and its text. The stream is out or err, and -- for what zathras says
// itself, which so stands out and is found with `grep ' -- '`.
func formatLogLine(l wire.LogLine, zone *time.Location) string {
	stream := l.Stream
	switch l.Stream {
	case logring.Stdout:
		stream = "out"
	case logring.Stderr:
		stream = "err"
	case logring.Zathras:
		stream = "-- "
	}
	text := l.Text
	if l.Lost > 0 {
		noun := "lines"
		if l.Lost == 1 {
			noun = "line"
		}
		text = fmt.Sprintf("… %d %s lost", l.Lost, noun)
	}
	return fmt.Sprintf("%s %s %s", l.At.In(zone).Format("15:04:05.000"), stream, text)
}
