package zathras

import (
	"context"
	"io"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// answer writes what answers req to s. ctx is done when draal has closed
// the stream or the connection has ended.
func (a *Agent) answer(ctx context.Context, s node.Stream, req *wire.Request) {
	switch {
	default:
		wire.WriteError(s, wire.Errorf(wire.CodeBadRequest, "a zathras answers prepare, activate, logs and shell, and this request is none of them"))
	}
}

// answerShell answers a shell request, on a stream that it reads itself.
// A zathras has no shell yet.
func (a *Agent) answerShell(ctx context.Context, s node.Stream, req *wire.ShellRequest) {
	wire.WriteError(s, wire.Errorf(wire.CodeBadRequest, "a zathras has no shell yet"))
	s.CloseWrite()
	io.Copy(io.Discard, s) // until draal has read the answer and closed
}
