### Task 10: reaper, the duties of process 1

**Files:**
- Create: `reaper/reaper.go`
- Test: `reaper/reaper_test.go`
- Create: `reaper/testdata/pid1/main.go` (the program the script runs as process 1)
- Create: `scripts/test-pid1.sh` (the test in a container; executable)
- Modify: `cmd/great-machine/main.go` (calls `reaper.Init` before anything else)
- Modify: `.github/workflows/test.yml` (a step `pid1` after the step `test`)

**Interfaces:**
- Consumes: `cli.Run`, which `main` calls already:

  ```go
  // Run runs the command line and returns the exit status: 0, 1 for a
  // failure, 2 for a command line that was not valid.
  func Run(args []string, stdout, stderr io.Writer) int
  ```

- Produces, exactly as the plan's Interfaces section has it:

  ```go
  package reaper

  // Init returns false at once in a process that is not process 1. In
  // process 1 it starts the program again as its child, with the same
  // arguments, environment and standard streams, passes SIGTERM, SIGINT and
  // SIGHUP on to it, waits for every child there is, and returns true and
  // the exit status of that child when it has ended.
  func Init() (status int, was bool)
  ```

  The status of a child that a signal ended is 128 plus the number of the signal. When the child cannot be started, `Init` writes why to standard error and returns `1, true`.

This is Spec section 7.2. Process 1 of a container has two duties that no other process has: the kernel hands it every orphan, which stays a zombie until process 1 waits for it, and a signal it has no handler for is not delivered to it at all. Waiting for every child (`wait4(-1)`) in the process that also runs the service with `os/exec` would take the service's exit status away from `os/exec`. So the program does not try to be both: as process 1 it starts itself again as a child, and the parent only passes signals on and waits. The child is an ordinary process, which is why `supervisor` and `ptyshell` use `os/exec` without a thought for any of this.

The package builds on Linux and macOS and needs no build tag. Outside process 1 `Init` does nothing, so the unit test is one line; what `Init` does as process 1 is tested in a container by `scripts/test-pid1.sh`, which needs `go` and a Docker that runs.

- [ ] **Step 1: Write the failing test**

Create `reaper/reaper_test.go`:

```go
package reaper

import (
	"os"
	"testing"
)

// What Init does as process 1 is tested in a container, by
// scripts/test-pid1.sh with the program in testdata/pid1.
func TestInitDoesNothingInAProcessThatIsNotTheFirst(t *testing.T) {
	if os.Getpid() == 1 {
		t.Skip("this test is process 1")
	}
	status, was := Init()
	if status != 0 || was {
		t.Errorf("Init() = %d, %v; want 0, false", status, was)
	}
}
```

- [ ] **Step 2: Run the test to see it fail**

Run: `go test ./reaper/`
Expected: FAIL — the package does not build: `undefined: Init`

- [ ] **Step 3: Write the implementation**

Create `reaper/reaper.go`:

```go
// Package reaper does what the first process of a container has to do,
// so that the rest of the program need not: it runs the program again as
// a child, and itself only passes signals on and waits for every child
// there is, the orphans the kernel hands to process 1 among them.
package reaper

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Init returns false at once in a process that is not process 1. In
// process 1 it starts the program again as its child, with the same
// arguments, environment and standard streams, passes SIGTERM, SIGINT and
// SIGHUP on to it, waits for every child there is, and returns true and
// the exit status of that child when it has ended.
//
// The status of a child that a signal ended is 128 plus the number of the
// signal, as a shell has it.
func Init() (status int, was bool) {
	if os.Getpid() != 1 {
		return 0, false
	}
	return run(), true
}

// run is Init in process 1.
func run() int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "reaper: finding the program:", err)
		return 1
	}

	// The signals are asked for before the child is started, so that
	// the SIGCHLD of a child that ends at once is not missed.
	signals := make(chan os.Signal, 64)
	signal.Notify(signals, syscall.SIGCHLD, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	// Not os/exec, whose Wait would wait for this one child: the loop
	// below is what waits, for this child and every other.
	child, err := syscall.ForkExec(exe, os.Args, &syscall.ProcAttr{
		Env:   os.Environ(),
		Files: []uintptr{0, 1, 2},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "reaper: starting %s: %v\n", exe, err)
		return 1
	}

	for sig := range signals {
		if sig != syscall.SIGCHLD {
			syscall.Kill(child, sig.(syscall.Signal))
			continue
		}
		// One SIGCHLD may stand for several children: take every one
		// that has ended.
		for {
			var ws syscall.WaitStatus
			pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
			if err == syscall.EINTR {
				continue
			}
			if err != nil || pid <= 0 {
				break
			}
			if pid != child {
				continue
			}
			switch {
			case ws.Exited():
				return ws.ExitStatus()
			case ws.Signaled():
				return 128 + int(ws.Signal())
			}
		}
	}
	return 1
}
```

- [ ] **Step 4: Run the test to see it pass**

Run: `go vet ./reaper/ && go test -race ./reaper/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add reaper/
git commit -m "Reaper: as process 1, run the program as a child and wait for every child"
```

- [ ] **Step 6: Write the test in a container**

The program that is run as process 1. It is `package main` under `testdata`, which `go build ./...`, `go vet ./...` and `go test ./...` pass over; the script builds it by its path. Create `reaper/testdata/pid1/main.go`:

```go
// Command pid1 is what scripts/test-pid1.sh runs as the first process of
// a container, to test the package reaper.
//
//	pid1 serve   makes an orphan, then waits for SIGTERM and exits with 0
//	pid1 exit N  exits with N
package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/fables-for-robots/great-machine/reaper"
)

func main() {
	if status, was := reaper.Init(); was {
		os.Exit(status)
	}
	fmt.Printf("child: pid %d, parent %d\n", os.Getpid(), os.Getppid())
	if len(os.Args) < 2 {
		fmt.Println("usage: pid1 serve | pid1 exit N")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "exit":
		status, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Println(err)
			os.Exit(2)
		}
		os.Exit(status)
	case "serve":
		term := make(chan os.Signal, 1)
		signal.Notify(term, syscall.SIGTERM)
		// The shell ends at once and leaves the sleep behind, which
		// the kernel hands to process 1.
		shell := exec.Command("/bin/sh", "-c", "sleep 2 & echo orphan: pid $!")
		shell.Stdout, shell.Stderr = os.Stdout, os.Stderr
		if err := shell.Run(); err != nil {
			fmt.Println("making the orphan:", err)
			os.Exit(2)
		}
		fmt.Println("serving")
		<-term
		fmt.Println("child: got SIGTERM")
		os.Exit(0)
	}
	os.Exit(2)
}
```

Create `scripts/test-pid1.sh`:

```sh
#!/bin/sh
# Tests the package reaper where it does something: as process 1 of a
# container. The program in reaper/testdata/pid1 is built for Linux and
# run as the first process of a busybox container, and the script checks
# that it runs itself again as a child, that an orphan leaves no zombie,
# that SIGTERM reaches the child, and that the child's exit status becomes
# the container's. It needs go and docker, and prints "ok" at the end.
#
#   scripts/test-pid1.sh
set -eu
cd "$(dirname "$0")/.."

image=busybox:1.36
name="great-machine-pid1-$$"
tmp=$(mktemp -d)
cleanup() {
	docker rm -f "$name" >/dev/null 2>&1 || true
	rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 1' INT TERM HUP

fail() {
	echo "test-pid1: FAILED: $1" >&2
	echo "--- what the container wrote:" >&2
	docker logs "$name" >&2 2>&1 || true
	exit 1
}

# wait_for_log TEXT: waits until the container has written TEXT.
wait_for_log() {
	i=0
	until docker logs "$name" 2>&1 | grep -q "$1"; do
		i=$((i + 1))
		[ "$i" -lt 100 ] || return 1
		sleep 0.1
	done
}

# states: a line for every process of the container, as /proc has it:
# pid (name) state ...
states() {
	docker exec "$name" sh -c 'for f in /proc/[0-9]*/stat; do cat "$f" 2>/dev/null || true; done'
}

# The container runs on the architecture of the Docker server, which on a
# Mac is a virtual machine.
arch=$(docker version --format '{{.Server.Arch}}') || {
	echo "test-pid1: FAILED: docker does not answer" >&2
	exit 1
}
case "$arch" in
x86_64) arch=amd64 ;;
aarch64) arch=arm64 ;;
esac
GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build -o "$tmp/pid1" ./reaper/testdata/pid1
chmod 755 "$tmp" "$tmp/pid1"

docker run -d --name "$name" -v "$tmp:/test:ro" "$image" /test/pid1 serve >/dev/null

# (a) Process 1 has started the program again, as a child.
wait_for_log '^serving$' || fail "(a) the program did not get to serve"
docker logs "$name" 2>&1 | grep -q '^child: pid [0-9]*, parent 1$' ||
	fail "(a) no line of a child whose parent is process 1"
if docker logs "$name" 2>&1 | grep -q '^child: pid 1,'; then
	fail "(a) the program runs as process 1 itself"
fi

# (b) The orphan, a sleep whose parent has ended, leaves no zombie. First
# it has to be there, as a child of process 1 (in busybox it is a shell,
# which has sleep built in); then it has to go.
orphan=$(docker logs "$name" 2>&1 | sed -n 's/^orphan: pid \([0-9]*\)$/\1/p')
[ -n "$orphan" ] || fail "(b) no line of the orphan"
states | grep -q "^$orphan ([^)]*) [^Z] 1 " ||
	fail "(b) the orphan $orphan is no process whose parent is process 1"
i=0
while states | grep -q "^$orphan ([^)]*) [^Z] "; do
	i=$((i + 1))
	[ "$i" -lt 100 ] || fail "(b) the orphan did not end"
	sleep 0.1
done
sleep 0.5
if states | grep ') Z '; then
	fail "(b) a zombie is left"
fi

# (c) SIGTERM to process 1 reaches the child, which exits with 0, long
# before docker stop would kill the container.
start=$(date +%s)
docker stop -t 20 "$name" >/dev/null
took=$(($(date +%s) - start))
docker logs "$name" 2>&1 | grep -q '^child: got SIGTERM$' || fail "(c) SIGTERM did not reach the child"
status=$(docker inspect --format '{{.State.ExitCode}}' "$name")
[ "$status" = 0 ] || fail "(c) the container exited with $status, want 0"
[ "$took" -lt 10 ] || fail "(c) the container took $took seconds to stop"
docker rm -f "$name" >/dev/null

# (d) The exit status of the child is the exit status of the container.
status=0
docker run --name "$name" -v "$tmp:/test:ro" "$image" /test/pid1 exit 3 >/dev/null 2>&1 || status=$?
[ "$status" = 3 ] || fail "(d) the container exited with $status, want 3"

echo ok
```

Make it executable:

```bash
chmod +x scripts/test-pid1.sh
```

What the script relies on: `docker version --format '{{.Server.Arch}}'` names the architecture the container runs on (`amd64` or `arm64`; on a Mac that is the virtual machine's, not necessarily the host's); the temporary directory can be mounted into a container (on a Mac, `mktemp -d` makes it under `/var/folders`, which Docker Desktop shares by default); and in busybox `sleep` is built into the shell, so the orphan shows in `/proc` under the name `sh` — which is why the script goes by the orphan's process ID and not by its name.

- [ ] **Step 7: Run it**

Run: `go vet ./reaper/testdata/pid1 && scripts/test-pid1.sh`
Expected: after a few seconds (longer the first time, when `busybox:1.36` is pulled) the one line

```
ok
```

and exit status 0. `docker ps -a --filter name=great-machine-pid1` then lists no container. If Docker does not run on this machine the script ends with `test-pid1: FAILED: docker does not answer`; say so in your report instead of skipping the step silently, because then only CI has run this test.

A failure names the check and prints what the container wrote, for example `test-pid1: FAILED: (b) a zombie is left`.

- [ ] **Step 8: Call Init first in main**

`cmd/great-machine/main.go` becomes, whole:

```go
// Command great-machine is draal, zathras and the operator's commands.
package main

import (
	"os"

	"github.com/fables-for-robots/great-machine/cli"
	"github.com/fables-for-robots/great-machine/reaper"
)

func main() {
	// As the first process of a container the program runs itself again
	// as a child, and here only passes signals on and waits for every
	// child, so that none stays a zombie.
	if status, was := reaper.Init(); was {
		os.Exit(status)
	}
	os.Exit(cli.Run(os.Args, os.Stdout, os.Stderr))
}
```

`Init` has to come before anything that starts a goroutine or a process, which is why it is the first statement of `main` and not a command of `cli`.

- [ ] **Step 9: Build, and run everything**

Run: `go build ./... && go vet ./... && go test ./reaper/ ./cmd/...`
Expected: no output from `go build` and `go vet`; `ok` for `reaper`, and `[no test files]` for `cmd/great-machine`.

- [ ] **Step 10: Add the step to CI**

In `.github/workflows/test.yml`, in the job `test`, add this step directly after the step named `test`, before the comment that follows it and the step named `nix build`, indented as the other steps of the list:

```yaml
      - name: pid1
        run: scripts/test-pid1.sh
```

The step has no setup of its own. It needs `go` (of the version in `go.mod`) and `docker` on the `PATH`, and a Docker daemon the runner's user may use. `go` comes from wherever the steps `vet` and `test` get it: the job runs every `run` step in the flake's shell (`defaults.run.shell`), and that shell keeps the runner's `PATH` behind its own, so `docker`, which `ubuntu-latest` has with its daemon running, is found from inside it. Should the job have no such default and its steps name the Nix shell themselves, write this step's `run:` as they do.

Check that the step is where it belongs:

Run: `grep -n -B2 -A1 'name: pid1' .github/workflows/test.yml`
Expected: the `run:` line of the step `test`, then the two lines of the new step, with the same indentation.

- [ ] **Step 11: Commit**

```bash
git add reaper/testdata/pid1/main.go scripts/test-pid1.sh cmd/great-machine/main.go .github/workflows/test.yml
git commit -m "Reaper: main calls Init, and a test as process 1 of a container"
```
