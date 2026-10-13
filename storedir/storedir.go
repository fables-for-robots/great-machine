// Package storedir is the store of a zathras: a directory, /nix/store,
// that it fills with the store paths of what draal has it prepare.
//
// A store path is pulled from draal's store into a pack cache, a
// packstore of Amber-Store Core beside the store directory, written out
// as the files of a Nix store, and moved to its name when all of it is on
// disk. The move is the record: a name in the store directory is a store
// path that is whole, and there is no list of what was fetched.
//
// Nothing is mounted, so a zathras needs no capability for its store, and
// the store is the same on every system. The pack cache is kept so that a
// store path costs less the second time: a pull downloads what the cache
// lacks, which for a store path that a rollout changed is the difference
// to the one before.
package storedir

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/amber-store/core/packstore"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// DefaultDir is the store directory unless Config says otherwise.
const DefaultDir = "/nix/store"

const (
	// packsDir is the pack cache, in the cache directory.
	packsDir = "packs"
	// partialPrefix begins the name a store path is written under, beside
	// the name it is moved to when it is whole: the prefix and a number.
	// It is beside it, and in no directory of its own, because a
	// directory nobody may write to can be moved within its directory and
	// not out of it: the move to another directory writes its "..",
	// which only root may then. It is a number and not the name of the
	// store path, because that name may be as long as a name can be. No
	// store path has a name that begins with a dot.
	partialPrefix = ".great-machine-partial-"
)

// Config is what a Store is made of. The zero value of a number or of a
// time stands for its default.
type Config struct {
	// Dial dials the store, which is draal, for jaccard-store's protocol.
	// A zathras dials it from the endpoint of its connection to draal
	// (node.Again): it has one key, and draal lets a zathras pull that is
	// registered. It is called when a pull needs a connection and there
	// is none; its context bounds the dial alone. Required.
	Dial func(ctx context.Context) (node.Opener, error)
	// Service is the service of the zathras: its prefix in the store is
	// where its store paths are pulled from. Required.
	Service string
	// Cache is the directory the pack cache is kept in, as packs. Open
	// makes it. Required.
	Cache string
	// Dir is the store directory, which Open makes if it is not there.
	// "": DefaultDir. What it holds stays, and is taken for the store
	// paths it is named as.
	Dir string

	Jobs           int           // 0: 20 store paths fetched at a time
	DialTimeout    time.Duration // 0: 30 seconds for a dial
	RetryWait      time.Duration // 0: 1 second before a pull that failed is tried a second time
	RetryMax       time.Duration // 0: 15 seconds, the longest the wait grows to as it doubles
	PullTimeout    time.Duration // 0: 2 minutes for which a pull that fails is tried again, from its first failure
	AttemptTimeout time.Duration // 0: 1 hour for one attempt at a pull

	// Log is where the store logs. nil: slog.Default().
	Log *slog.Logger

	// wrapDial is for tests: it is given what dials the store and returns
	// what the store dials with.
	wrapDial func(func(context.Context) (conn, error)) func(context.Context) (conn, error)
	// syncDir is for tests: what syncs the store directory when a store
	// path was moved to its name. nil: syncDir.
	syncDir func(dir string) error
}

// withDefaults returns cfg with every zero value replaced by its default.
func (cfg Config) withDefaults() Config {
	if cfg.Dir == "" {
		cfg.Dir = DefaultDir
	}
	if cfg.Jobs <= 0 {
		cfg.Jobs = 20
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 30 * time.Second
	}
	if cfg.RetryWait == 0 {
		cfg.RetryWait = time.Second
	}
	if cfg.RetryMax == 0 {
		cfg.RetryMax = 15 * time.Second
	}
	if cfg.PullTimeout == 0 {
		cfg.PullTimeout = 2 * time.Minute
	}
	if cfg.AttemptTimeout == 0 {
		cfg.AttemptTimeout = time.Hour
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.syncDir == nil {
		cfg.syncDir = syncDir
	}
	return cfg
}

// check returns why a store cannot be made of cfg, or nil.
func (cfg Config) check() error {
	if cfg.Dial == nil {
		return errors.New("storedir: nothing to dial the store with")
	}
	if cfg.Service == "" {
		return errors.New("storedir: no service")
	}
	if err := wire.ValidService(cfg.Service); err != nil {
		return fmt.Errorf("storedir: %w", err)
	}
	if cfg.Cache == "" {
		return errors.New("storedir: no directory for the pack cache")
	}
	return nil
}

// NotFoundError is the error of Preload for a store path that the store
// has none of under the prefix of the service.
type NotFoundError struct {
	Path    string // of the store path: /nix/store/<name>
	Service string
}

// Error says which store path the store does not have, and for which
// service: a store path is in the store of a service when a rollout of
// that service has pushed it.
func (e *NotFoundError) Error() string {
	return fmt.Sprintf("the store has no %s for the service %s: it holds what the rollouts of %s have pushed",
		e.Path, e.Service, e.Service)
}

// errClosed is what a fetch ends with, and a Preload returns, once the
// store is closed.
var errClosed = errors.New("the store of the zathras is closed")

// Store is the store of a zathras. Its methods may be called from any
// goroutine.
type Store struct {
	cfg     Config
	log     *slog.Logger
	dir     string
	prefix  string // of the references of the service
	objects *packstore.Store
	dial    func(context.Context) (conn, error)

	// ctx is done when the store is closed: it ends every fetch.
	ctx  context.Context
	stop context.CancelFunc
	// jobs has a place for every fetch that runs.
	jobs chan struct{}

	mu      sync.Mutex
	closed  bool
	fetches map[string]*fetch // by the name of the store path
	running sync.WaitGroup    // the fetches

	// The connection to the store, under mu. It is there while a fetch
	// is under way, from the first that needs it, and closed when the
	// last has ended.
	conn    conn   // nil: none
	connGen uint64 // which of the connections that were dialed conn is
	dialing *dial  // the dial under way; nil: none

	// partials numbers what is written to the store directory.
	partials atomic.Uint64

	close    sync.Once
	closeErr error
}

// Open makes the store directory and the cache if they are not there,
// makes both directories that the user of this process alone may write
// to, opens the pack cache, and removes what an earlier run was writing
// when it ended. The store is not dialed: it is needed when a store path
// is asked for that is not there, and not before.
func Open(cfg Config) (*Store, error) {
	if err := cfg.check(); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	for _, dir := range []string{cfg.Dir, cfg.Cache} {
		if err := own(dir); err != nil {
			return nil, fmt.Errorf("storedir: %s has to be the zathras's alone to write to, and cannot be made so: %w", dir, err)
		}
	}
	if err := sweep(cfg.Dir); err != nil {
		return nil, fmt.Errorf("storedir: removing what was being written to %s: %w", cfg.Dir, err)
	}
	objects, err := packstore.Open(filepath.Join(cfg.Cache, packsDir))
	if err != nil {
		return nil, fmt.Errorf("storedir: the pack cache: %w", err)
	}
	s := &Store{
		cfg:     cfg,
		log:     cfg.Log,
		dir:     cfg.Dir,
		prefix:  wire.StorePrefix(cfg.Service),
		objects: objects,
		jobs:    make(chan struct{}, cfg.Jobs),
		fetches: make(map[string]*fetch),
	}
	s.dial = dialer(cfg)
	if cfg.wrapDial != nil {
		s.dial = cfg.wrapDial(s.dial)
	}
	s.ctx, s.stop = context.WithCancel(context.Background())
	s.log.Info("the store of the zathras", "directory", cfg.Dir, "cache", cfg.Cache, "service", cfg.Service)
	return s, nil
}

// own makes dir a directory that the user of this process alone may write
// to, and everybody may read: mode 0755. A volume of a pod comes writable
// by everybody, and in such a directory a service that is another user
// could move a store path away and put its own in its place.
func own(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	uid, gid := os.Geteuid(), os.Getegid()
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != uid || int(st.Gid) != gid {
		if err := os.Chown(dir, uid, gid); err != nil {
			return err
		}
	}
	if info.Mode() != fs.ModeDir|0o755 {
		return os.Chmod(dir, 0o755)
	}
	return nil
}

// sweep removes what an earlier run was writing to the store directory
// when it ended: everything whose name begins with partialPrefix.
func sweep(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), partialPrefix) {
			continue
		}
		if err := removeTree(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// Close ends the fetches that are under way, which fails the Preloads
// that wait for them, closes the connection to the store and the pack
// cache, and returns when that is done. What a fetch was writing is
// removed by it, or by the next Open. Close may be called more than once.
func (s *Store) Close() error {
	s.close.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.stop()
		s.running.Wait()
		s.mu.Lock()
		c := s.conn
		s.conn = nil
		s.mu.Unlock()
		if c != nil {
			c.Close()
		}
		if err := s.objects.Close(); err != nil {
			s.closeErr = fmt.Errorf("storedir: closing the pack cache: %w", err)
		}
	})
	return s.closeErr
}
