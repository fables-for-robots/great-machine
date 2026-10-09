package node_test

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// quiet is a logger that says nothing, to keep the output of the tests to
// what failed.
var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func newKey(t *testing.T) node.Key {
	t.Helper()
	k, err := node.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	return k
}

// listen binds an endpoint on loopback, without relays, that is closed
// when the test ends.
func listen(t *testing.T, k node.Key) node.Listener {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	l, err := node.Listen(ctx, node.ListenConfig{Key: k, Local: true, Log: quiet})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

// dial connects to l over loopback. The connection is closed when the
// test ends.
func dial(t *testing.T, k node.Key, l node.Listener) node.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	c, err := node.Dial(ctx, node.DialConfig{
		Key:    k,
		Server: l.ID(),
		Addrs:  []netip.AddrPort{l.LocalAddr()},
		Log:    quiet,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// accept returns the next connection of l.
func accept(t *testing.T, l node.Listener) node.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	c, err := l.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// irohPair makes a connection over go-iroh on loopback.
func irohPair(t *testing.T) (client, server node.Conn) {
	t.Helper()
	serverKey, clientKey := newKey(t), newKey(t)
	l := listen(t, serverKey)
	client = dial(t, clientKey, l)
	server = accept(t, l)
	if got := client.RemoteID(); got != serverKey.ID() {
		t.Errorf("the client's RemoteID is %s, want the ID of the server's key, %s", got, serverKey.ID())
	}
	if got := server.RemoteID(); got != clientKey.ID() {
		t.Errorf("the server's RemoteID is %s, want the ID of the client's key, %s", got, clientKey.ID())
	}
	return client, server
}

// This settles the first item of section 15 of the spec: the side that
// accepted a connection opens streams on it, and the side that dialed
// accepts them.
func TestIrohConn(t *testing.T) {
	testConn(t, irohPair)
}

func TestListen(t *testing.T) {
	k := newKey(t)
	l := listen(t, k)
	if l.ID() != k.ID() {
		t.Errorf("the listener's ID is %s, want that of its key, %s", l.ID(), k.ID())
	}
	addr := l.LocalAddr()
	if !addr.Addr().IsLoopback() || addr.Port() == 0 {
		t.Errorf("a local listener is bound to %s, want a port on loopback", addr)
	}
}

func TestListenOnTheAddressItIsGiven(t *testing.T) {
	// Find a port that is free, by binding one and closing it.
	first := listen(t, newKey(t))
	addr := first.LocalAddr()
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	l, err := node.Listen(ctx, node.ListenConfig{Key: newKey(t), Local: true, Bind: addr, Log: quiet})
	if err != nil {
		t.Fatalf("Listen on %s: %v", addr, err)
	}
	defer l.Close()
	if l.LocalAddr() != addr {
		t.Errorf("bound to %s, want %s", l.LocalAddr(), addr)
	}
}

func TestListenRefusesNoKey(t *testing.T) {
	l, err := node.Listen(context.Background(), node.ListenConfig{Local: true, Log: quiet})
	if err == nil {
		l.Close()
		t.Fatal("Listen without a key: no error")
	}
}

func TestListenerCloseEndsAccept(t *testing.T) {
	l := listen(t, newKey(t))
	failed := make(chan error, 1)
	go func() {
		_, err := l.Accept(context.Background())
		failed <- err
	}()
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := wait(t, failed, "the Accept that waited"); err == nil {
		t.Error("the Accept that waited returned a connection")
	}
	if _, err := l.Accept(context.Background()); err == nil {
		t.Error("Accept after Close returned a connection")
	}
	if err := l.Close(); err != nil {
		t.Errorf("a second Close: %v", err)
	}
}

func TestListenerAcceptGivesUpWithItsContext(t *testing.T) {
	l := listen(t, newKey(t))
	short, stop := context.WithTimeout(context.Background(), notYet)
	defer stop()
	if _, err := l.Accept(short); err == nil {
		t.Fatal("Accept returned a connection nobody made")
	}
	// The listener is as good as before.
	client := dial(t, newKey(t), l)
	server := accept(t, l)
	open(t, client, server, "still here")
}

// Shutting an endpoint down says nothing to the peers, so Close of the
// listener has to end each connection itself.
func TestListenerCloseEndsItsConnections(t *testing.T) {
	l := listen(t, newKey(t))
	client := dial(t, newKey(t), l)
	server := accept(t, l)
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitFor(t, soon, server.Done(), "Done of the connection that was accepted")
	waitFor(t, soon, client.Done(), "Done of the connection that was dialed")
}

func TestListenerAcceptsSeveralConnections(t *testing.T) {
	l := listen(t, newKey(t))
	keys := []node.Key{newKey(t), newKey(t), newKey(t)}
	want := make(map[wire.ID]bool)
	for _, k := range keys {
		dial(t, k, l)
		want[k.ID()] = true
	}
	for range keys {
		c := accept(t, l)
		if !want[c.RemoteID()] {
			t.Errorf("accepted a connection of %s, which did not dial or was accepted before", c.RemoteID())
		}
		delete(want, c.RemoteID())
	}
}

func TestDialRefuses(t *testing.T) {
	k := newKey(t)
	l := listen(t, newKey(t))
	addrs := []netip.AddrPort{l.LocalAddr()}
	tests := []struct {
		name string
		cfg  node.DialConfig
	}{
		{"no key", node.DialConfig{Server: l.ID(), Addrs: addrs}},
		{"no server", node.DialConfig{Key: k, Addrs: addrs}},
		{"a server ID that is none", node.DialConfig{Key: k, Server: "draal", Addrs: addrs}},
		{"its own ID", node.DialConfig{Key: k, Server: k.ID(), Addrs: addrs}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cfg.Log = quiet
			c, err := node.Dial(context.Background(), tt.cfg)
			if err == nil {
				c.Close()
				t.Fatal("no error")
			}
		})
	}
}

func TestDialFailsWhenTheServerIsAnother(t *testing.T) {
	l := listen(t, newKey(t))
	other := newKey(t)
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	c, err := node.Dial(ctx, node.DialConfig{
		Key:    newKey(t),
		Server: other.ID(),
		Addrs:  []netip.AddrPort{l.LocalAddr()},
		Log:    quiet,
	})
	if err == nil {
		c.Close()
		t.Fatal("Dial reached a server with another key than the one it asked for")
	}
}

func TestDialGivesUpWithItsContext(t *testing.T) {
	// An address at which nothing answers: that of a listener that was closed.
	l := listen(t, newKey(t))
	addr := l.LocalAddr()
	id := l.ID()
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	short, stop := context.WithTimeout(context.Background(), notYet)
	defer stop()
	c, err := node.Dial(short, node.DialConfig{Key: newKey(t), Server: id, Addrs: []netip.AddrPort{addr}, Log: quiet})
	if err == nil {
		c.Close()
		t.Fatal("Dial reached a listener that was closed")
	}
}

// The context of Dial bounds the dial alone.
func TestTheConnectionOutlivesTheContextOfItsDial(t *testing.T) {
	l := listen(t, newKey(t))
	ctx, cancel := context.WithCancel(context.Background())
	client, err := node.Dial(ctx, node.DialConfig{Key: newKey(t), Server: l.ID(), Addrs: []netip.AddrPort{l.LocalAddr()}, Log: quiet})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()
	cancel()
	server := accept(t, l)
	open(t, client, server, "after the context of the dial")
	open(t, server, client, "and back")
}

// Calls of Accept may be concurrent: they take turns, and none of them
// spins while it waits for its turn.
func TestConcurrentAcceptsTakeTurns(t *testing.T) {
	l := listen(t, newKey(t))
	type accepted struct {
		c   node.Conn
		err error
	}
	got := make(chan accepted, 2)
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	for range 2 {
		go func() {
			c, err := l.Accept(ctx)
			got <- accepted{c, err}
		}()
	}
	keys := []node.Key{newKey(t), newKey(t)}
	for _, k := range keys {
		dial(t, k, l)
	}
	seen := make(map[wire.ID]bool)
	for range 2 {
		a := wait(t, got, "an Accept")
		if a.err != nil {
			t.Fatalf("Accept: %v", a.err)
		}
		t.Cleanup(func() { a.c.Close() })
		seen[a.c.RemoteID()] = true
	}
	if len(seen) != 2 || !seen[keys[0].ID()] || !seen[keys[1].ID()] {
		t.Errorf("the two Accepts returned the connections of %v, want those of %s and %s", seen, keys[0].ID(), keys[1].ID())
	}
}

// counter is a slog.Handler that counts the records it gets.
type counter struct{ n *atomic.Int64 }

func (c counter) Enabled(context.Context, slog.Level) bool  { return true }
func (c counter) Handle(context.Context, slog.Record) error { c.n.Add(1); return nil }
func (c counter) WithAttrs([]slog.Attr) slog.Handler        { return c }
func (c counter) WithGroup(string) slog.Handler             { return c }

func TestAnAcceptWaitingForItsTurnGivesUpWithItsContext(t *testing.T) {
	var logged atomic.Int64
	ctx0, cancel0 := context.WithTimeout(context.Background(), patience)
	defer cancel0()
	l, err := node.Listen(ctx0, node.ListenConfig{Key: newKey(t), Local: true, Log: slog.New(counter{&logged})})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	first := make(chan error, 1)
	holding, release := context.WithCancel(context.Background())
	defer release()
	go func() {
		_, err := l.Accept(holding)
		first <- err
	}()
	// Let the first Accept take its turn.
	time.Sleep(notYet)

	short, stop := context.WithTimeout(context.Background(), notYet)
	defer stop()
	failed := make(chan error, 1)
	go func() {
		_, err := l.Accept(short)
		failed <- err
	}()
	if err := waitFor(t, soon, failed, "the Accept that waited for its turn"); err == nil {
		t.Error("the Accept that waited for its turn returned a connection")
	}
	// A call that waits for its turn does not spin: the old loop logged
	// thousands of failed accepts in this time.
	if n := logged.Load(); n > 20 {
		t.Errorf("Accept logged %d records while it waited for its turn; it spins", n)
	}
	// A third waits for its turn, and Close ends it.
	third := make(chan error, 1)
	go func() {
		_, err := l.Accept(context.Background())
		third <- err
	}()
	time.Sleep(notYet)
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := waitFor(t, soon, third, "the Accept that waited for its turn at Close"); err == nil {
		t.Error("the Accept that waited for its turn returned a connection after Close")
	}
	waitFor(t, soon, first, "the Accept that held the turn")
}
