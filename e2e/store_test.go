package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/amber-store/core/ingest"
	"github.com/amber-store/core/key"
	"github.com/amber-store/core/packstore"
	"github.com/amber-store/jaccard-store/client"
	jsnode "github.com/amber-store/jaccard-store/node"
	jswire "github.com/amber-store/jaccard-store/wire"
	irohkey "github.com/tmc/go-iroh/key"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/push"
	"github.com/fables-for-robots/great-machine/wire"
)

// pushed is what a push said it did.
type pushed struct {
	mu               sync.Mutex
	present, missing int
	bytes            map[string]uint64 // of the pack that was uploaded, by the name of the store path
}

func (p *pushed) Found(present, missing int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.present, p.missing = present, missing
}

func (p *pushed) Pushed(storePath string, bytes uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.bytes == nil {
		p.bytes = make(map[string]uint64)
	}
	p.bytes[filepath.Base(storePath)] = bytes
}

// newKey makes a key that draal has never seen.
func newKey(t *testing.T) node.Key {
	t.Helper()
	k, err := node.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	return k
}

// storePathOf makes a directory as a store path is one, under dir, with
// one file of the content given in it, and returns its path.
func storePathOf(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Join(path, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "bin", "program"), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// rootOf returns the root of the tree of a store path: what a reference
// to it points at, wherever it was pushed from.
func rootOf(t *testing.T, path string) key.Key {
	t.Helper()
	objects, err := packstore.Open(filepath.Join(t.TempDir(), "packstore"), packstore.WithSync(false))
	if err != nil {
		t.Fatal(err)
	}
	defer objects.Close()
	root, _, err := ingest.Dir(objects, path, ingest.Opts{NoIgnore: true})
	if err != nil {
		t.Fatalf("importing %s: %v", path, err)
	}
	return root
}

// pushAs pushes store paths to draal, where it listens now, with the key
// given, under the prefix given.
func (h *harness) pushAs(ctx context.Context, k node.Key, prefix string, paths ...string) (*pushed, push.Result, error) {
	h.t.Helper()
	h.mu.Lock()
	addr := h.addr
	h.mu.Unlock()
	said := &pushed{}
	res, err := push.Push(ctx, push.Config{
		Key:      k,
		Server:   h.draalKey.ID(),
		Addrs:    []netip.AddrPort{addr},
		Prefix:   prefix,
		TempDir:  h.t.TempDir(),
		Progress: said,
	}, paths)
	return said, res, err
}

// pullAs fetches a reference from draal, where it listens now, with the
// key given, as the mount of a zathras does, and returns the root it has.
func (h *harness) pullAs(ctx context.Context, k node.Key, name string) (key.Key, error) {
	h.t.Helper()
	server, err := irohkey.ParseEndpointID(string(h.draalKey.ID()))
	if err != nil {
		h.t.Fatal(err)
	}
	h.mu.Lock()
	addr := h.addr
	h.mu.Unlock()
	conn, err := jsnode.Dial(ctx, jsnode.DialConfig{Key: k.Iroh(), ALPN: jswire.ALPN, Server: server, Addrs: []netip.AddrPort{addr}, Log: quiet})
	if err != nil {
		h.t.Fatalf("dialing draal for the store: %v", err)
	}
	defer conn.Close()
	objects, err := packstore.Open(filepath.Join(h.t.TempDir(), "packstore"), packstore.WithSync(false))
	if err != nil {
		h.t.Fatal(err)
	}
	defer objects.Close()
	res, err := client.New(conn, nil).Pull(ctx, objects, name, client.PullOptions{})
	return res.Root, err
}

// storeFigures are what the page of the store says the store holds.
type storeFigures struct {
	Refs       int64 `json:"refs"`
	BasePacks  int64 `json:"base_packs"`
	PatchPacks int64 `json:"patch_packs"`
}

func (h *harness) storeFigures() storeFigures {
	h.t.Helper()
	h.mu.Lock()
	page := h.store.Admin()
	h.mu.Unlock()
	rec := httptest.NewRecorder()
	page.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stats", nil))
	var f storeFigures
	if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil || rec.Code != http.StatusOK {
		h.t.Fatalf("the figures of the store: %d %s (%v)", rec.Code, rec.Body, err)
	}
	return f
}

// The closures of two services are pushed to draal itself, under its own
// endpoint ID, each under the prefix of its service; whoever knows the ID
// pulls them again; and only an operator pushes. The bucket of the store
// sets the environment of the test, so this test is not parallel.
func TestTheStoreThroughDraal(t *testing.T) {
	h := start(t, options{store: true})
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	dir := t.TempDir()
	glibc := storePathOf(t, dir, "aaa-glibc", "what both services are linked against")
	web := storePathOf(t, dir, "bbb-web", "the program of web")
	api := storePathOf(t, dir, "ccc-api", "the program of api")

	// draal says where the closures of a service go: to itself, under
	// the prefix of the service.
	prefix := make(map[string]string)
	for _, service := range []string{"web", "api"} {
		info, err := h.op.Store(ctx, service)
		want := wire.StoreInfo{Server: h.draalKey.ID(), Prefix: "/services/" + service + "/nix/store/"}
		if err != nil || info != want {
			t.Fatalf("Store(%q) = %+v, %v; want %+v", service, info, err, want)
		}
		prefix[service] = info.Prefix
	}

	said, res, err := h.pushAs(ctx, h.opKey, prefix["web"], glibc, web)
	if err != nil || res != (push.Result{Pushed: 2}) {
		t.Fatalf("the push of web's closure = %+v, %v; want two store paths pushed", res, err)
	}
	if said.bytes["aaa-glibc"] == 0 || said.bytes["bbb-web"] == 0 {
		t.Errorf("the push of web's closure uploaded %v, want a pack for each store path", said.bytes)
	}

	// The store path the two services have in common becomes a reference
	// of api's own, and is uploaded no second time: the store has a pack
	// of that content.
	said, res, err = h.pushAs(ctx, h.opKey, prefix["api"], glibc, api)
	if err != nil || res != (push.Result{Pushed: 2}) {
		t.Fatalf("the push of api's closure = %+v, %v; want two store paths pushed", res, err)
	}
	if said.present != 0 || said.missing != 2 {
		t.Errorf("the store had %d of api's store paths and lacked %d, want none and two: a reference of web is none of api", said.present, said.missing)
	}
	if said.bytes["aaa-glibc"] != 0 {
		t.Errorf("the store path both services have was uploaded again, %d bytes of it", said.bytes["aaa-glibc"])
	}
	if said.bytes["ccc-api"] == 0 {
		t.Errorf("the program of api was not uploaded: %v", said.bytes)
	}
	if f := h.storeFigures(); f.Refs != 4 || f.BasePacks+f.PatchPacks != 3 {
		t.Errorf("the store holds %d references in %d packs, want 4 in 3", f.Refs, f.BasePacks+f.PatchPacks)
	}
	// A closure that is there is asked about and not pushed.
	if _, res, err := h.pushAs(ctx, h.opKey, prefix["api"], glibc, api); err != nil || res != (push.Result{Present: 2}) {
		t.Errorf("the second push of api's closure = %+v, %v; want both store paths there already", res, err)
	}

	// Who may pull does, and what comes back is what was pushed. A key
	// that was granted nothing pulls nothing: knowing draal's ID is not
	// enough.
	stranger := newKey(t)
	if _, err := h.pullAs(ctx, stranger, prefix["web"]+filepath.Base(web)); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("the pull of a stranger: %v, want it refused as unauthorized", err)
	}
	pulls := func() {
		t.Helper()
		for service, paths := range map[string][]string{"web": {glibc, web}, "api": {glibc, api}} {
			for _, path := range paths {
				name := prefix[service] + filepath.Base(path)
				got, err := h.pullAs(ctx, h.opKey, name)
				if err != nil {
					t.Errorf("pulling %s as an admin: %v", name, err)
				} else if want := rootOf(t, path); got != want {
					t.Errorf("%s has the root %s, and what was pushed has %s", name, got, want)
				}
			}
		}
		// A store path is a reference of the services it was pushed for
		// and of no other: under the prefix of api there is no program
		// of web.
		if _, err := h.pullAs(ctx, h.opKey, prefix["api"]+"bbb-web"); !errors.Is(err, client.ErrNotFound) {
			t.Errorf("pulling the program of web under the prefix of api: %v, want no such reference", err)
		}
	}
	pulls()

	// Only an operator pushes, and one who is none is told so.
	if _, res, err := h.pushAs(ctx, stranger, prefix["web"], storePathOf(t, t.TempDir(), "ddd-evil", "not the program of web")); err == nil ||
		!strings.Contains(err.Error(), "unauthorized") || res != (push.Result{}) {
		t.Errorf("the push of a stranger = %+v, %v; want it refused as unauthorized", res, err)
	}
	if f := h.storeFigures(); f.Refs != 4 {
		t.Errorf("the store holds %d references after the push of a stranger, want 4 as before", f.Refs)
	}

	// The store request names a service, and is an operator's.
	if _, err := h.op.Store(ctx, "Web"); wire.CodeOf(err) != wire.CodeBadRequest {
		t.Errorf("Store of what is no name of a service: %v, want a bad-request", err)
	}
	if _, err := h.operator(stranger).Store(ctx, "web"); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("Store as a stranger: %v, want unauthorized", err)
	}

	// A draal that is started again is the store it was: its database
	// and the bucket hold everything.
	h.restartDraal()
	pulls()
	if f := h.storeFigures(); f.Refs != 4 {
		t.Errorf("the store holds %d references after draal was started again, want 4", f.Refs)
	}
}
