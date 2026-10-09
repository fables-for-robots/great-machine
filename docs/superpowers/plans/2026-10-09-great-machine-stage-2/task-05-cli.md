### Task 5: `--web-addr` and `--web-host`, and the server of the page beside the iroh endpoint

**Files:**
- Modify: `cli/draal.go` (the two flags, their places in `draalConfig`, and `runDraal`, which starts the server and stops it first)
- Create: `cli/web.go` (the HTTP server: its start, its stop, the line of the log)
- Modify: `cli/commands_test.go`, `cli/fake_test.go` (the flags and their variables)
- Test: `cli/web_test.go`, `cli/draal_test.go`
- Create: `scripts/test-page.sh` (asks the draal of a program, or of an image, for the page)
- Modify: `.github/workflows/test.yml` (the step `nix build` asks the program for the page)

**Interfaces:**
- Consumes:
  - The package `web` of Tasks 1 and 2: `web.New(cfg web.Config) http.Handler` with `web.Config{Controller draal.Controller; Loopback bool; Hosts []string; Log *slog.Logger; …}`, `web.Built() bool`, `web.IsLoopback(addr string) bool`. Of the handler this task relies on what the contract says of it: outside `/api/` it answers `GET` with the page, `index.html` for a path that is no file, and with status 501 when the page is not built; it answers a request by its `Host` as the table "The host name" has it (a loopback IP address and `localhost` always, a name of `Hosts` always and however it is written, any other name with 403); `GET /api/watch` is a WebSocket whose first message comes at once; **and the handler of every WebSocket returns when the context of its request is done, within a second or two also when the page does not answer**, which is how this task ends the sockets at shutdown.
  - `github.com/coder/websocket` (in `go.mod` since Task 2), in a test, as a page's side of a socket.
  - `draal.New(draal.Config) *draal.Draal`, `(*draal.Draal).Serve(ctx, node.Listener) error`; `node.Listen(ctx, node.ListenConfig{Key, Bind, Local, Log})`, where `Local` is the field for tests: no relay, nothing announced, bound to 127.0.0.1.
  - In `cli`: `required`, `noArguments`, `usageError`; in its tests `buffer`, `waitFor`, `cleanEnvironment`, `read`, `idA`, `idB`.
  - Task 4: `packages.<system>.default` is built from the repository with the page at `web/dist`.
- Produces:
  - `great-machine draal --web-addr ADDR` (`GREAT_MACHINE_WEB_ADDR`), default `127.0.0.1:8080`, empty for no web page. A value that is not `HOST:PORT` is a command line that is not valid: status 2. An address that cannot be bound ends the command with status 1 and `great-machine: the web page: listen tcp …`.
  - `great-machine draal --web-host NAME` (`GREAT_MACHINE_WEB_HOST`), which may be repeated and whose variable takes several with commas between them: a host name without a port, of letters, digits, hyphens and dots, 253 characters at most, kept in lower case and without a dot at its end. Anything else is status 2. The names are `web.Config.Hosts`.
  - The log, in this order after `serving`: `level=INFO msg="web page" addr=127.0.0.1:8080 url=http://127.0.0.1:8080/` (`addr` is what was bound, `url` has the host of the flag, or `localhost` when the flag names every address), with ` hosts=draal.example.com,draal.internal` at its end when `--web-host` gave names, and without `hosts=` when it gave none; `level=WARN msg="the web page was not built into this program: it answers every request with a line that says so"` when `!web.Built()`; `level=WARN msg="the web page listens on an address that is not loopback: it has no authentication, and whoever reaches it has a shell in every container" addr=…` when `!web.IsLoopback(addr)`.
  - `scripts/test-page.sh PROGRAM` and `scripts/test-page.sh --image IMAGE`: starts `draal`, reads the URL from the log, and fails unless `GET /` and `GET /s/web` are answered with a page. Task 11 uses the second form.

Two things about `net/http` decide the shape of `cli/web.go`. First,
`http.Server.Shutdown` neither waits for nor closes a connection that a
handler took over, which is what a WebSocket is, and `Close` does not
close it either. What ends the sockets is the context of their requests:
the server is given a `BaseContext` that `Stop` cancels. Second, the
server loses count of those handlers, so `cli` counts them itself and
`Stop` waits for them, since they call into draal and the database is
closed right after. Step 9 has the test that pins both. The server has
a time for the header of a request (10 seconds) and for a connection
that is idle (2 minutes), and none for a read or a write, which would
cut a WebSocket.

- [ ] **Step 1: Write the failing tests of the flag**

In `cli/fake_test.go`, `GREAT_MACHINE_WEB_ADDR` and `GREAT_MACHINE_WEB_HOST` join the variables that `cleanEnvironment` takes out of the environment:

```go
// variables are the environment variables the commands read.
var variables = []string{
	"GREAT_MACHINE_DATA", "GREAT_MACHINE_OPERATORS", "JACCARD_SERVER", "JACCARD_PREFIX", "GREAT_MACHINE_BIND", "GREAT_MACHINE_WEB_ADDR", "GREAT_MACHINE_WEB_HOST",
	"GREAT_MACHINE_DRAAL", "GREAT_MACHINE_SERVICE", "GREAT_MACHINE_NAME", "GREAT_MACHINE_PRELOAD_URL",
	"GREAT_MACHINE_SHELL", "GREAT_MACHINE_STOP_TIMEOUT", "GREAT_MACHINE_HEALTH_ADDR",
	"GREAT_MACHINE_KEY", "GREAT_MACHINE_PARALLEL", "GREAT_MACHINE_DETACH", "GREAT_MACHINE_JOBS",
	"GREAT_MACHINE_TEMP_DIR", "GREAT_MACHINE_FOLLOW",
}
```

In `cli/commands_test.go`, in the table of `TestInvalidCommandLines`, the rows of draal get seven more, before `draal: an argument`:

```go
		// draal.
		{"draal: no --data", nil, []string{"draal", "--jaccard-server", idB, "--jaccard-prefix", "nix/"}, "--data is required: give it, or set GREAT_MACHINE_DATA"},
		{"draal: no --jaccard-server", nil, []string{"draal", "--data", "d", "--jaccard-prefix", "nix/"}, "--jaccard-server is required: give it, or set JACCARD_SERVER"},
		{"draal: no --jaccard-prefix", nil, []string{"draal", "--data", "d", "--jaccard-server", idB}, "--jaccard-prefix is required: give it, or set JACCARD_PREFIX"},
		{"draal: a bad --jaccard-server", nil, []string{"draal", "--data", "d", "--jaccard-server", "nonsense", "--jaccard-prefix", "nix/"}, `--jaccard-server: "nonsense" is not an endpoint ID`},
		{"draal: an operator that is no endpoint ID", nil,
			[]string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/", "--operator", idA, "--operator", "alice"},
			`--operator: "alice" is not an endpoint ID`},
		{"draal: the variable stands in for --operator", map[string]string{"GREAT_MACHINE_OPERATORS": idA + ",bob"},
			[]string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/"}, `--operator: "bob" is not an endpoint ID`},
		{"draal: a bad --bind", nil, []string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/", "--bind", "4433"}, `--bind: "4433" is not an address to bind`},
		{"draal: a bad --web-addr", nil, []string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/", "--web-addr", "8080"}, `--web-addr: "8080" is not an address to listen on`},
		{"draal: the variable stands in for --web-addr", map[string]string{"GREAT_MACHINE_WEB_ADDR": "localhost"},
			[]string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/"}, `--web-addr: "localhost" is not an address to listen on`},
		{"draal: a --web-host with a port", nil, []string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/", "--web-host", "draal.example.com:8080"},
			`--web-host: "draal.example.com:8080" is not a host name: want letters, digits, hyphens and dots, 253 characters at most, and no port`},
		{"draal: a --web-host that is a URL", nil, []string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/", "--web-host", "https://draal.example.com"},
			`--web-host: "https://draal.example.com" is not a host name`},
		{"draal: a --web-host that is a dot", nil, []string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/", "--web-host", "."}, `--web-host: "." is not a host name`},
		{"draal: a --web-host that is too long", nil, []string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/", "--web-host", strings.Repeat("a", 254)}, `is not a host name`},
		{"draal: the variable stands in for --web-host", map[string]string{"GREAT_MACHINE_WEB_HOST": "draal.example.com,draal_2"},
			[]string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/"}, `--web-host: "draal_2" is not a host name`},
		{"draal: an argument", nil, []string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/", "now"}, `draal takes no argument, got "now"`},
```

and `TestReadDraal` expects the default in both of its cases, with a test of each flag behind it:

```go
func TestReadDraal(t *testing.T) {
	cleanEnvironment(t)
	var got draalConfig
	reader := func(c *cli.Context) (err error) {
		got, err = readDraal(c)
		return err
	}

	// The variables alone. The operators have commas between them, and
	// what a comma too many leaves empty is passed over.
	t.Setenv("GREAT_MACHINE_DATA", "/var/lib/draal")
	t.Setenv("GREAT_MACHINE_OPERATORS", idA+", "+idB+",")
	t.Setenv("JACCARD_SERVER", idC)
	t.Setenv("JACCARD_PREFIX", "nix/")
	read(t, draalCommand(), reader)
	want := draalConfig{
		data:      "/var/lib/draal",
		operators: []wire.ID{wire.ID(idA), wire.ID(idB)},
		store:     wire.StoreInfo{Server: wire.ID(idC), Prefix: "nix/"},
		webAddr:   "127.0.0.1:8080",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("from the variables: got %+v, want %+v", got, want)
	}

	// A flag wins over its variable, and --operator may be repeated.
	read(t, draalCommand(), reader, "--data", "data", "--operator", idC, "--operator", idA,
		"--jaccard-server", idA, "--jaccard-prefix", "store/", "--bind", "0.0.0.0:4433")
	want = draalConfig{
		data:      "data",
		operators: []wire.ID{wire.ID(idC), wire.ID(idA)},
		store:     wire.StoreInfo{Server: wire.ID(idA), Prefix: "store/"},
		webAddr:   "127.0.0.1:8080",
		bind:      netip.MustParseAddrPort("0.0.0.0:4433"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("from the flags: got %+v, want %+v", got, want)
	}
}

// TestReadDraalWebAddr: --web-addr has a default, its variable stands in
// for it, the flag wins, and an empty value is no web page.
func TestReadDraalWebAddr(t *testing.T) {
	cleanEnvironment(t)
	var got draalConfig
	reader := func(c *cli.Context) (err error) {
		got, err = readDraal(c)
		return err
	}
	args := []string{"--data", "d", "--jaccard-server", idA, "--jaccard-prefix", "nix/"}
	with := func(more ...string) []string { return append(append([]string{}, args...), more...) }

	read(t, draalCommand(), reader, args...)
	if got.webAddr != "127.0.0.1:8080" {
		t.Errorf("the default: got %q, want 127.0.0.1:8080", got.webAddr)
	}
	read(t, draalCommand(), reader, with("--web-addr", "")...)
	if got.webAddr != "" {
		t.Errorf("an empty flag: got %q, want no web page", got.webAddr)
	}
	read(t, draalCommand(), reader, with("--web-addr", " [::1]:9000 ")...)
	if got.webAddr != "[::1]:9000" {
		t.Errorf("the flag: got %q, want [::1]:9000", got.webAddr)
	}

	t.Setenv("GREAT_MACHINE_WEB_ADDR", "localhost:8081")
	read(t, draalCommand(), reader, args...)
	if got.webAddr != "localhost:8081" {
		t.Errorf("the variable: got %q, want localhost:8081", got.webAddr)
	}
	read(t, draalCommand(), reader, with("--web-addr", ":8082")...)
	if got.webAddr != ":8082" {
		t.Errorf("the flag over the variable: got %q, want :8082", got.webAddr)
	}
	t.Setenv("GREAT_MACHINE_WEB_ADDR", "")
	read(t, draalCommand(), reader, args...)
	if got.webAddr != "" {
		t.Errorf("an empty variable: got %q, want no web page", got.webAddr)
	}
}

// TestReadDraalWebHost: --web-host may be repeated, its variable takes
// several with commas between them, and a name is kept in lower case and
// without a dot at its end.
func TestReadDraalWebHost(t *testing.T) {
	cleanEnvironment(t)
	var got draalConfig
	reader := func(c *cli.Context) (err error) {
		got, err = readDraal(c)
		return err
	}
	args := []string{"--data", "d", "--jaccard-server", idA, "--jaccard-prefix", "nix/"}
	with := func(more ...string) []string { return append(append([]string{}, args...), more...) }

	read(t, draalCommand(), reader, args...)
	if got.webHosts != nil {
		t.Errorf("without the flag: got %q, want none", got.webHosts)
	}
	read(t, draalCommand(), reader, with("--web-host", "Draal.Example.COM.", "--web-host", "draal-2.internal", "--web-host", "xn--bcher-kva.example")...)
	if want := []string{"draal.example.com", "draal-2.internal", "xn--bcher-kva.example"}; !reflect.DeepEqual(got.webHosts, want) {
		t.Errorf("the flag, repeated: got %q, want %q", got.webHosts, want)
	}
	// A name of 253 characters is the longest there is.
	longest := strings.Repeat("a", 253)
	read(t, draalCommand(), reader, with("--web-host", longest)...)
	if want := []string{longest}; !reflect.DeepEqual(got.webHosts, want) {
		t.Errorf("a name of 253 characters: got %q", got.webHosts)
	}

	t.Setenv("GREAT_MACHINE_WEB_HOST", "draal.example.com, DRAAL.internal,")
	read(t, draalCommand(), reader, args...)
	if want := []string{"draal.example.com", "draal.internal"}; !reflect.DeepEqual(got.webHosts, want) {
		t.Errorf("the variable: got %q, want %q", got.webHosts, want)
	}
	read(t, draalCommand(), reader, with("--web-host", "other.example.com")...)
	if want := []string{"other.example.com"}; !reflect.DeepEqual(got.webHosts, want) {
		t.Errorf("the flag over the variable: got %q, want %q", got.webHosts, want)
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `nix develop .#bare --command go test ./cli/ -run 'TestReadDraal|TestInvalidCommandLines'`
Expected: FAIL, the package does not compile: `unknown field webAddr in struct literal of type draalConfig` and `got.webAddr undefined (type draalConfig has no field or method webAddr)`, and then `too many errors`

- [ ] **Step 3: The flag**

In `cli/draal.go`, the imports get `net`:

```go
import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)
```

and the command, its configuration and `readDraal` become, with `webHost` behind them:

```go
// draalCommand is `great-machine draal`, the controller.
func draalCommand() *cli.Command {
	return &cli.Command{
		Name:  "draal",
		Usage: "the controller: zathrases report to it, operators roll out through it",
		Description: "The endpoint ID draal logs at its start is all that a zathras or an\n" +
			"operator needs to reach it.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "data", EnvVars: []string{"GREAT_MACHINE_DATA"},
				Usage: "`DIR`ectory of draal: its key (created on first run) and its database (required)"},
			&cli.StringSliceFlag{Name: "operator", EnvVars: []string{"GREAT_MACHINE_OPERATORS"},
				Usage: "endpoint `ID` that may act as an operator, as `great-machine id` prints it; may be repeated, and the variable takes several with commas between them"},
			&cli.StringFlag{Name: "jaccard-server", EnvVars: []string{"JACCARD_SERVER"},
				Usage: "endpoint `ID` of the jaccard-store server that closures are pushed to (required)"},
			&cli.StringFlag{Name: "jaccard-prefix", EnvVars: []string{"JACCARD_PREFIX"},
				Usage: "`PREFIX` put before the name of a store path to make its reference; the same value the sidecars are given (required)"},
			&cli.StringFlag{Name: "web-addr", EnvVars: []string{"GREAT_MACHINE_WEB_ADDR"}, Value: "127.0.0.1:8080",
				Usage: "`ADDR`ess the web page listens on; empty for no web page"},
			&cli.StringSliceFlag{Name: "web-host", EnvVars: []string{"GREAT_MACHINE_WEB_HOST"},
				Usage: "host `NAME` under which the web page is answered besides IP addresses and localhost, for a draal behind a proxy that authenticates; may be repeated, and the variable takes several with commas between them"},
			&cli.StringFlag{Name: "bind", EnvVars: []string{"GREAT_MACHINE_BIND"},
				Usage: "UDP `IP:PORT` the iroh endpoint binds (default: every address, a port the system picks)"},
		},
		Action: func(c *cli.Context) error {
			cfg, err := readDraal(c)
			if err != nil {
				return err
			}
			return runDraal(c.Context, cfg, slog.New(slog.NewTextHandler(c.App.ErrWriter, nil)))
		},
	}
}

// draalConfig is what the command line of draal says.
type draalConfig struct {
	data      string
	operators []wire.ID
	store     wire.StoreInfo
	webAddr   string         // "" for no web page
	webHosts  []string       // the names the web page answers to besides, in lower case
	bind      netip.AddrPort // the zero value: every address, a port the system picks
}

// readDraal reads the command line of draal.
func readDraal(c *cli.Context) (draalConfig, error) {
	if err := noArguments(c); err != nil {
		return draalConfig{}, err
	}
	var cfg draalConfig
	var err error
	if cfg.data, err = required(c, "data", "GREAT_MACHINE_DATA"); err != nil {
		return draalConfig{}, err
	}
	// The library has cut the values at their commas already. What is
	// left empty by a comma too many, or by a space, is not an ID.
	for _, text := range c.StringSlice("operator") {
		if text = strings.TrimSpace(text); text == "" {
			continue
		}
		id, err := node.ParseID(text)
		if err != nil {
			return draalConfig{}, usageError("--operator: %q is not an endpoint ID, as `great-machine id` prints one: %w", text, err)
		}
		cfg.operators = append(cfg.operators, id)
	}
	server, err := required(c, "jaccard-server", "JACCARD_SERVER")
	if err != nil {
		return draalConfig{}, err
	}
	if cfg.store.Server, err = node.ParseID(server); err != nil {
		return draalConfig{}, usageError("--jaccard-server: %q is not an endpoint ID: %w", server, err)
	}
	if cfg.store.Prefix, err = required(c, "jaccard-prefix", "JACCARD_PREFIX"); err != nil {
		return draalConfig{}, err
	}
	if cfg.webAddr = strings.TrimSpace(c.String("web-addr")); cfg.webAddr != "" {
		if _, _, err := net.SplitHostPort(cfg.webAddr); err != nil {
			return draalConfig{}, usageError("--web-addr: %q is not an address to listen on: want HOST:PORT, such as 127.0.0.1:8080, or nothing for no web page", cfg.webAddr)
		}
	}
	// As with --operator: the library has cut the values at their commas,
	// and what a comma too many leaves empty is passed over.
	for _, text := range c.StringSlice("web-host") {
		if text = strings.TrimSpace(text); text == "" {
			continue
		}
		host, err := webHost(text)
		if err != nil {
			return draalConfig{}, err
		}
		cfg.webHosts = append(cfg.webHosts, host)
	}
	if bind := c.String("bind"); bind != "" {
		if cfg.bind, err = netip.ParseAddrPort(bind); err != nil {
			return draalConfig{}, usageError("--bind: %q is not an address to bind: want IP:PORT, such as 0.0.0.0:4433 or [::]:4433", bind)
		}
	}
	return cfg, nil
}

// webHost returns text as a host name the web page answers to: in lower
// case, without a dot at its end. A name has letters, digits, hyphens
// and dots, 253 characters at most, and no port.
func webHost(text string) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(text), ".")
	ok := host != "" && len(host) <= 253
	for _, c := range host {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.') {
			ok = false
		}
	}
	if !ok {
		return "", usageError("--web-host: %q is not a host name: want letters, digits, hyphens and dots, 253 characters at most, and no port, such as draal.example.com", text)
	}
	return host, nil
}
```

`runDraal` is not changed yet: the flags are read and nothing listens.

- [ ] **Step 4: Run the tests to see them pass**

Run: `nix develop .#bare --command go test ./cli/`
Expected: `ok  	github.com/fables-for-robots/great-machine/cli`

- [ ] **Step 5: Commit**

```bash
git add cli/
git commit -m "CLI: draal reads --web-addr and --web-host"
```

- [ ] **Step 6: Write the failing tests of the server**

`cli/web_test.go`. `TestWebServerStopEndsWebSockets` is the one that holds the shutdown to what the contract says of it, "its sockets are closed": its handler takes the connection over as the handler of a WebSocket does, and `Stop` has to end it and wait for it.

```go
package cli

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebURL(t *testing.T) {
	bound := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 4321}
	tests := []struct{ addr, want string }{
		{"127.0.0.1:8080", "http://127.0.0.1:4321/"},
		{"127.0.0.1:0", "http://127.0.0.1:4321/"},
		{"localhost:8080", "http://localhost:4321/"},
		{"[::1]:8080", "http://[::1]:4321/"},
		{":8080", "http://localhost:4321/"},
		{"0.0.0.0:8080", "http://localhost:4321/"},
		{"[::]:8080", "http://localhost:4321/"},
		{"10.1.2.3:8080", "http://10.1.2.3:4321/"},
	}
	for _, tt := range tests {
		if got := webURL(tt.addr, bound); got != tt.want {
			t.Errorf("webURL(%q) is %q, want %q", tt.addr, got, tt.want)
		}
	}
}

// quietLog is a logger that writes to out.
func quietLog(out io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(out, nil))
}

// TestAnnounceWeb: the line of the log, and the two warnings.
func TestAnnounceWeb(t *testing.T) {
	bound := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}
	const notBuilt, notLoopback = "the web page was not built into this program", "not loopback: it has no authentication, and whoever reaches it has a shell in every container"

	var logged buffer
	announceWeb(quietLog(&logged), "127.0.0.1:8080", bound, nil, true, true)
	if got, want := logged.String(), `level=INFO msg="web page" addr=127.0.0.1:8080 url=http://127.0.0.1:8080/`; !strings.Contains(got, want) {
		t.Errorf("logged %q, want %q in it", got, want)
	}
	if got := logged.String(); strings.Count(got, "\n") != 1 {
		t.Errorf("a page that is built, on loopback, is one line: got %q", got)
	}

	if got := logged.String(); strings.Contains(got, "hosts=") {
		t.Errorf("without a host name the line names none: got %q", got)
	}

	// The host names the page answers to besides are in the line.
	logged = buffer{}
	announceWeb(quietLog(&logged), "127.0.0.1:8080", bound, []string{"draal.example.com", "draal.internal"}, true, true)
	if got, want := logged.String(), `msg="web page" addr=127.0.0.1:8080 url=http://127.0.0.1:8080/ hosts=draal.example.com,draal.internal`; !strings.Contains(got, want) {
		t.Errorf("logged %q, want %q in it", got, want)
	}

	logged = buffer{}
	announceWeb(quietLog(&logged), "127.0.0.1:8080", bound, nil, true, false)
	if got := logged.String(); !strings.Contains(got, "level=WARN") || !strings.Contains(got, notBuilt) || strings.Contains(got, notLoopback) {
		t.Errorf("a page that is not built: logged %q", got)
	}

	logged = buffer{}
	announceWeb(quietLog(&logged), ":8080", &net.TCPAddr{IP: net.IPv6unspecified, Port: 8080}, nil, false, true)
	if got := logged.String(); !strings.Contains(got, "level=WARN") || !strings.Contains(got, notLoopback) || !strings.Contains(got, "url=http://localhost:8080/") || strings.Contains(got, notBuilt) {
		t.Errorf("an address that is not loopback: logged %q", got)
	}
}

// TestWebServerStopEndsWebSockets: a connection that a handler took over,
// as the handler of a WebSocket does, is ended by Stop through the context
// of its request, and Stop waits for the handler. Neither Shutdown nor
// Close of http.Server does that.
func TestWebServerStopEndsWebSockets(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var taken, ended atomic.Bool
	handler := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		conn, _, err := rw.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("Hijack: %v", err)
			return
		}
		defer conn.Close()
		io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\n\r\n")
		taken.Store(true)
		<-r.Context().Done()
		ended.Store(true)
	})
	var logged buffer
	w := startWeb(listener, handler, 5*time.Second, quietLog(&logged))
	defer w.Stop()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "GET /api/watch HTTP/1.1\r\nHost: localhost\r\n\r\n")
	waitFor(t, "the handler to take the connection over", taken.Load)

	begun := time.Now()
	w.Stop()
	if !ended.Load() {
		t.Error("Stop returned before the handler of the connection had ended")
	}
	if took := time.Since(begun); took > 4*time.Second {
		t.Errorf("Stop took %s: it waited for a time to pass and not for the handler", took)
	}
	// The browser's side: the answer, and then the end.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	rest, err := io.ReadAll(conn)
	if err != nil {
		t.Errorf("reading the connection to its end: %v", err)
	}
	if !strings.HasPrefix(string(rest), "HTTP/1.1 101") {
		t.Errorf("the connection carried %q", rest)
	}
	if _, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second); err == nil {
		t.Error("the address still listens after Stop")
	}
	if logged.String() != "" {
		t.Errorf("Stop logged %q, want nothing", logged.String())
	}
	w.Stop() // a second time is nothing
}

// TestWebServerStopGivesUp: a handler that does not end with the context
// of its request holds Stop for the wait it was given, twice at most, and
// no longer.
func TestWebServerStopGivesUp(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var entered atomic.Bool
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		entered.Store(true)
		<-release
	})
	var logged buffer
	w := startWeb(listener, handler, 50*time.Millisecond, quietLog(&logged))
	defer close(release)

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
	waitFor(t, "the handler to be entered", entered.Load)

	stopped := make(chan struct{})
	go func() {
		w.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop waits for a handler that does not end")
	}
	if !strings.Contains(logged.String(), "still being answered") {
		t.Errorf("Stop logged %q, want it to say that requests were still being answered", logged.String())
	}
}
```

- [ ] **Step 7: Run the tests to see them fail**

Run: `nix develop .#bare --command go test ./cli/ -run 'TestWeb|TestAnnounceWeb'`
Expected: FAIL, the package does not compile: `undefined: webURL`, `undefined: announceWeb`, `undefined: startWeb`

- [ ] **Step 8: The server**

`cli/web.go`:

```go
package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The times of the server of the web page.
const (
	// webReadHeaderWait is how long a browser has to send the header of a
	// request.
	webReadHeaderWait = 10 * time.Second
	// webIdleWait is how long a connection may stay open with no request
	// on it.
	webIdleWait = 2 * time.Minute
	// webStopWait is how long the requests that are being answered have
	// to end when the server is stopped, and then once more the handlers
	// of the WebSockets.
	webStopWait = 5 * time.Second
)

// webServer is the HTTP server of the web page, beside the iroh endpoint
// of draal.
type webServer struct {
	server *http.Server
	end    context.CancelFunc // ends the context of every request
	failed chan error         // what Serve returned, when it was not told to end
	wait   time.Duration
	log    *slog.Logger

	mu       sync.Mutex
	stopping bool           // no request is begun any more
	handlers sync.WaitGroup // the requests being answered, WebSockets among them
	stop     sync.Once
}

// startWeb serves handler on listener until Stop. wait is how long Stop
// gives what is being answered to end.
func startWeb(listener net.Listener, handler http.Handler, wait time.Duration, log *slog.Logger) *webServer {
	requests, end := context.WithCancel(context.Background())
	w := &webServer{end: end, failed: make(chan error, 1), wait: wait, log: log}
	w.server = &http.Server{
		Handler:           w.counted(handler),
		ReadHeaderTimeout: webReadHeaderWait,
		IdleTimeout:       webIdleWait,
		// No WriteTimeout and no ReadTimeout: either would cut a
		// WebSocket, which lives as long as the page is open. The handler
		// bounds every write to a browser by itself. IdleTimeout does not
		// touch a WebSocket: the server has let go of its connection.
		BaseContext: func(net.Listener) context.Context { return requests },
		ErrorLog:    slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	go func() {
		if err := w.server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			w.failed <- fmt.Errorf("the web page: %w", err)
		}
	}()
	return w
}

// counted is handler, with every request it answers counted in
// w.handlers, so that Stop can wait for them. The server itself loses
// sight of a connection that became a WebSocket.
func (w *webServer) counted(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		if w.stopping {
			// Stop is waiting for the handlers, and one more must not be
			// added to those it waits for. No request begins this late,
			// for the listener is closed and the server begins none on
			// a connection it keeps, but for one: on a connection that
			// was accepted just before, whose request comes when
			// Shutdown has given up and Close has not been called yet.
			// It is not answered, and Close ends its connection.
			w.mu.Unlock()
			return
		}
		w.handlers.Add(1)
		w.mu.Unlock()
		defer w.handlers.Done()
		handler.ServeHTTP(rw, r)
	})
}

// Failed receives an error if the server ends by itself.
func (w *webServer) Failed() <-chan error { return w.failed }

// Stop ends the server: the listener is closed, the requests that are
// being answered get w.wait to end, and then the WebSockets are ended and
// waited for as long again. It may be called more than once.
func (w *webServer) Stop() {
	w.stop.Do(func() {
		ctx, done := context.WithTimeout(context.Background(), w.wait)
		defer done()
		// Shutdown closes the listener and waits for the requests that
		// are no WebSockets. Those it neither waits for nor closes, and
		// Close does not close them either: a connection that a handler
		// took over is the handler's. What ends them is the context of
		// their requests.
		w.server.Shutdown(ctx)
		w.mu.Lock()
		w.stopping = true
		w.mu.Unlock()
		w.end()
		w.server.Close()

		ended := make(chan struct{})
		go func() {
			w.handlers.Wait()
			close(ended)
		}()
		select {
		case <-ended:
		case <-time.After(w.wait):
			w.log.Warn("the web page was stopped while requests were still being answered")
		}
	})
}

// announceWeb logs where the web page is, with the host names it answers
// to besides when there are any, and warns about a page that
// was not built into the program and about an address that is not
// loopback.
func announceWeb(log *slog.Logger, addr string, bound net.Addr, hosts []string, loopback, built bool) {
	attrs := []any{"addr", bound.String(), "url", webURL(addr, bound)}
	if len(hosts) > 0 {
		attrs = append(attrs, "hosts", strings.Join(hosts, ","))
	}
	log.Info("web page", attrs...)
	if !built {
		log.Warn("the web page was not built into this program: it answers every request with a line that says so")
	}
	if !loopback {
		log.Warn("the web page listens on an address that is not loopback: it has no authentication, and whoever reaches it has a shell in every container", "addr", addr)
	}
}

// webURL returns the URL of the web page for a person on this machine:
// the host of addr, as --web-addr has it, with the port that was bound.
// For an address that is every address, the host is localhost.
func webURL(addr string, bound net.Addr) string {
	host, _, _ := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "localhost"
	}
	port := 0
	if tcp, ok := bound.(*net.TCPAddr); ok {
		port = tcp.Port
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/"
}
```

- [ ] **Step 9: Run the tests to see them pass**

Run: `nix develop .#bare --command go test ./cli/ -run 'TestWeb|TestAnnounceWeb' -v`
Expected:

```
--- PASS: TestWebURL (0.00s)
--- PASS: TestAnnounceWeb (0.00s)
--- PASS: TestWebServerStopEndsWebSockets (0.00s)
--- PASS: TestWebServerStopGivesUp (0.10s)
PASS
```

To see that the test of the sockets holds what it says, take the line `w.end()` out of `Stop` and run it again: it fails after 5 seconds with `Stop returned before the handler of the connection had ended`, because `Shutdown` and `Close` alone leave the connection open. Put the line back.

- [ ] **Step 10: Commit**

```bash
git add cli/web.go cli/web_test.go
git commit -m "CLI: the HTTP server of the web page, which ends its WebSockets when it stops"
```

- [ ] **Step 11: Write the failing tests of draal with the page**

`cli/draal_test.go`. These run the real `runDraal`, with the real handler of `web`. Its iroh endpoint is bound with `Local`, through a field of `draalConfig` that no flag sets, so the tests need no relay and take hundredths of a second. A second such field, `listenWeb`, lets `TestRunDraalWebFails` get hold of the listener of the page, to close it under the server. They pass with the page built into `web` and without it, and say which they met.

`TestRunDraalStopsWithPagesThatDoNotAnswer` is the one that holds the shutdown with the real sockets: three pages that hold the socket of the fleet and read no more, so that they answer neither a ping nor the close. draal has to stop well within the 5 seconds it gives its handlers, and without the warning that it gave up on them. It rests on `web` bounding the close of a socket (Task 2).

```go
package cli

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/fables-for-robots/great-machine/web"
	"github.com/fables-for-robots/great-machine/wire"
)

// draalForTest is the configuration of a draal whose iroh endpoint stays
// on this machine.
func draalForTest(t *testing.T, webAddr string) draalConfig {
	t.Helper()
	return draalConfig{
		data:    t.TempDir(),
		store:   wire.StoreInfo{Server: wire.ID(idB), Prefix: "nix/"},
		webAddr: webAddr,
		local:   true,
	}
}

var urlInLog = regexp.MustCompile(`msg="web page" addr=\S+ url=(\S+)`)

// TestRunDraalWeb runs draal with a web page on a free port: it answers
// HTTP, and when the context of the command is done it stops with no
// error and leaves nothing listening.
func TestRunDraalWeb(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logged buffer
	done := make(chan error, 1)
	cfg := draalForTest(t, "127.0.0.1:0")
	go func() { done <- runDraal(ctx, cfg, quietLog(&logged)) }()

	var url string
	waitFor(t, "draal to log the address of the web page", func() bool {
		select {
		case err := <-done:
			t.Fatalf("runDraal returned early: %v\n%s", err, logged.String())
		default:
		}
		m := urlInLog.FindStringSubmatch(logged.String())
		if m == nil {
			return false
		}
		url = m[1]
		return true
	})
	if !strings.HasPrefix(url, "http://127.0.0.1:") || strings.HasSuffix(url, ":0/") {
		t.Fatalf("the url in the log is %q, want that of the port that was bound", url)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	client.CloseIdleConnections()
	// The tests run with the page built and without it.
	notBuilt := strings.Contains(logged.String(), "the web page was not built into this program")
	if web.Built() {
		if resp.StatusCode != http.StatusOK || !strings.Contains(strings.ToLower(string(body)), "<!doctype html>") {
			t.Errorf("GET / is %d %q, want the page", resp.StatusCode, body)
		}
		if notBuilt {
			t.Errorf("draal warns that the page was not built, and it was:\n%s", logged.String())
		}
	} else {
		if resp.StatusCode != http.StatusNotImplemented {
			t.Errorf("GET / is %d %q, want 501 as the page was not built", resp.StatusCode, body)
		}
		if !notBuilt {
			t.Errorf("draal does not warn that the page was not built:\n%s", logged.String())
		}
	}
	if strings.Contains(logged.String(), "not loopback") {
		t.Errorf("draal warns about a loopback address:\n%s", logged.String())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runDraal returned %v, want nil", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("runDraal did not return when its context was done\n%s", logged.String())
	}
	if conn, err := net.DialTimeout("tcp", strings.TrimSuffix(strings.TrimPrefix(url, "http://"), "/"), time.Second); err == nil {
		conn.Close()
		t.Error("the web page still listens after draal has stopped")
	}
}

// TestRunDraalNoWeb: with an empty address there is no web page.
func TestRunDraalNoWeb(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logged buffer
	done := make(chan error, 1)
	cfg := draalForTest(t, "")
	go func() { done <- runDraal(ctx, cfg, quietLog(&logged)) }()
	waitFor(t, "draal to serve", func() bool { return strings.Contains(logged.String(), "msg=serving") })
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runDraal returned %v, want nil", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("runDraal did not return when its context was done")
	}
	if strings.Contains(logged.String(), "web page") {
		t.Errorf("draal without a web address logged about a web page:\n%s", logged.String())
	}
}

// TestRunDraalWebAddrTaken: an address that is taken ends the command
// with an error, and the iroh endpoint that was bound before it is closed.
func TestRunDraalWebAddrTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	// A UDP port for the iroh endpoint that the test can ask for again.
	probe, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	bind := netip.MustParseAddrPort(probe.LocalAddr().String())
	probe.Close()

	cfg := draalForTest(t, taken.Addr().String())
	cfg.bind = bind
	var logged buffer
	done := make(chan error, 1)
	go func() { done <- runDraal(context.Background(), cfg, quietLog(&logged)) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "the web page: listen tcp "+taken.Addr().String()) {
			t.Errorf("runDraal returned %v, want an error that names the web page and the address", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("runDraal did not return with an address that is taken\n%s", logged.String())
	}
	if !strings.Contains(logged.String(), "msg=serving") {
		t.Fatalf("the iroh endpoint was not bound before the web page:\n%s", logged.String())
	}
	// The endpoint is closed: its port can be bound again.
	waitFor(t, "the port of the iroh endpoint to be free", func() bool {
		again, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(bind))
		if err != nil {
			return false
		}
		again.Close()
		return true
	})
}

// startDraal runs draal in a goroutine and returns the URL of its web
// page, as its log has it, and the channel of what runDraal returns.
func startDraal(t *testing.T, ctx context.Context, cfg draalConfig, logged *buffer) (string, <-chan error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- runDraal(ctx, cfg, quietLog(logged)) }()
	var url string
	waitFor(t, "draal to log the address of the web page", func() bool {
		select {
		case err := <-done:
			t.Fatalf("runDraal returned early: %v\n%s", err, logged.String())
		default:
		}
		m := urlInLog.FindStringSubmatch(logged.String())
		if m == nil {
			return false
		}
		url = m[1]
		return true
	})
	return url, done
}

// TestDraalConfigWeb: the handler of the page is told whether the address
// is loopback, and the host names of --web-host.
func TestDraalConfigWeb(t *testing.T) {
	hosts := []string{"draal.example.com", "draal.internal"}
	got := draalConfig{webAddr: "127.0.0.1:8080", webHosts: hosts}.web(nil, nil)
	if !got.Loopback || !reflect.DeepEqual(got.Hosts, hosts) {
		t.Errorf("on loopback with host names: Loopback %v, Hosts %q", got.Loopback, got.Hosts)
	}
	got = draalConfig{webAddr: ":8080"}.web(nil, nil)
	if got.Loopback || got.Hosts != nil {
		t.Errorf("on every address without host names: Loopback %v, Hosts %q", got.Loopback, got.Hosts)
	}
}

// TestRunDraalWebHost: draal answers the page under a name of --web-host,
// however the browser writes it, logs the names at its start, and refuses
// a name it was not told.
func TestRunDraalWebHost(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := draalForTest(t, "127.0.0.1:0")
	cfg.webHosts = []string{"draal.example.com"}
	var logged buffer
	url, done := startDraal(t, ctx, cfg, &logged)
	if !strings.Contains(logged.String(), " hosts=draal.example.com") {
		t.Errorf("the log does not name the host:\n%s", logged.String())
	}

	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	status := func(host string) int {
		t.Helper()
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s as %s: %v", url, host, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode
	}
	// The tests run with the page built and without it.
	page := http.StatusNotImplemented
	if web.Built() {
		page = http.StatusOK
	}
	for _, host := range []string{"draal.example.com", "draal.example.com:8443", "Draal.Example.COM.", "localhost"} {
		if got := status(host); got != page {
			t.Errorf("asked as %s: %d, want %d", host, got, page)
		}
	}
	for _, host := range []string{"other.example.com", "example.com", "draal.example.com.evil.example"} {
		if got := status(host); got != http.StatusForbidden {
			t.Errorf("asked as %s, a name draal was not told: %d, want 403", host, got)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runDraal returned %v, want nil", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("runDraal did not return when its context was done")
	}
}

// TestRunDraalStopsWithPagesThatDoNotAnswer: pages that hold a socket and
// read no more, as a laptop asleep or a tab frozen in the background,
// neither answer a ping nor the close of their socket. draal stops all
// the same, well within the time it gives the handlers, and with every
// handler ended: it does not close its endpoint and its database under
// one that still runs.
func TestRunDraalStopsWithPagesThatDoNotAnswer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logged buffer
	url, done := startDraal(t, ctx, draalForTest(t, "127.0.0.1:0"), &logged)

	for range 3 {
		dialing, stop := context.WithTimeout(context.Background(), 5*time.Second)
		conn, _, err := websocket.Dial(dialing, "ws"+strings.TrimPrefix(url, "http")+"api/watch", nil)
		if err != nil {
			stop()
			t.Fatalf("the socket of the fleet: %v", err)
		}
		defer conn.CloseNow()
		// The first fleet: the handler runs. From here on the page reads
		// nothing, and so answers nothing.
		_, _, err = conn.Read(dialing)
		stop()
		if err != nil {
			t.Fatalf("the first fleet: %v", err)
		}
	}

	begun := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runDraal returned %v, want nil", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("runDraal did not return when its context was done\n%s", logged.String())
	}
	if took := time.Since(begun); took > webStopWait-time.Second {
		t.Errorf("draal took %s to stop, and gives its handlers %s", took, webStopWait)
	}
	if strings.Contains(logged.String(), "still being answered") {
		t.Errorf("draal gave up on handlers that still ran:\n%s", logged.String())
	}
}

// TestRunDraalWebFails: when the server of the page ends by itself, draal
// ends with its error, and closes its iroh endpoint.
func TestRunDraalWebFails(t *testing.T) {
	probe, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	bind := netip.MustParseAddrPort(probe.LocalAddr().String())
	probe.Close()

	cfg := draalForTest(t, "127.0.0.1:0")
	cfg.bind = bind
	bound := make(chan net.Listener, 1)
	cfg.listenWeb = func(network, addr string) (net.Listener, error) {
		l, err := net.Listen(network, addr)
		if err == nil {
			bound <- l
		}
		return l, err
	}
	var logged buffer
	_, done := startDraal(t, context.Background(), cfg, &logged)

	// The listener is closed under the server, which is how its Accept
	// fails for good.
	(<-bound).Close()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "the web page: ") || !errors.Is(err, net.ErrClosed) {
			t.Errorf("runDraal returned %v, want the error of the web page", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("runDraal did not return when the server of the page had failed\n%s", logged.String())
	}
	if strings.Contains(logged.String(), "msg=stopping") {
		t.Errorf("draal logged an end that was asked for:\n%s", logged.String())
	}
	waitFor(t, "the port of the iroh endpoint to be free", func() bool {
		again, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(bind))
		if err != nil {
			return false
		}
		again.Close()
		return true
	})
}
```

- [ ] **Step 12: Run the tests to see them fail**

Run: `nix develop .#bare --command go test ./cli/ -run 'TestRunDraal|TestDraalConfigWeb'`
Expected: FAIL, the package does not compile: `unknown field local in struct literal of type draalConfig`, `draalConfig{…}.web undefined (type draalConfig has no field or method web)` and `cfg.listenWeb undefined (type draalConfig has no field or method listenWeb)`

- [ ] **Step 13: draal serves the page**

In `cli/draal.go`, the imports get the package `web`:

```go
import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/web"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)
```

`draalConfig` gets the two fields of the tests:

```go
// draalConfig is what the command line of draal says.
type draalConfig struct {
	data      string
	operators []wire.ID
	store     wire.StoreInfo
	webAddr   string         // "" for no web page
	webHosts  []string       // the names the web page answers to besides, in lower case
	bind      netip.AddrPort // the zero value: every address, a port the system picks
	// local is for tests: the iroh endpoint uses no relay, announces
	// nothing and is bound to 127.0.0.1 unless bind says otherwise. No
	// flag sets it.
	local bool
	// listenWeb is for tests: what binds the address of the web page.
	// Nil means net.Listen. No flag sets it.
	listenWeb func(network, addr string) (net.Listener, error)
}
```

and `runDraal` becomes, with the method that makes the configuration of the handler before it:

```go
// web returns the configuration of the handler of the web page, which
// answers from c.
func (cfg draalConfig) web(c draal.Controller, log *slog.Logger) web.Config {
	return web.Config{Controller: c, Loopback: web.IsLoopback(cfg.webAddr), Hosts: cfg.webHosts, Log: log}
}

// runDraal is draal until ctx is done, which SIGINT and SIGTERM do. That
// is how draal is meant to end, and no error.
func runDraal(ctx context.Context, cfg draalConfig, log *slog.Logger) (err error) {
	if err := os.MkdirAll(cfg.data, 0o700); err != nil {
		return fmt.Errorf("the data directory: %w", err)
	}
	key, err := node.LoadOrCreateKey(filepath.Join(cfg.data, draalKeyFile))
	if err != nil {
		return fmt.Errorf("the key of draal: %w", err)
	}
	database, err := db.Open(filepath.Join(cfg.data, draalDBFile))
	if err != nil {
		return fmt.Errorf("the database: %w", err)
	}
	// Deferred first, so that it happens last: after the listener is
	// closed and Serve, which writes to the database, has returned.
	defer func() { err = errors.Join(err, database.Close()) }()

	if len(cfg.operators) == 0 {
		log.Warn("no --operator was given: nobody can act as an operator over iroh")
	}
	listener, err := node.Listen(ctx, node.ListenConfig{Key: key, Bind: cfg.bind, Local: cfg.local, Log: log})
	if err != nil {
		return fmt.Errorf("binding the iroh endpoint: %w", err)
	}
	defer func() { err = errors.Join(err, listener.Close()) }()
	log.Info("serving", "endpoint", string(listener.ID()), "addr", listener.LocalAddr().String(), "operators", len(cfg.operators))

	// The web page listens before draal serves: an address that is taken
	// ends the command here, with the endpoint and the database closed
	// by what is deferred above.
	var pageListener net.Listener
	if cfg.webAddr != "" {
		listen := cfg.listenWeb
		if listen == nil {
			listen = net.Listen
		}
		if pageListener, err = listen("tcp", cfg.webAddr); err != nil {
			return fmt.Errorf("the web page: %w", err)
		}
	}

	d := draal.New(draal.Config{
		DB:    database,
		Authz: authz.NewAllowlist(cfg.operators),
		Store: cfg.store,
		Log:   log,
	})

	// The order of the end, once ctx is done: the web page is stopped
	// first, its WebSockets with it, and only then does Serve get to
	// know, through a context of its own. The endpoint and the database
	// are closed after Serve has returned, as before.
	serving, stopServing := context.WithCancel(context.WithoutCancel(ctx))
	defer stopServing()
	pageFailed := make(chan error, 1)
	if pageListener == nil {
		defer context.AfterFunc(ctx, stopServing)()
	} else {
		handler := cfg.web(d, log)
		page := startWeb(pageListener, web.New(handler), webStopWait, log)
		// For the ways out of this function that are not the end of ctx.
		defer page.Stop()
		announceWeb(log, cfg.webAddr, pageListener.Addr(), cfg.webHosts, handler.Loopback, web.Built())
		go func() {
			select {
			case <-ctx.Done():
			case err := <-page.Failed():
				pageFailed <- err
			case <-serving.Done():
				return
			}
			page.Stop()
			stopServing()
		}()
	}

	err = d.Serve(serving, listener)
	select {
	case err := <-pageFailed:
		return err
	default:
	}
	if err != nil && ctx.Err() == nil {
		return err
	}
	log.Info("stopping")
	return nil
}
```

- [ ] **Step 14: Run the tests to see them pass**

Run: `nix develop .#bare --command go test ./cli/ -run 'TestRunDraal|TestDraalConfigWeb' -v`
Expected:

```
--- PASS: TestRunDraalWeb (0.02s)
--- PASS: TestRunDraalNoWeb (0.01s)
--- PASS: TestRunDraalWebAddrTaken (0.01s)
--- PASS: TestDraalConfigWeb (0.00s)
--- PASS: TestRunDraalWebHost (0.02s)
--- PASS: TestRunDraalStopsWithPagesThatDoNotAnswer (…s)
--- PASS: TestRunDraalWebFails (0.01s)
PASS
```

`TestRunDraalStopsWithPagesThatDoNotAnswer` takes as long as `web` gives a page to take the close of its socket, a second or so. If it takes 5 seconds and fails with `draal gave up on handlers that still ran`, the handlers of `web` wait for a page to answer the close: that is `web`'s to mend (Task 2), not this task's.

Run: `nix develop .#bare --command sh -c 'test -z "$(gofmt -l cli)" && go vet ./cli/ && go test -race -count=3 ./cli/ && go test ./...'`
Expected: `ok` for every package.

What these tests do not hold: that `Serve` learns of the end only after the page was stopped. The order is in `runDraal`, and nothing outside it can see it.

- [ ] **Step 15: Commit**

```bash
git add cli/
git commit -m "CLI: draal serves the web page beside its iroh endpoint, and stops it first"
```

- [ ] **Step 16: A script that asks a program for the page**

`scripts/test-page.sh`:

```sh
#!/bin/sh
# Tests that a program has the web page built into it, by starting its
# draal and asking it for the page as a browser would. A program without
# the page answers 501, which fails the test. It needs curl for a program,
# and docker for an image, and prints "ok" at the end.
#
#   scripts/test-page.sh ./result/bin/great-machine
#   scripts/test-page.sh --image ghcr.io/fables-for-robots/great-machine:nix
#
# The draal it starts has an endpoint ID of its own and no operator, and
# dials iroh's relays as every draal does.
set -eu

# A jaccard-store server that is never dialed: draal only hands it on.
server=0000000000000000000000000000000000000000000000000000000000000000

if [ "${1:-}" = --image ]; then
	image=${2:?usage: test-page.sh --image IMAGE}
	program=
else
	program=${1:?usage: test-page.sh PROGRAM, or test-page.sh --image IMAGE}
	image=
fi

name="great-machine-page-$$"
tmp=$(mktemp -d)
pid=
cleanup() {
	if [ -n "$image" ]; then
		docker rm -f "$name" >/dev/null 2>&1 || true
	elif [ -n "$pid" ]; then
		kill "$pid" 2>/dev/null || true
		wait "$pid" 2>/dev/null || true
	fi
	rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 1' INT TERM HUP

# log: what draal has written so far.
log() {
	if [ -n "$image" ]; then
		docker logs "$name" 2>&1
	else
		cat "$tmp/log"
	fi
}

fail() {
	echo "test-page: FAILED: $1" >&2
	echo "--- what draal wrote:" >&2
	log >&2 || true
	exit 1
}

# get URL: the body of the answer, and failure for a status that is not 2xx.
get() {
	if [ -n "$image" ]; then
		docker exec "$name" wget -q -O - "$1"
	else
		curl -fsS "$1"
	fi
}

if [ -n "$image" ]; then
	# As in a pod: another store over /nix/store, and the page on the
	# loopback address of the container, where only the container reaches
	# it. That is why it is asked from inside, with busybox's wget.
	docker run -d --name "$name" --tmpfs /nix/store "$image" \
		draal --data /tmp/draal --jaccard-server "$server" --jaccard-prefix nix/ >/dev/null
else
	# Port 0 is a port the system picks; draal logs the one it got.
	"$program" draal --data "$tmp/data" --jaccard-server "$server" --jaccard-prefix nix/ \
		--web-addr 127.0.0.1:0 2>"$tmp/log" &
	pid=$!
fi

# draal binds its iroh endpoint first, which waits for a relay, 15 seconds
# at most; 600 steps of 0.1 second leave room above that.
i=0
until log | grep -q 'msg="web page"'; do
	i=$((i + 1))
	[ "$i" -lt 600 ] || fail "draal did not log its web page"
	sleep 0.1
done
url=$(log | sed -n 's/.*msg="web page".* url=\([^ ]*\).*/\1/p' | head -n 1)
[ -n "$url" ] || fail "no url in the line of the web page"

if log | grep -q 'the web page was not built into this program'; then
	fail "draal says that the page was not built into it"
fi
page=$(get "$url") || fail "GET $url failed"
echo "$page" | grep -qi '<!doctype html>' || fail "GET $url is no page: $page"
# A path that is no file is the page too: its routes are the browser's.
page=$(get "${url}s/web") || fail "GET ${url}s/web failed"
echo "$page" | grep -qi '<!doctype html>' || fail "GET ${url}s/web is no page: $page"

echo ok
```

Run: `chmod +x scripts/test-page.sh`

- [ ] **Step 17: The program that Nix builds has the page**

This is the check that closes item 2 of the specification's section 15 from end to end: the page built by one derivation, put into the source of another, embedded by gonixgo's build of the package `web`, and served by the program.

```bash
git add scripts/test-page.sh
nix build --option allow-unsafe-native-code-during-evaluation true
scripts/test-page.sh ./result/bin/great-machine
rm result
```

Expected: `ok`, within a few seconds (draal waits for a relay first, 15 seconds at most).

And a program without the page is told apart. With `web/dist` holding the placeholder alone, as Task 4 left it:

```bash
tmp=$(mktemp -d)
nix develop .#bare --command go build -o "$tmp/great-machine" ./cmd/great-machine
scripts/test-page.sh "$tmp/great-machine"; echo "status $?"
rm -r "$tmp"
```

Expected: `test-page: FAILED: draal says that the page was not built into it` (or `GET http://127.0.0.1:…/ failed`, when the script asked before the warning was logged), what draal wrote, with `level=WARN msg="the web page was not built into this program: …"` in it, and `status 1`.

- [ ] **Step 18: CI asks the program for the page**

In `.github/workflows/test.yml`, the step `nix build` becomes:

```yaml
      - name: nix build
        run: |
          nix build --option allow-unsafe-native-code-during-evaluation true
          ./result/bin/great-machine version
          # The program that Nix built has the page: its draal is asked for it.
          scripts/test-page.sh ./result/bin/great-machine
```

- [ ] **Step 19: Commit**

```bash
git add scripts/test-page.sh .github/workflows/test.yml
git commit -m "CI: the program that Nix built is asked for the page"
```
