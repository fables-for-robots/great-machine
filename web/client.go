package web

import (
	"context"
	"io"

	"github.com/fables-for-robots/great-machine/wire"
)

// Client is what the API answers from: the client of an operator, which
// asks draal with the operator's key. What the page may do is what that
// key may: draal decides, and the handler passes on what it says.
type Client interface {
	// Watch sends the fleet the key may see, at once and after every
	// change, until ctx is done, and then closes the channel.
	Watch(ctx context.Context) (<-chan wire.Fleet, error)
	// Rollouts returns the last rollouts of a service, newest first.
	Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error)
	// History returns the samples kept of a connected zathras, oldest first.
	History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error)
	// Logs sends the ring of the zathras, and with follow what follows,
	// and closes the channel at the end or when ctx is done.
	Logs(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error)
	// Shell starts a shell in the container of the zathras. ctx bounds the
	// start alone; the session lasts until its Close.
	Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (Shell, error)
	// Me returns the endpoint ID of the operator's key and its grants.
	Me(ctx context.Context) (wire.Identity, error)
	// Grants returns every grant. It is an admin's to ask, as Grant and
	// Revoke are.
	Grants(ctx context.Context) ([]wire.Grant, error)
	// Grant gives a key a role.
	Grant(ctx context.Context, req wire.GrantRequest) error
	// Revoke takes a grant away.
	Revoke(ctx context.Context, req wire.RevokeRequest) error
	// StorePage returns what the page of the store answers a GET of path
	// with, the query of the path among it. It is an admin's to ask.
	StorePage(ctx context.Context, path string) (wire.StorePage, error)
}

// Shell is a shell session as the page has it.
type Shell interface {
	io.Reader // output; io.EOF when the shell has ended
	io.Writer // input
	// Resize tells the shell the new size of the terminal.
	Resize(cols, rows uint16) error
	// Wait returns the exit status when the shell has ended. The status
	// comes behind the output, so Wait returns only once Read has reached
	// the end.
	Wait(ctx context.Context) (int, error)
	// Close ends the session, and the shell if it runs. It ends a Read
	// and a Write that wait, each with an error, and returns at once. It
	// may be called from another goroutine, and more than once.
	Close() error
}
