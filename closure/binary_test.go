package closure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeStore lays out a store directory in a temporary directory, makes the
// package work with it for the length of the test, and returns the
// temporary directory and the store directory, <root>/nix/store.
//
// In it are:
//
//	<store>/aaaa-web/bin/web        a regular file
//	<store>/aaaa-web/bin/tool       a symbolic link to <store>/bbbb-tool/bin/tool
//	<store>/aaaa-web/bin/gone       a symbolic link to nothing
//	<store>/aaaa-web/bin/leak       a symbolic link to <root>/work/elsewhere
//	<store>/aaaa-web/bin/etc        a symbolic link to <root>/nix/etc
//	<store>/aaaa-web/bin/rel        a relative symbolic link to <store>/bbbb-tool/bin/tool
//	<store>/bbbb-tool/bin/tool      a regular file
//	<store>/eeee-linked/bin         a symbolic link to <root>/nix/etc
//	<root>/nix/etc/passwd           a regular file outside the store
//	<root>/work/result              a symbolic link to <store>/aaaa-web
//	<root>/work/elsewhere           a regular file outside the store
//	<root>/work/link                a symbolic link to <root>/work/elsewhere
//	<root>/work/dangling            a symbolic link to nothing
func fakeStore(t *testing.T) (root, store string) {
	t.Helper()
	// The temporary directory of macOS is behind a symbolic link, and a
	// resolved path is compared with the store directory.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store = filepath.Join(root, "nix", "store")
	for _, dir := range []string{
		filepath.Join(store, "aaaa-web", "bin"),
		filepath.Join(store, "bbbb-tool", "bin"),
		filepath.Join(store, "eeee-linked"),
		filepath.Join(root, "nix", "etc"),
		filepath.Join(root, "work"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{
		filepath.Join(store, "aaaa-web", "bin", "web"),
		filepath.Join(store, "bbbb-tool", "bin", "tool"),
		filepath.Join(root, "nix", "etc", "passwd"),
		filepath.Join(root, "work", "elsewhere"),
	} {
		if err := os.WriteFile(file, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		filepath.Join(store, "aaaa-web", "bin", "tool"): filepath.Join(store, "bbbb-tool", "bin", "tool"),
		filepath.Join(store, "aaaa-web", "bin", "gone"): filepath.Join(store, "cccc-gone", "bin", "gone"),
		filepath.Join(store, "aaaa-web", "bin", "leak"): filepath.Join(root, "work", "elsewhere"),
		filepath.Join(store, "aaaa-web", "bin", "etc"):  filepath.Join(root, "nix", "etc"),
		filepath.Join(store, "aaaa-web", "bin", "rel"):  filepath.Join("..", "..", "bbbb-tool", "bin", "tool"),
		filepath.Join(store, "eeee-linked", "bin"):      filepath.Join(root, "nix", "etc"),
		filepath.Join(root, "work", "result"):           filepath.Join(store, "aaaa-web"),
		filepath.Join(root, "work", "link"):             filepath.Join(root, "work", "elsewhere"),
		filepath.Join(root, "work", "dangling"):         filepath.Join(root, "work", "nothing"),
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	old := storeDir
	storeDir = store
	t.Cleanup(func() { storeDir = old })
	return root, store
}

func TestBinary(t *testing.T) {
	root, store := fakeStore(t)
	// The relative paths of the table are relative to <root>/work.
	t.Chdir(filepath.Join(root, "work"))

	web := filepath.Join(store, "aaaa-web")
	tests := []struct {
		name          string
		path          string
		binary, store string
	}{
		{"inside the store, as written", web + "/bin/web", web + "/bin/web", web},
		{"inside the store, cleaned", web + "/bin/../bin//web", web + "/bin/web", web},
		{"relative, through the result link", "result/bin/web", web + "/bin/web", web},
		{"relative with a dot, through the result link", "./result/bin/web", web + "/bin/web", web},
		{"absolute, through the result link", root + "/work/result/bin/web", web + "/bin/web", web},
		// The link inside the store is not resolved: the binary is the
		// path that was written, and its store path the one written.
		{"a link inside the store to another store path", web + "/bin/tool", web + "/bin/tool", web},
		// Through the result link the path is resolved all the way.
		{"through the result link to a link", "result/bin/tool", store + "/bbbb-tool/bin/tool", store + "/bbbb-tool"},
		// A link inside the store to another store path, through a relative
		// link, is taken as written too.
		{"a relative link inside the store to another store path", web + "/bin/rel", web + "/bin/rel", web},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary, storePath, err := Binary(tt.path)
			if err != nil {
				t.Fatalf("Binary(%q): %v", tt.path, err)
			}
			if binary != tt.binary || storePath != tt.store {
				t.Errorf("Binary(%q) = %q, %q; want %q, %q", tt.path, binary, storePath, tt.binary, tt.store)
			}
		})
	}
}

// Review Focus 2: a PATH for rollout that is a directory, is missing, is a
// store path without a file in it, or resolves outside /nix/store ends
// with an error that names the path and says what is wrong.
func TestBinaryRefuses(t *testing.T) {
	root, store := fakeStore(t)
	t.Chdir(filepath.Join(root, "work"))

	web := filepath.Join(store, "aaaa-web")
	tests := []struct {
		name string
		path string
		want string // what the error says is wrong
	}{
		{"missing, inside the store", web + "/bin/nothing", "does not exist"},
		{"missing store path", store + "/dddd-none/bin/web", "does not exist"},
		{"a directory", web + "/bin", "is a directory"},
		{"a directory through the result link", "result/bin", "is a directory"},
		{"the store path itself", web, "is a store path, not a binary"},
		{"the store path itself, with a slash", web + "/", "is a store path, not a binary"},
		{"the store path through the result link", "result", "is a store path, not a binary"},
		{"the store directory", store, "is the store directory"},
		{"the store directory, with a slash", store + "/", "is the store directory"},
		{"a file outside the store", root + "/work/elsewhere", "is not inside " + store},
		{"a relative file outside the store", "elsewhere", "is not inside " + store},
		{"a link to a file outside the store", "link", "resolves to " + root + "/work/elsewhere, which is not inside " + store},
		{"a dangling link", "dangling", "is a symbolic link to something that does not exist"},
		{"a dangling link inside the store", web + "/bin/gone", "is a symbolic link to something that does not exist"},
		{"a relative path inside nothing", "bin/web", "does not exist"},
		{"dots that leave the store", web + "/../../etc/passwd", "is not inside " + store},
		{"dots that leave the store for nothing", web + "/../../etc/shadow", "does not exist"},
		// A path inside the store that leaves it through a link is refused,
		// with the place it resolves to.
		{"a file inside the store that links to a file outside", web + "/bin/leak", "resolves to " + root + "/work/elsewhere, which is not inside " + store},
		{"a store path whose bin links to a directory outside", store + "/eeee-linked/bin/passwd", "resolves to " + root + "/nix/etc/passwd, which is not inside " + store},
		// The directory check comes before the check of the store: the
		// link is a directory, so it is refused as one.
		{"a link inside the store to a directory outside it", web + "/bin/etc", "is a directory, not a binary"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary, storePath, err := Binary(tt.path)
			if err == nil {
				t.Fatalf("Binary(%q) = %q, %q; want an error", tt.path, binary, storePath)
			}
			if !strings.HasPrefix(err.Error(), tt.path+" ") {
				t.Errorf("Binary(%q): the error does not begin with the path: %v", tt.path, err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Binary(%q): the error does not say %q: %v", tt.path, tt.want, err)
			}
		})
	}

	t.Run("an empty path", func(t *testing.T) {
		_, _, err := Binary("")
		if err == nil || !strings.Contains(err.Error(), "empty") {
			t.Errorf(`Binary(""): %v; want an error that says the path is empty`, err)
		}
	})
}
