package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// The flags that more than one command has. Each is a function: a flag
// keeps the value it was given, and so cannot be shared by two commands.

// draalFlag is --draal, of zathras and of every operator command.
func draalFlag() cli.Flag {
	return &cli.StringFlag{Name: "draal", EnvVars: []string{"GREAT_MACHINE_DRAAL"},
		Usage: "endpoint `ID` of draal, as draal logs it at its start (required)"}
}

// keyFlag is --key, of every operator command.
func keyFlag() cli.Flag {
	return &cli.StringFlag{Name: "key", EnvVars: []string{"GREAT_MACHINE_KEY"},
		Usage: "`FILE` of the operator's key, created on first use (default: great-machine/operator.key in the user's configuration directory)"}
}

// parallelFlag is --parallel, of rollout and rollback.
func parallelFlag() cli.Flag {
	return &cli.IntFlag{Name: "parallel", EnvVars: []string{"GREAT_MACHINE_PARALLEL"}, Value: 1,
		Usage: "zathrases restarted at a time; 0 is all of them at once"}
}

// detachFlag is --detach, of rollout and rollback.
func detachFlag() cli.Flag {
	return &cli.BoolFlag{Name: "detach", EnvVars: []string{"GREAT_MACHINE_DETACH"},
		Usage: "start the rollout and do not wait for it"}
}

// operatorFlags are the flags of every command that talks to draal as an
// operator, with the command's own behind them.
func operatorFlags(own ...cli.Flag) []cli.Flag {
	return append([]cli.Flag{draalFlag(), keyFlag()}, own...)
}

// required returns the value of a flag that has to be set, by itself or
// by its variable.
func required(c *cli.Context, name, variable string) (string, error) {
	v := strings.TrimSpace(c.String(name))
	if v == "" {
		return "", usageError("--%s is required: give it, or set %s", name, variable)
	}
	return v, nil
}

// draalID returns the endpoint ID that --draal names.
func draalID(c *cli.Context) (wire.ID, error) {
	text, err := required(c, "draal", "GREAT_MACHINE_DRAAL")
	if err != nil {
		return "", err
	}
	id, err := node.ParseID(text)
	if err != nil {
		return "", usageError("--draal: %q is not an endpoint ID, as draal logs one at its start: %w", text, err)
	}
	return id, nil
}

// parallel returns the value of --parallel.
func parallel(c *cli.Context) (int, error) {
	n := c.Int("parallel")
	if n < 0 {
		return 0, usageError("--parallel: %d is not a number of zathrases: want 0 for all of them at once, or more", n)
	}
	return n, nil
}

// arguments returns the arguments of a command that takes those named, of
// which the first min have to be there. The library stops reading flags
// at the first argument, so a flag behind it would be taken for one: that
// is refused here, with what to do about it.
func arguments(c *cli.Context, min int, names ...string) ([]string, error) {
	args := c.Args().Slice()
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") && arg != "-" {
			return nil, usageError("%s: the flag %s has to come before %s", c.Command.Name, arg, names[0])
		}
	}
	if len(args) > len(names) {
		return nil, usageError("%s: unexpected argument %q", c.Command.Name, args[len(names)])
	}
	if len(args) < min {
		return nil, usageError("%s: %s is missing; usage: great-machine %s %s", c.Command.Name, names[len(args)], c.Command.Name, c.Command.ArgsUsage)
	}
	return args, nil
}

// noArguments refuses any argument, for a command that takes none.
func noArguments(c *cli.Context) error {
	if c.Args().Present() {
		return usageError("%s takes no argument, got %q", c.Command.Name, c.Args().First())
	}
	return nil
}

// service returns name if it can be the name of a service.
func service(name string) (string, error) {
	if err := wire.ValidService(name); err != nil {
		return "", usageError("%q is not the name of a service: want a DNS label, such as web or api-2", name)
	}
	return name, nil
}

// parseSize reads a count of bytes: a number, or one with the unit KiB,
// MiB or GiB, as jaccard-stored reads one.
func parseSize(text string) (int64, error) {
	units := []struct {
		suffix string
		factor int64
	}{{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}}
	factor := int64(1)
	digits := strings.TrimSpace(text)
	for _, u := range units {
		if rest, ok := strings.CutSuffix(digits, u.suffix); ok {
			digits, factor = strings.TrimSpace(rest), u.factor
			break
		}
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || n <= 0 || n > (1<<62)/factor {
		return 0, fmt.Errorf("%q is not a size: want bytes, or a number with KiB, MiB or GiB", text)
	}
	return n * factor, nil
}
