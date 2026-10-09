// Command pid1 is what scripts/test-pid1.sh runs as the first process of
// a container, to test the package reaper.
//
//	pid1 serve   makes an orphan, then waits for SIGTERM and exits with 0
//	pid1 exit N  exits with N
package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/fables-for-robots/great-machine/reaper"
)

func main() {
	if status, was := reaper.Init(); was {
		os.Exit(status)
	}
	fmt.Printf("child: pid %d, parent %d\n", os.Getpid(), os.Getppid())
	if len(os.Args) < 2 {
		fmt.Println("usage: pid1 serve | pid1 exit N")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "exit":
		status, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Println(err)
			os.Exit(2)
		}
		os.Exit(status)
	case "serve":
		term := make(chan os.Signal, 1)
		signal.Notify(term, syscall.SIGTERM)
		// The shell ends at once and leaves the sleep behind, which
		// the kernel hands to process 1.
		shell := exec.Command("/bin/sh", "-c", "sleep 5 & echo orphan: pid $!")
		shell.Stdout, shell.Stderr = os.Stdout, os.Stderr
		if err := shell.Run(); err != nil {
			fmt.Println("making the orphan:", err)
			os.Exit(2)
		}
		fmt.Println("serving")
		<-term
		fmt.Println("child: got SIGTERM")
		os.Exit(0)
	}
	os.Exit(2)
}
