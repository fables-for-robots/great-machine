package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// The files of draal in its data directory.
const (
	draalKeyFile = "draal.key"
	draalDBFile  = "draal.sqlite"
)

// draalCommand is `great-machine draal`, the controller.
func draalCommand() *cli.Command {
	return &cli.Command{
		Name:  "draal",
		Usage: "the controller: zathrases report to it, operators roll out through it",
		Description: "The endpoint ID draal logs at its start is all that a zathras or an\n" +
			"operator needs to reach it.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "data", EnvVars: []string{"GREAT_MACHINE_DATA"},
				Usage: "`DIR`ectory of draal: its key (created on first run) and its database (required)"},
			&cli.StringSliceFlag{Name: "operator", EnvVars: []string{"GREAT_MACHINE_OPERATORS"},
				Usage: "endpoint `ID` that may act as an operator, as `great-machine id` prints it; may be repeated, and the variable takes several with commas between them"},
			&cli.StringFlag{Name: "jaccard-server", EnvVars: []string{"JACCARD_SERVER"},
				Usage: "endpoint `ID` of the jaccard-store server that closures are pushed to (required)"},
			&cli.StringFlag{Name: "jaccard-prefix", EnvVars: []string{"JACCARD_PREFIX"},
				Usage: "`PREFIX` put before the name of a store path to make its reference; the same value the sidecars are given (required)"},
			&cli.StringFlag{Name: "web-addr", EnvVars: []string{"GREAT_MACHINE_WEB_ADDR"}, Value: "127.0.0.1:8080",
				Usage: "`ADDR`ess the web page listens on; empty for no web page"},
			&cli.StringFlag{Name: "bind", EnvVars: []string{"GREAT_MACHINE_BIND"},
				Usage: "UDP `IP:PORT` the iroh endpoint binds (default: every address, a port the system picks)"},
		},
		Action: func(c *cli.Context) error {
			cfg, err := readDraal(c)
			if err != nil {
				return err
			}
			return runDraal(c.Context, cfg, slog.New(slog.NewTextHandler(c.App.ErrWriter, nil)))
		},
	}
}

// draalConfig is what the command line of draal says.
type draalConfig struct {
	data      string
	operators []wire.ID
	store     wire.StoreInfo
	webAddr   string         // "" for no web page
	bind      netip.AddrPort // the zero value: every address, a port the system picks
}

// readDraal reads the command line of draal.
func readDraal(c *cli.Context) (draalConfig, error) {
	if err := noArguments(c); err != nil {
		return draalConfig{}, err
	}
	var cfg draalConfig
	var err error
	if cfg.data, err = required(c, "data", "GREAT_MACHINE_DATA"); err != nil {
		return draalConfig{}, err
	}
	// The library has cut the values at their commas already. What is
	// left empty by a comma too many, or by a space, is not an ID.
	for _, text := range c.StringSlice("operator") {
		if text = strings.TrimSpace(text); text == "" {
			continue
		}
		id, err := node.ParseID(text)
		if err != nil {
			return draalConfig{}, usageError("--operator: %q is not an endpoint ID, as `great-machine id` prints one: %w", text, err)
		}
		cfg.operators = append(cfg.operators, id)
	}
	server, err := required(c, "jaccard-server", "JACCARD_SERVER")
	if err != nil {
		return draalConfig{}, err
	}
	if cfg.store.Server, err = node.ParseID(server); err != nil {
		return draalConfig{}, usageError("--jaccard-server: %q is not an endpoint ID: %w", server, err)
	}
	if cfg.store.Prefix, err = required(c, "jaccard-prefix", "JACCARD_PREFIX"); err != nil {
		return draalConfig{}, err
	}
	if cfg.webAddr = strings.TrimSpace(c.String("web-addr")); cfg.webAddr != "" {
		if _, _, err := net.SplitHostPort(cfg.webAddr); err != nil {
			return draalConfig{}, usageError("--web-addr: %q is not an address to listen on: want HOST:PORT, such as 127.0.0.1:8080, or nothing for no web page", cfg.webAddr)
		}
	}
	if bind := c.String("bind"); bind != "" {
		if cfg.bind, err = netip.ParseAddrPort(bind); err != nil {
			return draalConfig{}, usageError("--bind: %q is not an address to bind: want IP:PORT, such as 0.0.0.0:4433 or [::]:4433", bind)
		}
	}
	return cfg, nil
}

// runDraal is draal until ctx is done, which SIGINT and SIGTERM do. That
// is how draal is meant to end, and no error.
func runDraal(ctx context.Context, cfg draalConfig, log *slog.Logger) (err error) {
	if err := os.MkdirAll(cfg.data, 0o700); err != nil {
		return fmt.Errorf("the data directory: %w", err)
	}
	key, err := node.LoadOrCreateKey(filepath.Join(cfg.data, draalKeyFile))
	if err != nil {
		return fmt.Errorf("the key of draal: %w", err)
	}
	database, err := db.Open(filepath.Join(cfg.data, draalDBFile))
	if err != nil {
		return fmt.Errorf("the database: %w", err)
	}
	// Deferred first, so that it happens last: after the listener is
	// closed and Serve, which writes to the database, has returned.
	defer func() { err = errors.Join(err, database.Close()) }()

	if len(cfg.operators) == 0 {
		log.Warn("no --operator was given: nobody can act as an operator over iroh")
	}
	listener, err := node.Listen(ctx, node.ListenConfig{Key: key, Bind: cfg.bind, Log: log})
	if err != nil {
		return fmt.Errorf("binding the iroh endpoint: %w", err)
	}
	defer func() { err = errors.Join(err, listener.Close()) }()
	log.Info("serving", "endpoint", string(listener.ID()), "addr", listener.LocalAddr().String(), "operators", len(cfg.operators))

	d := draal.New(draal.Config{
		DB:    database,
		Authz: authz.NewAllowlist(cfg.operators),
		Store: cfg.store,
		Log:   log,
	})
	if err := d.Serve(ctx, listener); err != nil && ctx.Err() == nil {
		return err
	}
	log.Info("stopping")
	return nil
}
