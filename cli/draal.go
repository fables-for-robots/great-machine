package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/amber-store/jaccard-store/bucket"
	jsserver "github.com/amber-store/jaccard-store/server"
	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/storeserver"
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
			&cli.StringSliceFlag{Name: "admin", EnvVars: []string{"GREAT_MACHINE_ADMINS"},
				Usage: "endpoint `ID` of an admin, as `great-machine id` prints it: an admin whatever was granted, whom no request revokes; may be repeated, and the variable takes several with commas between them. The flag was --operator and the variable GREAT_MACHINE_OPERATORS, and both are still taken"},
			// The name the flag had up to v0.3.0: who was an operator is
			// an admin.
			&cli.StringSliceFlag{Name: "operator", EnvVars: []string{"GREAT_MACHINE_OPERATORS"}, Hidden: true},
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
	bind      netip.AddrPort // the zero value: every address, a port the system picks
	// local is for tests: the iroh endpoint uses no relay, announces
	// nothing and is bound to 127.0.0.1 unless bind says otherwise. No
	// flag sets it.
	local bool
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
	for _, flag := range []string{"admin", "operator"} {
		for _, text := range c.StringSlice(flag) {
			if text = strings.TrimSpace(text); text == "" {
				continue
			}
			id, err := node.ParseID(text)
			if err != nil {
				return draalConfig{}, usageError("--%s: %q is not an endpoint ID, as `great-machine id` prints one: %w", flag, text, err)
			}
			cfg.operators = append(cfg.operators, id)
		}
	}
	if cfg.store, err = readStore(c); err != nil {
		return draalConfig{}, err
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
			return storeConfig{}, usageError("--s3-endpoint: %s is not the URL of an S3 service: want http://HOST[:PORT] or https://HOST[:PORT], with the path the service is under if it has one", shownEndpoint(e))
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

// shownEndpoint is a value of --s3-endpoint as a refusal repeats it. One
// with an @, a ? or a # in it is not repeated at all: a user, a query
// and a fragment are where somebody may have put a secret, a refusal
// goes to the log of a pod, and no cut of such a value is sure to leave
// the secret out.
func shownEndpoint(e string) string {
	if strings.ContainsAny(e, "@?#") {
		return "a value with an @, a ? or a # in it, which is not repeated here,"
	}
	return strconv.Quote(e)
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

	// Who may do what: the admins of the command line, and the grants of
	// the database, which draal decides by from a copy in memory.
	grants, err := database.Grants(ctx)
	if err != nil {
		return fmt.Errorf("the grants: %w", err)
	}
	allow := authz.New(authz.Config{Admins: cfg.operators}, grants)
	if !allow.Admins() {
		log.Warn("no admin was given on the command line and the database holds none: nobody can grant anything, and so nobody can do anything")
	}

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

	d := draal.New(draal.Config{
		DB:    database,
		Authz: allow,
		Store: store,
		ID:    listener.ID(),
		Log:   log,
	})

	// The order of the end, once ctx is done: Serve returns, the store is
	// stopped, the endpoint is closed, and the two databases are closed,
	// the store's first. draal serves no web page and binds no port but
	// that of its endpoint: the page is `great-machine web`'s, on the
	// machine of an operator.
	err = d.Serve(ctx, listener)
	if err != nil && ctx.Err() == nil {
		return err
	}
	log.Info("stopping")
	return nil
}
