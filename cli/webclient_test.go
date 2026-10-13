package cli

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/node/nodetest"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/wire"
)

// pageDraal is a draal for the page of these tests, on a network in
// memory. It answers who the key is, the grants and the fleet, counts
// how often it was dialed, and can hold a dial up, fail it, and end the
// connections it has.
type pageDraal struct {
	t        *testing.T
	network  *nodetest.Net
	listener node.Listener

	mu     sync.Mutex
	dials  int
	fail   error         // what a dial fails with, if set
	hold   chan struct{} // what a dial waits for, if set
	conns  []node.Conn   // the connections that were accepted
	watchs int           // how many watch the fleet now
}

// aPageDraal starts a draal of the page, which ends with the test.
func aPageDraal(t *testing.T) *pageDraal {
	t.Helper()
	d := &pageDraal{t: t, network: nodetest.New()}
	d.listener = d.network.Listen(nodetest.ID(1))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for {
			conn, err := d.listener.Accept(ctx)
			if err != nil {
				return
			}
			d.mu.Lock()
			d.conns = append(d.conns, conn)
			d.mu.Unlock()
			go func() {
				for {
					s, err := conn.AcceptStream(ctx)
					if err != nil {
						return
					}
					go d.serve(s)
				}
			}()
		}
	}()
	return d
}

// serve answers the request of one stream.
func (d *pageDraal) serve(s node.Stream) {
	defer s.Close()
	var req wire.Request
	if err := wire.Read(s, &req); err != nil {
		return
	}
	hold := func() {
		var nothing wire.Request
		wire.Read(s, &nothing)
	}
	switch {
	case req.Me != nil:
		wire.Write(s, &wire.Response{Me: &wire.Identity{ID: nodetest.ID(2), Grants: []wire.Grant{{ID: nodetest.ID(2), Role: wire.RoleAdmin, Fixed: true}}}})
	case req.Grants != nil:
		wire.Write(s, &wire.Response{Grants: &wire.GrantList{Grants: []wire.Grant{{ID: nodetest.ID(2), Role: wire.RoleAdmin, Fixed: true}}}})
	case req.Watch != nil:
		d.mu.Lock()
		d.watchs++
		d.mu.Unlock()
		wire.Write(s, &wire.Response{Fleet: &wire.Fleet{Services: []wire.Service{{Name: "web"}}}})
		hold()
		d.mu.Lock()
		d.watchs--
		d.mu.Unlock()
		return
	case req.Rollouts != nil:
		wire.WriteError(s, wire.Errorf(wire.CodeUnauthorized, "no deployer of %s", req.Rollouts.Service))
	default:
		wire.WriteError(s, wire.Errorf(wire.CodeBadRequest, "the draal of the test answers no such request"))
	}
	s.CloseWrite()
	hold()
}

// dial is what the page's client dials with.
func (d *pageDraal) dial(ctx context.Context) (*operator.Client, <-chan struct{}, error) {
	d.mu.Lock()
	d.dials++
	fail, hold := d.fail, d.hold
	d.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	if fail != nil {
		return nil, nil, fail
	}
	conn, err := d.network.Dial(ctx, nodetest.ID(2), nodetest.ID(1))
	if err != nil {
		return nil, nil, err
	}
	return operator.New(conn), conn.Done(), nil
}

// dialed returns how often the draal was dialed.
func (d *pageDraal) dialed() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials
}

// watching returns how many watch the fleet.
func (d *pageDraal) watching() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.watchs
}

// cut ends every connection the draal has, as a draal that goes away.
func (d *pageDraal) cut() {
	d.mu.Lock()
	conns := d.conns
	d.conns = nil
	d.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
}

// The client of the page dials draal when it is first asked for
// something, and not before; it keeps the one connection, and asks
// draal over it.
func TestThePagesClientDialsWhenItIsFirstAsked(t *testing.T) {
	d := aPageDraal(t)
	c := newPageClient(d.dial)
	defer c.Close()
	if n := d.dialed(); n != 0 {
		t.Fatalf("draal was dialed %d times before anything was asked", n)
	}
	for range 3 {
		me, err := c.Me(t.Context())
		if err != nil || me.ID != nodetest.ID(2) {
			t.Fatalf("Me = %+v, %v", me, err)
		}
	}
	if grants, err := c.Grants(t.Context()); err != nil || len(grants) != 1 {
		t.Errorf("Grants = %+v, %v", grants, err)
	}
	if n := d.dialed(); n != 1 {
		t.Errorf("draal was dialed %d times for four requests, want once", n)
	}
	// What draal refuses is passed on as it refused it.
	if _, err := c.Rollouts(t.Context(), "api"); wire.CodeOf(err) != wire.CodeUnauthorized || !strings.Contains(err.Error(), "no deployer of api") {
		t.Errorf("Rollouts = %v, want the refusal of draal", err)
	}
}

// Those who ask while draal is being dialed wait for the one dial.
func TestThePagesClientDialsOnceForAllWhoAsk(t *testing.T) {
	d := aPageDraal(t)
	d.hold = make(chan struct{})
	c := newPageClient(d.dial)
	defer c.Close()
	const asking = 8
	results := make(chan error, asking)
	for range asking {
		go func() {
			_, err := c.Me(context.Background())
			results <- err
		}()
	}
	waitFor(t, "draal to be dialed", func() bool { return d.dialed() >= 1 })
	time.Sleep(50 * time.Millisecond)
	close(d.hold)
	for range asking {
		if err := <-results; err != nil {
			t.Errorf("Me: %v", err)
		}
	}
	if n := d.dialed(); n != 1 {
		t.Errorf("draal was dialed %d times for %d who asked at once, want once", n, asking)
	}
}

// What is asked while draal cannot be reached is refused as unavailable,
// with what the dial said. The next who asks dials again.
func TestThePagesClientWhileDraalCannotBeReached(t *testing.T) {
	d := aPageDraal(t)
	d.fail = errors.New("no address found")
	c := newPageClient(d.dial)
	defer c.Close()
	for i := range 2 {
		_, err := c.Me(t.Context())
		if wire.CodeOf(err) != wire.CodeUnavailable || !strings.Contains(err.Error(), "no address found") {
			t.Fatalf("Me = %v, want unavailable with what the dial said", err)
		}
		if n := d.dialed(); n != i+1 {
			t.Errorf("draal was dialed %d times after %d requests", n, i+1)
		}
	}
	if _, err := c.Watch(t.Context()); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("Watch = %v, want unavailable", err)
	}
	if _, err := c.Shell(t.Context(), nodetest.ID(7), wire.ShellRequest{}); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("Shell = %v, want unavailable", err)
	}
	// And it is there again.
	d.mu.Lock()
	d.fail = nil
	d.mu.Unlock()
	if _, err := c.Me(t.Context()); err != nil {
		t.Errorf("Me, when draal can be reached again: %v", err)
	}
}

// A connection that was lost ends what was asked on it, and the next who
// asks dials again.
func TestThePagesClientDialsAgainAfterTheConnectionWasLost(t *testing.T) {
	d := aPageDraal(t)
	c := newPageClient(d.dial)
	defer c.Close()
	fleets, err := c.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if f := <-fleets; len(f.Services) != 1 {
		t.Fatalf("the fleet is %+v", f)
	}
	d.cut()
	select {
	case _, ok := <-fleets:
		if ok {
			t.Error("a fleet came after the connection was lost")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the watch did not end with its connection")
	}
	if me, err := c.Me(t.Context()); err != nil || me.ID != nodetest.ID(2) {
		t.Fatalf("Me after the connection was lost = %+v, %v", me, err)
	}
	if n := d.dialed(); n != 2 {
		t.Errorf("draal was dialed %d times, want twice: once more after the connection was lost", n)
	}
}

// Who asks gives up with its own context, and the dial goes on for the
// others.
func TestThePagesClientGivesUpWithWhoAsks(t *testing.T) {
	d := aPageDraal(t)
	d.hold = make(chan struct{})
	c := newPageClient(d.dial)
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	gone := make(chan error, 1)
	go func() {
		_, err := c.Me(ctx)
		gone <- err
	}()
	stays := make(chan error, 1)
	go func() {
		_, err := c.Me(context.Background())
		stays <- err
	}()
	waitFor(t, "draal to be dialed", func() bool { return d.dialed() >= 1 })
	cancel()
	select {
	case err := <-gone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("who gave up was answered %v, want the error of its context", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("who gave up was not answered")
	}
	close(d.hold)
	if err := <-stays; err != nil {
		t.Errorf("who waited was answered %v", err)
	}
	if n := d.dialed(); n != 1 {
		t.Errorf("draal was dialed %d times, want once", n)
	}
}

// Close ends a dial that is under way and the connection, and what is
// asked afterwards is unavailable.
func TestThePagesClientClosed(t *testing.T) {
	d := aPageDraal(t)
	c := newPageClient(d.dial)
	fleets, err := c.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	<-fleets
	c.Close()
	if _, ok := <-fleets; ok {
		t.Error("a fleet came after the client was closed")
	}
	if _, err := c.Me(t.Context()); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("Me after Close = %v, want unavailable", err)
	}
	if n := d.dialed(); n != 1 {
		t.Errorf("draal was dialed %d times, want once: not again after Close", n)
	}
	c.Close() // a second time is nothing

	// One that is closed while it dials ends the dial.
	d2 := aPageDraal(t)
	d2.hold = make(chan struct{})
	c2 := newPageClient(d2.dial)
	asked := make(chan error, 1)
	go func() {
		_, err := c2.Me(context.Background())
		asked <- err
	}()
	waitFor(t, "draal to be dialed", func() bool { return d2.dialed() >= 1 })
	closed := make(chan struct{})
	go func() {
		c2.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(30 * time.Second):
		t.Fatal("Close waits for a dial that does not end")
	}
	if err := <-asked; wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("who asked while the client was closed was answered %v, want unavailable", err)
	}
}
