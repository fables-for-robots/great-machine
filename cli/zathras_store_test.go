package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The command as a whole, with the store it really has: a zathras that
// runs has made its store directory and its cache, has made both its own
// to write to, and has removed what an earlier run was writing to the
// store directory when it ended. It dials nobody for that.
func TestRunZathrasOpensTheStoreItReallyHas(t *testing.T) {
	cfg := aZathras(t)
	root := t.TempDir()
	cfg.storeDir = filepath.Join(root, "store")
	cfg.storeCache = filepath.Join(root, "cache")
	// The store directory as the volume of a pod comes, with what a run
	// that was killed left in it.
	half := filepath.Join(cfg.storeDir, ".great-machine-partial-0123-half")
	if err := os.MkdirAll(half, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(half, "file"), []byte("half"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfg.storeDir, 0o777); err != nil {
		t.Fatal(err)
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var logged buffer
	done := make(chan error, 1)
	go func() { done <- runZathras(ctx, cfg, quietLog(&logged)) }()
	// The health check listens after the store is opened.
	deadline := time.Now().Add(20 * time.Second)
	for healthz(cfg.healthAddr) == 0 {
		select {
		case err := <-done:
			t.Fatalf("runZathras returned before its health check answered: %v\n%s", err, logged.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the health check of the zathras does not answer\n%s", logged.String())
		}
		time.Sleep(5 * time.Millisecond)
	}

	if info, err := os.Stat(cfg.storeDir); err != nil || info.Mode() != os.ModeDir|0o755 {
		t.Errorf("the store directory: %v, %v, want a directory of mode 0755", info, err)
	}
	if _, err := os.Lstat(half); !os.IsNotExist(err) {
		t.Errorf("what an earlier run was writing is still in the store directory (%v)", err)
	}
	if info, err := os.Stat(filepath.Join(cfg.storeCache, "packs")); err != nil || !info.IsDir() {
		t.Errorf("the pack cache is not a directory packs in --store-cache: %v", err)
	}

	stop()
	if err := returned(t, done, &logged); err != nil {
		t.Errorf("runZathras returned %v, want nil\n%s", err, logged.String())
	}
}
