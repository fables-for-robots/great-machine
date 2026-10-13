package node_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/node/nodetest"
	"github.com/fables-for-robots/great-machine/wire"
)

// A connection that was dialed dials its peer again, for another
// protocol, from the endpoint it has: the peer sees the same endpoint ID
// on both, each carries streams, and the second ends with the first.
func TestAgainDialsAnotherProtocolFromTheSameEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const other = "probe/1"
	seen := make(chan wire.ID, 4)
	l, err := node.Listen(ctx, node.ListenConfig{Key: newKey(t), Local: true, Log: quiet, Handlers: map[string]func(node.Conn){
		other: func(c node.Conn) {
			seen <- c.RemoteID()
			for {
				s, err := c.AcceptStream(ctx)
				if err != nil {
					return
				}
				go func() {
					buf := make([]byte, 4)
					if _, err := io.ReadFull(s, buf); err == nil {
						s.Write(append([]byte("other:"), buf...))
					}
					s.CloseWrite()
				}()
			}
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	// A connection of another protocol is handed to its handler while
	// Accept is called, so it is called all along, as draal calls it.
	accepted := make(chan node.Conn, 4)
	go func() {
		for {
			c, err := l.Accept(ctx)
			if err != nil {
				return
			}
			accepted <- c
		}
	}()

	k := newKey(t)
	c := dial(t, k, l)
	var first node.Conn
	select {
	case first = <-accepted:
	case <-ctx.Done():
		t.Fatal("the connection of great-machine's protocol was not accepted")
	}
	second, err := node.Again(ctx, c, other)
	if err != nil {
		t.Fatalf("Again: %v", err)
	}
	select {
	case id := <-seen:
		if id != k.ID() || first.RemoteID() != k.ID() {
			t.Errorf("the peer saw %s for the other protocol and %s for great-machine's, want %s for both", id, first.RemoteID(), k.ID())
		}
	case <-ctx.Done():
		t.Fatal("the handler of the other protocol was not given the connection")
	}

	ask := func() (string, error) {
		s, err := second.OpenStreamConn(ctx)
		if err != nil {
			return "", err
		}
		defer s.Close()
		s.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err := s.Write([]byte("ping")); err != nil {
			return "", err
		}
		got, err := io.ReadAll(s)
		return string(got), err
	}
	for range 3 {
		if got, err := ask(); err != nil || got != "other:ping" {
			t.Fatalf("over the second connection: %q, %v", got, err)
		}
	}
	// The first is as it was.
	s, err := c.OpenStream(ctx)
	if err != nil {
		t.Fatalf("a stream of the first connection: %v", err)
	}
	if _, err := s.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	in, err := first.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("the peer did not get the stream of the first connection: %v", err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(in, buf); err != nil || string(buf) != "hello" {
		t.Fatalf("over the first connection: %q, %v", buf, err)
	}

	// The second ends with the first: the endpoint is the first's.
	c.Close()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := ask(); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the second connection still answers after the first was closed")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := second.Close(); err != nil {
		t.Logf("closing the second after it has ended: %v", err)
	}
}

// Only a connection that was dialed here has an endpoint to dial from.
func TestAgainRefusesAConnectionThatWasNotDialedHere(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, server := irohPair(t)
	_ = client
	if o, err := node.Again(ctx, server, "probe/1"); err == nil {
		o.Close()
		t.Error("a connection that was accepted dialed its peer again")
	}
	net := nodetest.New()
	lt := net.Listen(nodetest.ID(1))
	defer lt.Close()
	a, err := net.Dial(ctx, nodetest.ID(2), nodetest.ID(1))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if o, err := node.Again(ctx, a, "probe/1"); err == nil {
		o.Close()
		t.Error("a connection of a test's network dialed its peer again")
	}
}

func TestSignAndVerify(t *testing.T) {
	k, other := newKey(t), newKey(t)
	text := wire.StatementText(other.ID(), k.ID())
	sig := k.Sign(text)
	if len(sig) != 64 {
		t.Fatalf("a signature has %d bytes, want 64", len(sig))
	}
	if err := node.Verify(k.ID(), text, sig); err != nil {
		t.Errorf("the signature of the key is not verified by its ID: %v", err)
	}
	if err := node.Verify(other.ID(), text, sig); err == nil {
		t.Error("the signature is verified by the ID of another key")
	}
	if err := node.Verify(k.ID(), wire.StatementText(k.ID(), other.ID()), sig); err == nil {
		t.Error("the signature is verified for another text")
	}
	for _, bad := range [][]byte{nil, sig[:63], append(append([]byte{}, sig...), 0)} {
		if err := node.Verify(k.ID(), text, bad); err == nil {
			t.Errorf("a signature of %d bytes is verified", len(bad))
		}
	}
	if err := node.Verify("not an ID", text, sig); err == nil {
		t.Error("a signature is verified by what is no endpoint ID")
	}
}

func TestLoadKeyMakesNoFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.key")
	if _, err := node.LoadKey(missing); err == nil {
		t.Error("a key was loaded from a file that is not there")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("LoadKey made the file it did not find (%v)", err)
	}
	path := filepath.Join(dir, "role.key")
	made, err := node.LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := node.LoadKey(path)
	if err != nil || loaded.ID() != made.ID() {
		t.Errorf("LoadKey of a key file: %s, %v; want %s", loaded.ID(), err, made.ID())
	}
	if err := os.WriteFile(path, []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := node.LoadKey(path); err == nil {
		t.Error("a key was loaded from a file that holds none")
	}
}
