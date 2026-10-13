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
	DB *db.DB
	// Authz decides who may do what, by the grants. draal tells it who
	// is registered as a zathras, and puts the grants of the database in
	// the place of those it has whenever one was granted or revoked. nil:
	// nobody may do anything.
	Authz *authz.Authority
	// Store is the store that is answered beside draal, under its
	// endpoint ID. draal asks it for its page and has it end the
	// connections of a key whose grant was revoked. nil: there is none.
	Store StoreServer
	// ID is the endpoint ID of draal. It is that of the store as well,
	// which draal answers on the same endpoint: what the store request
	// of an operator is answered with.
	ID wire.ID
	// StoreDir is the directory a rollout's binary has to be in. "":
	// /nix/store. The tests of the whole point it at a directory they can
	// write to.
	StoreDir       string
	Settle         time.Duration    // 0: 45 seconds
	StableAfter    time.Duration    // 0: 30 seconds
	PrepareTimeout time.Duration    // 0: 30 minutes, also of one prepare
	ActivateWait   time.Duration    // 0: 30 seconds, of one activate
	RetryAfter     time.Duration    // 0: 30 seconds
	AbsentGrace    time.Duration    // 0: 2 minutes
	GoneAfter      time.Duration    // 0: 10 seconds
	WatchEvery     time.Duration    // 0: 1 second
	History        time.Duration    // 0: 15 minutes
	RequestWait    time.Duration    // 0: 5 seconds, for the first frame of a stream
	RefuseWait     time.Duration    // 0: 2 seconds, for the peer to end a refused stream
	MaxStreams     int              // 0: 64 streams of a connection served at a time
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
	if cfg.ActivateWait == 0 {
		cfg.ActivateWait = 30 * time.Second
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
	if cfg.RequestWait == 0 {
		cfg.RequestWait = 5 * time.Second
	}
	if cfg.RefuseWait == 0 {
		cfg.RefuseWait = 2 * time.Second
	}
	if cfg.MaxStreams == 0 {
		cfg.MaxStreams = 64
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Authz == nil {
		cfg.Authz = authz.New(authz.Config{}, nil)
	}
	return cfg
}

// StoreServer is what draal asks of the store that is answered under its
// endpoint ID: *storeserver.Store.
type StoreServer interface {
	// Drop ends every connection the peer has to the store.
	Drop(peer wire.ID)
	// Page returns what the page of the store answers a GET of path with.
	Page(path string) wire.StorePage
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
	// Close ends the session, and the shell if it runs. It ends a Read
	// and a Write that wait, each with an error, and returns at once. It
	// may be called from another goroutine, and more than once.
	Close() error
}

// Controller is what an operator can do. The handler of the protocol is
// written against it, and the made-up fleet of the demos is one.
type Controller interface {
	// Store returns where the closures of a service are pushed to:
	// draal's own endpoint ID, on which it answers jaccard-store's
	// protocol, and the prefix of the service. A name that is no name of
	// a service is a bad-request.
	Store(ctx context.Context, service string) (wire.StoreInfo, error)
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

	// access is held while a grant is put or removed: the database is
	// written and the authority given its grants as one step.
	access sync.Mutex

	mu       sync.Mutex
	conns    map[node.Conn]struct{}     // the connections that are served
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
	name    string
	conn    node.Conn
	by      wire.ID // the role key that signed for the zathras
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
	d := &Draal{
		cfg:      cfg,
		log:      cfg.Log,
		fleet:    fleet.New(cfg.History),
		conns:    map[node.Conn]struct{}{},
		sessions: map[wire.ID]*session{},
		tallies:  map[string]tally{},
		subs:     map[chan struct{}]struct{}{},
	}
	// Who is registered is the fleet's to say: a zathras pulls the store
	// paths of the service it is registered for.
	d.cfg.Authz.SetRegistered(d.ServiceOf)
	return d
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
