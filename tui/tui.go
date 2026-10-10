// Package tui is the terminal UI of great-machine: what the web page
// shows, in a terminal. It shows every service and every connected
// zathras with its state, its figures and its log, followed live, and
// rolls nothing out.
//
// It talks to draal through an Operator, as the other operator commands
// do, and is three parts. The model is the state and a function of the
// messages it gets: it touches no terminal and no network, and says what
// it wants done as effects. The views are functions from the model to
// text. The program around them (Run) is Bubble Tea: it reads the
// terminal, draws the views, carries out the effects against the
// Operator, and tells the model what came of them.
//
// Nothing that draal or a zathras sent reaches the terminal as it came:
// see text.go.
package tui

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/fables-for-robots/great-machine/term"
	"github.com/fables-for-robots/great-machine/wire"
)

// Operator is what the terminal UI asks of draal. An *operator.Client is
// one, but for the type that its Shell returns: cli has the adapter.
type Operator interface {
	Watch(ctx context.Context) (<-chan wire.Fleet, error)
	Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error)
	History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error)
	Logs(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error)
	Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (Shell, error)
	Close() error
}

// Shell is a shell session: what term.Attach joins the terminal to, and
// Close, which ends it.
type Shell interface {
	term.Session
	Close() error
}

// Config configures the terminal UI.
type Config struct {
	// Dial connects to draal. It is called at the start, and again, with
	// pauses that grow, whenever the connection was lost. The terminal UI
	// closes what Dial returned.
	Dial func(ctx context.Context) (Operator, error)
	// In and Out are the terminal.
	In, Out *os.File
	// Draal is how the header names draal: the start of its endpoint ID.
	Draal string
	// Each of these has a default when it is zero:
	RetryFirst time.Duration    // the first pause before Dial is tried again; 1 second
	RetryMax   time.Duration    // the longest pause; 30 seconds
	Now        func() time.Time // time.Now
	Log        *slog.Logger     // nowhere: a terminal UI has no place to log to
}

// withDefaults returns cfg with the defaults in place of what is zero.
func (cfg Config) withDefaults() Config {
	if cfg.RetryFirst <= 0 {
		cfg.RetryFirst = time.Second
	}
	if cfg.RetryMax <= 0 {
		cfg.RetryMax = 30 * time.Second
	}
	if cfg.RetryMax < cfg.RetryFirst {
		cfg.RetryMax = cfg.RetryFirst
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	return cfg
}
