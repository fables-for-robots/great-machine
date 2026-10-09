// Command testservice is a service for tests: it writes what it is told
// to, and exits, hangs or ignores SIGTERM on request.
//
//	testservice [--stdout TEXT] [--stderr TEXT] [--exit-after D] [--exit-code N] [--ignore-term] [--spawn]
//
// It writes TEXT and a line feed to the stream named, once, at its start,
// and after it has arranged how it takes SIGTERM: a test that has read
// the text knows that SIGTERM does what the flags say. With --exit-after
// it exits after D with --exit-code (0 unless given). Without, it runs
// until SIGTERM and exits with 0, or with --ignore-term until it is
// killed. --spawn starts a child in its process group that ignores
// SIGTERM and writes its process ID to standard output as "child <pid>".
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// childEnv is set in the environment of the child that --spawn starts.
const childEnv = "TESTSERVICE_CHILD"

func main() {
	if os.Getenv(childEnv) != "" {
		child()
	}
	stdout := flag.String("stdout", "", "write `TEXT` and a line feed to standard output at the start")
	stderr := flag.String("stderr", "", "write `TEXT` and a line feed to standard error at the start")
	exitAfter := flag.Duration("exit-after", 0, "exit after `D`")
	exitCode := flag.Int("exit-code", 0, "the exit status of --exit-after")
	ignoreTerm := flag.Bool("ignore-term", false, "ignore SIGTERM")
	spawn := flag.Bool("spawn", false, "start a child in this process group that ignores SIGTERM")
	flag.Parse()
	given := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { given[f.Name] = true })

	term := make(chan os.Signal, 1)
	if *ignoreTerm {
		signal.Ignore(syscall.SIGTERM)
	} else {
		signal.Notify(term, syscall.SIGTERM)
	}
	if *spawn {
		pid, err := startChild()
		if err != nil {
			fmt.Fprintln(os.Stderr, "testservice:", err)
			os.Exit(2)
		}
		fmt.Printf("child %d\n", pid)
	}
	if given["stdout"] {
		fmt.Fprintln(os.Stdout, *stdout)
	}
	if given["stderr"] {
		fmt.Fprintln(os.Stderr, *stderr)
	}

	var after <-chan time.Time
	if given["exit-after"] {
		after = time.After(*exitAfter)
	}
	// The ticker keeps the select from being the last thing that could
	// ever happen, which the Go runtime would take for a deadlock.
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-after:
			os.Exit(*exitCode)
		case <-term:
			os.Exit(0)
		case <-tick.C:
		}
	}
}

// startChild starts this program again as the child of --spawn, and
// returns its process ID when the child ignores SIGTERM.
func startChild() (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	defer r.Close()
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), childEnv+"=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{w} // file descriptor 3 of the child
	err = cmd.Start()
	w.Close()
	if err != nil {
		return 0, err
	}
	// The child closes its end when it is ready.
	io.Copy(io.Discard, r)
	return cmd.Process.Pid, nil
}

// child is what --spawn starts: it ignores SIGTERM, says so by closing
// file descriptor 3, and stays until it is killed. It keeps the standard
// output and standard error of its parent open.
func child() {
	signal.Ignore(syscall.SIGTERM)
	os.NewFile(3, "ready").Close()
	for {
		time.Sleep(time.Hour)
	}
}
