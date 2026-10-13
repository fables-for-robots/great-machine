package storedir

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// fetch is the fetching of one store path, which everybody who needs the
// store path waits for.
type fetch struct {
	ctx    context.Context // done when nobody waits any more, or the store is closed
	cancel context.CancelFunc
	done   chan struct{} // closed when the fetch has ended
	err    error         // read after done

	// Under Store.mu.
	waiters int
	// abandoned says that the last who waited has given up: the fetch is
	// being ended, and whoever needs the store path now waits for that
	// and begins a fetch of its own.
	abandoned bool
}

// Preload fetches the store paths of names, each the name of a store path
// without /nix/store/, into the store directory. A store path that is
// there is asked of nobody.
//
// The first that cannot be fetched ends the others of this call and is
// the error: a *NotFoundError for a store path the store has none of for
// the service, and otherwise one that names the store path and says why.
// A name that is none of a store path is refused before anything is
// fetched.
//
// Store paths are fetched Jobs at a time, over all the calls that run,
// and a store path is fetched once however many calls need it. Preload
// ends with ctx: a fetch that no call waits for any more is ended, and
// what it wrote is removed.
func (s *Store) Preload(ctx context.Context, names []string) error {
	seen := make(map[string]bool, len(names))
	var wanted []string
	for _, name := range names {
		if err := validName(name); err != nil {
			return err
		}
		if !seen[name] {
			seen[name] = true
			wanted = append(wanted, name)
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg    sync.WaitGroup
		once  sync.Once
		first error
	)
	for _, name := range wanted {
		wg.Go(func() {
			if err := s.ensure(ctx, name); err != nil {
				once.Do(func() {
					first = err
					cancel()
				})
			}
		})
	}
	wg.Wait()
	return first
}

// validName returns nil for a name that can be that of a store path: one
// name of a file, which does not begin with a dot. No store path of Nix
// does, and what is being written to the store directory does.
func validName(name string) error {
	if !oneName(name) || strings.HasPrefix(name, ".") {
		return fmt.Errorf("%q is no name of a store path", name)
	}
	return nil
}

// there reports whether the store path of name is in the store directory.
func (s *Store) there(name string) (bool, error) {
	_, err := os.Lstat(filepath.Join(s.dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// ensure returns when the store path of name is in the store directory,
// or with why it is not. It waits for the fetch of the name, which it
// begins if there is none, and gives up with ctx.
func (s *Store) ensure(ctx context.Context, name string) error {
	for {
		if is, err := s.there(name); is || err != nil {
			return err
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return errClosed
		}
		f := s.fetches[name]
		if f == nil {
			f = &fetch{done: make(chan struct{})}
			f.ctx, f.cancel = context.WithCancel(s.ctx)
			s.fetches[name] = f
			s.running.Add(1)
			go s.run(name, f)
		}
		if f.abandoned {
			// It is being ended. When it is gone, the store path is
			// looked for again, and fetched anew.
			s.mu.Unlock()
			select {
			case <-f.done:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		f.waiters++
		s.mu.Unlock()

		select {
		case <-f.done:
			return f.err
		case <-ctx.Done():
			s.mu.Lock()
			f.waiters--
			if f.waiters == 0 {
				f.abandoned = true
				f.cancel()
			}
			s.mu.Unlock()
			return ctx.Err()
		}
	}
}

// run is the goroutine of a fetch.
func (s *Store) run(name string, f *fetch) {
	defer s.running.Done()
	err := s.fetch(f.ctx, name)
	if err != nil && f.ctx.Err() != nil && s.ctx.Err() != nil {
		err = errClosed
	}
	// The fetch leaves the table before those who waited are answered:
	// whoever then finds the store path missing begins a new one.
	s.mu.Lock()
	delete(s.fetches, name)
	s.mu.Unlock()
	f.err = err
	f.cancel()
	close(f.done)
}

// fetch makes the store path of name be in the store directory: it pulls
// the reference into the pack cache, writes it out beside its name, and
// moves it there.
func (s *Store) fetch(ctx context.Context, name string) error {
	select {
	case s.jobs <- struct{}{}:
		defer func() { <-s.jobs }()
	case <-ctx.Done():
		return ctx.Err()
	}
	// It may have been fetched since it was looked for.
	if is, err := s.there(name); is || err != nil {
		return err
	}
	dest := filepath.Join(s.dir, name)
	began := time.Now()

	s.enter()
	res, err := s.pull(ctx, name)
	s.leave()
	if err != nil {
		return err
	}

	partial := filepath.Join(s.dir, partialPrefix+name)
	// Nothing is there: a fetch removes what it wrote when it fails, and
	// Open what a run left. This is for what neither saw.
	if err := removeTree(partial); err != nil {
		return fmt.Errorf("%s could not be written out: %w", dest, err)
	}
	if err := writeOut(ctx, s.objects.Get, res.Root, partial); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s could not be written out: %w", dest, err)
	}
	if err := os.Rename(partial, dest); err != nil {
		return errors.Join(fmt.Errorf("%s could not be written out: %w", dest, err), removeTree(partial))
	}
	if err := syncDir(s.dir); err != nil {
		// The move may not be on disk, so the store path does not count,
		// and is not left where the next run would take it for whole.
		return errors.Join(fmt.Errorf("%s could not be written out: %w", dest, err), removeTree(dest))
	}
	s.log.Info("fetched", "path", dest, "packs", res.Packs, "objects", res.Objects, "bytes", res.Bytes,
		"took", time.Since(began).Round(time.Millisecond))
	return nil
}

// syncDir makes the entries of the directory path durable: a move into it
// is on disk when it returns.
func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return fmt.Errorf("syncing %s: %w", path, err)
	}
	return d.Close()
}
