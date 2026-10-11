### Task 3: A prefix for every service, and who may do what with the store

Up to now draal was told a prefix and handed it to every rollout. From here on every service has references of its own, `/services/<service>/nix/store/<name>`, the `store` request of an operator names the service it asks for, and `authz` knows the two things a peer can ask of the store. Nothing answers the store's protocol yet (Tasks 4 to 6): this task is what the others stand on, and it changes what `rollout` asks and how many store paths it pushes at a time.

Between this task and Task 6 draal still has its old flags: `--jaccard-server` goes on naming the server of a push, and `--jaccard-prefix` is still read and no longer used.

**Files:**
- Modify: `wire/service.go`, `authz/authz.go`, `cli/rollout.go`, `demo/fleet.go`, `draal/control.go`, `draal/draal.go`, `draal/handler.go`, `operator/client.go`, `wire/messages.go`, `push/push.go`
- Test: `wire/service_test.go`, `authz/authz_test.go`, `cli/rollout_test.go`, `draal/control_test.go`, `draal/serve_test.go`, `draal/streams_test.go`, `operator/client_test.go`, `operator/close_test.go`, `web/fake_test.go`, `push/jobs_test.go`

**Interfaces:**
- Consumes: nothing of the other tasks.
- Produces:
  - `wire.StorePrefix(service string) string`: `"/services/" + service + "/nix/store/"`. It does not check the name.
  - `wire.StoreRequest{Service string}` (`json:"service"`).
  - `authz.OpStorePull` (`"store-pull"`, every peer) and `authz.OpStoreWrite` (`"store-write"`, operators); the service of both is `""`. `authz.OpStore` is asked with the service the request names.
  - `draal.Controller.Store(ctx context.Context, service string) (wire.StoreInfo, error)`: a name that is no name of a service is a `bad-request`; the prefix is `wire.StorePrefix(service)`.
  - `operator.Client.Store(ctx context.Context, service string) (wire.StoreInfo, error)`.
  - `push`: 20 jobs when `Config.Jobs` is 0; `great-machine rollout --jobs` is 20 unless told otherwise.

- [ ] **Step 1: Write the failing test: the prefix of a service's references**

Change `wire/service_test.go`:

```diff
--- a/wire/service_test.go
+++ b/wire/service_test.go
@@ -53,3 +53,19 @@ func TestValidService(t *testing.T) {
 		}
 	}
 }
+
+func TestStorePrefix(t *testing.T) {
+	if got, want := StorePrefix("web"), "/services/web/nix/store/"; got != want {
+		t.Errorf("StorePrefix(%q) = %q, want %q", "web", got, want)
+	}
+	// The references of two services are never the same, and none of one
+	// service is under the prefix of another: no prefix begins another.
+	names := []string{"web", "web-2", "we", "a", "a-b", "b"}
+	for _, a := range names {
+		for _, b := range names {
+			if a != b && strings.HasPrefix(StorePrefix(a), StorePrefix(b)) {
+				t.Errorf("the prefix of %q begins with that of %q", a, b)
+			}
+		}
+	}
+}
```

- [ ] **Step 2: Run it, and see it fail**

Run: `go test ./wire/`

Expected: it fails, with

```text
wire/service_test.go:58:18: undefined: StorePrefix
```

- [ ] **Step 3: Write the code: wire.StorePrefix**

Change `wire/service.go`:

```diff
--- a/wire/service.go
+++ b/wire/service.go
@@ -26,3 +26,13 @@ func ValidService(name string) error {
 	}
 	return nil
 }
+
+// StorePrefix returns what is put before the name of a store path to make
+// its reference in the store, for a store path of the service:
+// "/services/<service>/nix/store/". Every service has references of its
+// own, and the /nix/store of a pod is made of its service's alone. The
+// name of a service is a DNS label and needs no escaping; StorePrefix
+// does not check that service is one.
+func StorePrefix(service string) string {
+	return "/services/" + service + "/nix/store/"
+}
```

- [ ] **Step 4: Run it, and see it pass**

Run: `go test ./wire/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 5: Commit**

```bash
git add wire/service_test.go wire/service.go
git commit -m 'Wire: the prefix of a service'\''s references'
```

- [ ] **Step 6: Write the failing test: a pull of the store is every peer's, a write an operator's**

Change `authz/authz_test.go`:

```diff
--- a/authz/authz_test.go
+++ b/authz/authz_test.go
@@ -20,7 +20,7 @@ var operations = []struct {
 	service string
 }{
 	{OpRegister, "web"},
-	{OpStore, ""},
+	{OpStore, "web"},
 	{OpRollout, "web"},
 	{OpRollback, "web"},
 	{OpWatch, ""},
@@ -28,6 +28,13 @@ var operations = []struct {
 	{OpHistory, "web"},
 	{OpLogs, "web"},
 	{OpShell, "web"},
+	{OpStorePull, ""},
+	{OpStoreWrite, ""},
+}
+
+// open is whether every peer may do op, an operator or not.
+func open(op Operation) bool {
+	return op == OpRegister || op == OpStorePull
 }
 
 func TestAllowlist(t *testing.T) {
@@ -49,7 +56,7 @@ func TestAllowlist(t *testing.T) {
 			var a Authorizer = NewAllowlist(tt.operators)
 			for _, o := range operations {
 				err := a.Authorize(tt.peer, o.op, o.service)
-				allowed := tt.operator || o.op == OpRegister
+				allowed := tt.operator || open(o.op)
 				if allowed {
 					if err != nil {
 						t.Errorf("%s: refused: %v", o.op, err)
@@ -91,6 +98,32 @@ func TestRegisterIsAllowedOnEveryService(t *testing.T) {
 	}
 }
 
+// What the pods run is pulled by a key that draal does not know, the one
+// the mount of a zathras makes for itself: a pull of the store is open.
+// Everything else of the store is an operator's.
+func TestTheStoreIsPulledByEveryPeerAndWrittenByOperators(t *testing.T) {
+	a := NewAllowlist([]wire.ID{alice})
+	for _, peer := range []wire.ID{alice, mallory, ""} {
+		if err := a.Authorize(peer, OpStorePull, ""); err != nil {
+			t.Errorf("a pull of the store by %q: %v", peer, err)
+		}
+	}
+	if err := a.Authorize(alice, OpStoreWrite, ""); err != nil {
+		t.Errorf("a write to the store by an operator: %v", err)
+	}
+	err := a.Authorize(mallory, OpStoreWrite, "")
+	if wire.CodeOf(err) != wire.CodeUnauthorized {
+		t.Fatalf("a write to the store by a stranger: %v, want unauthorized", err)
+	}
+	if !strings.Contains(err.Error(), "store-write") || !strings.Contains(err.Error(), string(mallory)) {
+		t.Errorf("the refusal names neither the operation nor the peer: %q", err)
+	}
+	// The store request of an operator names a service now.
+	if err := a.Authorize(mallory, OpStore, "web"); err == nil || !strings.Contains(err.Error(), "web") {
+		t.Errorf("store on web by a stranger: %v, want a refusal that names the service", err)
+	}
+}
+
 func TestTheZeroAllowlistRefuses(t *testing.T) {
 	var a Allowlist
 	if err := a.Authorize(alice, OpShell, "web"); wire.CodeOf(err) != wire.CodeUnauthorized {
```

- [ ] **Step 7: Run it, and see it fail**

Run: `go test ./authz/`

Expected: it fails, with

```text
authz/authz_test.go:31:3: undefined: OpStorePull
```

- [ ] **Step 8: Write the code: authz.OpStorePull and OpStoreWrite**

Change `authz/authz.go`:

```diff
--- a/authz/authz.go
+++ b/authz/authz.go
@@ -9,8 +9,9 @@ import (
 // Operation is what a peer asks to do.
 type Operation string
 
-// The operations. OpRegister is what a zathras does; the others are an
-// operator's.
+// The operations. OpRegister is what a zathras does, and OpStorePull what
+// the mount of a zathras does, with a key of its own that draal does not
+// know; the others are an operator's.
 const (
 	OpRegister Operation = "register"
 	OpStore    Operation = "store"
@@ -21,15 +22,24 @@ const (
 	OpHistory  Operation = "history"
 	OpLogs     Operation = "logs"
 	OpShell    Operation = "shell"
+
+	// The two operations of the store, which draal answers on
+	// jaccard-store's protocol. OpStorePull is a pull of a reference.
+	// OpStoreWrite is everything else of that protocol: the three steps
+	// of a push, a list and a delete.
+	OpStorePull  Operation = "store-pull"
+	OpStoreWrite Operation = "store-write"
 )
 
 // Authorizer decides whether peer may do op on service. service is ""
-// for store and watch. A refusal is a *wire.Error with CodeUnauthorized.
+// for watch and for the two operations of the store. A refusal is a
+// *wire.Error with CodeUnauthorized.
 type Authorizer interface {
 	Authorize(peer wire.ID, op Operation, service string) error
 }
 
-// Allowlist lets every peer register and its operators do everything else.
+// Allowlist lets every peer register and pull from the store, and its
+// operators do everything else.
 type Allowlist struct {
 	operators map[wire.ID]struct{}
 }
@@ -49,10 +59,10 @@ func NewAllowlist(operators []wire.ID) Allowlist {
 }
 
 // Authorize returns nil when peer may do op on service: for OpRegister
-// always, and for every other operation when peer is an operator. The
-// refusal names the operation and the peer.
+// and OpStorePull always, and for every other operation when peer is an
+// operator. The refusal names the operation and the peer.
 func (a Allowlist) Authorize(peer wire.ID, op Operation, service string) error {
-	if op == OpRegister {
+	if op == OpRegister || op == OpStorePull {
 		return nil
 	}
 	if _, ok := a.operators[peer]; ok {
```

- [ ] **Step 9: Run it, and see it pass**

Run: `go test ./authz/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 10: Commit**

```bash
git add authz/authz_test.go authz/authz.go
git commit -m 'Authz: a pull of the store is every peer'\''s, a write an operator'\''s'
```

- [ ] **Step 11: Write the failing test: the store request names its service, and is answered with that service's prefix**

Change `cli/rollout_test.go`:

```diff
--- a/cli/rollout_test.go
+++ b/cli/rollout_test.go
@@ -98,7 +98,7 @@ func newFakeRollout(t *testing.T, last wire.RolloutInfo) *fakeRollout {
 		t.Fatal(err)
 	}
 	fake := &fakeDraal{
-		store:  wire.StoreInfo{Server: wire.ID(idC), Prefix: "nix/"},
+		store:  wire.StoreInfo{Server: wire.ID(idC), Prefix: "/services/web/nix/store/"},
 		answer: wire.RolloutInfo{ID: 13, Service: "web", Generation: 8, State: wire.RolloutPreparing},
 		fleets: []wire.Fleet{
 			rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutPreparing, Prepared: 2}),
@@ -147,7 +147,7 @@ func (f *fakeRollout) deps() rolloutDeps {
 			f.mu.Unlock()
 			for i, c := range clients {
 				cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
-				_, err := c.Store(cctx)
+				_, err := c.Store(cctx, "web")
 				cancel()
 				if err == nil {
 					f.t.Errorf("the push was called while client %d is open", i+1)
@@ -194,7 +194,7 @@ func TestRunRollout(t *testing.T) {
 	}
 
 	// The push went to the store draal named, as the operator.
-	if f.pushed.Server != wire.ID(idC) || f.pushed.Prefix != "nix/" || f.pushed.Jobs != 5 || f.pushed.TempDir != "/scratch" || f.pushed.Key.ID() != f.key.ID() {
+	if f.pushed.Server != wire.ID(idC) || f.pushed.Prefix != "/services/web/nix/store/" || f.pushed.Jobs != 5 || f.pushed.TempDir != "/scratch" || f.pushed.Key.ID() != f.key.ID() {
 		t.Errorf("the push was configured %+v", f.pushed)
 	}
 	if want := []string{"/nix/store/aaa-glibc", "/nix/store/abc-web", "/nix/store/zzz-openssl"}; !reflect.DeepEqual(f.paths, want) {
@@ -204,11 +204,15 @@ func TestRunRollout(t *testing.T) {
 		t.Errorf("the calls were %v, want %v", got, want)
 	}
 	// The store was asked of the first client, before the push, and the
-	// rollout of the second, after it.
+	// rollout of the second, after it. It was asked for the service that
+	// is rolled out, whose prefix is what the push went under.
 	var order []string
 	for _, req := range f.fake.asked() {
 		switch {
 		case req.Store != nil:
+			if req.Store.Service != "web" {
+				t.Errorf("the store was asked for the service %q, want web", req.Store.Service)
+			}
 			order = append(order, "store")
 		case req.Rollout != nil:
 			order = append(order, "rollout")
```

Change `draal/control_test.go`:

```diff
--- a/draal/control_test.go
+++ b/draal/control_test.go
@@ -81,9 +81,20 @@ func webRollout(parallel int) wire.RolloutRequest {
 func TestStore(t *testing.T) {
 	cfg := testConfig(t)
 	d := New(cfg)
-	got, err := d.Store(context.Background())
-	if err != nil || got != cfg.Store {
-		t.Fatalf("Store = %+v, %v; want %+v", got, err, cfg.Store)
+	// Every service has a prefix of its own, and one draal has not heard
+	// of yet has one like any other: the first rollout of a service
+	// pushes before draal knows the service.
+	for _, service := range []string{"web", "never-rolled-out"} {
+		got, err := d.Store(context.Background(), service)
+		want := wire.StoreInfo{Server: cfg.Store.Server, Prefix: "/services/" + service + "/nix/store/"}
+		if err != nil || got != want {
+			t.Errorf("Store(%q) = %+v, %v; want %+v", service, got, err, want)
+		}
+	}
+	for _, service := range []string{"", "Web", "a/b", "..", "web/../api"} {
+		if got, err := d.Store(context.Background(), service); wire.CodeOf(err) != wire.CodeBadRequest {
+			t.Errorf("Store(%q) = %+v, %v; want a bad-request", service, got, err)
+		}
 	}
 }
 
```

Change `draal/serve_test.go`:

```diff
--- a/draal/serve_test.go
+++ b/draal/serve_test.go
@@ -533,7 +533,7 @@ func TestHistory(t *testing.T) {
 func requests(zathras wire.ID) map[string]*wire.Request {
 	rollout := webRollout(1)
 	return map[string]*wire.Request{
-		"store":    {Store: &wire.StoreRequest{}},
+		"store":    {Store: &wire.StoreRequest{Service: "web"}},
 		"rollout":  {Rollout: &rollout},
 		"rollback": {Rollback: &wire.RollbackRequest{Service: "web"}},
 		"watch":    {Watch: &wire.WatchRequest{}},
@@ -595,9 +595,23 @@ func TestARefusalAboutAZathrasNamesNoService(t *testing.T) {
 func TestProtocolStore(t *testing.T) {
 	cfg := testConfig(t)
 	w := startWorld(t, cfg)
-	_, resp, err := ask(t, w.dial(operatorID), &wire.Request{Store: &wire.StoreRequest{}})
-	if err != nil || resp.Store == nil || *resp.Store != cfg.Store {
-		t.Fatalf("store = %+v, %v; want %+v", resp, err, cfg.Store)
+	op := w.dial(operatorID)
+	_, resp, err := ask(t, op, &wire.Request{Store: &wire.StoreRequest{Service: "web"}})
+	want := wire.StoreInfo{Server: cfg.Store.Server, Prefix: wire.StorePrefix("web")}
+	if err != nil || resp.Store == nil || *resp.Store != want {
+		t.Fatalf("store of web = %+v, %v; want %+v", resp, err, want)
+	}
+	// The request names its service: one that names none, or what is no
+	// name of a service, is refused.
+	for _, service := range []string{"", "a/b"} {
+		if _, _, err := ask(t, op, &wire.Request{Store: &wire.StoreRequest{Service: service}}); wire.CodeOf(err) != wire.CodeBadRequest {
+			t.Errorf("store of %q = %v, want a bad-request", service, err)
+		}
+	}
+	// It is authorized for the service it names.
+	_, _, err = ask(t, w.dial(strangerID), &wire.Request{Store: &wire.StoreRequest{Service: "web"}})
+	if wire.CodeOf(err) != wire.CodeUnauthorized || !strings.Contains(err.Error(), "on service web") {
+		t.Errorf("store of web as a stranger = %v, want unauthorized, naming the service", err)
 	}
 }
 
```

Change `draal/streams_test.go`:

```diff
--- a/draal/streams_test.go
+++ b/draal/streams_test.go
@@ -16,7 +16,7 @@ import (
 // draal serves conn: a request was asked on it and its stream is closed.
 func goroutines(t *testing.T, conn node.Conn) int {
 	t.Helper()
-	s, _, _ := ask(t, conn, &wire.Request{Store: &wire.StoreRequest{}})
+	s, _, _ := ask(t, conn, &wire.Request{Store: &wire.StoreRequest{Service: "web"}})
 	s.Close()
 	return runtime.NumGoroutine()
 }
@@ -142,7 +142,7 @@ func TestARefusedStreamIsClosedWithinRefuseWait(t *testing.T) {
 	before := goroutines(t, stranger)
 
 	cases := map[string]*wire.Request{
-		"store of a stranger":        {Store: &wire.StoreRequest{}},
+		"store of a stranger":        {Store: &wire.StoreRequest{Service: "web"}},
 		"shell of a stranger":        {Shell: &wire.ShellRequest{Zathras: operatorID}},
 		"a register with a bad name": {Register: &wire.Register{Service: "web", Name: "a b", State: idle()}},
 	}
```

Change `operator/client_test.go`:

```diff
--- a/operator/client_test.go
+++ b/operator/client_test.go
@@ -108,15 +108,15 @@ func refuse(s node.Stream, code wire.Code, message string) {
 }
 
 func TestStore(t *testing.T) {
-	want := wire.StoreInfo{Server: nodetest.ID(9), Prefix: "nix/"}
+	want := wire.StoreInfo{Server: nodetest.ID(9), Prefix: "/services/web/nix/store/"}
 	_, c := connect(t, func(req *wire.Request, s node.Stream) {
-		if req.Store == nil {
-			refuse(s, wire.CodeBadRequest, "not a store request")
+		if req.Store == nil || req.Store.Service != "web" {
+			refuse(s, wire.CodeBadRequest, "not a store request for the service web")
 			return
 		}
 		answer(s, &wire.Response{Store: &want})
 	})
-	got, err := c.Store(t.Context())
+	got, err := c.Store(t.Context(), "web")
 	if err != nil || got != want {
 		t.Fatalf("Store = %+v, %v; want %+v", got, err, want)
 	}
@@ -190,7 +190,7 @@ func TestAnErrorOfDraalIsAWireError(t *testing.T) {
 			t.Errorf("%s = %v, want a *wire.Error with the code %s", what, err, code)
 		}
 	}
-	_, err := c.Store(ctx)
+	_, err := c.Store(ctx, "web")
 	check("Store", err, wire.CodeUnauthorized)
 	_, err = c.Rollout(ctx, wire.RolloutRequest{Service: "web"})
 	check("Rollout", err, wire.CodeUnauthorized)
@@ -215,7 +215,7 @@ func TestAnAnswerOfAnotherKindIsAnError(t *testing.T) {
 	_, c := connect(t, func(req *wire.Request, s node.Stream) {
 		answer(s, &wire.Response{Registered: &wire.Registered{}})
 	})
-	if _, err := c.Store(t.Context()); err == nil {
+	if _, err := c.Store(t.Context(), "web"); err == nil {
 		t.Error("Store took an answer that is no store")
 	}
 	if _, err := c.Watch(t.Context()); err == nil {
@@ -498,7 +498,7 @@ func TestARequestEndsWithItsContext(t *testing.T) {
 	ctx, cancel := context.WithCancel(t.Context())
 	failed := make(chan error, 1)
 	go func() {
-		_, err := c.Store(ctx)
+		_, err := c.Store(ctx, "web")
 		failed <- err
 	}()
 	cancel()
@@ -513,7 +513,7 @@ func TestCloseClosesTheConnection(t *testing.T) {
 		t.Fatalf("Close: %v", err)
 	}
 	wait(t, "the connection to end at draal", wait(t, "the connection", d.conns).Done())
-	if _, err := c.Store(t.Context()); err == nil {
+	if _, err := c.Store(t.Context(), "web"); err == nil {
 		t.Error("Store on a closed client returned no error")
 	}
 	if err := c.Close(); err != nil {
```

Change `operator/close_test.go`:

```diff
--- a/operator/close_test.go
+++ b/operator/close_test.go
@@ -416,7 +416,7 @@ func TestCloseTwiceAndFromSeveralGoroutines(t *testing.T) {
 					}
 					under.Add(1)
 					for {
-						if _, err := c.Store(context.Background()); err != nil {
+						if _, err := c.Store(context.Background(), "web"); err != nil {
 							return
 						}
 					}
@@ -453,7 +453,7 @@ func TestCloseTwiceAndFromSeveralGoroutines(t *testing.T) {
 			if err := c.Close(); err != nil {
 				t.Errorf("one more Close = %v, want nil", err)
 			}
-			if _, err := c.Store(context.Background()); !errors.Is(err, ErrClosed) {
+			if _, err := c.Store(context.Background(), "web"); !errors.Is(err, ErrClosed) {
 				t.Errorf("Store on a closed client = %v, want ErrClosed", err)
 			}
 			if _, err := c.Watch(context.Background()); !errors.Is(err, ErrClosed) {
```

Change `web/fake_test.go`:

```diff
--- a/web/fake_test.go
+++ b/web/fake_test.go
@@ -48,7 +48,7 @@ var _ draal.Controller = (*fake)(nil)
 // unset is what a fake answers when the test gave it nothing to answer.
 var unset = wire.Errorf(wire.CodeUnavailable, "the fake has no answer")
 
-func (f *fake) Store(ctx context.Context) (wire.StoreInfo, error) {
+func (f *fake) Store(ctx context.Context, service string) (wire.StoreInfo, error) {
 	panic("the API asked for the store")
 }
 
```

- [ ] **Step 12: Run it, and see it fail**

Run: `go test ./draal/ ./operator/ ./web/ ./cli/`

Expected: it fails, with

```text
draal/control_test.go:88:45: too many arguments in call to d.Store
```

- [ ] **Step 13: Write the code: the service in the store request: wire, draal, operator, demo, rollout**

Change `cli/rollout.go`:

```diff
--- a/cli/rollout.go
+++ b/cli/rollout.go
@@ -164,7 +164,7 @@ func runRollout(ctx context.Context, cfg rolloutConfig, deps rolloutDeps, stdout
 	// The step lasts until the push says what the server has: asking
 	// draal where the store is, and the store what it holds.
 	p.begin("asking the store")
-	store, err := client.Store(ctx)
+	store, err := client.Store(ctx, cfg.service)
 	if err != nil {
 		return fmt.Errorf("asking draal for the store: %w", err)
 	}
```

Change `demo/fleet.go`:

```diff
--- a/demo/fleet.go
+++ b/demo/fleet.go
@@ -729,7 +729,7 @@ func (d *Demo) connected(id wire.ID) (*zathras, error) {
 
 // Store is not of the demo: neither the page nor the terminal UI asks
 // for a store.
-func (d *Demo) Store(ctx context.Context) (wire.StoreInfo, error) {
+func (d *Demo) Store(ctx context.Context, service string) (wire.StoreInfo, error) {
 	return wire.StoreInfo{}, wire.Errorf(wire.CodeUnavailable, "the demo has no store")
 }
 
```

Change `draal/control.go`:

```diff
--- a/draal/control.go
+++ b/draal/control.go
@@ -14,9 +14,14 @@ import (
 // rolloutsListed is how many rollouts of a service Rollouts returns.
 const rolloutsListed = 20
 
-// Store returns the jaccard-store server and the prefix draal was given.
-func (d *Draal) Store(ctx context.Context) (wire.StoreInfo, error) {
-	return d.cfg.Store, nil
+// Store returns the jaccard-store server draal was given and the prefix
+// of the service. A service draal has not heard of has a prefix like any
+// other: the first rollout of a service pushes before draal knows it.
+func (d *Draal) Store(ctx context.Context, service string) (wire.StoreInfo, error) {
+	if err := wire.ValidService(service); err != nil {
+		return wire.StoreInfo{}, err
+	}
+	return wire.StoreInfo{Server: d.cfg.Store.Server, Prefix: wire.StorePrefix(service)}, nil
 }
 
 // ServiceOf returns the service of a connected zathras.
```

Change `draal/draal.go`:

```diff
--- a/draal/draal.go
+++ b/draal/draal.go
@@ -20,6 +20,8 @@ import (
 type Config struct {
 	DB    *db.DB
 	Authz authz.Authorizer
+	// Store names the jaccard-store server. Its prefix is not looked at:
+	// every service has a prefix of its own, wire.StorePrefix.
 	Store wire.StoreInfo
 	// StoreDir is the directory a rollout's binary has to be in. "":
 	// /nix/store. The tests of the whole point it at a directory they can
@@ -116,9 +118,10 @@ type Shell interface {
 // Controller is what an operator can do. The handler of the protocol and,
 // in stage 2, the HTTP API are written against it.
 type Controller interface {
-	// Store returns the jaccard-store server and the prefix that closures
-	// are pushed to.
-	Store(ctx context.Context) (wire.StoreInfo, error)
+	// Store returns where the closures of a service are pushed to: the
+	// jaccard-store server and the prefix of the service. A name that is
+	// no name of a service is a bad-request.
+	Store(ctx context.Context, service string) (wire.StoreInfo, error)
 	// Rollout records a new generation of a service and the rollout to it.
 	Rollout(ctx context.Context, by wire.ID, req wire.RolloutRequest) (wire.RolloutInfo, error)
 	// Rollback records a rollout to the stable generation of a service.
```

Change `draal/handler.go`:

```diff
--- a/draal/handler.go
+++ b/draal/handler.go
@@ -23,7 +23,7 @@ func (d *Draal) admit(peer wire.ID, req *wire.Request) error {
 	)
 	switch {
 	case req.Store != nil:
-		op = authz.OpStore
+		op, service = authz.OpStore, req.Store.Service
 	case req.Rollout != nil:
 		op, service = authz.OpRollout, req.Rollout.Service
 	case req.Rollback != nil:
@@ -152,7 +152,7 @@ func asked(req *wire.Request) []string {
 func (d *Draal) respond(ctx context.Context, peer wire.ID, req *wire.Request, send func(*wire.Response) bool) error {
 	switch {
 	case req.Store != nil:
-		info, err := d.Store(ctx)
+		info, err := d.Store(ctx, req.Store.Service)
 		if err != nil {
 			return err
 		}
```

Change `operator/client.go`:

```diff
--- a/operator/client.go
+++ b/operator/client.go
@@ -141,9 +141,10 @@ func unexpected(what string) error {
 	return fmt.Errorf("operator: %s: draal answered with something else", what)
 }
 
-// Store asks for the jaccard-store server and the prefix to push to.
-func (c *Client) Store(ctx context.Context) (wire.StoreInfo, error) {
-	resp, err := c.call(ctx, "store", &wire.Request{Store: &wire.StoreRequest{}})
+// Store asks where the closures of a service are to be pushed: for the
+// jaccard-store server and the prefix of the service.
+func (c *Client) Store(ctx context.Context, service string) (wire.StoreInfo, error) {
+	resp, err := c.call(ctx, "store", &wire.Request{Store: &wire.StoreRequest{Service: service}})
 	if err != nil {
 		return wire.StoreInfo{}, err
 	}
```

Change `wire/messages.go`:

```diff
--- a/wire/messages.go
+++ b/wire/messages.go
@@ -179,11 +179,14 @@ type ShellOutput struct {
 
 // An operator and draal.
 
-// StoreRequest asks where closures are to be pushed.
-type StoreRequest struct{}
+// StoreRequest asks where the closures of a service are to be pushed.
+type StoreRequest struct {
+	Service string `json:"service"`
+}
 
-// StoreInfo is the jaccard-store server of the fleet and the prefix of the
-// references a push makes there.
+// StoreInfo is where the closures of a service are pushed to: the
+// jaccard-store server, and the prefix of the references a push makes
+// there, which is the service's own (StorePrefix).
 type StoreInfo struct {
 	Server ID     `json:"server"`
 	Prefix string `json:"prefix"`
```

- [ ] **Step 14: Run it, and see it pass**

Run: `go test ./draal/ ./operator/ ./web/ ./cli/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 15: Commit**

```bash
git add cli/rollout_test.go draal/control_test.go draal/serve_test.go draal/streams_test.go operator/client_test.go operator/close_test.go web/fake_test.go cli/rollout.go demo/fleet.go draal/control.go draal/draal.go draal/handler.go operator/client.go wire/messages.go
git commit -m 'Store: the store request names its service, and is answered with that service'\''s prefix'
```

- [ ] **Step 16: Write the failing test: a rollout pushes 20 store paths at a time unless told otherwise**

Change `cli/rollout_test.go`:

```diff
--- a/cli/rollout_test.go
+++ b/cli/rollout_test.go
@@ -33,13 +33,13 @@ func TestReadRollout(t *testing.T) {
 	}{
 		{"arguments of the service", nil,
 			[]string{"web", "/nix/store/x/bin/web", "--", "--port", "80", "-v"},
-			rolloutConfig{service: "web", path: "/nix/store/x/bin/web", args: []string{"--port", "80", "-v"}, parallel: 1, jobs: 5}},
+			rolloutConfig{service: "web", path: "/nix/store/x/bin/web", args: []string{"--port", "80", "-v"}, parallel: 1, jobs: 20}},
 		{"no arguments of the service", nil,
 			[]string{"web", "./result/bin/web"},
-			rolloutConfig{service: "web", path: "./result/bin/web", parallel: 1, jobs: 5}},
+			rolloutConfig{service: "web", path: "./result/bin/web", parallel: 1, jobs: 20}},
 		{"nothing behind --", nil,
 			[]string{"web", "./result/bin/web", "--"},
-			rolloutConfig{service: "web", path: "./result/bin/web", args: []string{}, parallel: 1, jobs: 5}},
+			rolloutConfig{service: "web", path: "./result/bin/web", args: []string{}, parallel: 1, jobs: 20}},
 		{"flags of rollout, and the same words behind --", nil,
 			[]string{"--parallel", "0", "--detach", "--jobs", "2", "--temp-dir", "/var/tmp", "web", "/nix/store/x/bin/web", "--", "--detach", "--", "--parallel", "9"},
 			rolloutConfig{service: "web", path: "/nix/store/x/bin/web", args: []string{"--detach", "--", "--parallel", "9"},
```

Change `push/jobs_test.go`:

```diff
--- a/push/jobs_test.go
+++ b/push/jobs_test.go
@@ -53,19 +53,21 @@ func until(t *testing.T, what string, cond func() bool) {
 }
 
 func TestPushJobs(t *testing.T) {
+	// paths is how many store paths are pushed: more than may be pushed
+	// at a time, so that the bound is what holds the rest back.
 	tests := []struct {
-		jobs, want int
+		jobs, want, paths int
 	}{
-		{1, 1},
-		{2, 2},
-		{0, 5}, // the default
+		{1, 1, 8},
+		{2, 2, 8},
+		{0, 20, 24}, // the default
 	}
 	for _, tt := range tests {
 		t.Run(fmt.Sprintf("jobs %d", tt.jobs), func(t *testing.T) {
 			srv := startServer(t)
 			cfg := srv.config()
 			cfg.Jobs = tt.jobs
-			paths := storePaths(t, 8)
+			paths := storePaths(t, tt.paths)
 			hold := make(chan struct{})
 			count := watch(t, hold)
 
@@ -89,8 +91,8 @@ func TestPushJobs(t *testing.T) {
 			if out.err != nil {
 				t.Fatalf("Push: %v", out.err)
 			}
-			if out.res != (Result{Pushed: 8, Present: 0}) {
-				t.Errorf("Push = %+v, want eight pushed", out.res)
+			if out.res != (Result{Pushed: tt.paths, Present: 0}) {
+				t.Errorf("Push = %+v, want %d pushed", out.res, tt.paths)
 			}
 			if _, most := count(); most != tt.want {
 				t.Errorf("%d store paths were pushed at a time, want %d", most, tt.want)
```

- [ ] **Step 17: Run it, and see it fail**

Run: `go test ./push/ -run TestPushJobs; go test ./cli/ -run TestReadRollout`

Expected: it fails, with

```text
jobs_test.go:85: timed out waiting until 20 store paths are being pushed
rollout_test.go:59: got {... jobs:5 ...}, want {... jobs:20 ...}
```

- [ ] **Step 18: Write the code: 20 jobs of a push by default**

Change `cli/rollout.go`:

```diff
--- a/cli/rollout.go
+++ b/cli/rollout.go
@@ -30,7 +30,7 @@ func rolloutCommand() *cli.Command {
 			"with 1 when it halted or was superseded. An interrupt ends the following\n" +
 			"and not the rollout.",
 		Flags: operatorFlags(parallelFlag(), detachFlag(),
-			&cli.IntFlag{Name: "jobs", EnvVars: []string{"GREAT_MACHINE_JOBS"}, Value: 5,
+			&cli.IntFlag{Name: "jobs", EnvVars: []string{"GREAT_MACHINE_JOBS"}, Value: 20,
 				Usage: "store paths pushed at a time"},
 			&cli.StringFlag{Name: "temp-dir", EnvVars: []string{"GREAT_MACHINE_TEMP_DIR"},
 				Usage: "`DIR` in which the temporary stores of a push are made (default: the system's)"},
```

Change `push/push.go`:

```diff
--- a/push/push.go
+++ b/push/push.go
@@ -34,7 +34,7 @@ import (
 const (
 	// defaultJobs is how many store paths are pushed at a time when Config
 	// does not say.
-	defaultJobs = 5
+	defaultJobs = 20
 	// lookups is how many references the server is asked about at a time.
 	lookups = 16
 	// minDedup is the share of a store path's bytes a pack on the server
@@ -49,7 +49,7 @@ type Config struct {
 	Server   wire.ID          // the jaccard-store server
 	Addrs    []netip.AddrPort // for tests: where the server is
 	Prefix   string
-	Jobs     int      // 0: 5
+	Jobs     int      // 0: 20
 	TempDir  string   // "": the system's
 	Progress Progress // nil: none
 }
```

- [ ] **Step 19: Run it, and see it pass**

Run: `go test ./push/ -run TestPushJobs; go test ./cli/ -run TestReadRollout`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 20: Commit**

```bash
git add cli/rollout_test.go push/jobs_test.go cli/rollout.go push/push.go
git commit -m 'Store: a rollout pushes 20 store paths at a time unless told otherwise'
```

- [ ] **Step 21: The whole**

```bash
nix develop .#bare --command bash -c 'test -z "$(gofmt -l .)" && go vet ./... && CGO_ENABLED=0 GOOS=linux go vet ./... && go test ./...'
git status --short
```

Expected: nothing from `gofmt` and `go vet`; `ok` for the 30 packages that have tests; and nothing from `git status`: every step of this task was committed, and nothing was built into the tree.
