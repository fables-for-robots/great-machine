package draal

import (
	"context"
	"io"
	"log/slog"
	"path"
	"sync"
	"time"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/fleet"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// Config is what a Draal is made of. The zero value of a duration stands
// for the value of the spec.
type Config struct {
	DB    *db.DB
	Authz authz.Authorizer
	Store wire.StoreInfo
	// StoreDir is the directory a rollout's binary has to be in. "":
	// /nix/store. The tests of the whole point it at a directory they can
	// write to.
	StoreDir       string
	Settle         time.Duration    // 0: 45 seconds
	StableAfter    time.Duration    // 0: 30 seconds
	PrepareTimeout time.Duration    // 0: 30 minutes
	RetryAfter     time.Duration    // 0: 30 seconds
	AbsentGrace    time.Duration    // 0: 2 minutes
	GoneAfter      time.Duration    // 0: 10 seconds
	WatchEvery     time.Duration    // 0: 1 second
	History        time.Duration    // 0: 15 minutes
	Now            func() time.Time // nil: time.Now
	Log            *slog.Logger     // nil: slog.Default()

	// afterLook, if set, is called by the reconciler when it has looked at
	// the members of a service and before it reads the service's rollout:
	// the tests make things happen in between.
	afterLook func(service string)
}

// withDefaults returns cfg with every zero value replaced by its default.
func (cfg Config) withDefaults() Config {
	if cfg.StoreDir == "" {
		cfg.StoreDir = "/nix/store"
	}
	cfg.StoreDir = path.Clean(cfg.StoreDir)
	if cfg.Settle == 0 {
		cfg.Settle = 45 * time.Second
	}
	if cfg.StableAfter == 0 {
		cfg.StableAfter = 30 * time.Second
	}
	if cfg.PrepareTimeout == 0 {
		cfg.PrepareTimeout = 30 * time.Minute
	}
	if cfg.RetryAfter == 0 {
		cfg.RetryAfter = 30 * time.Second
	}
	if cfg.AbsentGrace == 0 {
		cfg.AbsentGrace = 2 * time.Minute
	}
	if cfg.GoneAfter == 0 {
		cfg.GoneAfter = 10 * time.Second
	}
	if cfg.WatchEvery == 0 {
		cfg.WatchEvery = time.Second
	}
	if cfg.History == 0 {
		cfg.History = 15 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return cfg
}

// Shell is a shell session as an operator has it.
type Shell interface {
	io.Reader // output; io.EOF when the shell has ended
	io.Writer // input
	// Resize tells the shell the new size of the terminal.
	Resize(cols, rows uint16) error
	// Wait returns the exit status when the shell has ended. The status
	// comes behind the output, so Wait returns only once Read has reached
	// the end.
	Wait(ctx context.Context) (int, error)
	// Close ends the session, and the shell if it runs.
	Close() error
}

// Controller is what an operator can do. The handler of the protocol and,
// in stage 2, the HTTP API are written against it.
type Controller interface {
	// Store returns the jaccard-store server and the prefix that closures
	// are pushed to.
	Store(ctx context.Context) (wire.StoreInfo, error)
	// Rollout records a new generation of a service and the rollout to it.
	Rollout(ctx context.Context, by wire.ID, req wire.RolloutRequest) (wire.RolloutInfo, error)
	// Rollback records a rollout to the stable generation of a service.
	Rollback(ctx context.Context, by wire.ID, req wire.RollbackRequest) (wire.RolloutInfo, error)
	// Watch sends the fleet at once and after every change, at most once
	// in WatchEvery, until ctx is done, and then closes the channel.
	Watch(ctx context.Context) (<-chan wire.Fleet, error)
	// Rollouts returns the last 20 rollouts of a service, newest first.
	Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error)
	// History returns the samples kept of a connected zathras, oldest first.
	History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error)
	// Logs sends the ring of the zathras, and with follow what follows,
	// and closes the channel at the end or when ctx is done.
	Logs(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error)
	// Shell starts a shell in the container of the zathras. ctx bounds the
	// start alone; the session lasts until its Close.
	Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (Shell, error)
	// ServiceOf returns the service of a connected zathras, for Authorize.
	ServiceOf(zathras wire.ID) (string, bool)
}

// Draal is the controller. It is a Controller.
type Draal struct {
	cfg   Config
	log   *slog.Logger
	fleet *fleet.Registry

	mu       sync.Mutex
	sessions map[wire.ID]*session       // the zathrases that are connected
	left     []leave                    // who left since the reconciler last looked, in order
	tallies  map[string]tally           // by service: the counts of the last decision
	subs     map[chan struct{}]struct{} // who is told that something changed
}

// session is the connection of a registered zathras, on which draal opens
// the streams of its commands, logs and shells.
type session struct {
	id      wire.ID
	service string
	conn    node.Conn
}

// leave is a zathras that left the fleet: its last report, and when draal
// let it go.
type leave struct {
	id      wire.ID
	service string
	state   wire.State
	at      time.Time
}

// tally is the counts of the last decision about a rollout.
type tally struct {
	rollout                      uint64
	prepared, activated, awaited int
}

// New returns a Draal. Its methods of Controller work at once; zathrases
// are connected, and rollouts carried out, while Serve runs.
func New(cfg Config) *Draal {
	cfg = cfg.withDefaults()
	return &Draal{
		cfg:      cfg,
		log:      cfg.Log,
		fleet:    fleet.New(cfg.History),
		sessions: map[wire.ID]*session{},
		tallies:  map[string]tally{},
		subs:     map[chan struct{}]struct{}{},
	}
}

// subscribe returns a channel with room for one value that gets one
// whenever changed is called, and a function that ends the subscription.
func (d *Draal) subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	d.mu.Lock()
	d.subs[ch] = struct{}{}
	d.mu.Unlock()
	return ch, func() {
		d.mu.Lock()
		delete(d.subs, ch)
		d.mu.Unlock()
	}
}

// changed tells the reconciler and every watcher that a rollout was
// recorded or has moved on.
func (d *Draal) changed() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for ch := range d.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// session returns the session of a connected zathras, or nil.
func (d *Draal) session(id wire.ID) *session {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sessions[id]
}
