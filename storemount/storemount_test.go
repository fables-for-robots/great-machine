package storemount

import (
	"strings"
	"testing"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// anID returns the endpoint ID of a key that is made for the test: one a
// store could have.
func anID(t *testing.T) wire.ID {
	t.Helper()
	key, err := node.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return key.ID()
}

// What a mount cannot be made of is said before anything is opened.
func TestWhatAMountIsMadeOf(t *testing.T) {
	good := Config{Server: anID(t), Service: "web", Cache: "/var/cache/great-machine"}
	if err := good.check(); err != nil {
		t.Errorf("a config with a server, a service and a cache: %v", err)
	}
	for name, c := range map[string]struct {
		change func(*Config)
		says   string
	}{
		"no server":                       {func(c *Config) { c.Server = "" }, "server"},
		"a server that is no endpoint ID": {func(c *Config) { c.Server = "d5aa1" }, `"d5aa1"`},
		"no service":                      {func(c *Config) { c.Service = "" }, "service"},
		"a service that is no name":       {func(c *Config) { c.Service = "Web/1" }, `"Web/1"`},
		"no cache":                        {func(c *Config) { c.Cache = "" }, "cache"},
	} {
		cfg := good
		c.change(&cfg)
		err := cfg.check()
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: check() = %v, want an error that says %s", name, err, c.says)
		}
	}
}

// What the error of a store path the store does not have says: the path,
// and the service whose store it is not in.
func TestNotFoundError(t *testing.T) {
	err := &NotFoundError{Path: "/nix/store/abc-hello-2.12", Service: "web"}
	for _, word := range []string{"/nix/store/abc-hello-2.12", "the service web"} {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("the error does not say %q: %v", word, err)
		}
	}
}
