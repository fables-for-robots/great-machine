// Package storemount is the store of a zathras: jaccard-nfs-nix-store's
// sidecar in this process. It serves the store paths of one service over
// NFS, on a port of the loopback interface, and mounts that on
// /nix/store: a store path is fetched from draal's store the first time
// it is named, or ahead of that with Preload.
//
// The server and the mount are for Linux alone. On another system Start
// fails, and a zathras runs with --no-store.
//
// The process that serves the store executes nothing from it itself: see
// package launcher.
package storemount

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/fables-for-robots/great-machine/wire"
)

// DefaultDir is where the store is mounted unless Config says otherwise.
const DefaultDir = "/nix/store"

// Config is what a mount is made of.
type Config struct {
	// Server is the endpoint ID of draal, which is the store's. Required.
	Server wire.ID
	// Addrs is for tests: where the server is. It is then dialed there,
	// and looked up nowhere.
	Addrs []netip.AddrPort
	// Service is the service of the zathras: its prefix in the store is
	// what the mount is made of. Required.
	Service string
	// Cache is the directory of what was fetched. It is of one server and
	// one service: with another, Start refuses it. Required.
	Cache string
	// Dir is the directory the store is mounted on, which is made if it
	// is not there. "": DefaultDir.
	Dir string
	// Log is where the mount logs. nil: slog.Default().
	Log *slog.Logger
}

// check returns why a mount cannot be made of cfg, or nil.
func (cfg Config) check() error {
	if cfg.Server == "" {
		return errors.New("storemount: no server: the endpoint ID of draal is needed")
	}
	if cfg.Service == "" {
		return errors.New("storemount: no service")
	}
	if err := wire.ValidService(cfg.Service); err != nil {
		return fmt.Errorf("storemount: %w", err)
	}
	if cfg.Cache == "" {
		return errors.New("storemount: no directory for the cache")
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
