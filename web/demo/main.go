// Command demo serves the web page of great-machine over a made-up fleet,
// for developing the page: the real handler of the package web, and
// behind it the controller of the package demo, which invents four
// services, their zathrases, their figures and their logs, and moves
// them along.
//
//	go run ./web/demo [--addr 127.0.0.1:8080] [--dist DIR] [--empty]
//
// With --empty there is no service at all, as draal has none when it was
// started for the first time. There is no store either: where a draal
// serves the page of its store, the demo serves a line that says so.
//
// Everything it shows is made up but the shell: THE SHELL OF THE PAGE IS A
// REAL SHELL ON THE MACHINE THAT RUNS THE DEMO, as the user who runs it.
// The page has no authentication and answers under every host name.
// Leave the demo on a loopback address.
//
// It is not part of the program, and nothing of great-machine uses it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fables-for-robots/great-machine/demo"
	"github.com/fables-for-robots/great-machine/web"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "where to listen, as `HOST:PORT`")
	dist := flag.String("dist", "", "serve the build of the page in `DIR` instead of the embedded one")
	empty := flag.Bool("empty", false, "serve a fleet without a service, as a draal that was just started")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "demo: %q is no argument of the demo\n", flag.Arg(0))
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *addr, *dist, *empty, os.Stderr, nil); err != nil {
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
}

// run serves the demo on addr until ctx is done: the made-up fleet, or
// with empty a fleet without a service. It writes to out what it serves,
// and calls ready, if set, with the URL once it listens.
func run(ctx context.Context, addr, dist string, empty bool, out io.Writer, ready func(url string)) error {
	cfg := web.Config{}
	if dist != "" {
		if info, err := os.Stat(dist); err != nil || !info.IsDir() {
			return fmt.Errorf("--dist %s is no directory", dist)
		}
		cfg.Dist = os.DirFS(dist)
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}
	defer l.Close()

	fleet := demo.New(empty)
	moving, stopMoving := context.WithCancel(context.Background())
	defer stopMoving()
	go fleet.Run(moving)
	cfg.Controller = fleet
	// The header of the page has a link to the page of the store, which
	// a draal has from jaccard-store, over the database of its store.
	cfg.Store = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "great-machine: the demo has a made-up fleet and no store. A draal serves the page of its store here: what was pushed, and what sharing and compression save.")
	})

	// The context of every request. A socket lives on when a server is
	// shut down, and ends when this is cancelled.
	requests, endRequests := context.WithCancel(context.Background())
	defer endRequests()
	srv := &http.Server{
		Handler:           web.New(cfg),
		BaseContext:       func(net.Listener) context.Context { return requests },
		ReadHeaderTimeout: 10 * time.Second,
	}

	url := "http://" + l.Addr().String() + "/"
	what := "a made-up fleet"
	if empty {
		what = "a fleet without a service"
	}
	fmt.Fprintf(out, "demo: %s behind the real API of great-machine, at %s\n", what, url)
	fmt.Fprintf(out, "demo: the shell of the page is a REAL SHELL ON THIS MACHINE, as the user who runs the demo\n")
	if !web.IsLoopback(addr) {
		fmt.Fprintf(out, "demo: %s is not loopback: whoever reaches it has that shell\n", addr)
	}
	if dist == "" && !web.Built() {
		fmt.Fprintf(out, "demo: the page is not built into this program, and %s answers 501. The API is there:\n", url)
		fmt.Fprintf(out, "demo: run `npm run dev` in ui/, or build the page and give its directory with --dist\n")
	}
	if ready != nil {
		ready(url)
	}

	served := make(chan error, 1)
	go func() { served <- srv.Serve(l) }()
	select {
	case err := <-served:
		return fmt.Errorf("serving: %w", err)
	case <-ctx.Done():
	}
	endRequests()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("shutting down: %w", err)
	}
	return nil
}
