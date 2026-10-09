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
