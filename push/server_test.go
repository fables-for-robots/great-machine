package push

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amber-store/core/fstree"
	"github.com/amber-store/core/key"
	"github.com/amber-store/core/packstore"
	"github.com/amber-store/core/tarexport"
	"github.com/amber-store/core/tarextract"
	"github.com/amber-store/jaccard-store/bucket/buckettest"
	"github.com/amber-store/jaccard-store/client"
	jsdb "github.com/amber-store/jaccard-store/db"
	jsnode "github.com/amber-store/jaccard-store/node"
	jsserver "github.com/amber-store/jaccard-store/server"
	jswire "github.com/amber-store/jaccard-store/wire"
	irohkey "github.com/tmc/go-iroh/key"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// testTimeout is how long one step of a test may take before the test
// gives up. Nothing waits for it when all is well.
const testTimeout = 30 * time.Second

// quiet drops what the server and the endpoints log.
var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// testServer is a jaccard-store server in the process of the test: its
// database in a temporary directory, its bucket a fake S3 in memory, its
// endpoint on the loopback interface.
type testServer struct {
	t     *testing.T
	id    wire.ID
	addrs []netip.AddrPort
}

// startServer starts a server that is stopped and removed when the test
// ends. The bucket sets the environment of the test with t.Setenv, so a
// test that starts a server cannot be parallel.
func startServer(t *testing.T) *testServer {
	t.Helper()
	b := buckettest.New(t)
	dir := t.TempDir()

	database, err := jsdb.Open(filepath.Join(dir, "store.sqlite"))
	if err != nil {
		t.Fatalf("opening the server's database: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	srv, err := jsserver.New(jsserver.Config{DB: database, Bucket: b, Scratch: filepath.Join(dir, "scratch"), Log: quiet})
	if err != nil {
		t.Fatalf("the server: %v", err)
	}
	sk, err := jsnode.LoadOrCreateKey(filepath.Join(dir, "server.key"))
	if err != nil {
		t.Fatalf("the server's key: %v", err)
	}
	ctx, stop := context.WithCancel(context.Background())
	ep, err := jsnode.Bind(ctx, jsnode.ServerConfig{Key: sk, ALPN: jswire.ALPN, Local: true, Log: quiet})
	if err != nil {
		stop()
		t.Fatalf("binding the server's endpoint: %v", err)
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		srv.Serve(ctx, ep)
	}()
	// Cleanups run last in, first out: the serving ends before the
	// database under it is closed.
	t.Cleanup(func() {
		stop()
		<-served
		ep.Shutdown(context.Background())
	})
	return &testServer{t: t, id: wire.ID(ep.ID().String()), addrs: []netip.AddrPort{ep.LocalAddr()}}
}

// config returns a Config for the server with a key of its own, the
// prefix "store/" and a temporary directory that is empty.
func (s *testServer) config() Config {
	s.t.Helper()
	k, err := node.NewKey()
	if err != nil {
		s.t.Fatal(err)
	}
	return Config{Key: k, Server: s.id, Addrs: s.addrs, Prefix: "store/", TempDir: s.t.TempDir()}
}

// client dials the server with a key of its own. The connection ends with
// the test.
func (s *testServer) client() *client.Client {
	s.t.Helper()
	k, err := node.NewKey()
	if err != nil {
		s.t.Fatal(err)
	}
	server, err := irohkey.ParseEndpointID(string(s.id))
	if err != nil {
		s.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	conn, err := jsnode.Dial(ctx, jsnode.DialConfig{Key: k.Iroh(), ALPN: jswire.ALPN, Server: server, Addrs: s.addrs, Log: quiet})
	if err != nil {
		s.t.Fatalf("dialing the server: %v", err)
	}
	s.t.Cleanup(func() { conn.Close() })
	return client.New(conn, nil)
}

// refs returns the names of the references the server has.
func (s *testServer) refs() []string {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	listed, err := s.client().List(ctx, "")
	if err != nil {
		s.t.Fatalf("listing the server's references: %v", err)
	}
	names := make([]string, len(listed))
	for i, r := range listed {
		names[i] = r.Name
	}
	return names
}

// pull fetches the reference name into a store of its own and returns the
// store and the root of the reference.
func (s *testServer) pull(name string) (*packstore.Store, key.Key) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	objects, err := packstore.Open(filepath.Join(s.t.TempDir(), "packstore"), packstore.WithSync(false))
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { objects.Close() })
	res, err := s.client().Pull(ctx, objects, name, client.PullOptions{})
	if err != nil {
		s.t.Fatalf("pulling %q: %v", name, err)
	}
	return objects, res.Root
}

// pullDir fetches the reference name, whose root has to be a directory,
// and extracts it into a new directory, as `jaccard-store pull-dir` does.
func (s *testServer) pullDir(name string) string {
	s.t.Helper()
	objects, root := s.pull(name)
	if t := root.Type(); t != key.DirLeaf && t != key.DirNode {
		s.t.Fatalf("the root of %q is a %v, want a directory", name, t)
	}
	dest := filepath.Join(s.t.TempDir(), "pulled")
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(tarexport.Write(pw, root, objects.Get)) }()
	err := tarextract.Extract(pr, dest)
	pr.CloseWithError(err)
	if err != nil {
		s.t.Fatalf("extracting %q: %v", name, err)
	}
	return dest
}

// pullFile fetches the reference name, whose root has to be the content
// of a file, and returns that content.
func (s *testServer) pullFile(name string) []byte {
	s.t.Helper()
	objects, root := s.pull(name)
	if t := root.Type(); t != key.Blob && t != key.FileNode {
		s.t.Fatalf("the root of %q is a %v, want the content of a file", name, t)
	}
	var content bytes.Buffer
	if err := fstree.WriteContent(&content, root, objects.Get); err != nil {
		s.t.Fatalf("reading %q: %v", name, err)
	}
	return content.Bytes()
}

// empty fails the test when the directory holds anything.
func empty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("%s was left behind in the temporary directory", e.Name())
	}
}
