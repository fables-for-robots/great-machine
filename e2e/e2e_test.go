package e2e

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

func TestRolloutCompletes(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 3})
	info := h.rollout("--stdout", "hello")
	f := h.await(info, wire.RolloutCompleted)

	if svc := serviceOf(f); svc.Stable != info.Generation {
		t.Errorf("the stable generation is %d, want %d", svc.Stable, info.Generation)
	}
	for _, p := range h.pods {
		if got := p.fetched(); !slices.Contains(got, storeName) {
			t.Errorf("the store of %s was asked to fetch %q, want the closure, %q", p.name, got, storeName)
		}
		st := p.agent.State()
		if st.Generation != info.Generation || st.Phase != wire.PhaseRunning || st.Binary != h.binary {
			t.Errorf("%s has the state %+v, want it to run generation %d", p.name, st, info.Generation)
		}
		if !alive(st.PID) {
			t.Errorf("the process %d of %s is not alive", st.PID, p.name)
		}
	}
}

func TestRolloutOfABinaryThatExitsHaltsAndIsRolledBack(t *testing.T) {
	t.Parallel()
	// A service counts as running the target when it has run for
	// StableAfter. On a machine that is busy with the tests of every
	// package, a program that exits ten milliseconds after its start can
	// take some hundred milliseconds to get there, so StableAfter is far
	// above that here, and the first rollout goes to all three at once to
	// make up for the time.
	h := start(t, options{zathrases: 3, stableAfter: 3 * time.Second})
	good := h.rolloutParallel(0)
	h.await(good, wire.RolloutCompleted)
	pids := map[wire.ID]int{}
	for _, p := range h.pods {
		pids[p.id] = p.agent.State().PID
	}

	bad := h.rollout("--exit-after", "10ms", "--exit-code", "3")
	f := h.await(bad, wire.RolloutHalted)
	if reason := rolloutOf(f).Reason; !strings.Contains(reason, "web-0") || !strings.Contains(reason, "status 3") {
		t.Errorf("the reason is %q, want the first zathras and its exit status in it", reason)
	}
	if got := h.pods[0].agent.State().Generation; got != bad.Generation {
		t.Errorf("web-0 has generation %d, want the one that failed, %d", got, bad.Generation)
	}
	for _, p := range h.pods[1:] {
		st := p.agent.State()
		if st.Generation != good.Generation || st.Phase != wire.PhaseRunning || st.PID != pids[p.id] {
			t.Errorf("%s has the state %+v, want it to run generation %d as before", p.name, st, good.Generation)
		}
	}

	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()
	back, err := h.op.Rollback(ctx, wire.RollbackRequest{Service: service, Parallel: 1})
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if back.Generation != good.Generation {
		t.Fatalf("the rollback targets generation %d, want %d", back.Generation, good.Generation)
	}
	h.await(back, wire.RolloutCompleted)
	if st := h.pods[0].agent.State(); st.Generation != good.Generation || st.Phase != wire.PhaseRunning || !alive(st.PID) {
		t.Errorf("after the rollback web-0 has the state %+v, want it to run generation %d", st, good.Generation)
	}
}

func TestLogs(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 1})
	p := h.pods[0]
	h.await(h.rollout("--stdout", "hello"), wire.RolloutCompleted)

	// The ring holds what the service wrote, and the line of the
	// supervisor about its start.
	has := func(lines []wire.LogLine, stream, text string) bool {
		for _, line := range lines {
			if line.Stream == stream && strings.Contains(line.Text, text) {
				return true
			}
		}
		return false
	}
	var ring []wire.LogLine
	eventually(t, "the line of the service in the ring", func() bool {
		ctx, cancel := context.WithTimeout(t.Context(), deadline)
		defer cancel()
		lines, err := h.op.Logs(ctx, p.id, false)
		if err != nil {
			t.Fatalf("Logs: %v", err)
		}
		ring = nil
		for line := range lines {
			ring = append(ring, line)
		}
		return has(ring, "stdout", "hello")
	})
	if !has(ring, "zathras", "started generation 1") {
		t.Errorf("the ring lacks the supervisor's line about the start: %+v", ring)
	}

	// With follow, what is written later arrives.
	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()
	lines, err := h.op.Logs(ctx, p.id, true)
	if err != nil {
		t.Fatalf("Logs with follow: %v", err)
	}
	h.rollout("--stdout", "again")
	for line := range lines {
		if line.Stream == "stdout" && line.Text == "again" {
			return
		}
	}
	t.Fatal("the line of the second rollout did not arrive while following")
}

func TestShell(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 1})
	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()
	sh, err := h.op.Shell(ctx, h.pods[0].id, wire.ShellRequest{Cols: 80, Rows: 24, Term: "dumb"})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	defer sh.Close()
	// The terminal echoes the command, quotes and all; only its output
	// has the word in one piece.
	if _, err := sh.Write([]byte("echo mar''ker; exit 3\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	output, err := io.ReadAll(sh)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(string(output), "marker") {
		t.Errorf("the output is %q, want marker in it", output)
	}
	if status, err := sh.Wait(ctx); err != nil || status != 3 {
		t.Errorf("Wait = %d, %v; want 3", status, err)
	}
}

func TestWatch(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 3})
	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()
	fleets, err := h.op.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	first := <-fleets
	zs := zathrasesOf(first)
	if len(zs) != 3 {
		t.Fatalf("the fleet has %d zathrases, want 3", len(zs))
	}
	for i, z := range zs {
		if z.Name != h.pods[i].name || z.ID != h.pods[i].id || z.State.Phase != wire.PhaseIdle || z.Connected.IsZero() {
			t.Errorf("zathras %d of the fleet is %+v, want %s (%s), idle", i, z, h.pods[i].name, h.pods[i].id)
		}
	}

	info := h.rollout()
	for f := range fleets {
		if r := rolloutOf(f); r != nil && r.ID == info.ID && r.State == wire.RolloutCompleted && runs(f, info.Generation, 3) {
			return
		}
	}
	t.Fatal("the watcher did not see every zathras run the generation and the rollout complete")
}

func TestAPeerThatIsNoOperatorIsRefused(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 1})
	key, err := node.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	stranger := h.operator(key)
	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()
	if _, err := stranger.Watch(ctx); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("Watch = %v, want unauthorized", err)
	}
	_, err = stranger.Rollout(ctx, wire.RolloutRequest{
		Service: service, Binary: h.binary, Closure: []string{storeName}, Parallel: 1,
	})
	if wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("Rollout = %v, want unauthorized", err)
	}
	if _, err := stranger.Rollback(ctx, wire.RollbackRequest{Service: service}); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("Rollback = %v, want unauthorized", err)
	}
	// Of a zathras that is connected.
	if _, err := stranger.Logs(ctx, h.pods[0].id, false); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("Logs = %v, want unauthorized", err)
	}
	if sh, err := stranger.Shell(ctx, h.pods[0].id, wire.ShellRequest{Cols: 80, Rows: 24}); wire.CodeOf(err) != wire.CodeUnauthorized {
		if sh != nil {
			sh.Close()
		}
		t.Errorf("Shell = %v, want unauthorized", err)
	}
	if svc := serviceOf(h.fleetWhere("the fleet", func(wire.Fleet) bool { return true })); svc == nil || svc.Rollout != nil {
		t.Errorf("after a refused rollout the service is %+v, want it without a rollout", svc)
	}
}

func TestHealthCheck(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 1})
	p := h.pods[0]
	if status, body := p.healthz(t); status != http.StatusServiceUnavailable || body != "idle" {
		t.Errorf("before any rollout /healthz = %d %q, want 503 idle", status, body)
	}

	h.await(h.rollout(), wire.RolloutCompleted)
	if status, body := p.healthz(t); status != http.StatusOK || body != "running" {
		t.Errorf("while the service runs /healthz = %d %q, want 200 running", status, body)
	}

	h.rollout("--exit-after", "10ms", "--exit-code", "3")
	eventually(t, "the health check to fail with backoff", func() bool {
		status, body := p.healthz(t)
		return status == http.StatusServiceUnavailable && body == "backoff"
	})
}

func TestAZathrasWhoseConnectionIsCutComesBack(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 2})
	p := h.pods[0]
	info := h.rollout()
	f := h.await(info, wire.RolloutCompleted)
	before, _ := zathrasOf(f, p.id)
	pid := p.agent.State().PID

	p.cut()
	f = h.fleetWhere("the zathras to be in the fleet again", func(f wire.Fleet) bool {
		z, ok := zathrasOf(f, p.id)
		return ok && z.Connected.After(before.Connected)
	})
	z, _ := zathrasOf(f, p.id)
	if z.Name != p.name || z.State.Generation != info.Generation {
		t.Errorf("the zathras came back as %+v, want %s with generation %d", z, p.name, info.Generation)
	}
	if st := p.agent.State(); st.PID != pid || st.Phase != wire.PhaseRunning || !alive(pid) {
		t.Errorf("after the cut the service is %+v, want it to run still as process %d", st, pid)
	}
}

func TestDraalRestartsInTheMiddleOfARollout(t *testing.T) {
	t.Parallel()
	// Settle is longer than a zathras takes to dial again, and every
	// zathras takes a second to be done, so that the rollout is under
	// way for three.
	h := start(t, options{zathrases: 3, settle: time.Second, stableAfter: time.Second})
	h.await(h.rollout(), wire.RolloutCompleted)

	info := h.rollout("--stdout", "second")
	h.fleetWhere("the first zathras to run the target", func(f wire.Fleet) bool {
		r := rolloutOf(f)
		if r == nil || r.ID != info.ID || r.State != wire.RolloutActivating {
			return false
		}
		for _, z := range zathrasesOf(f) {
			if z.State.Generation == info.Generation {
				return true
			}
		}
		return false
	})

	h.restartDraal()
	f := h.await(info, wire.RolloutCompleted)
	if !runs(f, info.Generation, 3) {
		t.Errorf("after the rollout the zathrases are %+v, want all three to run generation %d", zathrasesOf(f), info.Generation)
	}
	if svc := serviceOf(f); svc.Stable != info.Generation {
		t.Errorf("the stable generation is %d, want %d", svc.Stable, info.Generation)
	}
}
