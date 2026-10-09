### Task 1: Scaffold

The first code of the repository, which holds only `docs/` so far: a Go
module with the program and its `version` command, the Nix build, the dev
shells, and CI.

**Files:**
- Create: `flake.nix`, `flake.lock` (Nix writes it), `.envrc`, `.gitignore`
- Create: `go.mod`, `go.sum` (`go mod tidy` writes it)
- Create: `cmd/great-machine/main.go`
- Create: `cli/app.go`, `cli/version.go`
- Create: `.github/workflows/test.yml`
- Test: `cli/app_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `cli.Version` (`var Version = "dev"`), set by the linker with
    `-X github.com/fables-for-robots/great-machine/cli.Version=<version>`.
  - `func Run(args []string, stdout, stderr io.Writer) int`, as the
    contract has it. It returns 0; 1 for a failure, after writing
    `great-machine: <error>` and a line feed to `stderr`; 2 for a command
    line that was not valid, after writing the same line. Without a
    command, and with `--help`, it writes the help to `stdout` and
    returns 0.
  - In package `cli`, for the tasks that add commands:
    - `func commands() []*cli.Command` in `cli/app.go` returns every
      command of the program. Here it returns
      `[]*cli.Command{versionCommand()}`. A later task replaces its body.
    - `func versionCommand() *cli.Command` in `cli/version.go`. A command
      is a function like this one, in a file of its own.
    - `func usageError(format string, args ...any) error`: an error that
      makes `Run` return 2 instead of 1. The format is `fmt.Errorf`'s, `%w`
      included. A command's `Action` returns it for a missing argument,
      one too many, a flag that is required and not set, or a flag whose
      value cannot be. `Run` finds it with `errors.As`, so it may be
      wrapped. **Do not set `Required: true` on a flag**: the library then
      prints the command's whole help to `stdout` and `Run` returns 1.
      Check in the `Action` and return `usageError("--draal is required")`.
    - A flag that is not defined, and a flag whose value does not parse
      (`--parallel many`), already make `Run` return 2 for every command
      in `commands()`, and for their subcommands. A command sets no
      `OnUsageError` of its own.
    - A command writes its output to `c.App.Writer` and, where it has to,
      to `c.App.ErrWriter`: they are the `stdout` and `stderr` of `Run`. It
      does not print its own error; it returns it.
    - `c.Context` of a command is done at the first SIGINT or SIGTERM. A
      second signal ends the process, as the system does it.
    - The library stops reading flags at the first argument that is not
      one: `rollout --detach web PATH`, not `rollout web PATH --detach`.
      A `--` before the first argument is dropped by the library; one
      that comes after an argument stays in `c.Args()`, so a command that
      takes arguments behind `--` (`rollout`) cuts there itself.
    - `func newApp(stdout, stderr io.Writer) *cli.App` and
      `func run(ctx context.Context, app *cli.App, args []string) int`: a
      test makes an app, may add commands of its own to `app.Commands`,
      and runs it without signals.
  - `flake.nix`: `packages.default` (the program, by gonixgo, without
    cgo), `devShells.default` (`go`, `sqlc`, the built program) and
    `devShells.bare` (`go`, `sqlc`).
  - `.envrc`, which watches every top-level directory that has a Go file
    git tracks. **A new package needs no line in it.**
  - `.github/workflows/test.yml`: one job, `test`, a flat list of steps
    named `gofmt`, `vet`, `test`, `nix build`. Every `run` step of the job
    runs in the flake's `bare` shell, so `go` and `sqlc` are there without
    any setup. A later task adds its step between `test` and `nix build`.

Until Step 8, the Go commands of this task are run in the bare shell,
which Step 1 makes: `nix develop .#bare`. The default shell cannot be
entered before the module builds.

- [ ] **Step 1: The flake, and what goes with it**

`flake.nix`:

```nix
{
  description = "great-machine";
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";

    systems.url = "github:nix-systems/default";

    gonixgo = {
      url = "github:draganm/gonixgo/v0.2.0";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.systems.follows = "systems";
    };

  };

  outputs = { self, nixpkgs, systems, gonixgo, ... }@inputs:
    let
      eachSystem = f:
        nixpkgs.lib.genAttrs (import systems)
        (system: f system nixpkgs.legacyPackages.${system});

      # What `great-machine version` prints: the commit the program was
      # built from, with -dirty when the tree had changes.
      version = self.shortRev or self.dirtyShortRev or "dev";
    in {

      # Evaluating a gonixgo package runs `gonixgo resolve` through
      # builtins.exec, so every command that touches `packages` needs
      #   --option allow-unsafe-native-code-during-evaluation true
      packages = eachSystem (system: pkgs:
        let
          # This pkgs builds gonixgo's own tool and the Go program.
          goEnv = gonixgo.lib.mkGoEnv { inherit pkgs; };
        in {
          # The one program: draal, zathras and the operator's commands.
          # Without cgo it is linked statically on Linux, and runs in an
          # image that has no libc.
          default = goEnv.buildGoApplication {
            pname = "great-machine";
            src = ./.;
            subPackages = [ "cmd/great-machine" ];
            CGO_ENABLED = 0;
            ldflags = [
              "-X github.com/fables-for-robots/great-machine/cli.Version=${version}"
            ];
          };
        });

      # The shell carries the built program, so entering it (or direnv
      # reloading it after a source change, see .envrc) rebuilds it. It
      # therefore needs the same evaluation option as `packages`.
      devShells = eachSystem (system: pkgs: {
        default = pkgs.mkShell {
          hardeningDisable = [ "all" ];

          packages = with pkgs; [ go sqlc self.packages.${system}.default ];
        };

        # The same tools without the built program. The default shell
        # cannot be entered while the module does not resolve, for example
        # after a `go get` that left go.sum behind; this one can, to run
        # `go mod tidy`. It needs no evaluation option.
        bare = pkgs.mkShell {
          hardeningDisable = [ "all" ];

          packages = with pkgs; [ go sqlc ];
        };
      });
    };
}
```

`.envrc`:

```bash
#!/usr/bin/env bash

source_up_if_exists

# The dev shell contains great-machine built by gonixgo, so reload it (and
# rebuild the program) whenever the Go sources change. Every top-level
# directory that holds a Go file git tracks is watched, whole: a new
# package needs no line here, and watch_dir covers files added later too.
# Nix only sees files that git tracks, so `git add` new ones.
watch_file go.mod go.sum
while IFS= read -r dir; do
  watch_dir "$dir"
done < <(git ls-files '*.go' | sed -n 's|/.*||p' | sort -u)

# Evaluating a gonixgo package runs `gonixgo resolve` through builtins.exec.
use flake . --option allow-unsafe-native-code-during-evaluation true
```

`.gitignore`:

```gitignore
# nix build
/result
/result-*

# direnv
/.direnv/

# go build and go test -c
/great-machine
*.test
*.out

# the database of a draal that was run in the tree
*.sqlite
*.sqlite-wal
*.sqlite-shm

.DS_Store
```

- [ ] **Step 2: Enter the bare shell**

Nix sees only the files git tracks, so the flake is added first.

Run:

```bash
git add flake.nix .envrc .gitignore
nix develop .#bare -c go version
nix develop .#bare -c sqlc version
git add flake.lock
```

Expected: Nix writes `flake.lock`, and the two commands print
`go version go1.26.7 darwin/arm64` (the patch version is whatever
`nixos-26.05` has, 1.26.3 or later, and the platform is the machine's) and
`v1.31.1` (or later).

- [ ] **Step 3: Write the failing test**

`go.mod`:

```
module github.com/fables-for-robots/great-machine

go 1.26.3

require github.com/urfave/cli/v2 v2.27.7
```

`cli/app_test.go`:

```go
package cli

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/urfave/cli/v2"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := Run([]string{"great-machine", "version"}, &stdout, &stderr); got != 0 {
		t.Fatalf("Run returned %d, want 0; stderr: %s", got, stderr.String())
	}
	if got, want := stdout.String(), Version+"\n"; got != want {
		t.Errorf("stdout is %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr is %q, want nothing", stderr.String())
	}
}

// TestRunInvalid holds Run to 2, and to one line on stderr, for every
// command line that is not valid.
func TestRunInvalid(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		stderr string
	}{
		{"unknown command", []string{"great-machine", "vorlon"},
			"great-machine: unknown command \"vorlon\"\n"},
		{"unknown flag of the program", []string{"great-machine", "--vorlon"},
			"great-machine: flag provided but not defined: -vorlon\n"},
		{"unknown flag of a command", []string{"great-machine", "version", "--vorlon"},
			"great-machine: flag provided but not defined: -vorlon\n"},
		{"an argument too many", []string{"great-machine", "version", "now"},
			"great-machine: version takes no argument, got \"now\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := Run(tt.args, &stdout, &stderr); got != 2 {
				t.Errorf("Run returned %d, want 2", got)
			}
			if got := stderr.String(); got != tt.stderr {
				t.Errorf("stderr is %q, want %q", got, tt.stderr)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout is %q, want nothing", stdout.String())
			}
		})
	}
}

// TestRunErrors runs commands of the test's own: what a command returns
// decides between 1 and 2.
func TestRunErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		want   int
		stderr string
	}{
		{"a failure", errors.New("the machine is asleep"), 1,
			"great-machine: the machine is asleep\n"},
		{"a usage error", usageError("want one argument %s, got %d", "SERVICE", 0), 2,
			"great-machine: want one argument SERVICE, got 0\n"},
		{"a usage error inside another error", errors.Join(errors.New("first"), usageError("second")), 2,
			"great-machine: first\nsecond\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			app := newApp(&stdout, &stderr)
			app.Commands = append(app.Commands, &cli.Command{
				Name:   "fail",
				Action: func(*cli.Context) error { return tt.err },
			})
			if got := run(context.Background(), app, []string{"great-machine", "fail"}); got != tt.want {
				t.Errorf("run returned %d, want %d", got, tt.want)
			}
			if got := stderr.String(); got != tt.stderr {
				t.Errorf("stderr is %q, want %q", got, tt.stderr)
			}
		})
	}
}

// TestRunBadFlagValue: a flag whose value cannot be read is a command
// line that is not valid, for a command that was added after newApp too.
func TestRunBadFlagValue(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := newApp(&stdout, &stderr)
	app.Commands = append(app.Commands, &cli.Command{
		Name:   "count",
		Flags:  []cli.Flag{&cli.IntFlag{Name: "n"}},
		Action: func(*cli.Context) error { return nil },
	})
	if got := run(context.Background(), app, []string{"great-machine", "count", "--n", "many"}); got != 2 {
		t.Errorf("run returned %d, want 2; stderr: %s", got, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout is %q, want nothing", stdout.String())
	}
}

func TestRunHelp(t *testing.T) {
	for _, args := range [][]string{{"great-machine"}, {"great-machine", "--help"}} {
		var stdout, stderr bytes.Buffer
		if got := Run(args, &stdout, &stderr); got != 0 {
			t.Errorf("Run(%q) returned %d, want 0", args, got)
		}
		if !bytes.Contains(stdout.Bytes(), []byte("version")) {
			t.Errorf("Run(%q): the help does not name the version command:\n%s", args, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("Run(%q): stderr is %q, want nothing", args, stderr.String())
		}
	}
}
```

- [ ] **Step 4: Run the test to see it fail**

Run, in the bare shell:

```bash
go mod tidy
go test ./cli/
```

Expected: `go mod tidy` downloads urfave/cli and writes `go.sum`. The test
does not build:

```
# github.com/fables-for-robots/great-machine/cli [github.com/fables-for-robots/great-machine/cli.test]
cli/app_test.go:14:12: undefined: Run
cli/app_test.go:17:35: undefined: Version
cli/app_test.go:45:14: undefined: Run
cli/app_test.go:69:21: undefined: usageError
cli/app_test.go:71:75: undefined: usageError
cli/app_test.go:77:11: undefined: newApp
cli/app_test.go:82:14: undefined: run
cli/app_test.go:96:9: undefined: newApp
cli/app_test.go:102:12: undefined: run
cli/app_test.go:113:13: undefined: Run
cli/app_test.go:113:13: too many errors
FAIL	github.com/fables-for-robots/great-machine/cli [build failed]
```

- [ ] **Step 5: Write the implementation**

`cli/app.go`:

```go
// Package cli is the command line of great-machine: one command for
// draal, one for zathras, and the operator's.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/urfave/cli/v2"
)

// Version is the release this program was built as. The linker sets it:
// -X github.com/fables-for-robots/great-machine/cli.Version=v0.1.0
var Version = "dev"

// Run runs the command line and returns the exit status: 0, 1 for a
// failure, 2 for a command line that was not valid. A failure is written
// to stderr as "great-machine: <error>".
//
// The context of a command ends with the first SIGINT or SIGTERM. From
// then on signals are the system's again, so a second one ends the
// process at once.
func Run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()
	return run(ctx, newApp(stdout, stderr), args)
}

// commands returns every command of the program.
func commands() []*cli.Command {
	return []*cli.Command{versionCommand()}
}

// newApp returns the program with its commands, writing to stdout and
// stderr.
func newApp(stdout, stderr io.Writer) *cli.App {
	return &cli.App{
		Name:            "great-machine",
		Usage:           "roll a program built with Nix out to a group of pods",
		Writer:          stdout,
		ErrWriter:       stderr,
		HideHelpCommand: true,
		Commands:        commands(),
	}
}

// usage is an error that says the command line was not valid.
type usage struct{ err error }

func (u *usage) Error() string { return u.err.Error() }
func (u *usage) Unwrap() error { return u.err }

// usageError returns an error that makes Run return 2 instead of 1: the
// command line was not valid. A command returns it from its Action for a
// missing argument, one too many, a flag that is required and not set, or
// a flag whose value cannot be. The format is that of fmt.Errorf, %w
// included.
func usageError(format string, args ...any) error {
	return &usage{err: fmt.Errorf(format, args...)}
}

// run runs app and turns what it returns into an exit status. It is
// apart from Run so that a test can run an app with commands of its own.
func run(ctx context.Context, app *cli.App, args []string) int {
	// The library would print a flag it does not know and the whole help
	// by itself, and leave the process by itself for some errors. Both
	// are this function's to do.
	onUsage := func(_ *cli.Context, err error, _ bool) error { return usageError("%w", err) }
	app.OnUsageError = onUsage
	setOnUsage(app.Commands, onUsage)
	app.ExitErrHandler = func(*cli.Context, error) {}
	// Without a command the help is shown; a word that is no command is
	// not a reason to show it.
	app.Action = func(c *cli.Context) error {
		if c.Args().Present() {
			return usageError("unknown command %q", c.Args().First())
		}
		return cli.ShowAppHelp(c)
	}
	err := app.RunContext(ctx, args)
	if err == nil {
		return 0
	}
	fmt.Fprintln(app.ErrWriter, "great-machine:", err)
	var u *usage
	if errors.As(err, &u) {
		return 2
	}
	return 1
}

// setOnUsage makes every command, and the commands inside it, report a
// flag that could not be read through onUsage.
func setOnUsage(cmds []*cli.Command, onUsage cli.OnUsageErrorFunc) {
	for _, cmd := range cmds {
		cmd.OnUsageError = onUsage
		setOnUsage(cmd.Subcommands, onUsage)
	}
}
```

`cli/version.go`:

```go
package cli

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

// versionCommand is `great-machine version`.
func versionCommand() *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "print the version of this program",
		Action: func(c *cli.Context) error {
			if c.Args().Present() {
				return usageError("version takes no argument, got %q", c.Args().First())
			}
			_, err := fmt.Fprintln(c.App.Writer, Version)
			return err
		},
	}
}
```

`cmd/great-machine/main.go`:

```go
// Command great-machine is draal, zathras and the operator's commands.
package main

import (
	"os"

	"github.com/fables-for-robots/great-machine/cli"
)

func main() {
	os.Exit(cli.Run(os.Args, os.Stdout, os.Stderr))
}
```

- [ ] **Step 6: Run the tests to see them pass**

Run, in the bare shell:

```bash
go mod tidy
gofmt -l .
go vet ./...
go test ./...
```

Expected: `gofmt -l .` and `go vet ./...` print nothing, and

```
ok  	github.com/fables-for-robots/great-machine/cli	0.3s
?   	github.com/fables-for-robots/great-machine/cmd/great-machine	[no test files]
```

`go.mod` is now:

```
module github.com/fables-for-robots/great-machine

go 1.26.3

require github.com/urfave/cli/v2 v2.27.7

require (
	github.com/cpuguy83/go-md2man/v2 v2.0.7 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/xrash/smetrics v0.0.0-20240521201337-686a1a2994c1 // indirect
)
```

- [ ] **Step 7: Build with Nix**

Nix builds what git tracks, so everything is added first. The build is
gonixgo's: one derivation for each Go package.

Run:

```bash
git add go.mod go.sum cli cmd
nix build --option allow-unsafe-native-code-during-evaluation true
./result/bin/great-machine version
./result/bin/great-machine vorlon; echo "status $?"
rm result
```

Expected: the build ends without an error (a warning that the Git tree is
dirty is in order), and then

```
de2527b-dirty
great-machine: unknown command "vorlon"
status 2
```

where `de2527b` is whatever `git rev-parse --short HEAD` prints. The
`-dirty` is there because this task is not committed yet. `rm result`
removes the link to what was built: nothing that was built stays in the
repository.

Without the option the build fails with a message of gonixgo's that
names `allow-unsafe-native-code-during-evaluation` and the three ways to
set it: that is the option missing, not the flake broken.

- [ ] **Step 8: Enter the default shell**

Run:

```bash
nix develop --option allow-unsafe-native-code-during-evaluation true -c great-machine version
```

Expected: the same `de2527b-dirty`. The default shell has the built
program on its `PATH`. From here on it is the shell the commands of the
plan are run in; with direnv, `direnv allow` enters it by `.envrc`.

- [ ] **Step 9: CI**

`.github/workflows/test.yml`:

```yaml
name: Test

on: [push, pull_request]

permissions:
  contents: read

jobs:
  test:
    runs-on: ubuntu-latest
    # Every `run` step runs in the flake's bare shell, which has the Go
    # and the sqlc that the dev shell has. A step added here needs no
    # setup of its own for them.
    defaults:
      run:
        shell: nix develop .#bare --command bash -e {0}
    steps:
      - uses: actions/checkout@v4
      - uses: cachix/install-nix-action@v31
      - uses: actions/cache@v4
        with:
          path: |
            ~/go/pkg/mod
            ~/.cache/go-build
          key: go-${{ runner.os }}-${{ hashFiles('go.sum', 'flake.lock') }}
          restore-keys: go-${{ runner.os }}-
      - name: gofmt
        run: test -z "$(gofmt -l .)"
      - name: vet
        run: go vet ./...
      - name: test
        run: go test ./...
      # Steps that test more are added above this line. The last step
      # builds the program as a release does. gonixgo runs a program
      # while Nix evaluates, which Nix allows only when asked.
      - name: nix build
        run: |
          nix build --option allow-unsafe-native-code-during-evaluation true
          ./result/bin/great-machine version
```

Run:

```bash
nix run nixpkgs#actionlint -- .github/workflows/test.yml
```

Expected: no output, and an exit status of 0.

- [ ] **Step 10: Commit**

```bash
git add flake.nix flake.lock .envrc .gitignore go.mod go.sum cli cmd .github
git status --short
git commit -m "Scaffold: the module, the version command, the Nix build and CI"
```

Expected of `git status --short`, before the commit: every file of this
task with an `A`, and no line for `result`.
