package push

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// recorder is a Progress that keeps what it is told.
type recorder struct {
	mu      sync.Mutex
	found   [][2]int // present, missing
	pushed  []string
	bytesOf map[string]uint64
}

func (r *recorder) Found(present, missing int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.found = append(r.found, [2]int{present, missing})
}

func (r *recorder) Pushed(storePath string, bytes uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pushed = append(r.pushed, storePath)
	if r.bytesOf == nil {
		r.bytesOf = make(map[string]uint64)
	}
	r.bytesOf[storePath] = bytes
}

// check fails the test unless Found was called once, with present and
// missing, and Pushed once for each of the paths.
func (r *recorder) check(t *testing.T, present, missing int, paths ...string) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.found) != 1 || r.found[0] != [2]int{present, missing} {
		t.Errorf("Found was called with %v, want once with [%d %d]", r.found, present, missing)
	}
	got := slices.Sorted(slices.Values(r.pushed))
	want := slices.Sorted(slices.Values(paths))
	if !slices.Equal(got, want) {
		t.Errorf("Pushed was called for %q, want %q", got, want)
	}
}

// write writes a file with the mode, whatever the umask is.
func write(t *testing.T, path, content string, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// storePathDir makes a store path that is a directory, named name, in a
// directory that stands for the store: an executable file, a file nobody
// may write, an empty file, a symbolic link, a nested directory, and an
// ignore file that names one of the files.
func storePathDir(t *testing.T, store, name string) string {
	t.Helper()
	p := filepath.Join(store, name)
	write(t, filepath.Join(p, "bin", "web"), "#!/bin/sh\necho "+name+"\n", 0o555)
	write(t, filepath.Join(p, "share", "doc", "README"), "read me: "+name+"\n", 0o444)
	write(t, filepath.Join(p, "share", "empty"), "", 0o444)
	write(t, filepath.Join(p, "share", "ignored.log"), "pushed all the same\n", 0o444)
	write(t, filepath.Join(p, ".amberignore"), "*.log\n", 0o444)
	if err := os.Symlink("../share/doc/README", filepath.Join(p, "bin", "readme")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nix/store/eeee-elsewhere/lib", filepath.Join(p, "lib")); err != nil {
		t.Fatal(err)
	}
	return p
}

// storePaths makes n store paths that are directories.
func storePaths(t *testing.T, n int) []string {
	t.Helper()
	store := t.TempDir()
	paths := make([]string, n)
	for i := range paths {
		paths[i] = storePathDir(t, store, fmt.Sprintf("%04d-pkg-%d", i, i))
	}
	return paths
}

// sameTree fails the test unless the trees at want and got hold the same
// names, with the same types, permission bits, contents and link targets.
// The modes of the two roots are not compared: a reference records none.
func sameTree(t *testing.T, want, got string) {
	t.Helper()
	list := func(root string) map[string]string {
		entries := make(map[string]string)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil || rel == "." {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			switch {
			case info.Mode()&fs.ModeSymlink != 0:
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				entries[rel] = "link to " + target
			case info.IsDir():
				entries[rel] = fmt.Sprintf("directory %o", info.Mode().Perm())
			default:
				content, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				entries[rel] = fmt.Sprintf("file %o %q", info.Mode().Perm(), content)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return entries
	}
	w, g := list(want), list(got)
	for name, entry := range w {
		if g[name] != entry {
			t.Errorf("%s: pulled as %q, was pushed as %q", name, g[name], entry)
		}
	}
	for name, entry := range g {
		if _, ok := w[name]; !ok {
			t.Errorf("%s: pulled as %q, was not pushed", name, entry)
		}
	}
}

// push calls Push with a deadline.
func push(t *testing.T, cfg Config, paths []string) (Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	return Push(ctx, cfg, paths)
}

func TestPushDirectory(t *testing.T) {
	srv := startServer(t)
	cfg := srv.config()
	rec := &recorder{}
	cfg.Progress = rec
	path := storePathDir(t, t.TempDir(), "aaaa-web-1.0")

	res, err := push(t, cfg, []string{path})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if res != (Result{Pushed: 1, Present: 0}) {
		t.Errorf("Push = %+v, want one pushed and none present", res)
	}
	rec.check(t, 0, 1, path)
	if rec.bytesOf[path] == 0 {
		t.Error("Pushed was told of a pack of 0 bytes")
	}
	if got, want := srv.refs(), []string{"store/aaaa-web-1.0"}; !slices.Equal(got, want) {
		t.Errorf("the server has the references %q, want %q", got, want)
	}
	// Modes, links and the file an ignore file names: all as they were.
	sameTree(t, path, srv.pullDir("store/aaaa-web-1.0"))
	empty(t, cfg.TempDir)
}

func TestPushSingleFile(t *testing.T) {
	srv := startServer(t)
	cfg := srv.config()
	store := t.TempDir()
	script := filepath.Join(store, "bbbb-builder.sh")
	write(t, script, "#!/bin/sh\nexit 0\n", 0o555)
	// Larger than one chunk, so that its root is no single blob.
	large := filepath.Join(store, "cccc-source.tar")
	write(t, large, strings.Repeat("0123456789abcdef", 1<<16)+"the end", 0o444)
	none := filepath.Join(store, "dddd-empty")
	write(t, none, "", 0o444)

	res, err := push(t, cfg, []string{script, large, none})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if res != (Result{Pushed: 3, Present: 0}) {
		t.Errorf("Push = %+v, want three pushed and none present", res)
	}
	for _, path := range []string{script, large, none} {
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// The root of the reference is the file's content.
		got := srv.pullFile("store/" + filepath.Base(path))
		if !bytes.Equal(got, want) {
			t.Errorf("%s: pulled %d bytes, pushed %d, and they differ", path, len(got), len(want))
		}
	}
	empty(t, cfg.TempDir)
}

// A store path that is a symbolic link cannot be a reference: Push
// refuses it, names it, and pushes nothing of what it was given.
func TestPushSymbolicLink(t *testing.T) {
	srv := startServer(t)
	cfg := srv.config()
	rec := &recorder{}
	cfg.Progress = rec
	store := t.TempDir()
	dir := storePathDir(t, store, "aaaa-web-1.0")
	link := filepath.Join(store, "bbbb-web-current")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}

	res, err := push(t, cfg, []string{dir, link})
	if err == nil {
		t.Fatalf("Push = %+v, want an error for the symbolic link", res)
	}
	for _, want := range []string{link, "symbolic link"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not say %q: %v", want, err)
		}
	}
	if res.Pushed != 0 {
		t.Errorf("Push = %+v, want nothing pushed", res)
	}
	if got := srv.refs(); len(got) != 0 {
		t.Errorf("the server has the references %q, want none", got)
	}
	if len(rec.pushed) != 0 {
		t.Errorf("Pushed was called for %q", rec.pushed)
	}
	empty(t, cfg.TempDir)
}

// A symbolic link is refused also when it is given with a slash behind
// it, which would have it followed.
func TestPushSymbolicLinkWithASlash(t *testing.T) {
	srv := startServer(t)
	cfg := srv.config()
	store := t.TempDir()
	dir := storePathDir(t, store, "aaaa-web-1.0")
	link := filepath.Join(store, "bbbb-web-current")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}

	res, err := push(t, cfg, []string{link + "/"})
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Push = %+v, %v; want an error for the symbolic link", res, err)
	}
	if got := srv.refs(); len(got) != 0 {
		t.Errorf("the server has the references %q, want none", got)
	}
	empty(t, cfg.TempDir)
}

func TestPushAgain(t *testing.T) {
	srv := startServer(t)
	paths := storePaths(t, 3)
	if _, err := push(t, srv.config(), paths); err != nil {
		t.Fatalf("the first Push: %v", err)
	}

	cfg := srv.config()
	rec := &recorder{}
	cfg.Progress = rec
	res, err := push(t, cfg, paths)
	if err != nil {
		t.Fatalf("the second Push: %v", err)
	}
	if res != (Result{Pushed: 0, Present: 3}) {
		t.Errorf("the second Push = %+v, want none pushed and three present", res)
	}
	rec.check(t, 3, 0)
	empty(t, cfg.TempDir)
}

func TestPushPresentAndMissing(t *testing.T) {
	srv := startServer(t)
	paths := storePaths(t, 5)
	if _, err := push(t, srv.config(), paths[:2]); err != nil {
		t.Fatalf("the first Push: %v", err)
	}

	cfg := srv.config()
	rec := &recorder{}
	cfg.Progress = rec
	res, err := push(t, cfg, paths)
	if err != nil {
		t.Fatalf("the second Push: %v", err)
	}
	if res != (Result{Pushed: 3, Present: 2}) {
		t.Errorf("the second Push = %+v, want three pushed and two present", res)
	}
	rec.check(t, 2, 3, paths[2:]...)
	if got := srv.refs(); len(got) != 5 {
		t.Errorf("the server has the references %q, want five", got)
	}
	sameTree(t, paths[4], srv.pullDir("store/"+filepath.Base(paths[4])))
	empty(t, cfg.TempDir)
}

// A name that begins with the name of another is not taken for it, and a
// path given twice is pushed once.
func TestPushNamesThatBeginAlike(t *testing.T) {
	srv := startServer(t)
	store := t.TempDir()
	long := storePathDir(t, store, "aaaa-web-1.0")
	short := storePathDir(t, store, "aaaa-web")
	if _, err := push(t, srv.config(), []string{long}); err != nil {
		t.Fatalf("the first Push: %v", err)
	}

	cfg := srv.config()
	res, err := push(t, cfg, []string{short, long, short})
	if err != nil {
		t.Fatalf("the second Push: %v", err)
	}
	if res != (Result{Pushed: 1, Present: 1}) {
		t.Errorf("the second Push = %+v, want one pushed and one present", res)
	}
	if got, want := srv.refs(), []string{"store/aaaa-web", "store/aaaa-web-1.0"}; !slices.Equal(got, want) {
		t.Errorf("the server has the references %q, want %q", got, want)
	}
}

func TestPushPathThatDoesNotExist(t *testing.T) {
	srv := startServer(t)
	cfg := srv.config()
	cfg.Jobs = 1 // one after the other, in the order given
	rec := &recorder{}
	cfg.Progress = rec
	store := t.TempDir()
	first := storePathDir(t, store, "aaaa-first")
	nothing := filepath.Join(store, "bbbb-nothing")
	last := storePathDir(t, store, "cccc-last")

	res, err := push(t, cfg, []string{first, nothing, last})
	if err == nil {
		t.Fatalf("Push = %+v, want an error for the path that does not exist", res)
	}
	if !strings.Contains(err.Error(), nothing) {
		t.Errorf("the error does not name %s: %v", nothing, err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the error does not say that the path does not exist: %v", err)
	}
	if res != (Result{Pushed: 1, Present: 0}) {
		t.Errorf("Push = %+v, want the one before pushed", res)
	}
	rec.check(t, 0, 3, first)
	// What was not begun when the push failed was not pushed.
	if got, want := srv.refs(), []string{"store/aaaa-first"}; !slices.Equal(got, want) {
		t.Errorf("the server has the references %q, want %q", got, want)
	}
	empty(t, cfg.TempDir)
}

func TestPushRefusesBeforeDialing(t *testing.T) {
	// No server: what is refused here is refused before one is dialed.
	tests := []struct {
		name string
		path string
		want string
	}{
		{"a name no reference can have", "/nix/store/aaaa-web@1.0", "/nix/store/aaaa-web@1.0"},
		{"an empty path", "", "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := push(t, Config{Prefix: "store/"}, []string{"/nix/store/bbbb-fine", tt.path})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Push: %v; want an error that says %q", err, tt.want)
			}
		})
	}
}

func TestPushNothing(t *testing.T) {
	rec := &recorder{}
	res, err := push(t, Config{Prefix: "store/", Progress: rec}, nil)
	if err != nil || res != (Result{}) {
		t.Errorf("Push of nothing = %+v, %v", res, err)
	}
	rec.check(t, 0, 0)
}

func TestPushContextEndedBefore(t *testing.T) {
	srv := startServer(t)
	cfg := srv.config()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Push(ctx, cfg, storePaths(t, 2))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Push with a cancelled context: %v; want context.Canceled", err)
	}
	empty(t, cfg.TempDir)
}
