package node_test

import (
	"context"
	"io"
	"net/netip"
	"strings"
	"testing"
	"time"

	jsnode "github.com/amber-store/jaccard-store/node"
	jswire "github.com/amber-store/jaccard-store/wire"
	irohkey "github.com/tmc/go-iroh/key"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// listenWith binds an endpoint on loopback that accepts the protocols of
// handlers besides great-machine's own, and accepts its connections until
// the test ends: a connection of another protocol is handed over by
// Accept. The connections of great-machine's own protocol are sent on the
// channel.
func listenWith(t *testing.T, k node.Key, handlers map[string]func(node.Conn)) (node.Listener, <-chan node.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	l, err := node.Listen(ctx, node.ListenConfig{Key: k, Local: true, Handlers: handlers, Log: quiet})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	accepting, stop := context.WithCancel(context.Background())
	own := make(chan node.Conn, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := l.Accept(accepting)
			if err != nil {
				return
			}
			own <- c
		}
	}()
	t.Cleanup(func() {
		stop()
		l.Close()
		<-done
	})
	return l, own
}

// dialFor connects to l for another protocol than great-machine's, as a
// client of jaccard-store does. The connection is closed when the test
// ends.
func dialFor(t *testing.T, alpn string, k node.Key, l node.Listener) (*jsnode.Conn, error) {
	t.Helper()
	server, err := irohkey.ParseEndpointID(string(l.ID()))
	if err != nil {
		t.Fatalf("the ID of the listener: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	c, err := jsnode.Dial(ctx, jsnode.DialConfig{
		Key:    k.Iroh(),
		ALPN:   alpn,
		Server: server,
		Addrs:  []netip.AddrPort{l.LocalAddr()},
		Log:    quiet,
	})
	if err == nil {
		t.Cleanup(func() { c.Close() })
	}
	return c, err
}

// A connection that was dialed for the protocol of a handler reaches the
// handler, with the peer it is of, and its streams carry what is written
// in both directions. One for great-machine's own protocol reaches Accept
// as before.
func TestAConnectionOfAnotherProtocolGoesToItsHandler(t *testing.T) {
	serverKey, storeClient, ownClient := newKey(t), newKey(t), newKey(t)
	handed := make(chan node.Conn, 1)
	l, own := listenWith(t, serverKey, map[string]func(node.Conn){
		jswire.ALPN: func(c node.Conn) {
			handed <- c
			// The handler has the connection for as long as it does not
			// return.
			<-c.Done()
		},
	})

	c, err := dialFor(t, jswire.ALPN, storeClient, l)
	if err != nil {
		t.Fatalf("dialing for %s: %v", jswire.ALPN, err)
	}
	var server node.Conn
	select {
	case server = <-handed:
	case got := <-own:
		t.Fatalf("Accept returned the connection of %s, which is the handler's", got.RemoteID())
	case <-time.After(patience):
		t.Fatal("timed out waiting for the handler to be given the connection")
	}
	if got := server.RemoteID(); got != storeClient.ID() {
		t.Errorf("the handler's connection is of %s, want the client, %s", got, storeClient.ID())
	}

	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	out, err := c.OpenStreamConn(ctx)
	if err != nil {
		t.Fatalf("OpenStreamConn: %v", err)
	}
	defer out.Close()
	if _, err := out.Write([]byte("ping")); err != nil {
		t.Fatalf("writing to the stream: %v", err)
	}
	in, err := server.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream of the handler's connection: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(in, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("the handler read %q, %v; want ping", buf, err)
	}
	if _, err := in.Write([]byte("pong")); err != nil {
		t.Fatalf("the handler writing: %v", err)
	}
	in.Close()
	answer, err := io.ReadAll(out)
	if err != nil || string(answer) != "pong" {
		t.Fatalf("the client read %q, %v; want pong and the end of the stream", answer, err)
	}

	// great-machine's own protocol is Accept's.
	dial(t, ownClient, l)
	select {
	case got := <-own:
		if got.RemoteID() != ownClient.ID() {
			t.Errorf("Accept returned a connection of %s, want %s", got.RemoteID(), ownClient.ID())
		}
	case got := <-handed:
		t.Fatalf("the handler was given the connection of %s, which is Accept's", got.RemoteID())
	case <-time.After(patience):
		t.Fatal("timed out waiting for Accept to return the connection")
	}
}

// Without a handler for it, a protocol is not accepted: the dial fails in
// the handshake.
func TestWithoutAHandlerAnotherProtocolIsRefused(t *testing.T) {
	l, _ := listenWith(t, newKey(t), nil)
	if c, err := dialFor(t, jswire.ALPN, newKey(t), l); err == nil {
		t.Fatalf("a dial for %s completed with %s, which has no handler for it", jswire.ALPN, c.RemoteID())
	}
	// Nor is a protocol accepted that another has a handler for.
	l, _ = listenWith(t, newKey(t), map[string]func(node.Conn){jswire.ALPN: func(node.Conn) {}})
	if c, err := dialFor(t, "nobody/1", newKey(t), l); err == nil {
		t.Fatalf("a dial for nobody/1 completed with %s, which has no handler for it", c.RemoteID())
	}
}

// ended fails the test unless the connection of a client has ended: a
// stream is refused on it at once. An open that only runs out of its time
// is no sign of an end: a connection whose peer accepts no streams stops
// taking them at the peer's limit, open as it is.
func ended(t *testing.T, what string, c *jsnode.Conn) {
	t.Helper()
	deadline := time.Now().Add(patience)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		s, err := c.OpenStreamConn(ctx)
		ranOut := ctx.Err() != nil
		cancel()
		if err != nil && !ranOut {
			return
		}
		if err == nil {
			s.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: the connection of the client is still open", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A handler that returns is done with its connection, which is closed for
// it.
func TestAHandlerThatReturnsLeavesItsConnectionClosed(t *testing.T) {
	returned := make(chan struct{})
	l, _ := listenWith(t, newKey(t), map[string]func(node.Conn){
		jswire.ALPN: func(node.Conn) { close(returned) },
	})
	c, err := dialFor(t, jswire.ALPN, newKey(t), l)
	if err != nil {
		t.Fatalf("dialing: %v", err)
	}
	select {
	case <-returned:
	case <-time.After(patience):
		t.Fatal("timed out waiting for the handler")
	}
	ended(t, "the handler has returned", c)
}

// Close of the listener closes what a handler still has.
func TestCloseOfTheListenerClosesTheConnectionOfAHandler(t *testing.T) {
	handed := make(chan node.Conn, 1)
	left := make(chan struct{})
	l, _ := listenWith(t, newKey(t), map[string]func(node.Conn){
		jswire.ALPN: func(c node.Conn) {
			handed <- c
			<-c.Done()
			close(left)
		},
	})
	c, err := dialFor(t, jswire.ALPN, newKey(t), l)
	if err != nil {
		t.Fatalf("dialing: %v", err)
	}
	var server node.Conn
	select {
	case server = <-handed:
	case <-time.After(patience):
		t.Fatal("timed out waiting for the handler")
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-server.Done():
	case <-time.After(patience):
		t.Fatal("the connection of the handler is not done after Close of the listener")
	}
	select {
	case <-left:
	case <-time.After(patience):
		t.Fatal("the handler did not return after Close of the listener")
	}
	ended(t, "the listener is closed", c)
}

// What cannot be a handler is refused before anything is bound.
func TestListenRefusesHandlersThatCannotBe(t *testing.T) {
	tests := []struct {
		name     string
		handlers map[string]func(node.Conn)
		want     string
	}{
		{"great-machine's own protocol", map[string]func(node.Conn){wire.ALPN: func(node.Conn) {}}, wire.ALPN},
		{"no protocol", map[string]func(node.Conn){"": func(node.Conn) {}}, "no ALPN"},
		{"no handler", map[string]func(node.Conn){jswire.ALPN: nil}, jswire.ALPN},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := node.Listen(context.Background(), node.ListenConfig{Key: newKey(t), Local: true, Handlers: tt.handlers, Log: quiet})
			if err == nil {
				l.Close()
				t.Fatal("Listen bound an endpoint")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Listen: %v, want an error that names %q", err, tt.want)
			}
		})
	}
}

// The handlers are what Listen was given: a map that is changed
// afterwards changes nothing.
func TestTheHandlersAreThoseOfTheTimeOfListen(t *testing.T) {
	handed := make(chan node.Conn, 1)
	handlers := map[string]func(node.Conn){
		jswire.ALPN: func(c node.Conn) {
			handed <- c
			<-c.Done()
		},
	}
	l, own := listenWith(t, newKey(t), handlers)
	delete(handlers, jswire.ALPN)

	client := newKey(t)
	if _, err := dialFor(t, jswire.ALPN, client, l); err != nil {
		t.Fatalf("dialing for %s: %v", jswire.ALPN, err)
	}
	select {
	case server := <-handed:
		if got := server.RemoteID(); got != client.ID() {
			t.Errorf("the handler's connection is of %s, want the client, %s", got, client.ID())
		}
	case got := <-own:
		t.Fatalf("Accept returned the connection of %s, which is the handler's", got.RemoteID())
	case <-time.After(patience):
		t.Fatal("timed out waiting for the handler to be given the connection: the entry that was deleted from the map took it away")
	}
}
