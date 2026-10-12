// Command great-machine is draal, zathras and the operator's commands.
package main

import (
	"os"

	"github.com/fables-for-robots/great-machine/cli"
	"github.com/fables-for-robots/great-machine/launcher"
	"github.com/fables-for-robots/great-machine/reaper"
)

func main() {
	// A zathras starts its service and its shells as this program, which
	// then replaces itself with the program that was meant. That comes
	// before everything else: it is no command, and what follows it on
	// the command line is the other program's.
	if status, was := launcher.Replace(os.Args); was {
		os.Exit(status)
	}
	// As the first process of a container the program runs itself again
	// as a child, and here only passes signals on and waits for every
	// child, so that none stays a zombie.
	if status, was := reaper.Init(); was {
		os.Exit(status)
	}
	os.Exit(cli.Run(os.Args, os.Stdout, os.Stderr))
}
