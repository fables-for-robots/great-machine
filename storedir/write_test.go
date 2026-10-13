package storedir

import (
	"bytes"
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/amber-store/core/ingest"
	"github.com/amber-store/core/key"
	"github.com/amber-store/core/packstore"
	"golang.org/x/sys/unix"
)

// imported imports the file or the directory at path into a store of its
// own, as a push imports a store path, and returns what reads the store
// and the root of what was imported.
func imported(t *testing.T, path string) (func(key.Key) ([]byte, error), key.Key) {
	t.Helper()
	objects, err := packstore.Open(filepath.Join(t.TempDir(), "packstore"), packstore.WithSync(false))
	if err != nil {
		t.Fatalf("opening a store: %v", err)
	}
	t.Cleanup(func() { objects.Close() })
	built, root, err := ingest.Objects(path, ingest.Opts{NoIgnore: true})
	if err != nil {
		t.Fatalf("importing %s: %v", path, err)
	}
	_, err = objects.WriteParallel(func(yield func(packstore.Object, error) bool) {
		for o, err := range built {
			if !yield(packstore.Object{Key: o.Key, Data: o.Bytes}, err) || err != nil {
				return
			}
		}
	}, packstore.WriteOpts{})
	if err != nil {
		t.Fatalf("importing %s: %v", path, err)
	}
	return objects.Get, *root
}

// dest returns a path that is not there, in a directory of the test, and
// removes what the test writes at it: a tree that was written out has
// directories nobody may write to, which the test's own cleanup cannot
// remove.
func dest(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "out")
	t.Cleanup(func() {
		if err := removeTree(path); err != nil {
			t.Errorf("removing %s: %v", path, err)
		}
	})
	return path
}

// stamp gives path, which may be a symbolic link, the modification time
// at, and does not follow it.
func stamp(t *testing.T, path string, at time.Time) {
	t.Helper()
	ts := unix.NsecToTimespec(at.UnixNano())
	if err := unix.UtimesNanoAt(unix.AT_FDCWD, path, []unix.Timespec{ts, ts}, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		t.Fatalf("setting the time of %s: %v", path, err)
	}
}

// write makes the file at path with the content and the mode given, and
// the directories above it.
func write(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	// The mode asked for is narrowed by the umask, and has no setuid bit.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// nothingAt fails the test when something is at path.
func nothingAt(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("something is at %s (%v): what was written of a store path that failed was to be removed", path, err)
	}
}

func TestWriteOutATree(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	big := make([]byte, 3<<20)
	r := rand.New(rand.NewPCG(1, 2))
	for i := range big {
		big[i] = byte(r.Uint32())
	}
	files := map[string]struct {
		content []byte
		mode    os.FileMode
		want    os.FileMode
	}{
		"bin/tool":         {[]byte("#!/bin/sh\necho tool\n"), 0o755, 0o555},
		"share/doc/readme": {[]byte("hello\n"), 0o644, 0o444},
		"share/empty":      {nil, 0o644, 0o444},
		"share/big":        {big, 0o444, 0o444},
	}
	for name, f := range files {
		write(t, filepath.Join(src, name), f.content, f.mode)
	}
	links := map[string]string{
		"lib/tool":     "../bin/tool",
		"lib/dangling": "nowhere",
		"lib/outside":  "/etc/hosts",
	}
	if err := os.MkdirAll(filepath.Join(src, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(src, name)); err != nil {
			t.Fatal(err)
		}
	}
	// The times a reference records: one second after the epoch, as a Nix
	// store has it, and for one file and one link another, which is to
	// come back as it is.
	epoch := time.Unix(1, 0)
	other := time.Unix(1_700_000_000, 123_000_000)
	times := map[string]time.Time{}
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == src {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		at := epoch
		if rel == "share/doc/readme" || rel == "lib/dangling" {
			at = other
		}
		times[rel] = at
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Deepest first: a time set on a directory is undone by what is made
	// in it afterwards, and nothing is here.
	for rel, at := range times {
		stamp(t, filepath.Join(src, rel), at)
	}

	get, root := imported(t, src)
	out := dest(t)
	if err := writeOut(context.Background(), get, root, out); err != nil {
		t.Fatalf("writing the tree out: %v", err)
	}

	info, err := os.Lstat(out)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o555 {
		t.Errorf("the root is %v, want a directory of mode 0555", info.Mode())
	}
	if !info.ModTime().Equal(epoch) {
		t.Errorf("the root has the time %v, want %v: no reference records one for it", info.ModTime(), epoch)
	}
	seen := map[string]bool{}
	err = filepath.WalkDir(out, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == out {
			return err
		}
		rel, _ := filepath.Rel(out, path)
		seen[rel] = true
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if want, ok := times[rel]; !ok {
			t.Errorf("%s was written out, and was not in the tree", rel)
		} else if !info.ModTime().Equal(want) {
			t.Errorf("%s has the time %v, want %v", rel, info.ModTime(), want)
		}
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Geteuid() || int(st.Gid) != os.Getegid() {
			t.Errorf("%s is not this process's own (%+v)", rel, info.Sys())
		}
		switch {
		case info.IsDir():
			if info.Mode().Perm() != 0o555 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
				t.Errorf("the directory %s has the mode %v, want 0555", rel, info.Mode())
			}
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if target != links[rel] {
				t.Errorf("the link %s points to %q, want %q", rel, target, links[rel])
			}
		case info.Mode().IsRegular():
			f, ok := files[rel]
			if !ok {
				t.Errorf("the file %s was not in the tree", rel)
				return nil
			}
			if info.Mode() != f.want {
				t.Errorf("the file %s has the mode %v, want %v", rel, info.Mode(), f.want)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !bytes.Equal(got, f.content) {
				t.Errorf("the file %s has %d bytes that are not the %d that were imported", rel, len(got), len(f.content))
			}
		default:
			t.Errorf("%s is %v", rel, info.Mode())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for rel := range times {
		if !seen[rel] {
			t.Errorf("%s was in the tree, and was not written out", rel)
		}
	}
}

func TestWriteOutOneFile(t *testing.T) {
	src := filepath.Join(t.TempDir(), "builder.sh")
	content := []byte("#!/bin/sh\nexit 0\n")
	// Without an execute bit: a root that is the content of a file has no
	// mode recorded, and gets one all the same.
	write(t, src, content, 0o644)
	get, root := imported(t, src)
	out := dest(t)
	if err := writeOut(context.Background(), get, root, out); err != nil {
		t.Fatalf("writing the file out: %v", err)
	}
	info, err := os.Lstat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode() != 0o555 {
		t.Errorf("the mode is %v, want a regular file of mode 0555", info.Mode())
	}
	if !info.ModTime().Equal(time.Unix(1, 0)) {
		t.Errorf("the time is %v, want one second after the epoch", info.ModTime())
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("the content is %q, want %q", got, content)
	}
}

func TestWriteOutModes(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	modes := map[string]struct{ mode, want os.FileMode }{
		"setuid":        {0o755 | os.ModeSetuid, 0o555},
		"setgid":        {0o755 | os.ModeSetgid, 0o555},
		"for-everybody": {0o666, 0o444},
		"owner-alone":   {0o700, 0o555},
		"group-alone":   {0o654, 0o444}, // the execute bit of the owner is what counts
	}
	for name, m := range modes {
		write(t, filepath.Join(src, name), []byte(name), m.mode)
	}
	// A directory that everybody may write to, with the sticky bit.
	if err := os.Mkdir(filepath.Join(src, "tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(src, "tmp"), 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	get, root := imported(t, src)
	out := dest(t)
	if err := writeOut(context.Background(), get, root, out); err != nil {
		t.Fatalf("writing the tree out: %v", err)
	}
	for name, m := range modes {
		info, err := os.Lstat(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode() != m.want {
			t.Errorf("%s was imported with %v and has %v, want %v", name, m.mode, info.Mode(), m.want)
		}
	}
	info, err := os.Lstat(filepath.Join(out, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode() != os.ModeDir|0o555 {
		t.Errorf("the directory has %v, want a directory of mode 0555", info.Mode())
	}
}

func TestWriteOutRefusesWhatAStoreDoesNotHold(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	write(t, filepath.Join(src, "bin/tool"), []byte("tool"), 0o755)
	if err := unix.Mkfifo(filepath.Join(src, "bin", "queue"), 0o644); err != nil {
		t.Fatalf("making a FIFO: %v", err)
	}
	get, root := imported(t, src)
	out := dest(t)
	err := writeOut(context.Background(), get, root, out)
	if err == nil {
		t.Fatal("a tree with a FIFO in it was written out")
	}
	if !strings.Contains(err.Error(), "bin/queue") || !strings.Contains(err.Error(), "FIFO") {
		t.Errorf("the error is %q, want one that names bin/queue and says that it is a FIFO", err)
	}
	nothingAt(t, out)
}

func TestWriteOutEndsWithItsContext(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	write(t, filepath.Join(src, "bin/tool"), []byte("tool"), 0o755)
	get, root := imported(t, src)
	out := dest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := writeOut(ctx, get, root, out)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("the error is %v, want the context's", err)
	}
	nothingAt(t, out)
}

func TestAccept(t *testing.T) {
	for _, typ := range []key.Type{key.DirLeaf, key.DirNode, key.Blob, key.FileNode} {
		k, err := key.New(typ, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := accept(k); err != nil {
			t.Errorf("a root that is a %v is refused: %v", typ, err)
		}
	}
	for _, typ := range []key.Type{key.XattrSet, key.Commit} {
		k, err := key.New(typ, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := accept(k); err == nil {
			t.Errorf("a root that is a %v is accepted: it is neither a tree nor the content of a file", typ)
		}
	}
}

func TestEntryNames(t *testing.T) {
	for _, name := range []string{"a", ".hidden", "...", "a b", "ü"} {
		if !oneName(name) {
			t.Errorf("%q is refused, and is the name of one file", name)
		}
	}
	for _, name := range []string{"", ".", "..", "a/b", "/", "a\x00b"} {
		if oneName(name) {
			t.Errorf("%q is taken for the name of one file", name)
		}
	}
}

func TestRemoveTree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tree")
	write(t, filepath.Join(root, "a/b/file"), []byte("x"), 0o444)
	for _, dir := range []string{"a/b", "a", ""} {
		if err := os.Chmod(filepath.Join(root, dir), 0o555); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeTree(root); err != nil {
		t.Fatalf("removing a tree nobody may write to: %v", err)
	}
	nothingAt(t, root)
	if err := removeTree(root); err != nil {
		t.Errorf("removing what is not there: %v", err)
	}
}
