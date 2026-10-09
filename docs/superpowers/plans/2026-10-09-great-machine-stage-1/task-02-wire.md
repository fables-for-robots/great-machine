### Task 2: wire — messages, framing, error codes, names of services

**Files:**
- Create: `wire/frame.go` (the package comment, `MaxFrame`, the CBOR modes, `Write`, `Read`)
- Create: `wire/messages.go` (`ALPN`, `ID` and every message, as the contract has them)
- Create: `wire/error.go` (`Code`, `Error`, `Errorf`, `CodeOf`, `WriteError`, `ReadResponse`, `Call`)
- Create: `wire/service.go` (`ValidService`)
- Test: `wire/frame_test.go`, `wire/error_test.go`, `wire/messages_test.go`, `wire/service_test.go`
- Modify: `go.mod`, `go.sum` (`github.com/fxamacker/cbor/v2 v2.9.2`, if it is not there yet)

**Interfaces:**
- Consumes: nothing of the other tasks. `github.com/fxamacker/cbor/v2` v2.9.2, the version jaccard-store uses.
- Produces: the whole `wire` section of the contract, exactly: `ALPN`, `MaxFrame`, `ID`, `Code` and its five codes, `Error`, `Errorf`, `CodeOf`, `Write`, `Read`, `ReadResponse`, `Call`, `WriteError`, `ValidService`, `Request`, `Response`, and every message type with `RolloutState.UnderWay`.

What the contract leaves open and this task settles; the other tasks may rely on it:

- A `time.Time` travels as RFC 3339 text with nanoseconds, in UTC. `Read` returns it in UTC with the same instant; the zone is not kept. The zero time travels as CBOR null and comes back as the zero time (`IsZero()` is true).
- `Write` hands the whole frame to the writer in **one** call of its `Write`.
- `Read` returns `io.EOF` and `io.ErrUnexpectedEOF` unwrapped (`err == io.EOF` holds), and any other error of the reader wrapped with `%w`.
- `WriteError` of an error that wraps a `*wire.Error` writes that `*wire.Error` as it is, without the text that was wrapped around it.
- **A string that is not valid UTF-8 is written by `Write` without complaint and makes `Read` fail for the whole message** (`cbor: invalid UTF-8 string`). Nothing that is sent may hold one. For log lines the ring of Task 4 sees to that; `TestTextThatIsNoUTF8IsRefusedByTheReader` pins the fact that makes it necessary.
- A field the reader does not know is skipped.

- [ ] **Step 1: Add the CBOR library**

Skip this step if `go.mod` already requires `github.com/fxamacker/cbor/v2`.

Run: `go get github.com/fxamacker/cbor/v2@v2.9.2`
Expected: `go: added github.com/fxamacker/cbor/v2 v2.9.2` (and `github.com/x448/float16 v0.8.4`)

- [ ] **Step 2: Write the failing test of the framing**

`wire/frame_test.go`:

```go
package wire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// countingReader counts the bytes that were read through it.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func TestWriteReadSeveralFramesOnOneStream(t *testing.T) {
	var buf bytes.Buffer
	want := []LogLine{
		{Stream: "stdout", Text: "one"},
		{Stream: "stderr", Text: "two"},
		{Stream: "zathras", Lost: 3},
	}
	for i := range want {
		if err := Write(&buf, &want[i]); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	for i := range want {
		var got LogLine
		if err := Read(&buf, &got); err != nil {
			t.Fatalf("Read %d: %v", i, err)
		}
		if got != want[i] {
			t.Errorf("frame %d: got %+v, want %+v", i, got, want[i])
		}
	}
	var after LogLine
	if err := Read(&buf, &after); err != io.EOF {
		t.Errorf("Read after the last frame: got %v, want io.EOF", err)
	}
}

func TestWriteIsOneWriteOfHeaderAndBody(t *testing.T) {
	var w writeCounter
	if err := Write(&w, &LogLine{Stream: "stdout", Text: "x"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if w.calls != 1 {
		t.Errorf("Write called the writer %d times, want 1", w.calls)
	}
	frame := w.buf.Bytes()
	if len(frame) < headerLen {
		t.Fatalf("frame of %d bytes", len(frame))
	}
	if size := binary.BigEndian.Uint32(frame); int(size) != len(frame)-headerLen {
		t.Errorf("header says %d bytes, the body has %d", size, len(frame)-headerLen)
	}
}

// writeCounter counts the calls of Write.
type writeCounter struct {
	buf   bytes.Buffer
	calls int
}

func (w *writeCounter) Write(p []byte) (int, error) {
	w.calls++
	return w.buf.Write(p)
}

// A byte string of 65536 bytes or more is encoded behind five bytes, so
// this many bytes make a message of exactly MaxFrame.
const largestBytes = MaxFrame - 5

func TestWriteAtTheFrameLimit(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, make([]byte, largestBytes)); err != nil {
		t.Fatalf("Write of exactly MaxFrame: %v", err)
	}
	if got, want := buf.Len(), headerLen+MaxFrame; got != want {
		t.Fatalf("frame of %d bytes, want %d", got, want)
	}
	var back []byte
	if err := Read(&buf, &back); err != nil {
		t.Fatalf("Read of exactly MaxFrame: %v", err)
	}
	if len(back) != largestBytes {
		t.Errorf("read %d bytes, want %d", len(back), largestBytes)
	}
}

func TestWriteOverTheFrameLimit(t *testing.T) {
	var buf bytes.Buffer
	err := Write(&buf, make([]byte, largestBytes+1))
	if err == nil {
		t.Fatal("Write of MaxFrame+1: no error")
	}
	if !strings.Contains(err.Error(), "4194305") {
		t.Errorf("the error does not say the size 4194305: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("%d bytes were written of a message that is too large", buf.Len())
	}
}

func TestReadRefusesAFrameThatAnnouncesTooMuch(t *testing.T) {
	var header [headerLen]byte
	binary.BigEndian.PutUint32(header[:], MaxFrame+1)
	stream := append(header[:], make([]byte, 1024)...)
	r := &countingReader{r: bytes.NewReader(stream)}

	var msg LogLine
	err := Read(r, &msg)
	if err == nil {
		t.Fatal("Read: no error")
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("Read: got %v, want an error about the size", err)
	}
	if !strings.Contains(err.Error(), "4194305") {
		t.Errorf("the error does not say the size 4194305: %v", err)
	}
	if r.n != headerLen {
		t.Errorf("Read took %d bytes, want the %d of the header and no more", r.n, headerLen)
	}
}

func TestReadAtTheEnd(t *testing.T) {
	var whole bytes.Buffer
	if err := Write(&whole, &LogLine{Stream: "stdout", Text: "a line"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	frame := whole.Bytes()

	tests := []struct {
		name string
		data []byte
		want error
	}{
		{"nothing", nil, io.EOF},
		{"one byte of the header", frame[:1], io.ErrUnexpectedEOF},
		{"three bytes of the header", frame[:3], io.ErrUnexpectedEOF},
		{"the header and no body", frame[:headerLen], io.ErrUnexpectedEOF},
		{"the header and half the body", frame[:headerLen+(len(frame)-headerLen)/2], io.ErrUnexpectedEOF},
		{"all but the last byte", frame[:len(frame)-1], io.ErrUnexpectedEOF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var msg LogLine
			if err := Read(bytes.NewReader(tt.data), &msg); err != tt.want {
				t.Errorf("Read: got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestReadWrapsAnErrorOfTheReader(t *testing.T) {
	broken := errors.New("the stream broke")
	var msg LogLine
	err := Read(io.MultiReader(bytes.NewReader([]byte{0, 0}), errReader{broken}), &msg)
	if !errors.Is(err, broken) {
		t.Errorf("Read: got %v, want an error that wraps %v", err, broken)
	}
}

// errReader fails every Read with its error.
type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestReadRefusesAnEmptyFrame(t *testing.T) {
	var msg LogLine
	err := Read(bytes.NewReader(make([]byte, headerLen)), &msg)
	if err == nil || err == io.EOF || err == io.ErrUnexpectedEOF {
		t.Errorf("Read of a frame of 0 bytes: got %v, want an error of its own", err)
	}
}

func TestReadRefusesWhatIsNoMessage(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, "text where a map belongs"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var msg LogLine
	if err := Read(&buf, &msg); err == nil {
		t.Error("Read of a text into a LogLine: no error")
	}
}

func TestReadSkipsAFieldItDoesNotKnow(t *testing.T) {
	type newer struct {
		Stream string `json:"stream"`
		Text   string `json:"text"`
		Colour string `json:"colour"`
	}
	var buf bytes.Buffer
	if err := Write(&buf, &newer{Stream: "stdout", Text: "x", Colour: "green"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var got LogLine
	if err := Read(&buf, &got); err != nil {
		t.Fatalf("Read of a message with a field that is not known: %v", err)
	}
	if got.Stream != "stdout" || got.Text != "x" {
		t.Errorf("got %+v", got)
	}
}

func TestTimeKeepsItsInstantAndItsNanoseconds(t *testing.T) {
	zone := time.FixedZone("somewhere", 5*3600+1800)
	tests := []struct {
		name string
		at   time.Time
	}{
		{"UTC", time.Date(2026, 10, 9, 12, 34, 56, 123456789, time.UTC)},
		{"another zone", time.Date(2026, 10, 9, 12, 34, 56, 987654321, zone)},
		{"one nanosecond", time.Date(2026, 1, 1, 0, 0, 0, 1, time.UTC)},
		{"before 1970", time.Date(1969, 7, 20, 20, 17, 40, 5, time.UTC)},
		{"now, with its monotonic reading", time.Now()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Write(&buf, &LogLine{At: tt.at, Stream: "stdout"}); err != nil {
				t.Fatalf("Write: %v", err)
			}
			var got LogLine
			if err := Read(&buf, &got); err != nil {
				t.Fatalf("Read: %v", err)
			}
			if !got.At.Equal(tt.at) {
				t.Errorf("got %v, want %v", got.At, tt.at)
			}
			if got.At.Nanosecond() != tt.at.Nanosecond() {
				t.Errorf("got %d ns, want %d", got.At.Nanosecond(), tt.at.Nanosecond())
			}
		})
	}
}

func TestZeroTimeStaysZero(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, &State{Phase: PhaseIdle}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var got State
	if err := Read(&buf, &got); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !got.RestartAt.IsZero() {
		t.Errorf("RestartAt: got %v, want the zero time", got.RestartAt)
	}
}

func TestDurationRoundTrips(t *testing.T) {
	for _, d := range []time.Duration{0, 1, 1500 * time.Millisecond, 36 * time.Hour, -time.Second} {
		var buf bytes.Buffer
		if err := Write(&buf, &Exit{Ran: d}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		var got Exit
		if err := Read(&buf, &got); err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got.Ran != d {
			t.Errorf("got %v, want %v", got.Ran, d)
		}
	}
}

func TestBytesAreAByteString(t *testing.T) {
	data := []byte{0, 1, 0xff, 0xfe, '\n'}
	var buf bytes.Buffer
	if err := Write(&buf, &ShellInput{Data: data}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// A map of one pair, the text "data", and a byte string of 5 bytes
	// (major type 2: 0x45), not an array of numbers.
	if want := append([]byte{0xa1, 0x64, 'd', 'a', 't', 'a', 0x45}, data...); !bytes.Equal(buf.Bytes()[headerLen:], want) {
		t.Errorf("encoded as % x, want % x", buf.Bytes()[headerLen:], want)
	}
	var got ShellInput
	if err := Read(&buf, &got); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(got.Data, data) {
		t.Errorf("got % x, want % x", got.Data, data)
	}
}
```

- [ ] **Step 3: Run the test to see it fail**

Run: `go test ./wire/ -v`
Expected: FAIL — the package does not compile: `undefined: LogLine`, `undefined: Write`, `undefined: headerLen`

- [ ] **Step 4: Write the framing, the messages and the type of a refusal**

The messages are the contract's, word for word, with a comment on each. `Response` holds an `*Error`, so the type `Error` is written now; what is done with it comes in Step 8.

`wire/frame.go`:

```go
// Package wire is what travels between the parts of great-machine: the
// messages, how one is framed on a stream, the codes of a refusal, and
// what a service may be called.
//
// A message is CBOR behind its length as four bytes, big-endian. The asking
// side of a stream writes one Request and reads Responses. The package
// knows nothing of iroh: Write and Read take any io.Writer and io.Reader.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/fxamacker/cbor/v2"
)

// MaxFrame is the largest message, in bytes of CBOR.
const MaxFrame = 4 << 20

// headerLen is the length of what precedes a message: its length as four
// bytes, big-endian.
const headerLen = 4

// encMode and decMode are how every message is written and read.
//
// A time.Time travels as text with its nanoseconds, in UTC, so the instant
// survives and the zone does not. The zero time travels as null and is
// read back as the zero time. A time.Duration is an integer of
// nanoseconds, and a []byte a byte string. A field the reader does not
// know is skipped, so that a newer peer may send more.
//
// A string that is no valid UTF-8 is written as it is, and the reader
// refuses the whole message for it. Nothing that is sent may hold one: the
// ring of logring replaces such bytes before a line gets its text.
var (
	encMode = mustEncMode()
	decMode = mustDecMode()
)

func mustEncMode() cbor.EncMode {
	mode, err := cbor.EncOptions{
		Time:    cbor.TimeRFC3339NanoUTC,
		TimeTag: cbor.EncTagNone,
	}.EncMode()
	if err != nil {
		panic(fmt.Sprintf("wire: encoding mode: %v", err))
	}
	return mode
}

func mustDecMode() cbor.DecMode {
	mode, err := cbor.DecOptions{
		ExtraReturnErrors: cbor.ExtraDecErrorNone,
	}.DecMode()
	if err != nil {
		panic(fmt.Sprintf("wire: decoding mode: %v", err))
	}
	return mode
}

// Write writes msg as one frame. A message of more than MaxFrame is not
// written, and the error says how large it was.
//
// The frame is handed to w in one call of its Write, so frames that
// several goroutines write to one stream are not mixed if w itself takes
// one Write at a time.
func Write(w io.Writer, msg any) error {
	body, err := encMode.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encode %T: %w", msg, err)
	}
	if len(body) > MaxFrame {
		return fmt.Errorf("%T of %d bytes is more than the %d a frame may hold", msg, len(body), MaxFrame)
	}
	frame := make([]byte, headerLen+len(body))
	binary.BigEndian.PutUint32(frame, uint32(len(body)))
	copy(frame[headerLen:], body)
	if _, err := w.Write(frame); err != nil {
		return fmt.Errorf("write frame: %w", err)
	}
	return nil
}

// Read reads one frame into msg, which is a pointer to a zero value. It
// returns io.EOF when r ends before the first byte of a frame, and
// io.ErrUnexpectedEOF when it ends inside one. A frame that announces more
// than MaxFrame is an error, and nothing of it is read.
//
// io.EOF and io.ErrUnexpectedEOF are returned as they are, and an error of
// r is wrapped, so the caller finds each with errors.Is.
func Read(r io.Reader, msg any) error {
	var header [headerLen]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return err
		}
		return fmt.Errorf("read frame: %w", err)
	}
	size := binary.BigEndian.Uint32(header[:])
	if size > MaxFrame {
		return fmt.Errorf("a frame of %d bytes is announced, more than the %d a frame may hold", size, MaxFrame)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return io.ErrUnexpectedEOF
		}
		return fmt.Errorf("read frame: %w", err)
	}
	if size == 0 {
		return errors.New("a frame of 0 bytes holds no message")
	}
	if err := decMode.Unmarshal(body, msg); err != nil {
		return fmt.Errorf("decode %T: %w", msg, err)
	}
	return nil
}
```

`wire/messages.go`:

```go
package wire

import "time"

// ALPN is the protocol of every iroh connection of great-machine.
const ALPN = "great-machine/1"

// ID is an endpoint ID: 64 lowercase hexadecimal characters. node.ParseID
// makes one out of text; everywhere else an ID is taken as it is.
type ID string

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

// Phase is what a zathras is doing with its service.
type Phase string

// The phases of a zathras.
const (
	// PhaseIdle: the zathras has no generation.
	PhaseIdle Phase = "idle"
	// PhaseRunning: the service runs.
	PhaseRunning Phase = "running"
	// PhaseStopping: the service was told to end and has not ended.
	PhaseStopping Phase = "stopping"
	// PhaseBackoff: the service has ended and waits to be started.
	PhaseBackoff Phase = "backoff"
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

// Register is the request of a zathras to be counted among those of its
// service. After Registered it writes a State as a frame of its own for
// every report.
type Register struct {
	Service string `json:"service"`
	Name    string `json:"name"`
	Version string `json:"version"`
	State   State  `json:"state"`
}

// Registered answers Register.
type Registered struct{}

// Prepare asks a zathras to make the closure of a generation present.
type Prepare struct {
	Generation uint64   `json:"generation"`
	Binary     string   `json:"binary"`
	Closure    []string `json:"closure"` // names of store paths, without /nix/store/
}

// Prepared answers Prepare when the closure is present.
type Prepared struct{}

// Activate asks a zathras to run a generation in place of what it runs.
type Activate struct {
	Generation uint64   `json:"generation"`
	Binary     string   `json:"binary"`
	Args       []string `json:"args,omitempty"`
}

// Activated answers Activate when the zathras has taken the generation as
// its own.
type Activated struct{}

// LogsRequest asks for the ring. Zathras names the zathras when draal is
// asked, and is empty when a zathras is asked.
type LogsRequest struct {
	Zathras ID   `json:"zathras,omitempty"`
	Follow  bool `json:"follow,omitempty"`
}

// LogLine is a line of the ring. One with Lost set has no text: it says
// that so many lines were lost before the line that follows it.
//
// Text is valid UTF-8. The ring sees to that; a message with a text that
// is not is refused by Read.
type LogLine struct {
	At     time.Time `json:"at"`
	Stream string    `json:"stream"` // "stdout", "stderr" or "zathras"
	Text   string    `json:"text,omitempty"`
	Lost   uint64    `json:"lost,omitempty"`
}

// ShellRequest asks for a shell on a pseudo-terminal of the given size.
// Zathras names the zathras when draal is asked, and is empty when a
// zathras is asked.
type ShellRequest struct {
	Zathras ID     `json:"zathras,omitempty"`
	Cols    uint16 `json:"cols"`
	Rows    uint16 `json:"rows"`
	Term    string `json:"term,omitempty"`
}

// WinSize is the size of a terminal.
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

// StoreRequest asks where closures are to be pushed.
type StoreRequest struct{}

// StoreInfo is the jaccard-store server of the fleet and the prefix of the
// references a push makes there.
type StoreInfo struct {
	Server ID     `json:"server"`
	Prefix string `json:"prefix"`
}

// RolloutRequest asks for a rollout of a new generation of a service.
type RolloutRequest struct {
	Service  string   `json:"service"`
	Binary   string   `json:"binary"`
	Args     []string `json:"args,omitempty"`
	Closure  []string `json:"closure"`
	Parallel int      `json:"parallel"`
}

// RollbackRequest asks for a rollout back to the stable generation of a
// service.
type RollbackRequest struct {
	Service  string `json:"service"`
	Parallel int    `json:"parallel"`
}

// RolloutState is how far a rollout is.
type RolloutState string

// The states of a rollout.
const (
	// RolloutPreparing: the zathrases are making the closure present.
	RolloutPreparing RolloutState = "preparing"
	// RolloutActivating: the zathrases are moved to the generation.
	RolloutActivating RolloutState = "activating"
	// RolloutCompleted: every zathras runs the generation.
	RolloutCompleted RolloutState = "completed"
	// RolloutHalted: the rollout was stopped, and Reason says why.
	RolloutHalted RolloutState = "halted"
	// RolloutSuperseded: a newer rollout took its place.
	RolloutSuperseded RolloutState = "superseded"
)

// UnderWay says whether s is preparing or activating.
func (s RolloutState) UnderWay() bool {
	return s == RolloutPreparing || s == RolloutActivating
}

// RolloutInfo is a rollout as draal records it.
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
	// Of the connected zathrases, in a Fleet alone:
	Prepared  int `json:"prepared"`
	Activated int `json:"activated"`
	Awaited   int `json:"awaited"`
}

// WatchRequest asks for the fleet, now and whenever it changes.
type WatchRequest struct{}

// Fleet is every service draal knows, with its zathrases.
type Fleet struct {
	Services []Service `json:"services"` // by name
}

// Service is a service, its last rollout and its connected zathrases.
type Service struct {
	Name      string       `json:"name"`
	Stable    uint64       `json:"stable"`            // 0 for none
	Rollout   *RolloutInfo `json:"rollout,omitempty"` // the last one
	Zathrases []Zathras    `json:"zathrases"`         // by name, then by ID
}

// Zathras is a connected zathras and what it reported last.
type Zathras struct {
	ID        ID        `json:"id"`
	Name      string    `json:"name"`
	Connected time.Time `json:"connected"`
	State     State     `json:"state"`
}

// RolloutsRequest asks for the rollouts of a service.
type RolloutsRequest struct {
	Service string `json:"service"`
}

// RolloutList answers RolloutsRequest.
type RolloutList struct {
	Rollouts []RolloutInfo `json:"rollouts"` // newest first
}

// HistoryRequest asks for the samples kept of a zathras.
type HistoryRequest struct {
	Zathras ID `json:"zathras"`
}

// SampleList answers HistoryRequest.
type SampleList struct {
	Samples []Sample `json:"samples"` // oldest first
}
```

`wire/error.go`:

```go
package wire

import "fmt"

// Code says what kind of refusal an Error is.
type Code string

// The codes of a refusal.
const (
	// CodeBadRequest: the request itself is wrong, and will be wrong again.
	CodeBadRequest Code = "bad-request"
	// CodeUnauthorized: the peer may not do this.
	CodeUnauthorized Code = "unauthorized"
	// CodeNotFound: what the request names is not there.
	CodeNotFound Code = "not-found"
	// CodeUnavailable: it cannot be done now, and may be possible later.
	CodeUnavailable Code = "unavailable"
	// CodeInternal: the answering side failed.
	CodeInternal Code = "internal"
)

// Error is a refusal, as it travels.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

// Error returns the code and the message as "<code>: <message>".
func (e *Error) Error() string {
	return string(e.Code) + ": " + e.Message
}

// Errorf returns an Error with the code and the formatted message.
func Errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `go mod tidy && go vet ./wire/ && go test ./wire/ -v`
Expected: PASS — among others `TestWriteAtTheFrameLimit`, `TestWriteOverTheFrameLimit`, `TestReadRefusesAFrameThatAnnouncesTooMuch`, `TestReadAtTheEnd` with its six cases, `TestTimeKeepsItsInstantAndItsNanoseconds`, `TestZeroTimeStaysZero`

- [ ] **Step 6: Write the failing tests of the refusals and of the messages**

`wire/error_test.go`:

```go
package wire

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestErrorText(t *testing.T) {
	err := Errorf(CodeNotFound, "no service %q", "web")
	if got, want := err.Error(), `not-found: no service "web"`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if err.Code != CodeNotFound || err.Message != `no service "web"` {
		t.Errorf("got %+v", err)
	}
}

func TestCodeOf(t *testing.T) {
	refusal := Errorf(CodeUnavailable, "not now")
	tests := []struct {
		name string
		err  error
		want Code
	}{
		{"nil", nil, ""},
		{"an Error", refusal, CodeUnavailable},
		{"a wrapped Error", fmt.Errorf("asking draal: %w", refusal), CodeUnavailable},
		{"wrapped twice", fmt.Errorf("a: %w", fmt.Errorf("b: %w", refusal)), CodeUnavailable},
		{"any other error", errors.New("disk full"), CodeInternal},
		{"the end of a stream", io.EOF, CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CodeOf(tt.err); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWriteErrorAndReadResponse(t *testing.T) {
	refusal := Errorf(CodeUnauthorized, "%s may not roll out", "abc")
	tests := []struct {
		name string
		err  error
		want Error
	}{
		{"an Error", refusal, *refusal},
		{"a wrapped Error", fmt.Errorf("x: %w", refusal), *refusal},
		{"a plain error", errors.New("disk full"), Error{Code: CodeInternal, Message: "disk full"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteError(&buf, tt.err); err != nil {
				t.Fatalf("WriteError: %v", err)
			}
			resp, err := ReadResponse(&buf)
			if resp != nil {
				t.Errorf("ReadResponse returned %+v beside the error", resp)
			}
			var got *Error
			if !errors.As(err, &got) {
				t.Fatalf("ReadResponse: got %v (%T), want a *wire.Error", err, err)
			}
			if *got != tt.want {
				t.Errorf("got %+v, want %+v", *got, tt.want)
			}
			if code := CodeOf(err); code != tt.want.Code {
				t.Errorf("CodeOf: got %q, want %q", code, tt.want.Code)
			}
		})
	}
}

func TestReadResponse(t *testing.T) {
	var buf bytes.Buffer
	want := &Response{Store: &StoreInfo{Server: "ab", Prefix: "gm-"}}
	if err := Write(&buf, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := ReadResponse(&buf)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if got.Store == nil || *got.Store != *want.Store {
		t.Errorf("got %+v, want %+v", got.Store, want.Store)
	}
	if _, err := ReadResponse(&buf); err != io.EOF {
		t.Errorf("ReadResponse at the end: got %v, want io.EOF", err)
	}
}

// pipe is a stream whose peer is played by the test: what Call writes goes
// to sent, and what it reads comes from answer.
type pipe struct {
	sent   bytes.Buffer
	answer bytes.Buffer
}

func (p *pipe) Write(b []byte) (int, error) { return p.sent.Write(b) }
func (p *pipe) Read(b []byte) (int, error)  { return p.answer.Read(b) }

func TestCall(t *testing.T) {
	t.Run("an answer", func(t *testing.T) {
		var p pipe
		if err := Write(&p.answer, &Response{Rollout: &RolloutInfo{ID: 7, Service: "web"}}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		resp, err := Call(&p, &Request{Rollback: &RollbackRequest{Service: "web", Parallel: 2}})
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if resp.Rollout == nil || resp.Rollout.ID != 7 {
			t.Errorf("got %+v", resp)
		}
		var sent Request
		if err := Read(&p.sent, &sent); err != nil {
			t.Fatalf("Read of what Call sent: %v", err)
		}
		if sent.Rollback == nil || *sent.Rollback != (RollbackRequest{Service: "web", Parallel: 2}) {
			t.Errorf("Call sent %+v", sent)
		}
	})
	t.Run("a refusal", func(t *testing.T) {
		var p pipe
		if err := WriteError(&p.answer, Errorf(CodeNotFound, "no service web")); err != nil {
			t.Fatalf("WriteError: %v", err)
		}
		resp, err := Call(&p, &Request{Rollback: &RollbackRequest{Service: "web"}})
		if resp != nil || CodeOf(err) != CodeNotFound {
			t.Errorf("got %+v, %v; want a not-found", resp, err)
		}
	})
	t.Run("no answer", func(t *testing.T) {
		var p pipe
		if _, err := Call(&p, &Request{Store: &StoreRequest{}}); err != io.EOF {
			t.Errorf("got %v, want io.EOF", err)
		}
	})
}
```

`wire/messages_test.go` writes the escapes `\u…` and `\x…` as they stand here; type them as escapes, not as the characters they stand for:

`wire/messages_test.go`:

```go
package wire

import (
	"bytes"
	"reflect"
	"testing"
	"time"
)

// fill sets v, and everything in it, to something that is not its zero
// value: every pointer points, every slice has two elements, every field
// of every struct is set. A time is in UTC, as Read returns it, so that
// reflect.DeepEqual compares what was read with what was written.
func fill(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Interface().(type) {
	case time.Time:
		v.Set(reflect.ValueOf(time.Date(2026, 10, 9, 8, 7, 6, 123456789, time.UTC)))
		return
	case time.Duration:
		v.SetInt(int64(90*time.Second + 7*time.Nanosecond))
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fill(t, v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			fill(t, v.Field(i))
		}
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 2, 2))
		for i := 0; i < v.Len(); i++ {
			fill(t, v.Index(i))
		}
	case reflect.String:
		v.SetString("text \u00e9")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(-7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(200)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1.25)
	default:
		t.Fatalf("fill: a field of kind %s, which this test does not know how to set", v.Kind())
	}
}

// roundTripEachField writes a message of the type of zero once for each of
// its fields, with that field alone set and everything in it set, and
// expects to read the same.
func roundTripEachField(t *testing.T, zero any) {
	typ := reflect.TypeOf(zero)
	for i := 0; i < typ.NumField(); i++ {
		t.Run(typ.Field(i).Name, func(t *testing.T) {
			want := reflect.New(typ)
			fill(t, want.Elem().Field(i))

			var buf bytes.Buffer
			if err := Write(&buf, want.Interface()); err != nil {
				t.Fatalf("Write: %v", err)
			}
			got := reflect.New(typ)
			if err := Read(&buf, got.Interface()); err != nil {
				t.Fatalf("Read: %v", err)
			}
			if !reflect.DeepEqual(got.Interface(), want.Interface()) {
				t.Errorf("got  %+v\nwant %+v", got.Elem().Field(i).Elem(), want.Elem().Field(i).Elem())
			}
		})
	}
}

func TestEveryRequestRoundTrips(t *testing.T) {
	roundTripEachField(t, Request{})
}

func TestEveryResponseRoundTrips(t *testing.T) {
	roundTripEachField(t, Response{})
}

func TestFramesThatAreNoResponseRoundTrip(t *testing.T) {
	// A State is a frame of its own after Registered, and a ShellInput
	// after the first ShellOutput.
	for _, zero := range []any{State{}, ShellInput{}} {
		typ := reflect.TypeOf(zero)
		t.Run(typ.Name(), func(t *testing.T) {
			want := reflect.New(typ)
			fill(t, want.Elem())
			var buf bytes.Buffer
			if err := Write(&buf, want.Interface()); err != nil {
				t.Fatalf("Write: %v", err)
			}
			got := reflect.New(typ)
			if err := Read(&buf, got.Interface()); err != nil {
				t.Fatalf("Read: %v", err)
			}
			if !reflect.DeepEqual(got.Interface(), want.Interface()) {
				t.Errorf("got  %+v\nwant %+v", got.Elem(), want.Elem())
			}
		})
	}
}

func TestAnEmptyAnswerIsStillSet(t *testing.T) {
	// Registered, Prepared and Activated have no fields. The answer is
	// told from no answer by the pointer alone.
	var buf bytes.Buffer
	if err := Write(&buf, &Response{Prepared: &Prepared{}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := ReadResponse(&buf)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if got.Prepared == nil {
		t.Error("Prepared is nil after the round trip")
	}
	if got.Registered != nil || got.Activated != nil || got.Error != nil {
		t.Errorf("more than Prepared is set: %+v", got)
	}
}

func TestShellExitOfZeroIsStillSet(t *testing.T) {
	zero := 0
	var buf bytes.Buffer
	if err := Write(&buf, &Response{Shell: &ShellOutput{Exit: &zero}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := ReadResponse(&buf)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if got.Shell == nil || got.Shell.Exit == nil || *got.Shell.Exit != 0 {
		t.Errorf("got %+v, want an exit status of 0 that is set", got.Shell)
	}
}

func TestLogLineTextRoundTrips(t *testing.T) {
	texts := []string{
		"plain",
		"",
		"the replacement character \ufffd, which the ring puts where bytes were no UTF-8",
		"\ufffd\ufffd\ufffd",
		"a NUL \x00 and an escape \x1b[31m and a bell \a",
		"a tab\tand a carriage return\r",
		"two bytes \u00e9, three \u20ac, four \U0001f600",
		"the last code point \U0010ffff and the first of the private use \ue000",
		"a byte order mark \ufeff and a noncharacter \uffff",
		"combining e\u0301 and right-to-left \u202e",
	}
	for _, text := range texts {
		var buf bytes.Buffer
		if err := Write(&buf, &Response{Line: &LogLine{Stream: "stdout", Text: text}}); err != nil {
			t.Fatalf("Write of %q: %v", text, err)
		}
		got, err := ReadResponse(&buf)
		if err != nil {
			t.Fatalf("ReadResponse of %q: %v", text, err)
		}
		if got.Line == nil || got.Line.Text != text {
			t.Errorf("got %+v, want the text %q", got.Line, text)
		}
	}
}

// Text that is no valid UTF-8 is written without complaint and refused by
// the reader, with the whole message. This is why logring replaces such
// bytes before a line gets its text: a single one would end the stream of
// whoever follows the log.
func TestTextThatIsNoUTF8IsRefusedByTheReader(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, &Response{Line: &LogLine{Stream: "stdout", Text: "a\xffb"}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := ReadResponse(&buf); err == nil {
		t.Error("ReadResponse of a text that is no UTF-8: no error")
	}
}

func TestRolloutStateUnderWay(t *testing.T) {
	tests := []struct {
		state RolloutState
		want  bool
	}{
		{RolloutPreparing, true},
		{RolloutActivating, true},
		{RolloutCompleted, false},
		{RolloutHalted, false},
		{RolloutSuperseded, false},
		{"", false},
		{"unknown", false},
	}
	for _, tt := range tests {
		if got := tt.state.UnderWay(); got != tt.want {
			t.Errorf("%q.UnderWay(): got %v, want %v", tt.state, got, tt.want)
		}
	}
}
```

- [ ] **Step 7: Run the tests to see them fail**

Run: `go test ./wire/`
Expected: FAIL — the package does not compile: `undefined: CodeOf`, `undefined: WriteError`, `undefined: ReadResponse`, `undefined: Call`

- [ ] **Step 8: Write the rest of the refusals**

Replace `wire/error.go` with:

`wire/error.go`:

```go
package wire

import (
	"errors"
	"fmt"
	"io"
)

// Code says what kind of refusal an Error is.
type Code string

// The codes of a refusal.
const (
	// CodeBadRequest: the request itself is wrong, and will be wrong again.
	CodeBadRequest Code = "bad-request"
	// CodeUnauthorized: the peer may not do this.
	CodeUnauthorized Code = "unauthorized"
	// CodeNotFound: what the request names is not there.
	CodeNotFound Code = "not-found"
	// CodeUnavailable: it cannot be done now, and may be possible later.
	CodeUnavailable Code = "unavailable"
	// CodeInternal: the answering side failed.
	CodeInternal Code = "internal"
)

// Error is a refusal, as it travels.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

// Error returns the code and the message as "<code>: <message>".
func (e *Error) Error() string {
	return string(e.Code) + ": " + e.Message
}

// Errorf returns an Error with the code and the formatted message.
func Errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// CodeOf returns the code of the *Error in err's chain, CodeInternal for
// any other error and "" for nil.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternal
}

// WriteError writes a Response that carries err. An error that has no
// *Error in its chain is written as CodeInternal with its text. One that
// has is written as that *Error, without what was wrapped around it.
func WriteError(w io.Writer, err error) error {
	var e *Error
	if !errors.As(err, &e) {
		text := "no error"
		if err != nil {
			text = err.Error()
		}
		e = &Error{Code: CodeInternal, Message: text}
	}
	return Write(w, &Response{Error: e})
}

// ReadResponse reads one Response. A Response that carries an Error is
// returned as that *Error. The end of r is returned as Read returns it.
func ReadResponse(r io.Reader) (*Response, error) {
	var resp Response
	if err := Read(r, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return &resp, nil
}

// Call writes req and reads one Response, as ReadResponse does.
func Call(s io.ReadWriter, req *Request) (*Response, error) {
	if err := Write(s, req); err != nil {
		return nil, err
	}
	return ReadResponse(s)
}
```

- [ ] **Step 9: Run the tests to see them pass**

Run: `go vet ./wire/ && go test ./wire/ -v`
Expected: PASS — among others `TestEveryRequestRoundTrips` with a case for each of the 11 fields of `Request`, `TestEveryResponseRoundTrips` with one for each of the 11 fields of `Response`, `TestWriteErrorAndReadResponse`, `TestCall`, `TestLogLineTextRoundTrips`, `TestTextThatIsNoUTF8IsRefusedByTheReader`, `TestRolloutStateUnderWay`

- [ ] **Step 10: Write the failing test of the names of services**

`wire/service_test.go`:

```go
package wire

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestValidService(t *testing.T) {
	valid := []string{
		"a",
		"web",
		"web-2",
		"a1",
		"1a",
		"a--b",
		strings.Repeat("a", 63),
	}
	for _, name := range valid {
		if err := ValidService(name); err != nil {
			t.Errorf("ValidService(%q): %v", name, err)
		}
	}

	invalid := []string{
		"",
		"Web",
		"-a",
		"a-",
		"-",
		"a_b",
		"a.b",
		"a/b",
		"../x",
		"a b",
		"wéb",
		"web\n",
		strings.Repeat("a", 64),
	}
	for _, name := range invalid {
		err := ValidService(name)
		var refusal *Error
		if !errors.As(err, &refusal) {
			t.Errorf("ValidService(%q): got %v, want a *wire.Error", name, err)
			continue
		}
		if refusal.Code != CodeBadRequest {
			t.Errorf("ValidService(%q): code %q, want %q", name, refusal.Code, CodeBadRequest)
		}
		if !strings.Contains(refusal.Message, strconv.Quote(name)) {
			t.Errorf("ValidService(%q): the message does not quote the name: %s", name, refusal.Message)
		}
	}
}
```

- [ ] **Step 11: Run the test to see it fail**

Run: `go test ./wire/ -run TestValidService`
Expected: FAIL — `undefined: ValidService`

- [ ] **Step 12: Write ValidService**

`wire/service.go`:

```go
package wire

// maxServiceName is the length of the longest DNS label.
const maxServiceName = 63

// ValidService returns nil for a DNS label, [a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?,
// and a bad-request *Error that quotes the name otherwise.
func ValidService(name string) error {
	if name == "" {
		return Errorf(CodeBadRequest, "%q is no name of a service: it is empty", name)
	}
	if len(name) > maxServiceName {
		return Errorf(CodeBadRequest, "%q is no name of a service: it has %d characters, and %d is the most", name, len(name), maxServiceName)
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-':
			if i == 0 || i == len(name)-1 {
				return Errorf(CodeBadRequest, "%q is no name of a service: it begins or ends with a hyphen", name)
			}
		default:
			return Errorf(CodeBadRequest, "%q is no name of a service: only a to z, 0 to 9 and the hyphen are allowed", name)
		}
	}
	return nil
}
```

- [ ] **Step 13: Run the tests to see them pass**

Run: `gofmt -l wire/ && go vet ./wire/ && go test ./wire/`
Expected: `gofmt` prints nothing; `ok  	github.com/fables-for-robots/great-machine/wire`

- [ ] **Step 14: Commit**

```bash
git add go.mod go.sum wire/
git commit -m "Wire: messages, framing, error codes and names of services"
```
