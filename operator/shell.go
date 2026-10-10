package operator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// shellChunk is the most input one message carries.
const shellChunk = 16 << 10

// Shell asks for a shell in the container of a zathras, and returns the
// session once the shell runs, or what draal or the zathras refused. The
// zathras is the one given, whatever req.Zathras says. ctx bounds the
// start alone: the session lasts until its Close.
func (c *Client) Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (*Shell, error) {
	req.Zathras = zathras
	s, resp, stop, err := c.open(ctx, "shell", &wire.Request{Shell: &req})
	if err != nil {
		return nil, err
	}
	if !stop() {
		s.Close()
		return nil, fmt.Errorf("operator: shell: %w", context.Cause(ctx))
	}
	if resp.Shell == nil {
		s.Close()
		return nil, unexpected("shell")
	}
	return &Shell{s: s, ended: make(chan struct{}), clientClosed: c.done}, nil
}

// Shell is a session. It has the methods of draal.Shell. Read is for one
// goroutine; Write, Resize and Close may be called from others.
//
// Close of the client the session is of ends it: Read, Write, Resize and
// Wait fail with ErrClosed, but a Wait whose exit status had come before.
type Shell struct {
	s node.Stream

	clientClosed <-chan struct{} // closed by Close of the client

	rest []byte // output that was received and not read yet; Read's alone

	ended chan struct{} // closed when exit or err is set
	exit  int
	err   error

	wmu   sync.Mutex // one message at a time
	close sync.Once
}

// Read returns output of the shell, and io.EOF once its exit status has
// come. A session that ends without one ends with an error: ErrClosed
// when Close of the client ended it.
func (s *Shell) Read(p []byte) (int, error) {
	for len(s.rest) == 0 {
		select {
		case <-s.ended:
			if s.err != nil {
				return 0, s.err
			}
			return 0, io.EOF
		default:
		}
		resp, err := wire.ReadResponse(s.s)
		var refusal *wire.Error
		switch {
		case err != nil && s.clientIsClosed():
			s.err = fmt.Errorf("operator: the shell session ended: %w", ErrClosed)
			close(s.ended)
		case err == io.EOF:
			s.err = errors.New("operator: the shell session ended without an exit status")
			close(s.ended)
		case errors.As(err, &refusal):
			s.err = refusal
			close(s.ended)
		case err != nil:
			s.err = fmt.Errorf("operator: the shell session failed: %w", err)
			close(s.ended)
		case resp.Shell == nil:
		case resp.Shell.Exit != nil:
			s.exit = *resp.Shell.Exit
			s.rest = resp.Shell.Data
			close(s.ended)
			if len(s.rest) == 0 {
				return 0, io.EOF
			}
		default:
			s.rest = resp.Shell.Data
		}
	}
	n := copy(p, s.rest)
	s.rest = s.rest[n:]
	return n, nil
}

// clientIsClosed says whether Close of the client was called.
func (s *Shell) clientIsClosed() bool {
	select {
	case <-s.clientClosed:
		return true
	default:
		return false
	}
}

// failed makes the error of a Write or a Resize: ErrClosed when Close of
// the client has ended the session, and the error of the stream otherwise.
func (s *Shell) failed(what string, err error) error {
	if s.clientIsClosed() {
		return fmt.Errorf("operator: %s: %w", what, ErrClosed)
	}
	return fmt.Errorf("operator: %s: %w", what, err)
}

// Write sends p as input of the shell.
func (s *Shell) Write(p []byte) (int, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	written := 0
	for len(p) > 0 {
		chunk := p[:min(len(p), shellChunk)]
		if err := wire.Write(s.s, &wire.ShellInput{Data: chunk}); err != nil {
			return written, s.failed("writing to the shell", err)
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}

// Resize tells the shell the new size of the terminal.
func (s *Shell) Resize(cols, rows uint16) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if err := wire.Write(s.s, &wire.ShellInput{Resize: &wire.WinSize{Cols: cols, Rows: rows}}); err != nil {
		return s.failed("resizing the shell", err)
	}
	return nil
}

// Wait returns the exit status when the shell has ended, or the error the
// session ended with instead. The status comes behind the output, so Wait
// returns only once Read has reached the end. When the client is closed
// before that, Wait returns ErrClosed, also when nobody reads.
func (s *Shell) Wait(ctx context.Context) (int, error) {
	// An end that has come counts, whatever else has happened since.
	select {
	case <-s.ended:
		return s.result()
	default:
	}
	select {
	case <-s.ended:
		return s.result()
	case <-s.clientClosed:
		return 0, fmt.Errorf("operator: the shell session ended: %w", ErrClosed)
	case <-ctx.Done():
		return 0, context.Cause(ctx)
	}
}

// result is what Wait returns once ended is closed.
func (s *Shell) result() (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	return s.exit, nil
}

// Close ends the session, and the shell if it runs. It may be called
// more than once.
func (s *Shell) Close() error {
	s.close.Do(func() { s.s.Close() })
	return nil
}
