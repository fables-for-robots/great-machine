package storemount

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/amber-store/jaccard-nfs-nix-store/preload"
	"github.com/amber-store/jaccard-nfs-nix-store/refs"
	"github.com/amber-store/jaccard-nfs-nix-store/sidecar"
	jnode "github.com/amber-store/jaccard-store/node"
	irohkey "github.com/tmc/go-iroh/key"
	"golang.org/x/sys/unix"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// connect dials the store for jaccard-store's protocol. The tests replace
// it. It is no field of Config, whose file is built for every system:
// its type is of jaccard-nfs-nix-store, which is for Linux alone here.
var connect = refs.Connect

// Mount is the store of a zathras, served and mounted.
type Mount struct {
	service string
	sidecar *sidecar.Sidecar

	stop    sync.Once
	stopErr error
}

// Start makes a key, opens the cache, serves it over NFS on a loopback
// port the system picks, and mounts that on cfg.Dir, which it makes if it
// is not there. When it returns, the directory is the store.
//
// The store is not dialed here: it is needed when a store path is first
// named, and not before.
//
// A container that does not let the process mount makes Start fail with
// what the container needs. Whatever Start fails with, nothing is left
// mounted and nothing is left listening.
func Start(cfg Config) (*Mount, error) {
	if err := cfg.check(); err != nil {
		return nil, err
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	dir := cfg.Dir
	if dir == "" {
		dir = DefaultDir
	}
	server, err := irohkey.ParseEndpointID(string(cfg.Server))
	if err != nil {
		return nil, fmt.Errorf("storemount: the server %q is no endpoint ID: %w", cfg.Server, err)
	}
	// A key of the mount's own, new with every start, as the key of the
	// zathras is: the connections of the mount come and go at other times
	// than that of the zathras, and two endpoints with one key evict each
	// other at the relay.
	key, err := node.NewKey()
	if err != nil {
		return nil, fmt.Errorf("storemount: making a key: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("storemount: the directory of the store: %w", err)
	}
	sc, err := sidecar.Start(sidecar.Config{
		Prefix: wire.StorePrefix(cfg.Service),
		Server: string(cfg.Server),
		Cache:  cfg.Cache,
		Dial: func(ctx context.Context) (refs.Conn, error) {
			return connect(ctx, jnode.DialConfig{Key: key.Iroh(), Server: server, Addrs: cfg.Addrs, Log: log})
		},
		// Loopback keeps the server in the pod, and no port has to be
		// agreed on: the one process serves and mounts.
		Listen:       "127.0.0.1:0",
		Mount:        dir,
		MountOptions: sidecar.DefaultMountOptions,
		PullTimeout:  cfg.PullTimeout,
		AdoptTimeout: cfg.AdoptTimeout,
		// No preload endpoint: Preload is called.
		Log: log,
	})
	if err != nil {
		if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
			return nil, fmt.Errorf("storemount: %w: the container of a zathras has to let it mount: it needs CAP_SYS_ADMIN and, "+
				"where there is AppArmor, the profile Unconfined, as deploy/pod.yaml has them; "+
				"--no-store runs a zathras without a store of its own", err)
		}
		return nil, fmt.Errorf("storemount: %w", err)
	}
	log.Info("the store is mounted", "on", dir, "service", cfg.Service, "server", string(cfg.Server),
		"nfs", sc.Addr().String(), "cache", cfg.Cache, "client", string(key.ID()))
	return &Mount{service: cfg.Service, sidecar: sc}, nil
}

// Preload fetches the store paths of names, each the name of a store path
// without /nix/store/, 20 at a time. The first that cannot be fetched ends
// the others and is the error: a *NotFoundError for a store path the
// store has none of for the service, and otherwise one that names the
// store path and says why it could not be fetched. It ends with ctx, and
// with the mount.
func (m *Mount) Preload(ctx context.Context, names []string) error {
	err := m.sidecar.Preload(ctx, names)
	var failed *preload.Failure
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, sidecar.ErrStopped):
		return errors.New("the store of the zathras is stopped")
	case errors.As(err, &failed) && errors.Is(failed.Err, refs.ErrNotFound):
		return &NotFoundError{Path: failed.Path(), Service: m.service}
	case errors.As(err, &failed):
		return fmt.Errorf("%s could not be fetched from the store: %w", failed.Path(), failed.Err)
	}
	return fmt.Errorf("fetching from the store: %w", err)
}

// Stop takes the mount away and stops the server, in the order that
// jaccard-nfs-nix-store's Sidecar.Stop keeps: the mount goes while the
// server still answers. A mount that is in use is detached all the same,
// and the server waits a few seconds for the kernel to let go of it and
// then ends. So Stop does not wait for a program that still runs from
// the store: such a program gets an error, or SIGBUS, when it next needs
// a part of a file that it has not read. Whoever has the mount stops
// what runs from it first.
//
// Stop stops once: a second call returns what the first did, and takes
// no mount away that was made on the directory since.
func (m *Mount) Stop() error {
	m.stop.Do(func() {
		if err := m.sidecar.Stop(); err != nil {
			m.stopErr = fmt.Errorf("storemount: %w", err)
		}
	})
	return m.stopErr
}
