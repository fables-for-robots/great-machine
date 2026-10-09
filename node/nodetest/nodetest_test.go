package nodetest_test

import (
	"context"
	"io"
	"regexp"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/node/nodetest"
	"github.com/fables-for-robots/great-machine/wire"
)

// What a Conn and a Stream of this package do is tested in package node,
// by the tests that run over go-iroh as well (node/conn_test.go). Here is
// what the network in memory has of its own.

// patience is how long a test waits for what has to happen.
const patience = 10 * time.Second

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	t.Cleanup(cancel)
	return ctx
}

// wait returns what arrives on ch, and fails the test when nothing does.
func wait[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(patience):
		t.Fatalf("%s: nothing after %v", what, patience)
		panic("not reached")
	}
}

func TestID(t *testing.T) {
	hexID := regexp.MustCompile(`^[0-9a-f]{64}$`)
	seen := make(map[wire.ID]int)
	for _, n := range []int{0, 1, 2, 15, 16, 255, 1 << 20} {
		id := nodetest.ID(n)
		if !hexID.MatchString(string(id)) {
			t.Errorf("ID(%d) = %q, which is not 64 lowercase hexadecimal characters", n, id)
		}
		if other, ok := seen[id]; ok {
			t.Errorf("ID(%d) and ID(%d) are both %s", n, other, id)
		}
		seen[id] = n
	}
	if nodetest.ID(7) != nodetest.ID(7) {
		t.Error("ID(7) is not the same twice")
	}
	if got, want := nodetest.ID(255), wire.ID("00000000000000000000000000000000000000000000000000000000000000ff"); got != want {
		t.Errorf("ID(255) = %s, want %s", got, want)
	}
}

func TestDialAndAccept(t *testing.T) {
	ctx := testContext(t)
	n := nodetest.New()
	l := n.Listen(nodetest.ID(1))
	defer l.Close()
	if l.ID() != nodetest.ID(1) {
		t.Errorf("the listener's ID is %s, want %s", l.ID(), nodetest.ID(1))
	}

	// Dial returns before Accept is called, as a dial over iroh does.
	client, err := n.Dial(ctx, nodetest.ID(2), nodetest.ID(1))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()
	server, err := l.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer server.Close()
	if client.RemoteID() != nodetest.ID(1) {
		t.Errorf("the client's RemoteID is %s, want %s", client.RemoteID(), nodetest.ID(1))
	}
	if server.RemoteID() != nodetest.ID(2) {
		t.Errorf("the server's RemoteID is %s, want %s", server.RemoteID(), nodetest.ID(2))
	}
}

func TestDialFailsWhenNobodyListens(t *testing.T) {
	ctx := testContext(t)
	n := nodetest.New()
	if c, err := n.Dial(ctx, nodetest.ID(2), nodetest.ID(1)); err == nil {
		c.Close()
		t.Fatal("Dial to an ID nobody listens as: no error")
	}

	l := n.Listen(nodetest.ID(1))
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if c, err := n.Dial(ctx, nodetest.ID(2), nodetest.ID(1)); err == nil {
		c.Close()
		t.Fatal("Dial to an ID whose listener was closed: no error")
	}
}

func TestTwoNetsAreApart(t *testing.T) {
	ctx := testContext(t)
	a, b := nodetest.New(), nodetest.New()
	l := a.Listen(nodetest.ID(1))
	defer l.Close()
	if c, err := b.Dial(ctx, nodetest.ID(2), nodetest.ID(1)); err == nil {
		c.Close()
		t.Fatal("Dial reached a listener of another Net")
	}
}

func TestListenTwiceForOneIDPanics(t *testing.T) {
	n := nodetest.New()
	l := n.Listen(nodetest.ID(1))
	defer l.Close()
	defer func() {
		if recover() == nil {
			t.Error("a second Listen for an ID that listens did not panic")
		}
	}()
	n.Listen(nodetest.ID(1))
}

func TestListenAgainAfterClose(t *testing.T) {
	ctx := testContext(t)
	n := nodetest.New()
	if err := n.Listen(nodetest.ID(1)).Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// A draal that is started again listens as the ID it had.
	l := n.Listen(nodetest.ID(1))
	defer l.Close()
	c, err := n.Dial(ctx, nodetest.ID(2), nodetest.ID(1))
	if err != nil {
		t.Fatalf("Dial to the second listener: %v", err)
	}
	c.Close()
}

func TestListenerCloseEndsAccept(t *testing.T) {
	n := nodetest.New()
	l := n.Listen(nodetest.ID(1))
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
	n := nodetest.New()
	l := n.Listen(nodetest.ID(1))
	defer l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Accept(ctx); err == nil {
		t.Fatal("Accept returned a connection nobody made")
	}
}

// As over iroh: closing the listener ends the connections it accepted,
// and those that wait to be accepted.
func TestListenerCloseEndsItsConnections(t *testing.T) {
	ctx := testContext(t)
	n := nodetest.New()
	l := n.Listen(nodetest.ID(1))
	accepted, err := n.Dial(ctx, nodetest.ID(2), nodetest.ID(1))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	server, err := l.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	waiting, err := n.Dial(ctx, nodetest.ID(3), nodetest.ID(1))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	wait(t, server.Done(), "Done of the connection that was accepted")
	wait(t, accepted.Done(), "Done of the connection that was dialed")
	wait(t, waiting.Done(), "Done of the connection that waited to be accepted")
}

// stream opens a stream from client to server over a new Net.
func stream(t *testing.T) (opened, accepted node.Stream) {
	t.Helper()
	ctx := testContext(t)
	n := nodetest.New()
	l := n.Listen(nodetest.ID(1))
	t.Cleanup(func() { l.Close() })
	client, err := n.Dial(ctx, nodetest.ID(2), nodetest.ID(1))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	server, err := l.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	opened, err = client.OpenStream(ctx)
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if _, err := opened.Write([]byte{1}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	accepted, err = server.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream: %v", err)
	}
	if _, err := io.ReadFull(accepted, make([]byte, 1)); err != nil {
		t.Fatalf("Read: %v", err)
	}
	return opened, accepted
}

// The streams are buffered. This test has one goroutine: were a Write to
// wait for the peer's Read, it would never end.
func TestAWriteReturnsBeforeThePeerReads(t *testing.T) {
	opened, accepted := stream(t)
	frames := [][]byte{[]byte("a request"), make([]byte, 64<<10), []byte("and one more")}
	for _, frame := range frames {
		if n, err := opened.Write(frame); err != nil || n != len(frame) {
			t.Fatalf("Write of %d bytes: %d, %v", len(frame), n, err)
		}
	}
	// And in the other direction, before anything was read.
	if _, err := accepted.Write([]byte("an answer")); err != nil {
		t.Fatalf("Write of the answer: %v", err)
	}
	for _, frame := range frames {
		got := make([]byte, len(frame))
		if _, err := io.ReadFull(accepted, got); err != nil {
			t.Fatalf("Read of %d bytes: %v", len(frame), err)
		}
		if string(got) != string(frame) {
			t.Errorf("read %d bytes that are not the %d that were written", len(got), len(frame))
		}
	}
	got := make([]byte, len("an answer"))
	if _, err := io.ReadFull(opened, got); err != nil || string(got) != "an answer" {
		t.Errorf("read %q, %v; want the answer", got, err)
	}
}

// A Write waits when 512 KiB wait to be read, as a stream of QUIC does,
// and goes on when the peer reads. A frame may be larger than that: who
// writes one needs a peer that reads meanwhile.
func TestALargeWriteWaitsForThePeerToRead(t *testing.T) {
	opened, accepted := stream(t)
	const size = 4 << 20
	written := make(chan error, 1)
	go func() {
		_, err := opened.Write(make([]byte, size))
		written <- err
	}()
	select {
	case err := <-written:
		t.Fatalf("a Write of %d bytes returned %v before the peer read anything", size, err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := io.ReadFull(accepted, make([]byte, size)); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := wait(t, written, "the Write"); err != nil {
		t.Errorf("Write: %v", err)
	}
}

func TestAReadIntoNothingReturnsAtOnce(t *testing.T) {
	opened, _ := stream(t)
	if n, err := opened.Read(nil); n != 0 || err != nil {
		t.Errorf("Read(nil): %d, %v; want 0 and no error", n, err)
	}
}
