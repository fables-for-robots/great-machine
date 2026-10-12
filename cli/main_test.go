package cli

import (
	"os"
	"testing"

	"github.com/fables-for-robots/great-machine/launcher"
)

// The test binary is its own exec-in-place, as the program is: a zathras
// of these tests that starts a service or a shell starts this binary,
// which then becomes what was meant.
func TestMain(m *testing.M) {
	if status, was := launcher.Replace(os.Args); was {
		os.Exit(status)
	}
	os.Exit(m.Run())
}
