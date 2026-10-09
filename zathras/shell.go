package zathras

import (
	"context"
	"io"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/ptyshell"
	"github.com/fables-for-robots/great-machine/wire"
)

// answerShell runs a shell session on s: it starts the shell, says so
// with an empty output, and then copies what is typed to the shell and
// what the shell prints to the stream, until the shell has ended, which
// its exit status says, or the stream has, which ends the shell. ctx is
// done when the connection has ended.
func (a *Agent) answerShell(ctx context.Context, s node.Stream, req *wire.ShellRequest) {
	cfg := a.cfg.Shell
	cfg.Term, cfg.Cols, cfg.Rows = req.Term, req.Cols, req.Rows
	session, err := ptyshell.Start(cfg)
	if err != nil {
		a.log.Warn("a shell could not be started", "error", err)
		wire.WriteError(s, wire.Errorf(wire.CodeInternal, "%s", reason(err)))
		s.CloseWrite()
		io.Copy(io.Discard, s) // until draal has read the answer and closed
		return
	}
	a.log.Info("a shell has started")
	wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{}})

	// What is typed, and the changes of size, until the stream ends.
	streamEnded := make(chan struct{})
	go func() {
		defer close(streamEnded)
		for {
			var in wire.ShellInput
			if wire.Read(s, &in) != nil {
				return
			}
			if in.Resize != nil {
				session.Resize(in.Resize.Cols, in.Resize.Rows)
			}
			if len(in.Data) > 0 {
				// A Write that fails is one to a shell that has ended,
				// which the output says.
				session.Write(in.Data)
			}
		}
	}()

	// What the shell prints, and at its end the exit status.
	shellEnded := make(chan struct{})
	go func() {
		defer close(shellEnded)
		buf := make([]byte, 32<<10)
		for {
			n, err := session.Read(buf)
			if n > 0 {
				if wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{Data: buf[:n]}}) != nil {
					return
				}
			}
			if err != nil {
				break
			}
		}
		exit := session.Wait()
		wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{Exit: &exit}})
		s.CloseWrite()
	}()

	select {
	case <-shellEnded:
		// The exit status is on its way. draal closes the stream when it
		// has read it.
		select {
		case <-streamEnded:
		case <-ctx.Done():
		}
	case <-streamEnded:
	case <-ctx.Done():
	}
	session.Close() // ends the shell if it runs, and with it the output
	s.Close()       // ends the input, and an output that waits to be written
	<-shellEnded
	<-streamEnded
	a.log.Info("a shell has ended", "status", session.Wait())
}
