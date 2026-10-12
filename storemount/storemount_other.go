//go:build !linux

package storemount

import (
	"context"
	"errors"
	"fmt"
	"runtime"
)

// Mount is the store of a zathras, served and mounted. There is none on
// this system.
type Mount struct{}

// Start fails: the store of a zathras is served and mounted on Linux
// alone.
func Start(cfg Config) (*Mount, error) {
	return nil, fmt.Errorf("storemount: the store of a zathras is served and mounted on Linux alone, and this is %s: --no-store runs a zathras without a store of its own", runtime.GOOS)
}

// Preload fails: there is no mount.
func (m *Mount) Preload(ctx context.Context, names []string) error {
	return errors.ErrUnsupported
}

// Stop does nothing: there is no mount.
func (m *Mount) Stop() error { return nil }
