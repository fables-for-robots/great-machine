package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fables-for-robots/great-machine/health"
	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/preload"
	"github.com/fables-for-robots/great-machine/ptyshell"
	"github.com/fables-for-robots/great-machine/stats"
	"github.com/fables-for-robots/great-machine/supervisor"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/fables-for-robots/great-machine/zathras"
	"github.com/urfave/cli/v2"
)

// The ring of a zathras: the last 400 lines, of at most 16 KiB each.
const (
	ringLines   = 400
	ringMaxLine = 16 << 10
)

// zathrasCommand is `great-machine zathras`, the init of an app container.
func zathrasCommand() *cli.Command {
	return &cli.Command{
		Name:  "zathras",
		Usage: "the init of an app container: runs the service draal tells it to run",
		Description: "The health check, GET /healthz on --health-addr, passes while the service\n" +
			"runs and at no other time. It is a readiness probe, not a liveness probe.",
		Flags: []cli.Flag{
			draalFlag(),
			&cli.StringFlag{Name: "service", EnvVars: []string{"GREAT_MACHINE_SERVICE"},
				Usage: "`NAME` of the service this zathras belongs to (required)"},
			&cli.StringFlag{Name: "name", EnvVars: []string{"GREAT_MACHINE_NAME"},
				Usage: "`NAME` the operator sees (default: the host name, which in a pod is the pod's name)"},
			&cli.StringFlag{Name: "preload-url", EnvVars: []string{"GREAT_MACHINE_PRELOAD_URL"}, Value: "http://127.0.0.1:9889/v1/preload",
				Usage: "`URL` of the preload endpoint of the sidecar"},
			&cli.StringFlag{Name: "shell", EnvVars: []string{"GREAT_MACHINE_SHELL"}, Value: "/bin/sh",
				Usage: "`PATH` of the program of a shell session"},
			&cli.DurationFlag{Name: "stop-timeout", EnvVars: []string{"GREAT_MACHINE_STOP_TIMEOUT"}, Value: 2 * time.Minute,
				Usage: "how long a service has to end after SIGTERM, before SIGKILL"},
			&cli.StringFlag{Name: "health-addr", EnvVars: []string{"GREAT_MACHINE_HEALTH_ADDR"}, Value: ":9890",
				Usage: "`ADDR`ess the health check listens on; empty for none"},
		},
		Action: func(c *cli.Context) error {
			cfg, err := readZathras(c)
			if err != nil {
				return err
			}
			return runZathras(c.Context, cfg, slog.New(slog.NewTextHandler(c.App.ErrWriter, nil)))
		},
	}
}

// zathrasConfig is what the command line of zathras says.
type zathrasConfig struct {
	draal       wire.ID
	service     string
	name        string
	preloadURL  string
	shell       string
	stopTimeout time.Duration
	healthAddr  string // "" for no health check
}

// readZathras reads the command line of zathras.
func readZathras(c *cli.Context) (zathrasConfig, error) {
	if err := noArguments(c); err != nil {
		return zathrasConfig{}, err
	}
	cfg := zathrasConfig{
		name:        strings.TrimSpace(c.String("name")),
		preloadURL:  c.String("preload-url"),
		shell:       c.String("shell"),
		stopTimeout: c.Duration("stop-timeout"),
		healthAddr:  c.String("health-addr"),
	}
	var err error
	if cfg.draal, err = draalID(c); err != nil {
		return zathrasConfig{}, err
	}
	name, err := required(c, "service", "GREAT_MACHINE_SERVICE")
	if err != nil {
		return zathrasConfig{}, err
	}
	if cfg.service, err = service(name); err != nil {
		return zathrasConfig{}, err
	}
	if cfg.stopTimeout <= 0 {
		return zathrasConfig{}, usageError("--stop-timeout: %s is not a time a service has to end in", cfg.stopTimeout)
	}
	if cfg.name == "" {
		if cfg.name, err = os.Hostname(); err != nil {
			return zathrasConfig{}, fmt.Errorf("no host name to go by, give a name with --name: %w", err)
		}
	}
	return cfg, nil
}

// runZathras is zathras until ctx is done, which SIGINT and SIGTERM do:
// it then stops the service, ends the shells and returns nil.
func runZathras(ctx context.Context, cfg zathrasConfig, log *slog.Logger) error {
	// The health check listens first: an address that is taken ends the
	// command before anything is started or draal has heard of it.
	var listener net.Listener
	if cfg.healthAddr != "" {
		var err error
		if listener, err = net.Listen("tcp", cfg.healthAddr); err != nil {
			return fmt.Errorf("the health check: %w", err)
		}
	}

	// A new key every time: the endpoint ID is the identity of this
	// process, and two pods must not share one.
	key, err := node.NewKey()
	if err != nil {
		if listener != nil {
			listener.Close()
		}
		return fmt.Errorf("making a key: %w", err)
	}

	ring := logring.New(ringLines, ringMaxLine)
	sup := supervisor.New(supervisor.Config{
		Ring:        ring,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		StopTimeout: cfg.stopTimeout,
		Log:         log,
	})
	agent := zathras.New(zathras.Config{
		Service: cfg.service,
		Name:    cfg.name,
		Version: Version,
		Dial: func(ctx context.Context) (node.Conn, error) {
			return node.Dial(ctx, node.DialConfig{Key: key, Server: cfg.draal, Log: log})
		},
		Supervisor: sup,
		Ring:       ring,
		Stats:      stats.NewReader(),
		Preload:    preload.Client{URL: cfg.preloadURL},
		Shell:      ptyshell.Config{Shell: cfg.shell},
		Log:        log,
	})
	log.Info("zathras", "endpoint", string(key.ID()), "service", cfg.service, "name", cfg.name, "draal", string(cfg.draal))

	// Whatever ends first ends the rest: a signal, or a part that failed.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	failed := make(chan error, 3)

	var web *http.Server
	if listener != nil {
		web = &http.Server{
			Handler:           health.Handler(func() wire.Phase { return sup.State().Phase }),
			ReadHeaderTimeout: 10 * time.Second,
		}
		log.Info("health check", "addr", listener.Addr().String())
		go func() {
			if err := web.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
				failed <- fmt.Errorf("the health check: %w", err)
				cancel()
			}
		}()
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		if err := sup.Run(ctx); err != nil {
			failed <- fmt.Errorf("the supervisor: %w", err)
		}
		cancel()
	})
	wg.Go(func() {
		if err := agent.Run(ctx); err != nil && ctx.Err() == nil {
			failed <- fmt.Errorf("the connection to draal: %w", err)
		}
		cancel()
	})

	// The order of the end, once ctx is done:
	//
	//  1. The agent ends the shells and leaves draal, and the supervisor
	//     stops the service: SIGTERM, and SIGKILL after --stop-timeout.
	//     Both are waited for here.
	//  2. The health check is shut down last. While the service is being
	//     stopped it goes on answering, with 503 as the phase is no longer
	//     running, so that the pod is taken out of its Service by an
	//     answer and not by a connection that is refused.
	wg.Wait()
	if web != nil {
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		web.Shutdown(shutdown)
	}
	select {
	case err := <-failed:
		return err
	default:
		log.Info("stopped")
		return nil
	}
}
