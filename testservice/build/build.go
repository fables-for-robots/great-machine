// Package build builds the program testservice for the tests that need a
// real service.
package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Binary builds testservice into tb.TempDir() and returns its path.
func Binary(tb testing.TB) string {
	tb.Helper()
	out := filepath.Join(tb.TempDir(), "testservice")
	cmd := exec.Command("go", "build", "-o", out, "github.com/fables-for-robots/great-machine/testservice")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("building testservice: %v\n%s", err, output)
	}
	return out
}
