//go:build !linux

package storemount

import (
	"strings"
	"testing"
)

// Where the store cannot be served and mounted, Start says so and names
// the way for such a system.
func TestStartOnASystemThatIsNotLinux(t *testing.T) {
	m, err := Start(Config{Server: anID(t), Service: "web", Cache: t.TempDir(), Dir: t.TempDir()})
	if err == nil {
		m.Stop()
		t.Fatal("Start() = nil on a system that is not Linux")
	}
	for _, word := range []string{"Linux", "--no-store"} {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("the error does not say %q: %v", word, err)
		}
	}
}
