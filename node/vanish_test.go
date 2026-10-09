package node

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"
)

// The tests of a peer that vanishes are inside the package, because they
// shorten the idle timeout and take an endpoint away without a word,
// neither of which the package lets anybody else do.

const (
	// testIdle and testKeepAlive stand in for the 6 and 2 seconds.
	testIdle      = time.Second
	testKeepAlive = 300 * time.Millisecond
	// noticedWithin is how long the tests wait for a vanished peer to be
	// noticed.
	noticedWithin = 20 * time.Second
)

// vanishPair makes a connection over loopback whose dialing side asks for
// the short idle timeout.
func vanishPair(t *testing.T) (client, server *conn, l *listener) {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), noticedWithin)
	defer cancel()

	serverKey, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(ctx, ListenConfig{Key: serverKey, Local: true, Log: quiet})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	c, err := Dial(ctx, DialConfig{
		Key:         clientKey,
		Server:      ln.ID(),
		Addrs:       []netip.AddrPort{ln.LocalAddr()},
		Log:         quiet,
		idleTimeout: testIdle,
		keepAlive:   testKeepAlive,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	s, err := ln.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return c.(*conn), s.(*conn), ln.(*listener)
}

// noticed waits for done, and fails the test when it is closed at once,
// which would mean the peer was told, or not at all.
func noticed(t *testing.T, done <-chan struct{}, since time.Time) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(noticedWithin):
		t.Fatalf("the vanished peer was not noticed within %v", noticedWithin)
	}
	if took := time.Since(since); took < testIdle/2 {
		t.Errorf("Done was closed after %v, sooner than the idle timeout of %v allows: the peer did not vanish, it was told", took, testIdle)
	}
}

// A connection that is quiet is not one whose peer is gone: the
// keep-alive holds it for longer than the idle timeout.
func TestAQuietConnectionStays(t *testing.T) {
	t.Parallel()
	client, server, _ := vanishPair(t)
	select {
	case <-client.Done():
		t.Fatal("the quiet connection ended on the side that dialed")
	case <-server.Done():
		t.Fatal("the quiet connection ended on the side that accepted")
	case <-time.After(3 * testIdle):
	}
}

// The side that accepted notices that the side that dialed is gone: draal
// and a zathras whose pod was killed. The idle timeout the dial asked for
// holds for the side that listens too, but that side takes no less than
// 5 seconds from its peer, and jaccard-store, which binds it, lets nobody
// set its own. So this test takes 5 to 8 seconds, whatever testIdle is.
func TestTheVanishedDialerIsNoticed(t *testing.T) {
	t.Parallel()
	client, server, _ := vanishPair(t)
	start := time.Now()
	// Shutting the endpoint down without closing the connection first
	// sends nothing: to the peer it is as if the machine were gone.
	if err := client.release(); err != nil {
		t.Fatalf("shutting the endpoint down: %v", err)
	}
	noticed(t, server.Done(), start)
}

// The side that dialed notices that the side that accepted is gone: a
// zathras and a draal that was killed.
func TestTheVanishedListenerIsNoticed(t *testing.T) {
	t.Parallel()
	client, _, l := vanishPair(t)
	start := time.Now()
	if err := l.ep.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutting the endpoint down: %v", err)
	}
	noticed(t, client.Done(), start)
}

// What the program runs with, so that nobody changes it without meeting
// the contract: a peer that vanishes is noticed within 10 seconds, which
// is the idle timeout and one keep-alive of the side that listens, half
// the idle timeout.
func TestTheTimesOfTheProgram(t *testing.T) {
	if worst := idleTimeout + idleTimeout/2; worst > 10*time.Second {
		t.Errorf("an idle timeout of %v lets a vanished peer go unnoticed for %v, more than 10 seconds", idleTimeout, worst)
	}
	if keepAlive > idleTimeout/2 {
		t.Errorf("a keep-alive of %v is more than half the idle timeout of %v", keepAlive, idleTimeout)
	}
}
