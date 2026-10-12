package cli

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/amber-store/jaccard-nfs-nix-store/jstest"
	jmount "github.com/amber-store/jaccard-nfs-nix-store/mount"

	"github.com/fables-for-robots/great-machine/wire"
)

// The command as a whole, with the store it really has: a zathras that
// runs has its store mounted, and one that has ended has not.
//
// The store is mounted on a directory of the test. /nix/store itself is
// mounted on by the tests of package storemount alone: the tests of the
// packages run at the same time, and there is one /nix/store.
func TestRunZathrasMountsItsStore(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("mounting needs root: scripts/test-linux.sh runs this as root, in a container")
	}
	dial := jstest.Start(t).DialConfig()
	cfg := aZathras(t)
	// The store is draal's: one endpoint ID. Here that is a store alone,
	// which takes no zathras: this one dials for as long as it runs.
	cfg.draal, cfg.addrs = wire.ID(dial.Server.String()), dial.Addrs
	cfg.storeDir = t.TempDir()
	t.Cleanup(func() { jmount.Detach(cfg.storeDir) })
	mounted := func() bool {
		_, is, err := jmount.Mounted(cfg.storeDir)
		if err != nil {
			t.Fatal(err)
		}
		return is
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var logged bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- runZathras(ctx, cfg, quietLog(&logged)) }()
	answering(t, cfg.healthAddr)
	// The health check listens after the store is mounted.
	if !mounted() {
		t.Errorf("the zathras runs and its store is not mounted\n%s", logged.String())
	}
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runZathras returned %v, want nil\n%s", err, logged.String())
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("runZathras did not return\n%s", logged.String())
	}
	if mounted() {
		t.Errorf("the zathras has ended and its store is still mounted\n%s", logged.String())
	}
}
