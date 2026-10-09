package draal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// A Draal is a Controller.
var _ Controller = (*Draal)(nil)

// shellChunk is the most input one ShellInput carries.
const shellChunk = 16 << 10

// lost makes of an error of a stream to a zathras the answer for an
// operator: what the zathras answered as it is, and unavailable for a
// stream that failed.
func lost(zathras wire.ID, err error) error {
	var refusal *wire.Error
	if errors.As(err, &refusal) {
		return refusal
	}
	return wire.Errorf(wire.CodeUnavailable, "the connection of zathras %s failed: %v", zathras, err)
}

// open opens a stream on the connection of a connected zathras.
func (d *Draal) open(ctx context.Context, zathras wire.ID) (node.Stream, error) {
	sess := d.session(zathras)
	if sess == nil {
		return nil, notConnected(zathras)
	}
	s, err := sess.conn.OpenStream(ctx)
	if err != nil {
		return nil, lost(zathras, err)
	}
	return s, nil
}

// Logs asks the zathras for its ring and passes the lines on. The channel
// is closed at the end of the stream, or when ctx is done, which closes
// the stream so that the zathras stops following.
func (d *Draal) Logs(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error) {
	s, err := d.open(ctx, zathras)
	if err != nil {
		return nil, err
	}
	if err := wire.Write(s, &wire.Request{Logs: &wire.LogsRequest{Follow: follow}}); err != nil {
		s.Close()
		return nil, lost(zathras, err)
	}
	out := make(chan wire.LogLine)
	stop := context.AfterFunc(ctx, func() { s.Close() })
	go func() {
		defer close(out)
		defer s.Close()
		defer stop()
		for {
			resp, err := wire.ReadResponse(s)
			if err != nil {
				if err != io.EOF && ctx.Err() == nil {
					d.log.Warn("the log of a zathras ended", "zathras", zathras, "error", err)
				}
				return
			}
			if resp.Line == nil {
				continue
			}
			select {
			case out <- *resp.Line:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// Shell asks the zathras for a shell and returns the session once the
// shell runs, or the error the zathras answered.
func (d *Draal) Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (Shell, error) {
	s, err := d.open(ctx, zathras)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { s.Close() })
	req.Zathras = ""
	resp, err := wire.Call(s, &wire.Request{Shell: &req})
	if !stop() {
		s.Close()
		return nil, fmt.Errorf("draal: starting a shell of zathras %s: %w", zathras, context.Cause(ctx))
	}
	if err != nil {
		s.Close()
		return nil, lost(zathras, err)
	}
	if resp.Shell == nil {
		s.Close()
		return nil, wire.Errorf(wire.CodeInternal, "zathras %s answered a shell request with something else", zathras)
	}
	return &relayShell{zathras: zathras, s: s, ended: make(chan struct{})}, nil
}

// relayShell is a Shell over the stream to the zathras.
type relayShell struct {
	zathras wire.ID
	s       node.Stream

	rest []byte // output that was received and not read yet; Read's alone

	ended chan struct{} // closed when exit or err is set
	exit  int
	err   error

	wmu   sync.Mutex // one frame at a time
	close sync.Once
}

// Read returns output of the shell, and io.EOF once its exit status came.
func (r *relayShell) Read(p []byte) (int, error) {
	for len(r.rest) == 0 {
		select {
		case <-r.ended:
			if r.err != nil {
				return 0, r.err
			}
			return 0, io.EOF
		default:
		}
		resp, err := wire.ReadResponse(r.s)
		switch {
		case err == io.EOF:
			r.err = wire.Errorf(wire.CodeUnavailable, "the shell of zathras %s ended without an exit status", r.zathras)
			close(r.ended)
		case err != nil:
			r.err = lost(r.zathras, err)
			close(r.ended)
		case resp.Shell == nil:
		case resp.Shell.Exit != nil:
			r.exit = *resp.Shell.Exit
			r.rest = resp.Shell.Data
			close(r.ended)
			if len(r.rest) == 0 {
				return 0, io.EOF
			}
		default:
			r.rest = resp.Shell.Data
		}
	}
	n := copy(p, r.rest)
	r.rest = r.rest[n:]
	return n, nil
}

// Write sends p as input of the shell.
func (r *relayShell) Write(p []byte) (int, error) {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	written := 0
	for len(p) > 0 {
		chunk := p[:min(len(p), shellChunk)]
		if err := wire.Write(r.s, &wire.ShellInput{Data: chunk}); err != nil {
			return written, lost(r.zathras, err)
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}

// Resize tells the shell the new size of the terminal.
func (r *relayShell) Resize(cols, rows uint16) error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if err := wire.Write(r.s, &wire.ShellInput{Resize: &wire.WinSize{Cols: cols, Rows: rows}}); err != nil {
		return lost(r.zathras, err)
	}
	return nil
}

// Wait returns the exit status once Read has reached it, or the error the
// session ended with instead.
func (r *relayShell) Wait(ctx context.Context) (int, error) {
	select {
	case <-r.ended:
		if r.err != nil {
			return 0, r.err
		}
		return r.exit, nil
	case <-ctx.Done():
		return 0, context.Cause(ctx)
	}
}

// Close closes the stream, which ends the shell if it runs.
func (r *relayShell) Close() error {
	r.close.Do(func() { r.s.Close() })
	return nil
}
