package push

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// watch replaces the hook of the package for the length of the test. The
// work on a store path waits in it until hold is closed. watch returns a
// function that says how many are at work now and how many were at most.
func watch(t *testing.T, hold <-chan struct{}) func() (now, most int) {
	t.Helper()
	var mu sync.Mutex
	var now, most int
	old := pushing
	pushing = func(string) func() {
		mu.Lock()
		now++
		most = max(most, now)
		mu.Unlock()
		<-hold
		return func() {
			mu.Lock()
			now--
			mu.Unlock()
		}
	}
	t.Cleanup(func() { pushing = old })
	return func() (int, int) {
		mu.Lock()
		defer mu.Unlock()
		return now, most
	}
}

// until fails the test unless cond comes true before the deadline.
func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPushJobs(t *testing.T) {
	tests := []struct {
		jobs, want int
	}{
		{1, 1},
		{2, 2},
		{0, 5}, // the default
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("jobs %d", tt.jobs), func(t *testing.T) {
			srv := startServer(t)
			cfg := srv.config()
			cfg.Jobs = tt.jobs
			paths := storePaths(t, 8)
			hold := make(chan struct{})
			count := watch(t, hold)

			type outcome struct {
				res Result
				err error
			}
			done := make(chan outcome, 1)
			go func() {
				res, err := push(t, cfg, paths)
				done <- outcome{res, err}
			}()
			// As many as may be at work are held in the hook. Were more
			// let in, they would be counted there too.
			until(t, fmt.Sprintf("%d store paths are being pushed", tt.want), func() bool {
				now, _ := count()
				return now >= tt.want
			})
			close(hold)
			out := <-done
			if out.err != nil {
				t.Fatalf("Push: %v", out.err)
			}
			if out.res != (Result{Pushed: 8, Present: 0}) {
				t.Errorf("Push = %+v, want eight pushed", out.res)
			}
			if _, most := count(); most != tt.want {
				t.Errorf("%d store paths were pushed at a time, want %d", most, tt.want)
			}
			empty(t, cfg.TempDir)
		})
	}
}

// An interrupt while store paths are being pushed: Push returns soon,
// says why, and leaves nothing in the temporary directory.
func TestPushCancelled(t *testing.T) {
	srv := startServer(t)
	cfg := srv.config()
	cfg.Jobs = 2
	paths := storePaths(t, 6)
	hold := make(chan struct{})
	count := watch(t, hold)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := Push(ctx, cfg, paths)
		done <- outcome{res, err}
	}()
	until(t, "two store paths are being pushed", func() bool {
		now, _ := count()
		return now == 2
	})
	cancel()
	close(hold)

	select {
	case out := <-done:
		if !errors.Is(out.err, context.Canceled) {
			t.Errorf("Push = %+v, %v; want context.Canceled", out.res, out.err)
		}
		if out.res.Pushed != 0 {
			t.Errorf("Push = %+v, want nothing pushed", out.res)
		}
	case <-time.After(testTimeout):
		t.Fatal("Push did not return after its context was cancelled")
	}
	if _, most := count(); most != 2 {
		t.Errorf("%d store paths were begun, want the 2 that were at work", most)
	}
	if got := srv.refs(); len(got) != 0 {
		t.Errorf("the server has the references %q, want none", got)
	}
	empty(t, cfg.TempDir)
}
