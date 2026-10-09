// Package push takes store paths to a jaccard-store server: every store
// path becomes the reference prefix+name there, which the sidecar of a pod
// serves as /nix/store/<name>.
//
// A store path never changes under its name, so one whose reference the
// server has is not pushed again. Every other one is imported into a
// temporary store of its own, as `jaccard-store push-dir` imports a
// directory, and pushed with jaccard-store's client.
package push

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"sync"

	"github.com/amber-store/core/ingest"
	"github.com/amber-store/core/packstore"
	"github.com/amber-store/core/reference"
	"github.com/amber-store/jaccard-store/client"
	jsnode "github.com/amber-store/jaccard-store/node"
	jswire "github.com/amber-store/jaccard-store/wire"
	irohkey "github.com/tmc/go-iroh/key"
	"golang.org/x/sync/errgroup"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

const (
	// defaultJobs is how many store paths are pushed at a time when Config
	// does not say.
	defaultJobs = 5
	// lookups is how many references the server is asked about at a time.
	lookups = 16
	// minDedup is the share of a store path's bytes a pack on the server
	// must hold for the store path to be pushed as a patch against it: the
	// default of jaccard-store's push-dir.
	minDedup = 0.5
)

// Config says where store paths are pushed to, and how.
type Config struct {
	Key      node.Key         // the operator's
	Server   wire.ID          // the jaccard-store server
	Addrs    []netip.AddrPort // for tests: where the server is
	Prefix   string
	Jobs     int      // 0: 5
	TempDir  string   // "": the system's
	Progress Progress // nil: none
}

// Progress is told what a push does. Its methods are called from several
// goroutines.
type Progress interface {
	// Found says how many of the store paths the server has already, and
	// how many are to be pushed.
	Found(present, missing int)
	// Pushed says that one store path was pushed, and how many bytes its
	// pack came to.
	Pushed(storePath string, bytes uint64)
}

// Result says what a push did.
type Result struct {
	Pushed  int
	Present int
}

// silent is the Progress of a caller that wants none.
type silent struct{}

func (silent) Found(int, int)        {}
func (silent) Pushed(string, uint64) {}

// pushing is called when the work on one store path begins, and what it
// returns when that work has ended. The tests replace it.
var pushing = func(storePath string) (done func()) { return func() {} }

// item is one store path and the reference it is pushed as.
type item struct {
	path string
	ref  string
}

// Push makes the server hold the reference Prefix+name for every store
// path. It ends at the first path that fails, with that path in the error.
//
// The name of a store path is its last element. A store path may be a
// directory or a single regular file, which becomes a reference whose root
// is the file. One that is a symbolic link is refused before anything is
// pushed: the root of a reference cannot be a link, so the sidecar could
// not serve it as one.
//
// The Result counts what was done until Push returned, also when it
// returns an error.
func Push(ctx context.Context, cfg Config, storePaths []string) (Result, error) {
	progress := cfg.Progress
	if progress == nil {
		progress = silent{}
	}
	jobs := cfg.Jobs
	if jobs <= 0 {
		jobs = defaultJobs
	}

	// Before anything is dialed: a name the server will not take should
	// cost nothing.
	items := make([]item, 0, len(storePaths))
	seen := make(map[string]bool, len(storePaths))
	for _, p := range storePaths {
		ref := cfg.Prefix + filepath.Base(filepath.Clean(p))
		if p == "" {
			return Result{}, errors.New("pushing a store path: its path is empty")
		}
		if err := reference.ValidateName(ref); err != nil {
			return Result{}, fmt.Errorf("pushing %s as the reference %q: %w", p, ref, err)
		}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		items = append(items, item{path: p, ref: ref})
	}
	if len(items) == 0 {
		progress.Found(0, 0)
		return Result{}, nil
	}

	server, err := irohkey.ParseEndpointID(string(cfg.Server))
	if err != nil {
		return Result{}, fmt.Errorf("the jaccard-store server %q: %w", cfg.Server, err)
	}
	conn, err := jsnode.Dial(ctx, jsnode.DialConfig{
		Key:    cfg.Key.Iroh(),
		ALPN:   jswire.ALPN,
		Server: server,
		Addrs:  cfg.Addrs,
	})
	if err != nil {
		return Result{}, fmt.Errorf("the jaccard-store server: %w", err)
	}
	defer conn.Close()
	c := client.New(conn, nil)

	missing, err := absent(ctx, c, items)
	if err != nil {
		return Result{}, err
	}
	res := Result{Present: len(items) - len(missing)}
	for _, it := range missing {
		info, err := os.Lstat(it.path)
		// A path that is not there fails when its turn comes.
		if err == nil && info.Mode()&fs.ModeSymlink != 0 {
			return res, fmt.Errorf("pushing %s: the store path is a symbolic link, which cannot be pushed", it.path)
		}
	}
	progress.Found(res.Present, len(missing))

	var mu sync.Mutex // guards res.Pushed
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(jobs)
	for _, it := range missing {
		// Go waits for a free place. A push that failed meanwhile has
		// ended gctx, and nothing more is begun.
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			size, err := one(gctx, c, it, cfg.TempDir)
			if err != nil {
				return fmt.Errorf("pushing %s: %w", it.path, err)
			}
			mu.Lock()
			res.Pushed++
			mu.Unlock()
			progress.Pushed(it.path, size)
			return nil
		})
	}
	err = g.Wait()
	// The end of the caller's context is the cause of whatever it ended.
	if cerr := ctx.Err(); cerr != nil {
		err = fmt.Errorf("the push was interrupted: %w", cerr)
	}
	return res, err
}

// absent asks the server about the reference of every item and returns
// the items it has no reference for, in their order.
//
// The server is asked once for every name, with the name as the prefix of
// a listing, and not once for everything under the prefix: a closure is a
// few hundred store paths, and the store of a fleet holds every store path
// that was ever rolled out, which a listing would bring a thousand at a
// time.
func absent(ctx context.Context, c *client.Client, items []item) ([]item, error) {
	there := make([]bool, len(items))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(lookups)
	for i, it := range items {
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			refs, err := c.List(gctx, it.ref)
			if err != nil {
				return fmt.Errorf("asking the jaccard-store server for the reference %q: %w", it.ref, err)
			}
			// The listing is of every name that begins with this one.
			for _, r := range refs {
				if r.Name == it.ref {
					there[i] = true
					break
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	var missing []item
	for i, it := range items {
		if !there[i] {
			missing = append(missing, it)
		}
	}
	return missing, nil
}

// one pushes one store path through a temporary store of its own, which
// it removes however the push ends. It returns the size of the pack that
// was uploaded: 0 when the server had a pack of the same content already.
func one(ctx context.Context, c *client.Client, it item, tempDir string) (size uint64, err error) {
	defer pushing(it.path)()

	dir, err := os.MkdirTemp(tempDir, "great-machine-push-*")
	if err != nil {
		return 0, fmt.Errorf("temporary store: %w", err)
	}
	defer func() {
		if rerr := os.RemoveAll(dir); rerr != nil {
			err = errors.Join(err, fmt.Errorf("removing the temporary store: %w", rerr))
		}
	}()
	// Nothing here outlives the push: waiting for the disk buys nothing.
	objects, err := packstore.Open(filepath.Join(dir, "packstore"), packstore.WithSync(false))
	if err != nil {
		return 0, fmt.Errorf("temporary store: %w", err)
	}
	defer objects.Close()

	// The options of jaccard-store's push-dir, but for the ignore files: a
	// store path is pushed whole, whatever a .amberignore in it says.
	built, root, err := ingest.Objects(it.path, ingest.Opts{NoIgnore: true})
	if err != nil {
		return 0, fmt.Errorf("importing: %w", err)
	}
	_, err = objects.WriteParallel(func(yield func(packstore.Object, error) bool) {
		for o, err := range built {
			// The build knows nothing of ctx: an interrupt reaches it
			// here, between two objects.
			if err == nil {
				err = ctx.Err()
			}
			if !yield(packstore.Object{Key: o.Key, Data: o.Bytes}, err) || err != nil {
				return
			}
		}
	}, packstore.WriteOpts{})
	if err != nil {
		return 0, fmt.Errorf("importing: %w", err)
	}

	// The pack is built beside the store and goes with it.
	pushed, err := c.Push(ctx, objects, it.ref, *root, client.PushOptions{MinDedup: minDedup, TempDir: dir})
	if err != nil {
		return 0, err
	}
	return pushed.DataSize, nil
}
