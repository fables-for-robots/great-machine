package e2e

import (
	"os"
	"testing"

	"github.com/fables-for-robots/great-machine/launcher"
)

// The test binary is its own exec-in-place, as the program is: a pod
// whose service is started through a launcher starts this binary, which
// then becomes the service.
func TestMain(m *testing.M) {
	if status, was := launcher.Replace(os.Args); was {
		os.Exit(status)
	}
	os.Exit(m.Run())
}
