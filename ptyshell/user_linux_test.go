package ptyshell

import (
	"log/slog"
	"os"
	"testing"

	"github.com/fables-for-robots/great-machine/launcher"
)

// The shell of another user is started on a terminal that this process
// opened, as root. It is the shell's own all the same: the launcher gives
// it to the user, which it can because the terminal is the command's when
// the launcher is asked to start it. The shell opens it by its name, as
// whatever asks for the terminal of its session does.
func TestAShellOfAnotherUserCanOpenItsTerminal(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("another user takes root: scripts/test-linux.sh runs this as root, in a container")
	}
	l, err := launcher.New(launcher.Config{User: &launcher.User{UID: 65534, GID: 65534}, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("launcher.New: %v", err)
	}
	s, out := start(t, Config{Start: l.Start})
	typeIn(t, s, `t=$(tty); if : > "$t"; then echo "opened-$((6*7)) by $(id -u)"; else echo "denied-$((6*7)) to $(id -u)"; fi`)
	out.waitFor(t, "opened-42 by 65534")
}
