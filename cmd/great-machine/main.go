// Command great-machine is draal, zathras and the operator's commands.
package main

import (
	"os"

	"github.com/fables-for-robots/great-machine/cli"
	"github.com/fables-for-robots/great-machine/reaper"
)

func main() {
	// As the first process of a container the program runs itself again
	// as a child, and here only passes signals on and waits for every
	// child, so that none stays a zombie.
	if status, was := reaper.Init(); was {
		os.Exit(status)
	}
	os.Exit(cli.Run(os.Args, os.Stdout, os.Stderr))
}
