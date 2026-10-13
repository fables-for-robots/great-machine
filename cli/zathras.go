package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fables-for-robots/great-machine/health"
	"github.com/fables-for-robots/great-machine/launcher"
	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/ptyshell"
	"github.com/fables-for-robots/great-machine/stats"
	"github.com/fables-for-robots/great-machine/storemount"
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
		Description: "It serves the store of its service itself and mounts it on /nix/store,\n" +
			"for which its container needs CAP_SYS_ADMIN; the service and the shells\n" +
			"are started without that capability.\n\n" +
			"The health check, GET /healthz on --health-addr, passes while the service\n" +
			"runs and at no other time. It is a readiness probe, not a liveness probe.",
		Flags: []cli.Flag{
			draalFlag(),
			&cli.StringFlag{Name: "service", EnvVars: []string{"GREAT_MACHINE_SERVICE"},
				Usage: "`NAME` of the service this zathras belongs to (required)"},
			&cli.StringFlag{Name: "name", EnvVars: []string{"GREAT_MACHINE_NAME"},
				Usage: "`NAME` the operator sees (default: the host name, which in a pod is the pod's name)"},
			&cli.StringFlag{Name: "store-cache", EnvVars: []string{"GREAT_MACHINE_STORE_CACHE"}, Value: "/var/cache/great-machine",
				Usage: "`DIR`ectory of what the store has fetched; it is of one draal and one service"},
			&cli.BoolFlag{Name: "no-store", EnvVars: []string{"GREAT_MACHINE_NO_STORE"},
				Usage: "serve and mount no store: /nix/store is what the container has, and nothing is fetched"},
			&cli.StringFlag{Name: "user", EnvVars: []string{"GREAT_MACHINE_USER"},
				Usage: "`UID[:GID]` the service and the shells run as, with that group and no other (default: the user of zathras)"},
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

// zathrasStore is the store of a zathras, which it serves and mounts
// itself: *storemount.Mount.
type zathrasStore interface {
	zathras.Preloader
	// Stop takes the mount away and stops the server.
	Stop() error
}

// zathrasConfig is what the command line of zathras says.
type zathrasConfig struct {
	draal       wire.ID
	service     string
	name        string
	shell       string
	stopTimeout time.Duration
	healthAddr  string         // "" for no health check
	storeCache  string         // what the store has fetched
	noStore     bool           // no store of its own
	user        *launcher.User // who the service and the shells run as; nil: the user of zathras
	// addrs is for tests: draal, and with it the store, is dialed at these
	// addresses, without a relay and without a lookup. No flag sets it.
	addrs []netip.AddrPort
	// storeDir is for tests: the directory the store is mounted on. "":
	// /nix/store. No flag sets it.
	storeDir string
	// startStore is for tests: what starts the store. nil:
	// storemount.Start. No flag sets it.
	startStore func(storemount.Config) (zathrasStore, error)
}

// readZathras reads the command line of zathras.
func readZathras(c *cli.Context) (zathrasConfig, error) {
	if err := noArguments(c); err != nil {
		return zathrasConfig{}, err
	}
	cfg := zathrasConfig{
		name:        strings.TrimSpace(c.String("name")),
		shell:       c.String("shell"),
		stopTimeout: c.Duration("stop-timeout"),
		healthAddr:  c.String("health-addr"),
		storeCache:  c.String("store-cache"),
		noStore:     c.Bool("no-store"),
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
	if text := c.String("user"); text != "" {
		user, err := launcher.ParseUser(text)
		if err != nil {
			return zathrasConfig{}, usageError("--user: %v", err)
		}
		cfg.user = &user
	}
	if !cfg.noStore && cfg.storeCache == "" {
		return zathrasConfig{}, usageError("--store-cache: the store needs a directory for what it fetches; --no-store is a zathras without a store of its own")
	}
	if cfg.name == "" {
		if cfg.name, err = os.Hostname(); err != nil {
			return zathrasConfig{}, fmt.Errorf("no host name to go by, give a name with --name: %w", err)
		}
	}
	return cfg, nil
}

// runZathras is zathras until ctx is done, which SIGINT and SIGTERM do:
// it then stops the service, ends the shells, stops its store and returns
// nil.
func runZathras(ctx context.Context, cfg zathrasConfig, log *slog.Logger) (err error) {
	// What the service and the shells are started with comes first: a
	// zathras that cannot start them as the user of --user ends here,
	// before anything of it is there.
	starter, err := launcher.New(launcher.Config{User: cfg.user, Log: log})
	if err != nil {
		return err
	}

	// The store, before anything that could use it. It is not dialed yet:
	// draal is needed when a store path is first named.
	var store zathrasStore
	if cfg.noStore {
		log.Info("no store of its own (--no-store): /nix/store is what the container has, and a prepare fetches nothing")
	} else {
		start := cfg.startStore
		if start == nil {
			start = func(c storemount.Config) (zathrasStore, error) { return storemount.Start(c) }
		}
		store, err = start(storemount.Config{
			Server: cfg.draal, Addrs: cfg.addrs, Service: cfg.service,
			Cache: cfg.storeCache, Dir: cfg.storeDir, Log: log,
		})
		if err != nil {
			return fmt.Errorf("the store of the zathras: %w", err)
		}
	}
	// Every way out from here stops the store, once: at the end, when
	// what ran from it has ended, and on the ways out before that.
	var stopping sync.Once
	stopStore := func() {
		if store == nil {
			return
		}
		stopping.Do(func() {
			if serr := store.Stop(); serr != nil {
				log.Error("stopping the store", "error", serr)
				err = errors.Join(err, fmt.Errorf("stopping the store of the zathras: %w", serr))
			}
		})
	}
	defer stopStore()

	// The health check listens before anything is started or draal has
	// heard of this zathras: an address that is taken ends the command
	// here.
	var listener net.Listener
	if cfg.healthAddr != "" {
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
		Start:       starter.Start,
		Log:         log,
	})
	agentCfg := zathras.Config{
		Service: cfg.service,
		Name:    cfg.name,
		Version: Version,
		Dial: func(ctx context.Context) (node.Conn, error) {
			return node.Dial(ctx, node.DialConfig{Key: key, Server: cfg.draal, Addrs: cfg.addrs, Log: log})
		},
		Supervisor: sup,
		Ring:       ring,
		Stats:      stats.NewReader(),
		Shell:      ptyshell.Config{Shell: cfg.shell, Start: starter.Start},
		Log:        log,
	}
	if store != nil {
		// Not assigned when there is none: an interface that holds a nil
		// store would not be nil.
		agentCfg.Preload = store
	}
	agent := zathras.New(agentCfg)
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
	//  1. The agent ends the shells and what it prepares, and leaves
	//     draal, and the supervisor stops the service: SIGTERM, and
	//     SIGKILL after --stop-timeout. Both are waited for here.
	//  2. The store is stopped: nothing runs from it any more. The mount
	//     goes, and then its server.
	//  3. The health check is shut down last. While the service is being
	//     stopped it goes on answering, with 503 as the phase is no longer
	//     running, so that the pod is taken out of its Service by an
	//     answer and not by a connection that is refused.
	wg.Wait()
	stopStore()
	if web != nil {
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		web.Shutdown(shutdown)
	}
	select {
	case ferr := <-failed:
		return errors.Join(ferr, err)
	default:
	}
	if err != nil {
		return err
	}
	log.Info("stopped")
	return nil
}
