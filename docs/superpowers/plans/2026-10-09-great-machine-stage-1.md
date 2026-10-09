# great-machine, stage 1: zathras, draal and the operator commands — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A rollout from the laptop to pods: `great-machine rollout` pushes a Nix closure and draal moves every zathras of a service to it in two phases, with `status`, `logs` and `shell` on the command line.

**Architecture:** Every zathras dials draal over iroh and keeps one connection, on which draal opens streams for commands, logs and shells. A rollout is a row in SQLite; a pure function, `rollout.Decide`, compares it with what the zathrases report and says what to do next, and draal carries that out. Operators dial draal on the same ALPN and are let in by an allowlist of endpoint IDs.

**Tech Stack:** Go 1.26, go-iroh v0.3.0, CBOR (fxamacker/cbor/v2), SQLite (modernc.org/sqlite) through sqlc, urfave/cli/v2, creack/pty, jaccard-store's `client` package and amber-store core for the push, Nix with gonixgo for the build.

**Spec:** `docs/superpowers/specs/2026-10-09-great-machine-design.md`. This plan covers stage 1 of its section 16. The web page (stage 2) and the terminal UI (stage 3) get plans of their own, written against the code this plan leaves.

## Global Constraints

- Module `github.com/fables-for-robots/great-machine`, `go 1.26.3`. One program, `great-machine`, in `cmd/great-machine`.
- Every package is at the top level of the module. **No `internal/` directory.**
- SQL only through sqlc, on `modernc.org/sqlite`. What sqlc generates is committed in `db/dbq`, and `sqlc diff` prints nothing.
- Everything but HTTP travels over go-iroh v0.3.0 on the ALPN `great-machine/1`. A message is CBOR behind its length as four bytes, big-endian, of at most 4 MiB.
- The Nix build is gonixgo's `buildGoApplication` with `CGO_ENABLED = 0`. No package may need cgo.
- Tests use the standard library's `testing` only. No assertion library.
- Logging is `log/slog`. Errors are wrapped with `%w`.
- Every flag has an environment variable, and the flag wins. The names and defaults are those of section 3 of the spec.
- The values of the spec, verbatim: report every 2 s; a zathras is gone after 10 s without a report; redial after 1 s, doubling, up to 30 s; a service that ends within 30 s of its start is restarted after 1 s, doubling, up to 2 min; SIGKILL 2 min after SIGTERM; a ring of 400 lines of at most 16 KiB; 15 min of samples; draal waits 15 s after its start; a rollout that is `preparing` for 30 min is halted; the health check is `GET /healthz` on `:9890`.
- Every one of those durations is a field of a `Config` with the spec's value as its default, so that a test makes it short. No test sleeps for a duration of the spec.
- A test that needs a program builds it into `t.TempDir()`. Nothing that was built is left in the repository or anywhere else.
- Work happens on a branch, not on `main`. Commit messages follow `Area: what was done`, as the history has them.

## Review Focus

What the spec implies and a person will meet, and the task whose tests pin it:

1. **Output of a service that is no valid UTF-8, or has no line feed for megabytes.** The ring holds lines of valid UTF-8 of at most 16 KiB, whatever was written, and following the log never fails for what a line holds. Task 4.
2. **A `PATH` for `rollout` that is a directory, is missing, is a store path without a file in it, or resolves outside `/nix/store`.** The command ends with an error that names the path and says what is wrong, before anything is pushed. Task 18.
3. **A new pod whose `prepare` fails while no rollout is under way** (the store cannot be reached for a minute). It is tried again every 30 seconds until it works; the pod does not stay `idle` for ever. Task 15.
4. **Two zathrases of a service with the same name** (a fixed `--name` in a pod template). They are listed apart, a command that names them fails and prints their endpoint IDs, and the beginning of an endpoint ID is accepted where a name is. Task 17.
5. **`great-machine shell` with a standard input that is no terminal** (`echo uptime | great-machine shell web`). The input is passed on, and at its end the shell is told so and its exit status becomes that of the command. Task 20.

---

## File Structure

```
cmd/great-machine/main.go   main: reaper.Init, then cli.Run
cli/                        the commands (urfave/cli/v2), one file for each
wire/                       messages, framing, error codes, names of services
node/                       keys, listening and dialing over go-iroh
node/nodetest/              a network in memory, for tests without iroh
logring/                    the ring of the last lines and its followers
supervisor/                 starts, stops and restarts the service
testservice/                a program for tests: exits, hangs, ignores SIGTERM on request
testservice/build/          builds testservice into a test's temporary directory
stats/                      samples from cgroup v2 and /proc/net/dev
preload/                    the client of the sidecar's preload endpoint
ptyshell/                   shell sessions on a pseudo-terminal
health/                     the HTTP health check
reaper/                     as process 1: runs the program as its child and waits for every child
zathras/                    the init: its state, its connection to draal, its answers to commands
db/                         migrations, queries, the store of services, generations and rollouts
db/dbq/                     what sqlc makes
authz/                      Authorize and the allowlist
fleet/                      the connected zathrases, their reports and samples
rollout/                    Decide: the reconciler as a pure function
draal/                      Controller, its implementation, the handler of the protocol
operator/                   the client of draal
closure/                    the binary's store path and its closure, by nix-store
push/                       store paths to jaccard-store
term/                       a remote shell session on the local terminal
e2e/                        tests of the whole over go-iroh on loopback
deploy/                     pod.yaml, draal.yaml
scripts/                    test-pid1.sh
flake.nix, .envrc, sqlc.yaml, .github/workflows/
```

The spec's table of packages is changed in three places, and the spec was
brought in line: `logring`, `stats` and `supervisor` use the types of
`wire`, which has nothing of iroh in it; `reaper` is used by `main` alone,
because as process 1 the program runs itself again as a child and the
parent does nothing but pass signals on and wait; and `node/nodetest` and
`testservice` are added for the tests.

## Interfaces

This section is the contract between the tasks. A task implements what is
written here for its package, exactly, and uses the other packages only
through what is written here. An identifier that is not here is private to
its task.

### wire

```go
package wire

// ALPN is the protocol of every iroh connection of great-machine.
const ALPN = "great-machine/1"

// MaxFrame is the largest message, in bytes of CBOR.
const MaxFrame = 4 << 20

// ID is an endpoint ID: 64 lowercase hexadecimal characters. node.ParseID
// makes one out of text; everywhere else an ID is taken as it is.
type ID string

type Code string

const (
	CodeBadRequest   Code = "bad-request"
	CodeUnauthorized Code = "unauthorized"
	CodeNotFound     Code = "not-found"
	CodeUnavailable  Code = "unavailable"
	CodeInternal     Code = "internal"
)

// Error is a refusal, as it travels.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string // "<code>: <message>"
func Errorf(code Code, format string, args ...any) *Error

// CodeOf returns the code of the *Error in err's chain, CodeInternal for
// any other error and "" for nil.
func CodeOf(err error) Code

// Write writes msg as one frame. A message of more than MaxFrame is not
// written, and the error says how large it was.
func Write(w io.Writer, msg any) error

// Read reads one frame into msg. It returns io.EOF when r ends before the
// first byte of a frame, and io.ErrUnexpectedEOF when it ends inside one.
// A frame that announces more than MaxFrame is an error, and nothing of
// it is read.
func Read(r io.Reader, msg any) error

// ReadResponse reads one Response. A Response that carries an Error is
// returned as that *Error.
func ReadResponse(r io.Reader) (*Response, error)

// Call writes req and reads one Response, as ReadResponse does.
func Call(s io.ReadWriter, req *Request) (*Response, error)

// WriteError writes a Response that carries err. An error that has no
// *Error in its chain is written as CodeInternal with its text.
func WriteError(w io.Writer, err error) error

// ValidService returns nil for a DNS label, [a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?,
// and a bad-request *Error that quotes the name otherwise.
func ValidService(name string) error

// Request is the first message of a stream. Exactly one field is set.
type Request struct {
	Register *Register        `json:"register,omitempty"`
	Prepare  *Prepare         `json:"prepare,omitempty"`
	Activate *Activate        `json:"activate,omitempty"`
	Logs     *LogsRequest     `json:"logs,omitempty"`
	Shell    *ShellRequest    `json:"shell,omitempty"`
	Store    *StoreRequest    `json:"store,omitempty"`
	Rollout  *RolloutRequest  `json:"rollout,omitempty"`
	Rollback *RollbackRequest `json:"rollback,omitempty"`
	Watch    *WatchRequest    `json:"watch,omitempty"`
	Rollouts *RolloutsRequest `json:"rollouts,omitempty"`
	History  *HistoryRequest  `json:"history,omitempty"`
}

// Response is every message that answers a Request. Error, or exactly one
// of the others, is set.
type Response struct {
	Error      *Error       `json:"error,omitempty"`
	Registered *Registered  `json:"registered,omitempty"`
	Prepared   *Prepared    `json:"prepared,omitempty"`
	Activated  *Activated   `json:"activated,omitempty"`
	Line       *LogLine     `json:"line,omitempty"`
	Shell      *ShellOutput `json:"shell,omitempty"`
	Store      *StoreInfo   `json:"store,omitempty"`
	Rollout    *RolloutInfo `json:"rollout,omitempty"`
	Fleet      *Fleet       `json:"fleet,omitempty"`
	Rollouts   *RolloutList `json:"rollouts,omitempty"`
	History    *SampleList  `json:"history,omitempty"`
}

type Phase string

const (
	PhaseIdle     Phase = "idle"
	PhaseRunning  Phase = "running"
	PhaseStopping Phase = "stopping"
	PhaseBackoff  Phase = "backoff"
)

// Exit is how a service ended.
type Exit struct {
	Generation uint64        `json:"generation"`
	Status     int           `json:"status"`           // the exit status; -1 when a signal ended it or it never started
	Signal     string        `json:"signal,omitempty"` // such as "SIGKILL"
	Error      string        `json:"error,omitempty"`  // why the binary could not be started
	At         time.Time     `json:"at"`
	Ran        time.Duration `json:"ran"`
}

// Sample is the figures of a container at one time.
type Sample struct {
	At          time.Time `json:"at"`
	CPU         float64   `json:"cpu"`          // cores
	Memory      uint64    `json:"memory"`       // bytes
	MemoryLimit uint64    `json:"memory_limit"` // bytes; 0 for none
	DiskRead    float64   `json:"disk_read"`    // bytes per second
	DiskWrite   float64   `json:"disk_write"`
	NetRx       float64   `json:"net_rx"`
	NetTx       float64   `json:"net_tx"`
}

// State is what a zathras reports.
type State struct {
	Generation uint64        `json:"generation"` // 0 for none
	Binary     string        `json:"binary,omitempty"`
	Args       []string      `json:"args,omitempty"`
	Phase      Phase         `json:"phase"`
	PID        int           `json:"pid,omitempty"`         // while running
	RunningFor time.Duration `json:"running_for,omitempty"` // while running
	RestartAt  time.Time     `json:"restart_at"`            // while in backoff; the zero time otherwise
	LastExit   *Exit         `json:"last_exit,omitempty"`
	Restarts   uint64        `json:"restarts"`
	Prepared   []uint64      `json:"prepared,omitempty"` // ascending
	Preparing  uint64        `json:"preparing,omitempty"`
	Sample     *Sample       `json:"sample,omitempty"`
}

// A zathras and draal.

type Register struct {
	Service string `json:"service"`
	Name    string `json:"name"`
	Version string `json:"version"`
	State   State  `json:"state"`
}
type Registered struct{}

type Prepare struct {
	Generation uint64   `json:"generation"`
	Binary     string   `json:"binary"`
	Closure    []string `json:"closure"` // names of store paths, without /nix/store/
}
type Prepared struct{}

type Activate struct {
	Generation uint64   `json:"generation"`
	Binary     string   `json:"binary"`
	Args       []string `json:"args,omitempty"`
}
type Activated struct{}

// LogsRequest asks for the ring. Zathras names the zathras when draal is
// asked, and is empty when a zathras is asked.
type LogsRequest struct {
	Zathras ID   `json:"zathras,omitempty"`
	Follow  bool `json:"follow,omitempty"`
}

// LogLine is a line of the ring. One with Lost set has no text: it says
// that so many lines were lost before the line that follows it.
type LogLine struct {
	At     time.Time `json:"at"`
	Stream string    `json:"stream"` // "stdout", "stderr" or "zathras"
	Text   string    `json:"text,omitempty"`
	Lost   uint64    `json:"lost,omitempty"`
}

type ShellRequest struct {
	Zathras ID     `json:"zathras,omitempty"`
	Cols    uint16 `json:"cols"`
	Rows    uint16 `json:"rows"`
	Term    string `json:"term,omitempty"`
}
type WinSize struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// ShellInput is what the asking side sends after its ShellRequest.
type ShellInput struct {
	Data   []byte   `json:"data,omitempty"`
	Resize *WinSize `json:"resize,omitempty"`
}

// ShellOutput is what answers. The first one is empty and says that the
// shell runs. The last one has Exit set.
type ShellOutput struct {
	Data []byte `json:"data,omitempty"`
	Exit *int   `json:"exit,omitempty"`
}

// An operator and draal.

type StoreRequest struct{}
type StoreInfo struct {
	Server ID     `json:"server"`
	Prefix string `json:"prefix"`
}

type RolloutRequest struct {
	Service  string   `json:"service"`
	Binary   string   `json:"binary"`
	Args     []string `json:"args,omitempty"`
	Closure  []string `json:"closure"`
	Parallel int      `json:"parallel"`
}
type RollbackRequest struct {
	Service  string `json:"service"`
	Parallel int    `json:"parallel"`
}

type RolloutState string

const (
	RolloutPreparing  RolloutState = "preparing"
	RolloutActivating RolloutState = "activating"
	RolloutCompleted  RolloutState = "completed"
	RolloutHalted     RolloutState = "halted"
	RolloutSuperseded RolloutState = "superseded"
)

// UnderWay says whether s is preparing or activating.
func (s RolloutState) UnderWay() bool

type RolloutInfo struct {
	ID         uint64       `json:"id"`
	Service    string       `json:"service"`
	Generation uint64       `json:"generation"`
	Parallel   int          `json:"parallel"`
	State      RolloutState `json:"state"`
	Reason     string       `json:"reason,omitempty"`
	Created    time.Time    `json:"created"`
	CreatedBy  ID           `json:"created_by"`
	Finished   time.Time    `json:"finished"` // the zero time while under way
	// Of the connected zathrases, in a Fleet alone: how many have prepared
	// the target, how many run it and have for 30 seconds, and how many
	// do not yet. Activated and Awaited add up to the zathrases there are.
	Prepared  int `json:"prepared"`
	Activated int `json:"activated"`
	Awaited   int `json:"awaited"`
}

type WatchRequest struct{}
type Fleet struct {
	Services []Service `json:"services"` // by name
}
type Service struct {
	Name      string       `json:"name"`
	Stable    uint64       `json:"stable"`            // 0 for none
	Rollout   *RolloutInfo `json:"rollout,omitempty"` // the last one
	Zathrases []Zathras    `json:"zathrases"`         // by name, then by ID
}
type Zathras struct {
	ID        ID        `json:"id"`
	Name      string    `json:"name"`
	Connected time.Time `json:"connected"`
	State     State     `json:"state"`
}

type RolloutsRequest struct {
	Service string `json:"service"`
}
type RolloutList struct {
	Rollouts []RolloutInfo `json:"rollouts"` // newest first
}
type HistoryRequest struct {
	Zathras ID `json:"zathras"`
}
type SampleList struct {
	Samples []Sample `json:"samples"` // oldest first
}
```

`fxamacker/cbor` takes the `json` tags. A `time.Time` keeps its
nanoseconds across `Write` and `Read`.

**How streams are used**, by every task that speaks the protocol:

- The asking side opens a stream, writes one `Request` and reads
  `Response`s. It ends a stream it is done with by `Close`, on every path,
  and never by `CloseWrite`: to the answering side the end of what it
  reads is the withdrawal of the request.
- The answering side ends its last message with `CloseWrite` and then
  waits for the asking side to close the stream before it closes its own
  side, so that the last message is not lost.
- `register`: after `Registered` the zathras writes a `State` as a frame of
  its own for every report. Nothing more is written back.
- `logs`: one `Response` with `Line` for every line. Without `Follow` the
  answering side ends the stream after the last line with `CloseWrite`.
  With `Follow` the stream goes on until the asking side closes it.
- `shell`: the answering side writes the empty `ShellOutput` when the shell
  has started, or an `Error`. The asking side then writes `ShellInput`
  frames, and the answering side `Response`s with `Shell`, the last of
  which has `Exit`.
- `watch`: a `Response` with `Fleet` at once, and again when it changed.
- The answering side of a stream without end (`logs` with `Follow`,
  `shell`, `watch`) keeps reading the stream, and takes the end of what it
  reads, or an error, as the end of the request.
- A zathras answers `activate` with `bad-request` and the reason for a
  binary that is no executable file, and with `unavailable` when its
  supervisor has stopped because the pod is going.
- For draal a command **failed** only when the zathras answered it with an
  `Error` whose code is not `unavailable`. A command that got no answer,
  because the stream or the connection ended or draal withdrew it, and one
  answered `unavailable`, failed nothing: the zathras is going or gone, and
  if it is still there the command is sent again.

### node

```go
package node

// Key is the secret key of an endpoint.
type Key struct{ /* unexported */ }

func NewKey() (Key, error)

// LoadOrCreateKey reads the key file at path, which holds the seed as 64
// hexadecimal characters, and creates it with mode 0600, and its directory,
// when it is missing.
func LoadOrCreateKey(path string) (Key, error)

func (k Key) ID() wire.ID

// Iroh returns the key as go-iroh has it (key.SecretKey of
// github.com/tmc/go-iroh/key), for the packages that dial with a library
// of their own.
func (k Key) Iroh() key.SecretKey

// ParseID accepts an endpoint ID as 64 hexadecimal characters or as
// go-iroh writes it, and refuses what is no endpoint ID.
func ParseID(text string) (wire.ID, error)

// Stream is one stream of a connection, in both directions.
type Stream interface {
	io.Reader
	io.Writer
	// CloseWrite ends what this side sends. The peer reads io.EOF after
	// what was written, and this side can still read.
	CloseWrite() error
	// Close ends the stream for this side: it ends what this side sends,
	// as CloseWrite does, and gives up reading. The peer reads what was
	// written and then io.EOF, and its Write fails.
	Close() error
}

// Conn is a connection to a peer. Both sides open streams and accept them.
type Conn interface {
	RemoteID() wire.ID
	// OpenStream opens a stream. THE PEER DOES NOT SEE IT UNTIL SOMETHING
	// WAS WRITTEN TO IT: whoever opens a stream writes first.
	OpenStream(ctx context.Context) (Stream, error)
	// AcceptStream waits for a stream the peer opens. It fails when the
	// connection has ended.
	AcceptStream(ctx context.Context) (Stream, error)
	// Done is closed when the connection has ended, whoever ended it.
	Done() <-chan struct{}
	// Close ends the connection at once. What was written and has not
	// arrived is lost, and every Read, Write and AcceptStream that waits,
	// on either side, fails. A connection that was dialed is closed even
	// when Done is closed already: Close frees its endpoint.
	Close() error
}

type Listener interface {
	ID() wire.ID
	// LocalAddr is the UDP address the endpoint is bound to.
	LocalAddr() netip.AddrPort
	Accept(ctx context.Context) (Conn, error)
	// Close ends the listening and every connection that was accepted.
	Close() error
}

type ListenConfig struct {
	Key  Key
	Bind netip.AddrPort // the zero value: every address, a port the system picks
	// Local is for tests: no relay, nothing announced, bound to 127.0.0.1
	// unless Bind says otherwise.
	Local bool
	Log   *slog.Logger // nil: slog.Default()
}

// Listen binds an endpoint that accepts connections for wire.ALPN.
func Listen(ctx context.Context, cfg ListenConfig) (Listener, error)

type DialConfig struct {
	Key    Key
	Server wire.ID
	// Addrs is for tests: dial these addresses, use no relay, look nothing up.
	Addrs []netip.AddrPort
	Log   *slog.Logger
}

// Dial connects to the server for wire.ALPN. ctx bounds the dial alone.
// Close ends the connection and the endpoint that was bound for it.
func Dial(ctx context.Context, cfg DialConfig) (Conn, error)
```

A peer that vanishes without a word is noticed by `Done` within 10
seconds: `Dial` sets an idle timeout of 6 seconds and a keep-alive of 2,
which was measured at 6 to 9 seconds on either side. The `ctx` of `Listen`
bounds `Listen` alone, as that of `Dial` does. `Listen` and
`LoadOrCreateKey` call the `node` package of jaccard-store; `Dial` is code
of its own, modelled on jaccard-store's, because that one hides the
connection a zathras has to accept streams on. A `Write` returns before
the peer reads while less than 512 KiB wait unread on the stream, and
waits beyond that.

```go
package nodetest

// Net is a network in memory that behaves as go-iroh does wherever a
// protocol could tell: a stream is not seen by the peer before its first
// write, a Write returns before the peer has read while less than 512 KiB
// wait, and Close and CloseWrite end streams as node documents it.
// LocalAddr of its listeners is the zero value.
type Net struct{ /* unexported */ }

func New() *Net

// Listen makes id reachable. A second Listen for an id that listens fails
// the test through panic.
func (n *Net) Listen(id wire.ID) node.Listener

// Dial connects from to to, and fails when nobody listens as to.
func (n *Net) Dial(ctx context.Context, from, to wire.ID) (node.Conn, error)

// ID returns an endpoint ID for tests: 64 hexadecimal characters made of n.
func ID(n int) wire.ID
```

### logring

```go
package logring

const (
	Stdout  = "stdout"
	Stderr  = "stderr"
	Zathras = "zathras"
)

type Ring struct{ /* unexported */ }

// New returns a ring of the last size lines, of at most maxLine bytes
// each. great-machine uses New(400, 16<<10).
func New(size, maxLine int) *Ring

// SetClock replaces time.Now, for tests. Call it before anything is added.
func (r *Ring) SetClock(now func() time.Time)

// Add adds text as one line of stream. Bytes that are no valid UTF-8
// become U+FFFD, and a text of more than maxLine bytes becomes several
// lines.
func (r *Ring) Add(stream, text string)

// Writer returns a writer that adds what is written to it, cut at line
// feeds, as lines of stream. What has no line feed yet is held back until
// one comes, until it is maxLine bytes long, or until Flush. A carriage
// return before the line feed is dropped.
func (r *Ring) Writer(stream string) *Writer

type Writer struct{ /* unexported */ }

func (w *Writer) Write(p []byte) (int, error) // never fails
func (w *Writer) Flush()

// Lines returns the lines of the ring, oldest first.
func (r *Ring) Lines() []wire.LogLine

// Follow returns the lines of the ring and a channel of the lines that
// follow, with none between them lost and none twice. The channel is
// closed when ctx is done. A reader that lets more than buffer lines wait
// loses the oldest of them, and reads a line with Lost set, stream
// Zathras and no text in their place.
func (r *Ring) Follow(ctx context.Context, buffer int) ([]wire.LogLine, <-chan wire.LogLine)
```

### supervisor

```go
package supervisor

type Config struct {
	Ring           *logring.Ring // required: the service's lines and the supervisor's own
	Stdout, Stderr io.Writer     // copies of the service's output; nil for none
	Env            []string      // nil: os.Environ()
	Dir            string        // "": the directory of this process
	StopTimeout    time.Duration // 0: 2 minutes
	QuickExit      time.Duration // 0: 30 seconds
	BackoffMin     time.Duration // 0: 1 second
	BackoffMax     time.Duration // 0: 2 minutes
	Log            *slog.Logger  // nil: slog.Default()
}

type Supervisor struct{ /* unexported */ }

func New(cfg Config) *Supervisor

// Run does the work until ctx is done, then stops the service as the
// spec's 7.1 says and returns nil. Call it once.
func (s *Supervisor) Run(ctx context.Context) error

// ErrNotExecutable is in the chain of what Activate and CheckExecutable
// return for a binary that is no file this process may execute.
var ErrNotExecutable = errors.New("not an executable file")

// ErrStopped is what Activate returns once Run has ended.
var ErrStopped = errors.New("the supervisor has stopped")

// Activate checks the binary, and refuses with ErrNotExecutable leaving
// what runs alone. Otherwise it has Run take the generation as its own,
// and returns when Run has: State then shows the generation with a phase
// that is this generation's (running with the new PID, backoff when the
// binary could not be started, or stopping while the service that ran
// has not ended). Before Run was called it records the generation and
// returns at once, and the phase is idle until Run begins. From the
// moment Run's context is done it returns ErrStopped.
func (s *Supervisor) Activate(generation uint64, binary string, args []string) error

// State returns Generation, Binary, Args, Phase, PID, RunningFor,
// RestartAt, LastExit and Restarts. The other fields are the zero value.
func (s *Supervisor) State() wire.State

// Changes has room for one value and gets one after every change of what
// State returns but RunningFor.
func (s *Supervisor) Changes() <-chan struct{}

// CheckExecutable returns nil when path, through its symbolic links, is a
// regular file with an execute bit.
func CheckExecutable(path string) error
```

The supervisor's own lines in the ring, as stream `logring.Zathras`:
`started generation 7: /nix/store/…/bin/web (pid 123)` and
`generation 7 exited with status 1 after 3.2s; starting it again in 4s`
(or `…was ended by SIGKILL…`, `…could not be started: <error>…`, and
`; starting it again` when there is no delay).

```
testservice [--stdout TEXT] [--stderr TEXT] [--exit-after D] [--exit-code N] [--ignore-term] [--spawn]
```

`testservice` writes TEXT and a line feed to the stream named, once, at its
start. With `--exit-after` it exits after D with `--exit-code` (0 unless
given). Without, it runs until SIGTERM and exits with 0, or with
`--ignore-term` until it is killed. `--spawn` starts a child in its
process group that ignores SIGTERM and writes its process ID to standard
output as `child <pid>`.

```go
package build // testservice/build

// Binary builds testservice into tb.TempDir() and returns its path.
func Binary(tb testing.TB) string
```

### stats

```go
package stats

// Reader reads samples. The zero value is not usable.
type Reader struct {
	Cgroup string // the directory of the cgroup; NewReader: /sys/fs/cgroup
	NetDev string // NewReader: /proc/net/dev
	// unexported: the counters of the reading before
}

func NewReader() *Reader

// Read returns the sample of now. The rates are over the time since the
// reading before, and zero at the first. It returns false when a file is
// missing or cannot be understood: this system gives no samples.
func (r *Reader) Read(now time.Time) (wire.Sample, bool)
```

### preload

```go
package preload

type Client struct {
	URL  string       // the preload endpoint of the sidecar
	HTTP *http.Client // nil: http.DefaultClient
}

// Error is an answer of the endpoint that is not 200.
type Error struct {
	Status int
	Body   string // its first 4 KiB
}

func (e *Error) Error() string

// Preload posts /nix/store/<name> for every name, one on a line, and
// returns nil for an answer of 200.
func (c Client) Preload(ctx context.Context, names []string) error
```

### ptyshell

```go
package ptyshell

type Config struct {
	Shell string   // the program; "": /bin/sh
	Env   []string // nil: os.Environ(); TERM is set from Term
	Dir   string
	Term  string // "": xterm
	Cols  uint16 // 0: 80
	Rows  uint16 // 0: 24
}

type Session struct{ /* unexported */ }

// Start starts the shell on a pseudo-terminal, in a session of its own.
func Start(cfg Config) (*Session, error)

func (s *Session) Read(p []byte) (int, error)  // output; io.EOF when the shell has ended and all was read
func (s *Session) Write(p []byte) (int, error) // input
func (s *Session) Resize(cols, rows uint16) error

// Wait returns the exit status when the shell has ended: -1 when a signal
// ended it.
func (s *Session) Wait() int

// Close ends the shell if it runs, with SIGHUP to its process group and
// SIGKILL two seconds later, and frees the terminal. It may be called
// more than once.
func (s *Session) Close() error
```

### health

```go
package health

// Handler answers GET and HEAD of /healthz with 200 and "running" while
// phase returns wire.PhaseRunning, and with 503 and the phase otherwise.
// Every other path is 404, every other method 405.
func Handler(phase func() wire.Phase) http.Handler
```

### reaper

```go
package reaper

// Init returns false at once in a process that is not process 1. In
// process 1 it starts the program again as its child, with the same
// arguments, environment and standard streams, passes SIGTERM, SIGINT and
// SIGHUP on to it, waits for every child there is, and returns true and
// the exit status of that child when it has ended.
func Init() (status int, was bool)
```

### zathras

```go
package zathras

type Config struct {
	Service    string
	Name       string
	Version    string
	Dial       func(ctx context.Context) (node.Conn, error) // dials draal
	Supervisor *supervisor.Supervisor                        // the caller runs it
	Ring       *logring.Ring
	Stats      *stats.Reader   // nil: no samples
	Preload    preload.Client
	Shell      ptyshell.Config // Term, Cols and Rows come with the request
	ReportEvery time.Duration  // 0: 2 seconds
	RedialMin   time.Duration  // 0: 1 second
	RedialMax   time.Duration  // 0: 30 seconds
	Log         *slog.Logger
}

type Agent struct{ /* unexported */ }

func New(cfg Config) *Agent

// Run keeps a connection to draal until ctx is done: it dials, registers,
// reports and answers prepare, activate, logs and shell, and dials again
// when the connection has ended. It ends the shells before it returns.
func (a *Agent) Run(ctx context.Context) error

// State returns the whole state: the supervisor's, Prepared, Preparing
// and the latest sample.
func (a *Agent) State() wire.State
```

### db

```go
package db

var (
	ErrNotFound          = errors.New("not found")
	ErrNoStable          = errors.New("the service has no stable generation")
	ErrNothingToRollBack = errors.New("the last rollout completed")
)

type DB struct{ /* unexported */ }

// Open opens the database file at path, creating it and bringing its
// schema up to date.
func Open(path string) (*DB, error)
func (d *DB) Close() error

type Service struct {
	Name   string
	Stable uint64 // 0 for none
}

type Generation struct {
	Service   string
	Number    uint64
	Binary    string
	Args      []string
	Closure   []string
	Created   time.Time
	CreatedBy wire.ID
}

// Services returns every service, by name.
func (d *DB) Services(ctx context.Context) ([]Service, error)

// Service returns ErrNotFound for a service that has no row.
func (d *DB) Service(ctx context.Context, name string) (Service, error)

// Generation returns ErrNotFound for one that is not there.
func (d *DB) Generation(ctx context.Context, service string, number uint64) (Generation, error)

// NewRollout does in one transaction: makes the service's row if it is
// missing, records a generation with the next number, marks the rollout
// that is under way as superseded, and records a rollout that is
// preparing and targets the new generation. The counts of what it
// returns are zero.
func (d *DB) NewRollout(ctx context.Context, req wire.RolloutRequest, by wire.ID, now time.Time) (wire.RolloutInfo, error)

// NewRollback records a rollout that targets the stable generation, and
// supersedes the one under way. ErrNotFound: no such service.
// ErrNoStable. ErrNothingToRollBack: the last rollout completed.
func (d *DB) NewRollback(ctx context.Context, req wire.RollbackRequest, by wire.ID, now time.Time) (wire.RolloutInfo, error)

// LastRollout returns the newest rollout of the service, and false when
// it never had one.
func (d *DB) LastRollout(ctx context.Context, service string) (wire.RolloutInfo, bool, error)

// Rollouts returns the newest limit rollouts of the service, newest first.
func (d *DB) Rollouts(ctx context.Context, service string, limit int) ([]wire.RolloutInfo, error)

// SetRolloutState moves a rollout that is under way to activating,
// completed or halted, with the reason; any other state is an error. To
// completed and halted it also sets Finished, and to completed it makes
// the target the stable generation of the service. It returns false, and
// changes nothing, when the rollout is not under way any more or is not
// there.
func (d *DB) SetRolloutState(ctx context.Context, id uint64, state wire.RolloutState, reason string, now time.Time) (bool, error)
```

The errors of `db` are wrapped: callers use `errors.Is`. Times come back in
UTC with their nanoseconds. `Generation.Args` is nil for no arguments. A
rollout that was superseded has `Finished` set to the time of the rollout
that superseded it.

### authz

```go
package authz

type Operation string

const (
	OpRegister Operation = "register"
	OpStore    Operation = "store"
	OpRollout  Operation = "rollout"
	OpRollback Operation = "rollback"
	OpWatch    Operation = "watch"
	OpRollouts Operation = "rollouts"
	OpHistory  Operation = "history"
	OpLogs     Operation = "logs"
	OpShell    Operation = "shell"
)

// Authorizer decides whether peer may do op on service. service is ""
// for store and watch. A refusal is a *wire.Error with CodeUnauthorized.
type Authorizer interface {
	Authorize(peer wire.ID, op Operation, service string) error
}

// Allowlist lets every peer register and its operators do everything else.
type Allowlist struct{ /* unexported */ }

func NewAllowlist(operators []wire.ID) Allowlist
func (a Allowlist) Authorize(peer wire.ID, op Operation, service string) error
```

### fleet

```go
package fleet

// Member is a zathras that is connected.
type Member struct {
	ID        wire.ID
	Service   string
	Name      string
	Connected time.Time
	State     wire.State
	Reported  time.Time // when State came
}

type Registry struct{ /* unexported */ }

// New returns a registry that keeps the samples of the last history.
func New(history time.Duration) *Registry

// Join adds m, in place of a member with the same ID if there is one, and
// returns the epoch of this membership. Report and Leave with another
// epoch do nothing: they are of a connection that was replaced.
func (r *Registry) Join(m Member) (epoch uint64)
func (r *Registry) Report(id wire.ID, epoch uint64, st wire.State, now time.Time) bool
func (r *Registry) Leave(id wire.ID, epoch uint64) bool

func (r *Registry) Get(id wire.ID) (Member, bool)

// Service returns the members of a service, by name and then by ID.
func (r *Registry) Service(name string) []Member

// Services returns the names of the services that have members, sorted.
func (r *Registry) Services() []string

// History returns the samples kept of a member, oldest first.
func (r *Registry) History(id wire.ID) []wire.Sample

// Stale returns the members whose last report is older than after.
func (r *Registry) Stale(now time.Time, after time.Duration) []wire.ID

// Subscribe returns a channel with room for one value that gets one after
// every Join, Report and Leave that changed something, and a function
// that ends the subscription.
func (r *Registry) Subscribe() (<-chan struct{}, func())
```

`Join` takes a zero `Reported` to be `Connected`, so a member that never
reports goes stale as well. Samples are kept by their own times: one with
the time of the last one kept adds nothing, and what is older than
`history` before the newest sample is dropped. Every `Report` counts as a
change. The channel of `Subscribe` is never closed.

### rollout

```go
package rollout

// Failure is a command a zathras refused or failed.
type Failure struct {
	Generation uint64
	Activate   bool // false: a prepare
	Reason     string
	At         time.Time
}

// Input is everything Decide looks at for one service.
type Input struct {
	Now     time.Time
	Stable  uint64             // 0 for none
	Rollout *wire.RolloutInfo  // the last rollout of the service; nil for none
	Members []fleet.Member     // its connected zathrases
	// Preparing: a prepare that was sent and not answered, by generation.
	Preparing map[wire.ID]uint64
	// Activating: an activate that was sent and that no report shows yet.
	Activating map[wire.ID]uint64
	// Failed: the last failure of a zathras, if it had one.
	Failed         map[wire.ID]Failure
	StableAfter    time.Duration // 30 seconds
	PrepareTimeout time.Duration // 30 minutes
	RetryAfter     time.Duration // 30 seconds
}

type Command struct {
	Zathras    wire.ID
	Activate   bool // false: prepare
	Generation uint64
}

type Decision struct {
	Commands []Command
	// State is the state the rollout is to be moved to, with Reason, or ""
	// when it stays as it is.
	State  wire.RolloutState
	Reason string
	// The counts of the rollout, for wire.RolloutInfo.
	Prepared, Activated, Awaited int
	// Recheck is when to decide again if nothing happens before, or the
	// zero time.
	Recheck time.Time
}

// Decide says what section 6.2 of the spec asks for. It has no state and
// reads no clock: the same Input gives the same Decision.
func Decide(in Input) Decision
```

`Decide`, where the spec leaves room:

- It never sends a zathras a command while `Preparing` or `Activating` has
  that zathras.
- A zathras is **done** by `State.Generation`, `State.Phase` and
  `State.RunningFor` alone. No time a zathras reports is compared with `Now`.
- In a rollout, a `Failed` entry for the target halts it. The reason is
  `<name>: <reason>`.
- With no rollout under way, a zathras with a `Failed` entry for the
  generation it would be sent is left alone until `At + RetryAfter`, which
  becomes `Recheck`.
- `Recheck` is also when the first zathras that is under way will have run
  for `StableAfter`, and when a rollout that is `preparing` reaches
  `Created + PrepareTimeout`.
- A `Failed` entry halts a rollout only when its `Generation` is the target
  and its `At` is not before `Rollout.Created`: a failure that is older
  than the rollout is not the rollout's. Whoever fills `Failed` therefore
  takes `At` from the clock that `Created` and `Now` come from.
- When every member has prepared the target, the same decision goes on to
  the activating part, so the `State` of a decision for a `preparing`
  rollout may be `activating`, `completed` or `halted`.
- A zathras with an entry in `Preparing` or `Activating`, for whatever
  generation, is sent nothing. What a zathras reports as `Preparing` is
  not looked at.

### draal

```go
package draal

type Config struct {
	DB             *db.DB
	Authz          authz.Authorizer
	Store          wire.StoreInfo
	// StoreDir is the directory a rollout's binary has to be in. "":
	// /nix/store. The tests of the whole point it at a temporary directory.
	StoreDir       string
	Settle         time.Duration // 0: 15 seconds
	StableAfter    time.Duration // 0: 30 seconds
	PrepareTimeout time.Duration // 0: 30 minutes
	RetryAfter     time.Duration // 0: 30 seconds
	GoneAfter      time.Duration // 0: 10 seconds
	WatchEvery     time.Duration // 0: 1 second
	History        time.Duration // 0: 15 minutes
	Now            func() time.Time // nil: time.Now
	Log            *slog.Logger
}

// Shell is a shell session as an operator has it.
type Shell interface {
	io.Reader // output; io.EOF when the shell has ended
	io.Writer // input
	Resize(cols, rows uint16) error
	// Wait returns the exit status when the shell has ended.
	Wait(ctx context.Context) (int, error)
	Close() error
}

// Controller is what an operator can do. The handler of the protocol and,
// in stage 2, the HTTP API are written against it.
type Controller interface {
	Store(ctx context.Context) (wire.StoreInfo, error)
	Rollout(ctx context.Context, by wire.ID, req wire.RolloutRequest) (wire.RolloutInfo, error)
	Rollback(ctx context.Context, by wire.ID, req wire.RollbackRequest) (wire.RolloutInfo, error)
	// Watch sends the fleet at once and after every change, at most once
	// in WatchEvery, until ctx is done, and then closes the channel.
	Watch(ctx context.Context) (<-chan wire.Fleet, error)
	Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error)
	History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error)
	// Logs sends the ring of the zathras, and with follow what follows,
	// and closes the channel at the end or when ctx is done.
	Logs(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error)
	Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (Shell, error)
	// ServiceOf returns the service of a connected zathras, for Authorize.
	ServiceOf(zathras wire.ID) (string, bool)
}

type Draal struct{ /* unexported */ }

func New(cfg Config) *Draal // *Draal is a Controller

// Serve accepts connections from l and runs the reconciler until ctx is
// done. Then it closes every connection and returns nil. It does not
// close l. Call it once.
func (d *Draal) Serve(ctx context.Context, l node.Listener) error
```

`Logs`, `Shell` and `History` for a zathras that is not connected are
answered `unavailable` to an operator, and `unauthorized` to anyone else,
who is not to learn which IDs are connected. The `ctx` of `Shell` bounds
the start of the session alone. `Shell.Wait` returns once `Read` has
reached the end, since the exit status comes behind the output.

What `Rollout` refuses with `bad-request`, before the database is touched:
a name that is no service name; a `Binary` that is not an absolute path
under `/nix/store/` with a file name behind the store path; a `Closure`
that is empty or does not hold the store path of the binary; a name in the
closure that has a `/` in it; a `Parallel` below 0. `Rollback` refuses the
name and `Parallel` alike, and answers `not-found`, and `bad-request` for
`db.ErrNoStable` and `db.ErrNothingToRollBack`.

### operator

```go
package operator

// Client talks to draal on one connection. It is safe for concurrent use.
type Client struct{ /* unexported */ }

func New(conn node.Conn) *Client

// Close closes the connection. A second Close does nothing.
func (c *Client) Close() error

// Watch and Logs close their channel when the stream has ended and when
// ctx is done. History, Logs and Shell put their zathras into the request.
// Watch returns when the first fleet has come, and Logs when the first
// line has come or the stream has ended without one, so that a refusal
// of draal is the error they return.
func (c *Client) Store(ctx context.Context) (wire.StoreInfo, error)
func (c *Client) Rollout(ctx context.Context, req wire.RolloutRequest) (wire.RolloutInfo, error)
func (c *Client) Rollback(ctx context.Context, req wire.RollbackRequest) (wire.RolloutInfo, error)
func (c *Client) Watch(ctx context.Context) (<-chan wire.Fleet, error)
func (c *Client) Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error)
func (c *Client) History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error)
func (c *Client) Logs(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error)
func (c *Client) Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (*Shell, error)

// Shell is a session. It has the methods of draal.Shell.
type Shell struct{ /* unexported */ }

func (s *Shell) Read(p []byte) (int, error)
func (s *Shell) Write(p []byte) (int, error)
func (s *Shell) Resize(cols, rows uint16) error
func (s *Shell) Wait(ctx context.Context) (int, error)
func (s *Shell) Close() error

// MinIDPrefix is how many characters the beginning of an endpoint ID
// needs to name a zathras. Anything shorter is the beginning of a name.
const MinIDPrefix = 4

// Resolve finds the zathras of a service that name names: the one whose
// name is name; or else the one whose name, or whose ID, begins with
// name; or, with name "", the only one. Any other outcome is an error
// that lists the zathrases of the service with name and ID.
func Resolve(f wire.Fleet, service, name string) (wire.Zathras, error)
```

### closure

```go
package closure

const StoreDir = "/nix/store"

// Binary takes the PATH of a rollout. A path inside StoreDir is taken as
// written; any other is resolved through its symbolic links and has to
// end inside StoreDir. It returns the binary and the store path it is in
// (/nix/store/<name>). The binary has to be a regular file, through its
// symbolic links.
func Binary(path string) (binary, storePath string, err error)

// CheckLinux refuses a Mach-O binary.
func CheckLinux(binary string) error

// Of returns the store paths of the closure of storePath, sorted, by
// `nix-store --query --requisites`.
func Of(ctx context.Context, storePath string) ([]string, error)

// Names returns the names of store paths: what follows /nix/store/.
func Names(storePaths []string) []string
```

### push

```go
package push

type Config struct {
	Key      node.Key         // the operator's
	Server   wire.ID          // the jaccard-store server
	Addrs    []netip.AddrPort // for tests: where the server is
	Prefix   string
	Jobs     int    // 0: 5
	TempDir  string // "": the system's
	Progress Progress // nil: none
}

// Progress is told what a push does. Its methods are called from several
// goroutines.
type Progress interface {
	// Found says how many of the store paths the server has already, and
	// how many are to be pushed.
	Found(present, missing int)
	// Pushed says that one store path was pushed, and how many bytes its
	// pack came to, compressed: 0 when the server had a pack of the same
	// content already.
	Pushed(storePath string, bytes uint64)
}

type Result struct {
	Pushed  int
	Present int
}

// Push makes the server hold the reference Prefix+name for every store
// path, where name is the last element of the path. A store path is a
// directory or a regular file. One that is a symbolic link cannot be a
// reference: Push refuses it, before anything is pushed, with an error
// that names it. Push ends at the first path that fails, with that path
// in the error, and the Result beside an error counts what was done
// until then.
func Push(ctx context.Context, cfg Config, storePaths []string) (Result, error)
```

### term

```go
package term

// Session is what Attach joins the terminal to. *operator.Shell is one.
type Session interface {
	io.Reader
	io.Writer
	Resize(cols, rows uint16) error
	Wait(ctx context.Context) (int, error)
}

// Detach is the byte that ends Attach without ending the shell: Ctrl-].
const Detach = 0x1d

// Attach copies in to the session and the session to out until the shell
// has ended or, on a terminal, Detach was typed. When in is a terminal it
// is made raw, its size is sent at once and after every change, and it is
// restored before Attach returns. When in is no terminal it is copied as
// it is, and at its end the byte 0x04 is sent so that the shell ends.
func Attach(ctx context.Context, in *os.File, out io.Writer, s Session) (exit int, detached bool, err error)

// Size returns the size of the terminal f is, or 80 and 24.
func Size(f *os.File) (cols, rows uint16)
```

### cli

```go
package cli

// Version is set by the linker: -X github.com/fables-for-robots/great-machine/cli.Version=v0.1.0
var Version = "dev"

// Run runs the command line and returns the exit status: 0, 1 for a
// failure, 2 for a command line that was not valid.
func Run(args []string, stdout, stderr io.Writer) int
```

Inside the package (Task 1 defines them, Task 21 builds on them):

- `Run` owns the signals. The `c.Context` of every command is done at the
  first SIGINT or SIGTERM, and a second one kills the process. No command
  installs a handler of its own.
- `func usageError(format string, args ...any) error` is what a command's
  `Action` returns for a command line that is not valid; `Run` then
  returns 2. No flag is `Required: true`: the `Action` checks and returns
  a `usageError`.
- `func commands() []*cli.Command` lists the commands, each a
  `func xCommand() *cli.Command` in a file of its own.
- `func newApp(stdout, stderr io.Writer) *cli.App` and
  `func run(ctx context.Context, app *cli.App, args []string) int` let a
  test run a command without signals.
- A command writes to `c.App.Writer` and `c.App.ErrWriter`, and returns
  its error without printing it.
- Flags come before the arguments. `--parallel` has the variable
  `GREAT_MACHINE_PARALLEL`, `--detach` has `GREAT_MACHINE_DETACH` and
  `--follow`, `-f` has `GREAT_MACHINE_FOLLOW`.

---

## Tasks

Each task is a file of its own in
[`2026-10-09-great-machine-stage-1/`](2026-10-09-great-machine-stage-1/),
with its complete code, its tests and its commands. Whoever carries a task
out reads this file down to here, and the task's file. The tasks are done
in the order of their numbers; "needs" names the tasks whose packages a
task uses.

| # | Task | Delivers | Needs |
|---|---|---|---|
| 1 | [Scaffold](2026-10-09-great-machine-stage-1/task-01-scaffold.md) | the module, `great-machine version`, the Nix build by gonixgo, the dev shells, CI | |
| 2 | [wire](2026-10-09-great-machine-stage-1/task-02-wire.md) | messages, framing, error codes, names of services | 1 |
| 3 | [node](2026-10-09-great-machine-stage-1/task-03-node.md) | keys, listening and dialing over go-iroh; `nodetest`, a network in memory | 2 |
| 4 | [logring](2026-10-09-great-machine-stage-1/task-04-logring.md) | the ring of the last 400 lines and its followers | 2 |
| 5 | [supervisor](2026-10-09-great-machine-stage-1/task-05-supervisor.md) | starting, stopping and restarting the service, backoff; `testservice` | 4 |
| 6 | [stats](2026-10-09-great-machine-stage-1/task-06-stats.md) | samples from cgroup v2 and `/proc/net/dev` | 2 |
| 7 | [preload](2026-10-09-great-machine-stage-1/task-07-preload.md) | the client of the sidecar's preload endpoint | 1 |
| 8 | [ptyshell](2026-10-09-great-machine-stage-1/task-08-ptyshell.md) | shell sessions on a pseudo-terminal | 1 |
| 9 | [health](2026-10-09-great-machine-stage-1/task-09-health.md) | `GET /healthz` | 2 |
| 10 | [reaper](2026-10-09-great-machine-stage-1/task-10-reaper.md) | the duties of process 1, and their test in a container | 1 |
| 11 | [zathras](2026-10-09-great-machine-stage-1/task-11-zathras.md) | the agent: registering, reporting, answering prepare, activate, logs and shell | 3, 5, 6, 7, 8 |
| 12 | [db](2026-10-09-great-machine-stage-1/task-12-db.md) | services, generations and rollouts in SQLite through sqlc | 2 |
| 13 | [authz](2026-10-09-great-machine-stage-1/task-13-authz.md) | `Authorize` and the allowlist | 2 |
| 14 | [fleet](2026-10-09-great-machine-stage-1/task-14-fleet.md) | the connected zathrases, their reports and samples | 2 |
| 15 | [rollout](2026-10-09-great-machine-stage-1/task-15-rollout.md) | `Decide`, the reconciler as a function | 14 |
| 16 | [draal](2026-10-09-great-machine-stage-1/task-16-draal.md) | the controller: sessions, the reconciler loop, watch, logs and shell relays, the handler of the protocol | 3, 12, 13, 14, 15 |
| 17 | [operator](2026-10-09-great-machine-stage-1/task-17-operator.md) | the client of draal, and `Resolve` | 3 |
| 18 | [closure](2026-10-09-great-machine-stage-1/task-18-closure.md) | the binary of a rollout, its store path and its closure | 1 |
| 19 | [push](2026-10-09-great-machine-stage-1/task-19-push.md) | store paths to jaccard-store | 3 |
| 20 | [term](2026-10-09-great-machine-stage-1/task-20-term.md) | a remote shell session on the local terminal | 1 |
| 21 | [cli](2026-10-09-great-machine-stage-1/task-21-cli.md) | the commands `draal`, `zathras`, `id`, `rollout`, `rollback`, `status`, `logs`, `shell` | 2 to 20 |
| 22 | [e2e](2026-10-09-great-machine-stage-1/task-22-e2e.md) | tests of the whole over go-iroh on loopback | 3, 5, 9, 11, 16, 17 |
| 23 | [Delivery](2026-10-09-great-machine-stage-1/task-23-delivery.md) | the image, `deploy/`, the release workflow, the README | 21 |

## What was run while this plan was written

The code of the tasks is not a sketch. It was written in scratch modules,
and the Go files, SQL files and scripts of all 23 tasks were then put
together into one module outside the repository, as the tasks leave it:

- `go build ./...`, `go vet ./...` and `gofmt -l .` are clean, and
  `go test ./...` passed twice with every package's tests running at once,
  and once more with `-race`, on macOS (arm64) with Go 1.26.7.
- The code that was tested is, file for file, the text of the tasks.
- `node` ran against go-iroh v0.3.0 on loopback, `push` against a
  jaccard-store v0.7.0 server in the test's process, `db` against what
  sqlc 1.31.1 generates, `e2e` against all of it.
- On Linux, in containers: `logring`, `supervisor`, `stats`, `ptyshell`,
  the shell tests of `zathras`, the piped shell of `term` against busybox,
  and `scripts/test-pid1.sh` as a whole.
- The Nix build by gonixgo ran on aarch64-darwin, and the image was built
  and started on aarch64-linux, with a store mounted over `/nix/store`.

What was not run, and is seen for the first time when the tasks are
carried out:

- Both GitHub workflows on GitHub. They pass `actionlint`.
- The image for amd64. It was evaluated.
- `node` without `Local` and `Addrs`: announcing, looking a server up, a
  relay. That code follows jaccard-store's, which does this every day.
- The commands `draal`, `zathras` and `shell` as whole programs against
  each other. They are thin, compile, and stand on what `e2e` tests.
- Anything on amd64, and the Go tests of `node`, `draal`, `e2e`, `push`
  and `cli` on Linux.
- The sidecar serving a store path that is a single file.

Some steps name the message a test is expected to fail with before its
code is there. In tasks 3, 16, 17 and 22 those messages were reasoned and
not captured. Where the compiler words one differently, what counts is
that the step fails for the reason it names.
