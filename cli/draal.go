package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amber-store/jaccard-store/bucket"
	jsserver "github.com/amber-store/jaccard-store/server"
	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/storeserver"
	"github.com/fables-for-robots/great-machine/web"
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
			"operator needs to reach it. draal is the store as well: closures are\n" +
			"pushed to it and the pods fetch them from it, under the same endpoint\n" +
			"ID, and their packs are kept in an S3 bucket. The credentials of the\n" +
			"bucket are those the AWS SDK finds: AWS_ACCESS_KEY_ID and the rest, a\n" +
			"profile, or a role.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "data", EnvVars: []string{"GREAT_MACHINE_DATA"},
				Usage: "`DIR`ectory of draal: its key (created on first run) and its database (required)"},
			&cli.StringSliceFlag{Name: "operator", EnvVars: []string{"GREAT_MACHINE_OPERATORS"},
				Usage: "endpoint `ID` that may act as an operator, as `great-machine id` prints it; may be repeated, and the variable takes several with commas between them"},
			&cli.StringFlag{Name: "s3-bucket", EnvVars: []string{"GREAT_MACHINE_S3_BUCKET"},
				Usage: "`NAME` of the bucket the store keeps its packs in (required)"},
			&cli.StringFlag{Name: "s3-prefix", EnvVars: []string{"GREAT_MACHINE_S3_PREFIX"},
				Usage: "`PREFIX` of the name of every object in the bucket"},
			&cli.StringFlag{Name: "s3-endpoint", EnvVars: []string{"GREAT_MACHINE_S3_ENDPOINT"},
				Usage: "`URL` of an S3 service other than AWS; it is in the URLs that clients are given, so the laptop and the pods have to reach it under this name"},
			&cli.StringFlag{Name: "s3-region", EnvVars: []string{"GREAT_MACHINE_S3_REGION"},
				Usage: "`REGION` of the bucket, which the URLs of the store are signed for (default: what the AWS configuration says, as AWS_REGION; one of the two has to say it)"},
			&cli.BoolFlag{Name: "s3-path-style", EnvVars: []string{"GREAT_MACHINE_S3_PATH_STYLE"},
				Usage: "address the bucket in the path of a URL, not in its host name"},
			&cli.StringFlag{Name: "store-scratch", EnvVars: []string{"GREAT_MACHINE_STORE_SCRATCH"},
				Usage: "`DIR` in which packs are verified: the store makes a directory of its own there, great-machine-scratch, which it empties at every start (default: --data)"},
			&cli.IntFlag{Name: "store-verify-jobs", EnvVars: []string{"GREAT_MACHINE_STORE_VERIFY_JOBS"}, Value: 2,
				Usage: "packs verified at once; each needs scratch space for its uncompressed data"},
			&cli.StringFlag{Name: "store-max-pack-size", EnvVars: []string{"GREAT_MACHINE_STORE_MAX_PACK_SIZE"}, Value: "16GiB",
				Usage: "largest uncompressed `SIZE` of a pack, in bytes or with KiB, MiB or GiB; the scratch space needed is this times --store-verify-jobs"},
			&cli.BoolFlag{Name: "store-no-verify", EnvVars: []string{"GREAT_MACHINE_STORE_NO_VERIFY"},
				Usage: "record a pack without reading its data: one that is incomplete or corrupt is then found by whoever pulls it"},
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
	store     storeConfig
	webAddr   string         // "" for no web page
	bind      netip.AddrPort // the zero value: every address, a port the system picks
	// local is for tests: the iroh endpoint uses no relay, announces
	// nothing and is bound to 127.0.0.1 unless bind says otherwise. No
	// flag sets it.
	local bool
	// listenWeb is for tests: what binds the address of the web page.
	// Nil means net.Listen. No flag sets it.
	listenWeb func(network, addr string) (net.Listener, error)
	// objects is for tests: the bucket of the store itself. Nil means
	// the bucket the flags name. No flag sets it.
	objects jsserver.Bucket
	// storeGrace is for tests: how long the requests of the store that
	// are under way have when draal ends. 0: the 10 seconds of the
	// store. No flag sets it.
	storeGrace time.Duration
}

// storeConfig is what the command line of draal says of the store.
type storeConfig struct {
	bucket       bucket.Config
	scratch      string // where the store makes its scratch directory; "": the data directory
	verifyJobs   int
	maxPackBytes int64
	noVerify     bool
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
	if cfg.store, err = readStore(c); err != nil {
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

// readStore reads what the command line of draal says of the store.
func readStore(c *cli.Context) (storeConfig, error) {
	cfg := storeConfig{
		bucket: bucket.Config{
			Prefix:    c.String("s3-prefix"),
			Endpoint:  strings.TrimSpace(c.String("s3-endpoint")),
			Region:    strings.TrimSpace(c.String("s3-region")),
			PathStyle: c.Bool("s3-path-style"),
		},
		scratch:    strings.TrimSpace(c.String("store-scratch")),
		verifyJobs: c.Int("store-verify-jobs"),
		noVerify:   c.Bool("store-no-verify"),
	}
	var err error
	if cfg.bucket.Bucket, err = required(c, "s3-bucket", "GREAT_MACHINE_S3_BUCKET"); err != nil {
		return storeConfig{}, err
	}
	// The endpoint is in every URL a client is given. One without a
	// scheme would be taken for a path, and found out by the first push;
	// one without a host names no service; and a user, a query or a
	// fragment is no part of where a service is.
	if e := cfg.bucket.Endpoint; e != "" {
		u, err := url.Parse(e)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
			u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return storeConfig{}, usageError("--s3-endpoint: %q is not the URL of an S3 service: want http://HOST[:PORT] or https://HOST[:PORT], with the path the service is under if it has one", e)
		}
	}
	if cfg.verifyJobs < 1 {
		return storeConfig{}, usageError("--store-verify-jobs: %d is not a number of packs to verify at once: want 1 or more", cfg.verifyJobs)
	}
	if cfg.maxPackBytes, err = parseSize(c.String("store-max-pack-size")); err != nil {
		return storeConfig{}, usageError("--store-max-pack-size: %w", err)
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
		log.Warn("no --operator was given: nobody can act as an operator over iroh, and nobody can push to the store")
	}
	allow := authz.NewAllowlist(cfg.operators)

	// The store is opened before the endpoint is bound: one that cannot
	// be opened ends the command before draal has announced itself.
	store, err := storeserver.Open(ctx, storeserver.Config{
		Dir:          cfg.data,
		Bucket:       cfg.store.bucket,
		Objects:      cfg.objects,
		Scratch:      cfg.store.scratch,
		VerifyJobs:   cfg.store.verifyJobs,
		MaxPackBytes: cfg.store.maxPackBytes,
		NoVerify:     cfg.store.noVerify,
		Authz:        allow,
		StopGrace:    cfg.storeGrace,
		Log:          log,
	})
	if err != nil {
		// The URLs of the store are signed for a region. jaccard-store
		// says that it has none, and cannot say where one is given.
		if cfg.store.bucket.Region == "" && strings.Contains(err.Error(), "no region") {
			return fmt.Errorf("the store: %w: give the region of the bucket with --s3-region, or set GREAT_MACHINE_S3_REGION or AWS_REGION", err)
		}
		return fmt.Errorf("the store: %w", err)
	}
	// Deferred before the listener is, so that it happens after it: the
	// database of the store is closed when its connections are.
	defer func() { err = errors.Join(err, store.Close()) }()
	if cfg.store.noVerify {
		log.Warn("the store records packs without verifying them (--store-no-verify)")
	}

	// One endpoint for both protocols: the endpoint ID of draal is that
	// of the store.
	listener, err := node.Listen(ctx, node.ListenConfig{
		Key:      key,
		Bind:     cfg.bind,
		Local:    cfg.local,
		Handlers: map[string]func(node.Conn){storeserver.ALPN: store.ServeConn},
		Log:      log,
	})
	if err != nil {
		return fmt.Errorf("binding the iroh endpoint: %w", err)
	}
	defer func() { err = errors.Join(err, listener.Close()) }()
	// Deferred behind the listener, so that it happens before it: the
	// requests of the store that are under way have their grace while
	// their connections are still there.
	defer store.Stop()
	log.Info("serving", "endpoint", string(listener.ID()), "addr", listener.LocalAddr().String(), "operators", len(cfg.operators), "bucket", cfg.store.bucket.Bucket)

	// The web page listens before draal serves: an address that is taken
	// ends the command here, with the endpoint and the database closed
	// by what is deferred above.
	var pageListener net.Listener
	if cfg.webAddr != "" {
		listen := cfg.listenWeb
		if listen == nil {
			listen = net.Listen
		}
		if pageListener, err = listen("tcp", cfg.webAddr); err != nil {
			return fmt.Errorf("the web page: %w", err)
		}
	}

	d := draal.New(draal.Config{
		DB:    database,
		Authz: allow,
		ID:    listener.ID(),
		Log:   log,
	})

	// The order of the end, once ctx is done: the web page is stopped
	// first, its WebSockets with it, and only then does Serve get to
	// know, through a context of its own. When Serve has returned the
	// store is stopped, the endpoint is closed, and the two databases
	// are closed, the store's first.
	serving, stopServing := context.WithCancel(context.WithoutCancel(ctx))
	defer stopServing()
	pageFailed := make(chan error, 1)
	if pageListener == nil {
		defer context.AfterFunc(ctx, stopServing)()
	} else {
		page := startWeb(pageListener, web.New(web.Config{Controller: d, Store: store.Admin(), Log: log}), webStopWait, log)
		// For the ways out of this function that are not the end of ctx.
		defer page.Stop()
		announceWeb(log, cfg.webAddr, pageListener.Addr(), web.IsLoopback(cfg.webAddr), web.Built())
		go func() {
			select {
			case <-ctx.Done():
			case err := <-page.Failed():
				pageFailed <- err
			case <-serving.Done():
				return
			}
			page.Stop()
			stopServing()
		}()
	}

	err = d.Serve(serving, listener)
	select {
	case err := <-pageFailed:
		return err
	default:
	}
	if err != nil && ctx.Err() == nil {
		return err
	}
	log.Info("stopping")
	return nil
}
