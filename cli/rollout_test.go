package cli

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/push"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// What follows -- are the arguments of the service, verbatim, flags of
// rollout among them.
func TestReadRollout(t *testing.T) {
	cleanEnvironment(t)
	var got rolloutConfig
	reader := func(c *cli.Context) (err error) {
		got, err = readRollout(c)
		return err
	}
	tests := []struct {
		name string
		env  map[string]string
		args []string
		want rolloutConfig
	}{
		{"arguments of the service", nil,
			[]string{"web", "/nix/store/x/bin/web", "--", "--port", "80", "-v"},
			rolloutConfig{service: "web", path: "/nix/store/x/bin/web", args: []string{"--port", "80", "-v"}, parallel: 1, jobs: 5}},
		{"no arguments of the service", nil,
			[]string{"web", "./result/bin/web"},
			rolloutConfig{service: "web", path: "./result/bin/web", parallel: 1, jobs: 5}},
		{"nothing behind --", nil,
			[]string{"web", "./result/bin/web", "--"},
			rolloutConfig{service: "web", path: "./result/bin/web", args: []string{}, parallel: 1, jobs: 5}},
		{"flags of rollout, and the same words behind --", nil,
			[]string{"--parallel", "0", "--detach", "--jobs", "2", "--temp-dir", "/var/tmp", "web", "/nix/store/x/bin/web", "--", "--detach", "--", "--parallel", "9"},
			rolloutConfig{service: "web", path: "/nix/store/x/bin/web", args: []string{"--detach", "--", "--parallel", "9"},
				parallel: 0, detach: true, jobs: 2, tempDir: "/var/tmp"}},
		{"the variables, and a flag that wins over its own",
			map[string]string{"GREAT_MACHINE_PARALLEL": "3", "GREAT_MACHINE_DETACH": "true", "GREAT_MACHINE_JOBS": "9", "GREAT_MACHINE_TEMP_DIR": "/scratch"},
			[]string{"--jobs", "1", "web", "/nix/store/x/bin/web"},
			rolloutConfig{service: "web", path: "/nix/store/x/bin/web", parallel: 3, detach: true, jobs: 1, tempDir: "/scratch"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for name, value := range tt.env {
				t.Setenv(name, value)
			}
			read(t, rolloutCommand(), reader, tt.args...)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// fakeRollout is a draal that answers a rollout of web with #13 to
// generation 8, and the parts of a rollout that are not draal's.
type fakeRollout struct {
	fake    *fakeDraal
	client  *operator.Client
	key     node.Key
	pushed  push.Config
	paths   []string
	pushErr error
}

func newFakeRollout(t *testing.T, last wire.RolloutInfo) *fakeRollout {
	t.Helper()
	key, err := node.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeDraal{
		store:  wire.StoreInfo{Server: wire.ID(idC), Prefix: "nix/"},
		answer: wire.RolloutInfo{ID: 13, Service: "web", Generation: 8, State: wire.RolloutPreparing},
		fleets: []wire.Fleet{
			rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutPreparing, Prepared: 2}),
			rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutActivating, Prepared: 3, Activated: 1}),
			rolloutFleet(last),
		},
	}
	return &fakeRollout{fake: fake, client: fake.start(t), key: key}
}

func (f *fakeRollout) deps() rolloutDeps {
	return rolloutDeps{
		binary: func(path string) (string, string, error) {
			if path != "./result/bin/web" {
				return "", "", errors.New(path + ": no such file")
			}
			return "/nix/store/abc-web/bin/web", "/nix/store/abc-web", nil
		},
		checkLinux: func(string) error { return nil },
		closure: func(_ context.Context, storePath string) ([]string, error) {
			return []string{"/nix/store/aaa-glibc", storePath, "/nix/store/zzz-openssl"}, nil
		},
		connect: func(context.Context) (*operator.Client, node.Key, error) { return f.client, f.key, nil },
		push: func(_ context.Context, cfg push.Config, storePaths []string) (push.Result, error) {
			f.pushed, f.paths = cfg, storePaths
			if f.pushErr != nil {
				cfg.Progress.Found(1, 2)
				return push.Result{}, f.pushErr
			}
			cfg.Progress.Found(1, 2)
			cfg.Progress.Pushed(storePaths[0], 1024)
			cfg.Progress.Pushed(storePaths[1], 2048)
			return push.Result{Pushed: 2, Present: 1}, nil
		},
	}
}

func TestRunRollout(t *testing.T) {
	f := newFakeRollout(t, wire.RolloutInfo{ID: 13, State: wire.RolloutCompleted})
	var stdout, stderr buffer
	cfg := rolloutConfig{service: "web", path: "./result/bin/web", args: []string{"--port", "80"}, parallel: 2, jobs: 5, tempDir: "/scratch"}
	if err := runRollout(context.Background(), cfg, f.deps(), &stdout, plainProgress(&stderr)); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"resolving the binary: /nix/store/abc-web/bin/web (0.0s)",
		"reading the closure: 3 paths (0.0s)",
		"connecting to draal (0.0s)",
		"asking the store: 1 of 3 already there (0.0s)",
		"pushing: 2 paths, 3.0 KiB (0.0s)",
		"rolling out: generation 8, rollout #13 (0.0s)",
		"preparing: 3 zathrases (0.0s)",
		"activating: 3 zathrases (0.0s)",
		"total: rollout #13 completed: web runs generation 8 (0.0s)",
		"",
	}, "\n")
	if got := stderr.String(); got != want {
		t.Errorf("progress:\n%s\nwant:\n%s", got, want)
	}
	if stdout.String() != "" {
		t.Errorf("stdout is %q, want nothing", stdout.String())
	}

	// The push went to the store draal named, as the operator.
	if f.pushed.Server != wire.ID(idC) || f.pushed.Prefix != "nix/" || f.pushed.Jobs != 5 || f.pushed.TempDir != "/scratch" || f.pushed.Key.ID() != f.key.ID() {
		t.Errorf("the push was configured %+v", f.pushed)
	}
	if want := []string{"/nix/store/aaa-glibc", "/nix/store/abc-web", "/nix/store/zzz-openssl"}; !reflect.DeepEqual(f.paths, want) {
		t.Errorf("pushed %v, want %v", f.paths, want)
	}
	// draal was asked for the rollout with the names of the closure.
	var asked *wire.RolloutRequest
	for _, req := range f.fake.asked() {
		if req.Rollout != nil {
			asked = req.Rollout
		}
	}
	wantReq := &wire.RolloutRequest{Service: "web", Binary: "/nix/store/abc-web/bin/web", Args: []string{"--port", "80"},
		Closure: []string{"aaa-glibc", "abc-web", "zzz-openssl"}, Parallel: 2}
	if !reflect.DeepEqual(asked, wantReq) {
		t.Errorf("draal was asked %+v, want %+v", asked, wantReq)
	}
}

func TestRunRolloutHalted(t *testing.T) {
	f := newFakeRollout(t, wire.RolloutInfo{ID: 13, State: wire.RolloutHalted, Reason: "web-b: exited with status 1 after 0.2s"})
	var stdout, stderr buffer
	err := runRollout(context.Background(), rolloutConfig{service: "web", path: "./result/bin/web", jobs: 5}, f.deps(), &stdout, plainProgress(&stderr))
	if err == nil || err.Error() != "rollout #13 of web halted: web-b: exited with status 1 after 0.2s" {
		t.Fatalf("runRollout returned %v", err)
	}
	if !strings.HasSuffix(stderr.String(), "preparing: 3 zathrases (0.0s)\nactivating: failed after 0.0s\n") {
		t.Errorf("progress:\n%s", stderr.String())
	}
}

// A push that fails ends the command before draal has heard of a rollout.
func TestRunRolloutPushFails(t *testing.T) {
	f := newFakeRollout(t, wire.RolloutInfo{ID: 13, State: wire.RolloutCompleted})
	f.pushErr = errors.New("/nix/store/aaa-glibc: the server is full")
	var stdout, stderr buffer
	err := runRollout(context.Background(), rolloutConfig{service: "web", path: "./result/bin/web", jobs: 5}, f.deps(), &stdout, plainProgress(&stderr))
	if err == nil || err.Error() != "pushing the closure: /nix/store/aaa-glibc: the server is full" {
		t.Fatalf("runRollout returned %v", err)
	}
	if !strings.HasSuffix(stderr.String(), "asking the store: 1 of 3 already there (0.0s)\npushing: failed after 0.0s\n") {
		t.Errorf("progress:\n%s", stderr.String())
	}
	for _, req := range f.fake.asked() {
		if req.Rollout != nil {
			t.Error("draal was asked for a rollout after the push had failed")
		}
	}
}

// A PATH that is no binary ends the command before anyone is dialed.
func TestRunRolloutBadPath(t *testing.T) {
	f := newFakeRollout(t, wire.RolloutInfo{ID: 13, State: wire.RolloutCompleted})
	deps := f.deps()
	deps.connect = func(context.Context) (*operator.Client, node.Key, error) {
		t.Error("draal was dialed for a PATH that is no binary")
		return nil, node.Key{}, errors.New("not to be dialed")
	}
	var stdout, stderr buffer
	err := runRollout(context.Background(), rolloutConfig{service: "web", path: "./nothing", jobs: 5}, deps, &stdout, plainProgress(&stderr))
	if err == nil || err.Error() != "./nothing: no such file" {
		t.Fatalf("runRollout returned %v", err)
	}
	if got, want := stderr.String(), "resolving the binary: failed after 0.0s\n"; got != want {
		t.Errorf("progress: %q, want %q", got, want)
	}
}

func TestRunRolloutDetach(t *testing.T) {
	f := newFakeRollout(t, wire.RolloutInfo{ID: 13, State: wire.RolloutCompleted})
	var stdout, stderr buffer
	cfg := rolloutConfig{service: "web", path: "./result/bin/web", jobs: 5, detach: true}
	if err := runRollout(context.Background(), cfg, f.deps(), &stdout, plainProgress(&stderr)); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "rollout #13 of web to generation 8 was started; look at it with: great-machine status web\n"; got != want {
		t.Errorf("stdout is %q, want %q", got, want)
	}
	if !strings.HasSuffix(stderr.String(), "rolling out: generation 8, rollout #13 (0.0s)\n") {
		t.Errorf("progress:\n%s", stderr.String())
	}
	for _, req := range f.fake.asked() {
		if req.Watch != nil {
			t.Error("the fleet was watched with --detach")
		}
	}
}

// TestRunRollback asks a draal for a rollback, over the protocol, and
// follows the rollout.
func TestRunRollback(t *testing.T) {
	answer := wire.RolloutInfo{ID: 13, Service: "web", Generation: 7, State: wire.RolloutPreparing}
	fleets := func(last wire.RolloutInfo) []wire.Fleet {
		return []wire.Fleet{
			rolloutFleet(wire.RolloutInfo{ID: 12, State: wire.RolloutHalted, Reason: "what is rolled back from"}),
			rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutActivating, Prepared: 3, Activated: 2}),
			rolloutFleet(last),
		}
	}
	req := wire.RollbackRequest{Service: "web", Parallel: 0}

	t.Run("completed", func(t *testing.T) {
		fake := &fakeDraal{answer: answer, fleets: fleets(wire.RolloutInfo{ID: 13, State: wire.RolloutCompleted})}
		var stdout, stderr buffer
		if err := runRollback(context.Background(), fake.start(t), req, false, &stdout, plainProgress(&stderr)); err != nil {
			t.Fatal(err)
		}
		want := "rolling back: generation 7, rollout #13 (0.0s)\n" +
			"activating: 3 zathrases (0.0s)\n" +
			"total: rollout #13 completed: web runs generation 7 (0.0s)\n"
		if got := stderr.String(); got != want {
			t.Errorf("progress:\n%s\nwant:\n%s", got, want)
		}
		if asked := fake.asked()[0].Rollback; asked == nil || *asked != req {
			t.Errorf("draal was asked %+v, want %+v", asked, req)
		}
	})

	t.Run("halted", func(t *testing.T) {
		fake := &fakeDraal{answer: answer, fleets: fleets(wire.RolloutInfo{ID: 13, State: wire.RolloutHalted, Reason: "web-a: could not be started"})}
		var stdout, stderr buffer
		err := runRollback(context.Background(), fake.start(t), req, false, &stdout, plainProgress(&stderr))
		if err == nil || err.Error() != "rollout #13 of web halted: web-a: could not be started" {
			t.Fatalf("runRollback returned %v", err)
		}
	})

	t.Run("draal goes away", func(t *testing.T) {
		fake := &fakeDraal{answer: answer, fleets: fleets(wire.RolloutInfo{ID: 13, State: wire.RolloutActivating, Activated: 2})[:2], gone: true}
		var stdout, stderr buffer
		err := runRollback(context.Background(), fake.start(t), req, false, &stdout, plainProgress(&stderr))
		if err == nil || !strings.Contains(err.Error(), "rollout #13 of web goes on; look at it with: great-machine status web") {
			t.Fatalf("runRollback returned %v", err)
		}
	})

	t.Run("refused", func(t *testing.T) {
		fake := &fakeDraal{refuse: wire.Errorf(wire.CodeBadRequest, "the last rollout completed")}
		var stdout, stderr buffer
		err := runRollback(context.Background(), fake.start(t), req, false, &stdout, plainProgress(&stderr))
		if err == nil || err.Error() != "rolling web back: bad-request: the last rollout completed" || wire.CodeOf(err) != wire.CodeBadRequest {
			t.Fatalf("runRollback returned %v", err)
		}
		if got, want := stderr.String(), "rolling back: failed after 0.0s\n"; got != want {
			t.Errorf("progress: %q, want %q", got, want)
		}
	})

	t.Run("detach", func(t *testing.T) {
		fake := &fakeDraal{answer: answer}
		var stdout, stderr buffer
		if err := runRollback(context.Background(), fake.start(t), req, true, &stdout, plainProgress(&stderr)); err != nil {
			t.Fatal(err)
		}
		if got, want := stdout.String(), "rollout #13 of web to generation 7 was started; look at it with: great-machine status web\n"; got != want {
			t.Errorf("stdout is %q, want %q", got, want)
		}
	})
}

// push.Progress is called from several goroutines.
func TestPushSteps(t *testing.T) {
	var out buffer
	p := plainProgress(&out)
	p.begin("asking the store")
	steps := &pushSteps{p: p}
	steps.Found(40, 50)
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() { steps.Pushed("/nix/store/abc-web", 1024) })
	}
	wg.Wait()
	p.done("done")
	want := "asking the store: 40 of 90 already there (0.0s)\npushing: done (0.0s)\n"
	if got := out.String(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if got := steps.bytes.Load(); got != 50*1024 {
		t.Errorf("%d bytes were counted, want %d", got, 50*1024)
	}
}
