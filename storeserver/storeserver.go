// Package storeserver is the store in draal: the jaccard-store server,
// answered on the connections that draal's own endpoint accepts for
// jaccard-store's protocol. The endpoint ID of draal is so all that an
// operator or a pod needs, for the protocol of great-machine and for the
// store.
//
// The server is jaccard-store's package server over its db and its
// bucket, put together as jaccard-stored puts them together. Its own
// Serve is not used, because it takes a whole endpoint: a connection is
// given to ServeConn instead, and every stream of it is one request, which
// is authorized before the server sees it. A pull is every peer's, since
// the mount of a zathras pulls with a key of its own that draal does not
// know, and since what the store holds is what the pods run. Everything
// else, pushing, listing and deleting, is an operator's.
//
// Nothing of a pack passes through here: a client moves the bytes between
// itself and the bucket through URLs the server signs.
package storeserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amber-store/jaccard-store/admin"
	"github.com/amber-store/jaccard-store/bucket"
	jsdb "github.com/amber-store/jaccard-store/db"
	jsserver "github.com/amber-store/jaccard-store/server"
	jswire "github.com/amber-store/jaccard-store/wire"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// ALPN is the protocol the store answers: jaccard-store's.
const ALPN = "amber/jaccard-store/1"

// The store in its directory.
const (
	dbFile = "store.sqlite"
	// scratchDir is the directory the store makes for what it verifies,
	// in the directory it is given for that. It is emptied whenever the
	// store is opened, which is why it is the store's own, by a name
	// nothing else has: what it is made in may be the directory of the
	// store itself, with the database and draal's key in it, or any
	// other, and loses nothing.
	scratchDir = "great-machine-scratch"
)

// Config is what a Store is made of. The zero value of a number or of a
// duration stands for its default.
type Config struct {
	// Dir holds the database of the store, store.sqlite. It is the only
	// record of the references and of which pack leans on which.
	Dir string
	// Bucket names the S3 bucket the packs are kept in. The credentials
	// are those the AWS SDK finds.
	Bucket bucket.Config
	// Objects is for tests: the bucket itself. When it is set, Bucket is
	// not looked at.
	Objects jsserver.Bucket
	// Scratch is the directory in which packs are verified: the
	// uncompressed data of each is written there, MaxPackBytes at most
	// for each of VerifyJobs. The store makes a directory of its own in
	// it, great-machine-scratch, and empties that one whenever it is
	// opened: nothing else in Scratch is touched. "": Dir.
	Scratch      string
	VerifyJobs   int   // 0: 2 packs verified at once
	MaxPackBytes int64 // 0: 16 GiB, the largest pack, uncompressed
	// NoVerify records a pack without reading its data, as
	// jaccard-store's --no-verify does.
	NoVerify bool
	// Authz decides who may do what: a pull is asked as
	// authz.OpStorePull, everything else as authz.OpStoreWrite.
	Authz       authz.Authorizer
	RequestWait time.Duration // 0: 5 seconds, for the request of a stream
	AnswerWait  time.Duration // 0: 2 seconds, for the peer to end a stream that was answered
	MaxStreams  int           // 0: 64 streams of a connection answered at a time
	StopGrace   time.Duration // 0: 10 seconds, for the requests under way when Stop is called
	SweepEvery  time.Duration // 0: 30 seconds
	Log         *slog.Logger  // nil: slog.Default()
}

// withDefaults returns cfg with every zero value replaced by its default.
func (cfg Config) withDefaults() Config {
	if cfg.Scratch == "" {
		cfg.Scratch = cfg.Dir
	}
	if cfg.RequestWait == 0 {
		cfg.RequestWait = 5 * time.Second
	}
	if cfg.AnswerWait == 0 {
		cfg.AnswerWait = 2 * time.Second
	}
	if cfg.MaxStreams == 0 {
		cfg.MaxStreams = 64
	}
	if cfg.StopGrace == 0 {
		cfg.StopGrace = 10 * time.Second
	}
	if cfg.SweepEvery == 0 {
		cfg.SweepEvery = 30 * time.Second
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return cfg
}

// Store is the store of a draal. Its methods may be called from any
// goroutine.
type Store struct {
	cfg    Config
	log    *slog.Logger
	db     *jsdb.DB
	server *jsserver.Server

	// accepting is done when Stop begins: no stream is accepted after
	// that, and a stream whose request has not come is closed.
	accepting context.Context
	refuse    context.CancelFunc
	// requests is what every request is answered under. It is done when
	// the grace of Stop is over.
	requests context.Context
	end      context.CancelFunc

	mu       sync.Mutex
	stopping bool
	conns    map[node.Conn]struct{}
	// serving are the calls of ServeConn, and under the requests under
	// way. Both are added to under mu and only while stopping is false,
	// so that Stop waits for all there are.
	serving sync.WaitGroup
	under   sync.WaitGroup

	// streams is how many streams are being served. The tests look at
	// it.
	streams atomic.Int64

	stop    sync.Once
	stopped chan struct{}

	sweeping context.CancelFunc
	swept    chan struct{}

	close    sync.Once
	closeErr error
}

// Open opens the database and the bucket, makes the server and starts its
// sweeper. ctx bounds the opening alone.
//
// The bucket is not asked anything here: one that is not there, or cannot
// be reached, shows in the first request that needs it.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	switch {
	case cfg.Dir == "":
		return nil, errors.New("storeserver: no directory to keep the store in")
	case cfg.Authz == nil:
		return nil, errors.New("storeserver: nobody to authorize a request")
	case cfg.Objects == nil && cfg.Bucket.Bucket == "":
		return nil, errors.New("storeserver: no bucket")
	}
	cfg = cfg.withDefaults()
	objects := cfg.Objects
	if objects == nil {
		b, err := bucket.New(ctx, cfg.Bucket)
		if err != nil {
			return nil, fmt.Errorf("storeserver: %w", err)
		}
		objects = b
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("storeserver: the directory of the store: %w", err)
	}
	database, err := jsdb.Open(filepath.Join(cfg.Dir, dbFile))
	if err != nil {
		return nil, fmt.Errorf("storeserver: the database: %w", err)
	}
	srv, err := jsserver.New(jsserver.Config{
		DB:           database,
		Bucket:       objects,
		Scratch:      filepath.Join(cfg.Scratch, scratchDir),
		VerifyJobs:   cfg.VerifyJobs,
		MaxPackBytes: cfg.MaxPackBytes,
		NoVerify:     cfg.NoVerify,
		Log:          cfg.Log,
	})
	if err != nil {
		database.Close()
		return nil, fmt.Errorf("storeserver: %w", err)
	}
	s := &Store{
		cfg:     cfg,
		log:     cfg.Log,
		db:      database,
		server:  srv,
		conns:   make(map[node.Conn]struct{}),
		stopped: make(chan struct{}),
		swept:   make(chan struct{}),
	}
	s.accepting, s.refuse = context.WithCancel(context.Background())
	s.requests, s.end = context.WithCancel(context.Background())
	sweeping, stop := context.WithCancel(context.Background())
	s.sweeping = stop
	go func() {
		defer close(s.swept)
		srv.RunSweeper(sweeping, cfg.SweepEvery)
	}()
	return s, nil
}

// ServeConn answers the requests of one connection, each a stream, until
// the connection ends or the store is stopped, and closes the connection.
// It is what node.ListenConfig.Handlers has for ALPN.
//
// At most MaxStreams streams of the connection are answered at a time;
// one beyond them is closed at once, without an answer.
func (s *Store) ServeConn(conn node.Conn) {
	defer conn.Close()
	if !s.enter(conn) {
		return
	}
	defer s.leave(conn)

	var wg sync.WaitGroup
	places := make(chan struct{}, s.cfg.MaxStreams) // one for every stream served
	warned := false
	for {
		st, err := conn.AcceptStream(s.accepting)
		if err != nil {
			break
		}
		select {
		case places <- struct{}{}:
		default:
			if !warned {
				warned = true
				s.log.Warn("a peer opens more streams than the store answers at a time: closing those beyond", "peer", conn.RemoteID(), "most", s.cfg.MaxStreams)
			}
			st.Close()
			continue
		}
		s.streams.Add(1)
		wg.Go(func() {
			defer func() {
				s.streams.Add(-1)
				<-places
			}()
			s.serveStream(conn, st)
		})
	}
	// What is under way ends by itself, or with the grace of Stop.
	wg.Wait()
}

// enter notes a connection that is being served. It returns false when
// the store is being stopped: the connection is then not served.
func (s *Store) enter(conn node.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return false
	}
	s.conns[conn] = struct{}{}
	s.serving.Add(1)
	return true
}

func (s *Store) leave(conn node.Conn) {
	s.mu.Lock()
	delete(s.conns, conn)
	s.mu.Unlock()
	s.serving.Done()
}

// begin notes a request that is under way. It returns false when the
// store is being stopped: no request is begun then.
func (s *Store) begin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return false
	}
	s.under.Add(1)
	return true
}

// serveStream reads the request of a stream and answers it. The request
// has RequestWait to arrive, or the stream is closed.
func (s *Store) serveStream(conn node.Conn, st node.Stream) {
	defer st.Close()
	peer := conn.RemoteID()
	// A stream has no deadline: a timer closes it, which ends the Read.
	// So does the stop of the store: a request that has not come is not
	// under way.
	timer := time.AfterFunc(s.cfg.RequestWait, func() { st.Close() })
	unhook := context.AfterFunc(s.accepting, func() { st.Close() })
	var req jswire.Request
	err := jswire.ReadRequest(st, &req)
	late := !timer.Stop()
	if !unhook() {
		return
	}
	if late {
		s.log.Debug("no request within RequestWait", "peer", peer, "after", s.cfg.RequestWait)
		return
	}
	if err != nil {
		s.log.Debug("reading a request of the store", "peer", peer, "error", err)
		return
	}
	if !s.begin() {
		return
	}
	defer s.under.Done()

	resp := s.answer(peer, req)
	if err := jswire.WriteFrame(st, resp); err != nil {
		s.log.Debug("writing an answer of the store", "peer", peer, "operation", string(req.Op), "error", err)
		return
	}
	// The answer is the peer's to read. Closing a connection discards
	// what its streams have not delivered, and Stop closes the
	// connections when the last request has ended: so a request has not
	// ended before the peer has closed its stream, which jaccard-store's
	// client does when it has read the answer, or AnswerWait is over.
	wait := time.NewTimer(s.cfg.AnswerWait)
	defer wait.Stop()
	select {
	case <-st.Done():
	case <-wait.C:
	case <-s.requests.Done():
	}
}

// operation returns what Authorize is asked for an operation of
// jaccard-store's protocol, and false for one the store does not answer.
func operation(op jswire.Op) (authz.Operation, bool) {
	switch op {
	case jswire.OpPull:
		return authz.OpStorePull, true
	case jswire.OpPushStart, jswire.OpPushUpload, jswire.OpPushCommit, jswire.OpList, jswire.OpDelete:
		return authz.OpStoreWrite, true
	default:
		return "", false
	}
}

// answer authorizes a request and has the server answer it. A refusal is
// an answer of jaccard-store's protocol, which has no code for one: a
// bad_request whose message begins with "unauthorized".
func (s *Store) answer(peer wire.ID, req jswire.Request) jswire.Response {
	op, known := operation(req.Op)
	if !known {
		return jswire.Errorf(jswire.CodeBadRequest, "the store answers no such request")
	}
	if err := s.cfg.Authz.Authorize(peer, op, ""); err != nil {
		s.log.Warn("refused", "peer", peer, "operation", string(req.Op))
		return jswire.Errorf(jswire.CodeBadRequest, "unauthorized: %s is for operators", req.Op)
	}
	started := time.Now()
	resp := s.server.Handle(s.requests, string(peer), req)
	code := ""
	if resp.Error != nil {
		code = resp.Error.Code
	}
	s.log.Debug("a request of the store", "operation", string(req.Op), "peer", peer, "name", req.Name, "error", code, "took", time.Since(started).Round(time.Millisecond))
	return resp
}

// Admin returns jaccard-store's admin page over the store's database: the
// page, and under /api/ what it shows. It is read-only. It expects to be
// asked with the path it is mounted under stripped.
func (s *Store) Admin() http.Handler {
	return admin.Handler(s.db)
}

// Stop ends the answering: no new request is begun, the requests under way
// have StopGrace, and then every connection of the store is closed. It
// returns when that is done. It may be called more than once.
//
// A request ends with its context, which is done when the grace is over.
// One that does not, as a verification in the middle of a large pack does
// not until it is through, has the grace a second time and is then left
// behind: Stop says so and returns, and what the request still asks of the
// database after Close fails, which leaves nothing that the next start of
// the store does not find.
func (s *Store) Stop() {
	s.stop.Do(func() {
		defer close(s.stopped)
		s.mu.Lock()
		s.stopping = true
		s.mu.Unlock()
		// No stream is accepted any more, and a connection with nothing
		// under way is closed as its ServeConn returns.
		s.refuse()
		ended := make(chan struct{})
		go func() {
			s.under.Wait()
			close(ended)
		}()
		grace := time.NewTimer(s.cfg.StopGrace)
		defer grace.Stop()
		select {
		case <-ended:
		case <-grace.C:
			s.log.Warn("stopping the store with requests under way", "after", s.cfg.StopGrace)
		}
		// What is still under way ends with its context, and whoever
		// waits for a peer waits no longer.
		s.end()
		s.mu.Lock()
		conns := s.conns
		s.conns = make(map[node.Conn]struct{})
		s.mu.Unlock()
		for conn := range conns {
			conn.Close()
		}
		gone := make(chan struct{})
		go func() {
			<-ended
			s.serving.Wait()
			close(gone)
		}()
		last := time.NewTimer(s.cfg.StopGrace)
		defer last.Stop()
		select {
		case <-gone:
		case <-last.C:
			s.log.Warn("the store is stopped, with requests that have not ended with their context", "streams", s.streams.Load(), "after", 2*s.cfg.StopGrace)
		}
	})
	<-s.stopped
}

// Close stops the store (Stop), waits for the sweeper, and closes the
// database. It may be called more than once.
func (s *Store) Close() error {
	s.close.Do(func() {
		s.Stop()
		s.sweeping()
		<-s.swept
		if err := s.db.Close(); err != nil {
			s.closeErr = fmt.Errorf("storeserver: closing the database: %w", err)
		}
	})
	return s.closeErr
}
