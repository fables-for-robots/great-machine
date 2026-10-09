package cli

import (
	"fmt"
	"io"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/urfave/cli/v2"
)

// idCommand is `great-machine id`. It talks to nobody.
func idCommand() *cli.Command {
	return &cli.Command{
		Name:        "id",
		Usage:       "print the endpoint ID of the operator's key, creating the key if it is missing",
		Description: "The ID is what draal is given with --operator to let this operator in.",
		Flags:       []cli.Flag{keyFlag()},
		Action: func(c *cli.Context) error {
			if err := noArguments(c); err != nil {
				return err
			}
			path, err := keyPath(c)
			if err != nil {
				return err
			}
			return runID(path, c.App.Writer)
		},
	}
}

// runID prints the endpoint ID of the key in the file at path, which it
// creates if it is missing.
func runID(path string, stdout io.Writer) error {
	k, err := node.LoadOrCreateKey(path)
	if err != nil {
		return fmt.Errorf("the operator's key: %w", err)
	}
	_, err = fmt.Fprintln(stdout, k.ID())
	return err
}
