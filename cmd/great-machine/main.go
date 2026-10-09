// Command great-machine is draal, zathras and the operator's commands.
package main

import (
	"os"

	"github.com/fables-for-robots/great-machine/cli"
)

func main() {
	os.Exit(cli.Run(os.Args, os.Stdout, os.Stderr))
}
