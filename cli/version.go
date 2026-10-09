package cli

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

// versionCommand is `great-machine version`.
func versionCommand() *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "print the version of this program",
		Action: func(c *cli.Context) error {
			if c.Args().Present() {
				return usageError("version takes no argument, got %q", c.Args().First())
			}
			_, err := fmt.Fprintln(c.App.Writer, Version)
			return err
		},
	}
}
