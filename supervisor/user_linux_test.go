package supervisor

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/launcher"
	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/wire"
)

// A service of another user writes to pipes that the supervisor made, as
// root. The launcher gives them to the user, so a service that opens
// /dev/stdout or /dev/stderr anew, as many are configured to, writes
// where it would have written anyway: into the ring.
func TestAServiceOfAnotherUserCanOpenItsStandardStreams(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("another user takes root: scripts/test-linux.sh runs this as root, in a container")
	}
	l, err := launcher.New(launcher.Config{User: &launcher.User{UID: 65534, GID: 65534}, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("launcher.New: %v", err)
	}
	h := run(t, Config{QuickExit: time.Hour, Start: l.Start})
	h.activate(1, "/bin/sh", "-c", `echo "to standard output, as $(id -u)" > /dev/stdout; echo to standard error > /dev/stderr; sleep 1000`)
	h.wrote(logring.Stdout, "to standard output, as 65534")
	h.wrote(logring.Stderr, "to standard error")
	h.phase(wire.PhaseRunning)
}
