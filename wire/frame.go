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
