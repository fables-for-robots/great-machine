### Task 18: closure — the binary of a rollout, its store path and its closure

**Files:**
- Create: `closure/binary.go` (the package comment, `StoreDir`, `Binary`)
- Create: `closure/macho.go` (`CheckLinux`)
- Create: `closure/requisites.go` (`Of`, `Names`)
- Test: `closure/binary_test.go`, `closure/macho_test.go`, `closure/requisites_test.go`

**Interfaces:**
- Consumes: nothing of the other tasks. The standard library alone; `nix-store` is run as a command.
- Produces (the contract's `closure` section):

```go
package closure

const StoreDir = "/nix/store"

func Binary(path string) (binary, storePath string, err error)
func CheckLinux(binary string) error
func Of(ctx context.Context, storePath string) ([]string, error)
func Names(storePaths []string) []string
```

What the contract leaves open, and this task settles:

- `Binary`: a path that, after `filepath.Clean`, is absolute and begins with `/nix/store/` is taken as written (cleaned, its symbolic links **not** resolved). Any other path is made absolute and resolved with `filepath.EvalSymlinks`, and the result has to begin with `/nix/store/`. Either way there has to be a file name behind the store path, and the binary has to be a regular file through its symbolic links. Every error begins with the path as it was given, followed by what is wrong; the error for `""` says that the path is empty.
- `CheckLinux` passes a file of fewer than four bytes, and returns the error of `os.Open` for one it cannot open.
- `Of` refuses a line of the command's output that is not `/nix/store/<name>`, and returns an error with `context.Canceled` in its chain when its context was cancelled.
- The package works through the unexported variable `storeDir`, which is `StoreDir`; the tests point it at a directory of their own. The command runner is the unexported variable `run`.

This task pins **Review Focus 2**: `TestBinaryRefuses`.

The tests are in package `closure` itself, because they replace `storeDir` and `run`. They pass on macOS and on Linux.

- [ ] **Step 1: Write the failing test of `Binary`**

Create `closure/binary_test.go`:

```go
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
//	<store>/bbbb-tool/bin/tool      a regular file
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
```

- [ ] **Step 2: Run the test to see it fail**

Run: `go test ./closure/`
Expected: FAIL — the build fails with `undefined: storeDir` and `undefined: Binary`

- [ ] **Step 3: Write `Binary`**

Create `closure/binary.go`:

```go
// Package closure finds what a rollout is made of: the binary a path
// names, the store path the binary is in, and the closure of that store
// path, which nix-store is asked for.
package closure

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// StoreDir is the directory of the Nix store.
const StoreDir = "/nix/store"

// storeDir is the store directory the package works with. It is StoreDir,
// and a directory of their own in the tests.
var storeDir = StoreDir

// inStore reports whether the clean, absolute path is inside the store
// directory: the store directory itself is not.
func inStore(path string) bool {
	return strings.HasPrefix(path, storeDir+"/")
}

// Binary takes the PATH of a rollout. A path inside StoreDir is taken as
// written; any other is resolved through its symbolic links and has to
// end inside StoreDir. It returns the binary and the store path it is in
// (/nix/store/<name>). The binary has to be a regular file, through its
// symbolic links.
//
// Every error names the path as it was given and says what is wrong
// with it.
func Binary(path string) (binary, storePath string, err error) {
	if path == "" {
		return "", "", errors.New(`the path of the binary is empty`)
	}
	binary = filepath.Clean(path)
	if !filepath.IsAbs(binary) || !inStore(binary) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", "", fmt.Errorf("%s: %w", path, err)
		}
		if abs == storeDir {
			return "", "", fmt.Errorf("%s is the store directory, not a binary in it", path)
		}
		resolved, err := filepath.EvalSymlinks(abs)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if info, lerr := os.Lstat(abs); lerr == nil && info.Mode()&fs.ModeSymlink != 0 {
				return "", "", fmt.Errorf("%s is a symbolic link to something that does not exist", path)
			}
			return "", "", fmt.Errorf("%s does not exist", path)
		case err != nil:
			return "", "", fmt.Errorf("%s: %w", path, err)
		case resolved == storeDir:
			return "", "", fmt.Errorf("%s is the store directory, not a binary in it", path)
		case !inStore(resolved):
			if resolved == abs {
				return "", "", fmt.Errorf("%s is not inside %s", path, storeDir)
			}
			return "", "", fmt.Errorf("%s resolves to %s, which is not inside %s", path, resolved, storeDir)
		}
		binary = resolved
	}

	name, file, _ := strings.Cut(binary[len(storeDir)+1:], "/")
	storePath = storeDir + "/" + name
	if file == "" {
		return "", "", fmt.Errorf("%s is a store path, not a binary: name the file in it, such as %s/bin/NAME", path, storePath)
	}
	info, err := os.Stat(binary)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if linfo, lerr := os.Lstat(binary); lerr == nil && linfo.Mode()&fs.ModeSymlink != 0 {
			return "", "", fmt.Errorf("%s is a symbolic link to something that does not exist", path)
		}
		return "", "", fmt.Errorf("%s does not exist", path)
	case err != nil:
		return "", "", fmt.Errorf("%s: %w", path, err)
	case info.IsDir():
		return "", "", fmt.Errorf("%s is a directory, not a binary", path)
	case !info.Mode().IsRegular():
		return "", "", fmt.Errorf("%s is not a regular file", path)
	}
	return binary, storePath, nil
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./closure/ -run 'TestBinary' -v`
Expected: PASS — `TestBinary` with 7 subtests and `TestBinaryRefuses` with 18

- [ ] **Step 5: Commit**

```bash
git add closure/
git commit -m "Closure: the binary of a rollout and its store path"
```

- [ ] **Step 6: Write the failing test of `CheckLinux`**

Create `closure/macho_test.go`:

```go
package closure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckLinux(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		macOS   bool
	}{
		{"Mach-O 32-bit", []byte{0xfe, 0xed, 0xfa, 0xce, 0, 0, 0, 7}, true},
		{"Mach-O 64-bit", []byte{0xfe, 0xed, 0xfa, 0xcf, 0, 0, 0, 7}, true},
		{"Mach-O 32-bit, little-endian", []byte{0xce, 0xfa, 0xed, 0xfe, 7, 0, 0, 0}, true},
		{"Mach-O 64-bit, little-endian", []byte{0xcf, 0xfa, 0xed, 0xfe, 7, 0, 0, 1}, true},
		{"Mach-O universal", []byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 2}, true},
		{"Mach-O universal, little-endian", []byte{0xbe, 0xba, 0xfe, 0xca, 2, 0, 0, 0}, true},
		{"Mach-O, the magic number alone", []byte{0xcf, 0xfa, 0xed, 0xfe}, true},
		{"ELF", []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0}, false},
		{"a script", []byte("#!/bin/sh\nexec true\n"), false},
		{"an empty file", nil, false},
		{"two bytes", []byte{0xcf, 0xfa}, false},
		{"three bytes of a magic number", []byte{0xcf, 0xfa, 0xed}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "binary")
			if err := os.WriteFile(path, tt.content, 0o755); err != nil {
				t.Fatal(err)
			}
			err := CheckLinux(path)
			switch {
			case tt.macOS && err == nil:
				t.Fatal("CheckLinux passed a Mach-O binary")
			case tt.macOS:
				for _, want := range []string{path, "macOS", "Linux"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the error does not say %q: %v", want, err)
					}
				}
			case err != nil:
				t.Fatalf("CheckLinux: %v", err)
			}
		})
	}
}

func TestCheckLinuxFollowsLinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte{0xcf, 0xfa, 0xed, 0xfe, 7, 0, 0, 1}, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := CheckLinux(link); err == nil {
		t.Error("CheckLinux passed a symbolic link to a Mach-O binary")
	}
}

func TestCheckLinuxMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nothing")
	err := CheckLinux(path)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("CheckLinux of a file that is not there: %v; want an error that names it", err)
	}
}
```

- [ ] **Step 7: Run the test to see it fail**

Run: `go test ./closure/`
Expected: FAIL — the build fails with `undefined: CheckLinux`

- [ ] **Step 8: Write `CheckLinux`**

Create `closure/macho.go`:

```go
package closure

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
)

// machO are the four bytes a Mach-O file begins with: the 32-bit and the
// 64-bit format in both byte orders, and the universal ("fat") file in
// both.
var machO = [][4]byte{
	{0xfe, 0xed, 0xfa, 0xce},
	{0xfe, 0xed, 0xfa, 0xcf},
	{0xce, 0xfa, 0xed, 0xfe},
	{0xcf, 0xfa, 0xed, 0xfe},
	{0xca, 0xfe, 0xba, 0xbe},
	{0xbe, 0xba, 0xfe, 0xca},
}

// CheckLinux refuses a Mach-O binary: the pods run Linux, and a binary
// built on a Mac for the Mac would be pushed and rolled out only to fail
// at its start. Anything else passes, a script as well as an ELF binary.
func CheckLinux(binary string) error {
	f, err := os.Open(binary)
	if err != nil {
		return err
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			// Shorter than a magic number: no Mach-O binary.
			return nil
		}
		return fmt.Errorf("reading %s: %w", binary, err)
	}
	if slices.Contains(machO, magic) {
		return fmt.Errorf("%s is a macOS binary (Mach-O), and the pods run Linux: build it for Linux", binary)
	}
	return nil
}
```

- [ ] **Step 9: Run the tests to see them pass**

Run: `go test ./closure/ -run 'TestCheckLinux' -v`
Expected: PASS — `TestCheckLinux` with 12 subtests, `TestCheckLinuxFollowsLinks`, `TestCheckLinuxMissing`

- [ ] **Step 10: Commit**

```bash
git add closure/
git commit -m "Closure: refuse a Mach-O binary"
```

- [ ] **Step 11: Write the failing test of `Of` and `Names`**

Create `closure/requisites_test.go`. `TestOfRealStore` runs the real `nix-store` on the store path `nix-store` itself is in, and is skipped on a machine without Nix.

```go
package closure

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeRun replaces the command runner for the length of the test with one
// that records its arguments and returns out and err.
func fakeRun(t *testing.T, out string, err error) *[][]string {
	t.Helper()
	var calls [][]string
	old := run
	run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		return []byte(out), err
	}
	t.Cleanup(func() { run = old })
	return &calls
}

func TestOf(t *testing.T) {
	calls := fakeRun(t, "/nix/store/cccc-web\n\n/nix/store/aaaa-glibc\n  \n/nix/store/bbbb-openssl\n", nil)
	got, err := Of(context.Background(), "/nix/store/cccc-web")
	if err != nil {
		t.Fatalf("Of: %v", err)
	}
	want := []string{"/nix/store/aaaa-glibc", "/nix/store/bbbb-openssl", "/nix/store/cccc-web"}
	if !slices.Equal(got, want) {
		t.Errorf("Of = %q, want %q", got, want)
	}
	wantCall := []string{"nix-store", "--query", "--requisites", "/nix/store/cccc-web"}
	if len(*calls) != 1 || !slices.Equal((*calls)[0], wantCall) {
		t.Errorf("the command was run as %q, want once as %q", *calls, wantCall)
	}
}

func TestOfNoTrailingLineFeed(t *testing.T) {
	fakeRun(t, "/nix/store/bbbb-web\n/nix/store/aaaa-glibc", nil)
	got, err := Of(context.Background(), "/nix/store/bbbb-web")
	if err != nil {
		t.Fatalf("Of: %v", err)
	}
	if want := []string{"/nix/store/aaaa-glibc", "/nix/store/bbbb-web"}; !slices.Equal(got, want) {
		t.Errorf("Of = %q, want %q", got, want)
	}
}

func TestOfCommandFails(t *testing.T) {
	failed := errors.New("exit status 1: error: path '/nix/store/cccc-web' is not valid")
	fakeRun(t, "", failed)
	_, err := Of(context.Background(), "/nix/store/cccc-web")
	if !errors.Is(err, failed) {
		t.Fatalf("Of: %v; want the error of the command in its chain", err)
	}
	for _, want := range []string{"nix-store --query --requisites /nix/store/cccc-web", "is not valid"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not say %q: %v", want, err)
		}
	}
}

func TestOfLineOutsideTheStore(t *testing.T) {
	for _, line := range []string{"/tmp/elsewhere", "warning: something", "/nix/store/", "/nix/store/aaaa-web/bin"} {
		fakeRun(t, "/nix/store/aaaa-glibc\n"+line+"\n", nil)
		_, err := Of(context.Background(), "/nix/store/cccc-web")
		if err == nil || !strings.Contains(err.Error(), line) {
			t.Errorf("Of with the line %q: %v; want an error that quotes the line", line, err)
		}
	}
}

func TestOfCancelled(t *testing.T) {
	fakeRun(t, "/nix/store/aaaa-glibc\n", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Of(ctx, "/nix/store/cccc-web")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Of with a cancelled context: %v; want context.Canceled", err)
	}
}

// The real runner: a command that fails comes back with its standard
// error, and one that is cut short with the end of the context.
func TestRun(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this machine")
	}
	out, err := run(context.Background(), "sh", "-c", "echo out; echo err >&2")
	if err != nil || string(out) != "out\n" {
		t.Errorf("run = %q, %v; want the standard output alone", out, err)
	}
	_, err = run(context.Background(), "sh", "-c", "echo it went wrong >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "it went wrong") || !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("run of a command that fails: %v; want its status and its standard error", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := run(ctx, "sh", "-c", "sleep 60"); !errors.Is(err, context.Canceled) {
		t.Errorf("run with a cancelled context: %v; want context.Canceled", err)
	}
}

func TestNames(t *testing.T) {
	got := Names([]string{"/nix/store/aaaa-glibc", "/nix/store/bbbb-web"})
	if want := []string{"aaaa-glibc", "bbbb-web"}; !slices.Equal(got, want) {
		t.Errorf("Names = %q, want %q", got, want)
	}
	if got := Names(nil); len(got) != 0 {
		t.Errorf("Names(nil) = %q, want none", got)
	}
}

// With a real Nix on the machine: the closure of the store path nix-store
// itself is in holds that store path.
func TestOfRealStore(t *testing.T) {
	program, err := exec.LookPath("nix-store")
	if err != nil {
		t.Skip("no nix-store on this machine")
	}
	resolved, err := filepath.EvalSymlinks(program)
	if err != nil {
		t.Fatal(err)
	}
	_, storePath, err := Binary(resolved)
	if err != nil {
		t.Skipf("nix-store is not in the store: %v", err)
	}
	paths, err := Of(context.Background(), storePath)
	if err != nil {
		t.Fatalf("Of(%q): %v", storePath, err)
	}
	if !slices.Contains(paths, storePath) {
		t.Errorf("the closure of %s does not hold it: %q", storePath, paths)
	}
	if !slices.IsSorted(paths) {
		t.Errorf("the closure is not sorted: %q", paths)
	}
	for _, name := range Names(paths) {
		if name == "" || strings.Contains(name, "/") {
			t.Errorf("%q is no name of a store path", name)
		}
	}
}
```

- [ ] **Step 12: Run the test to see it fail**

Run: `go test ./closure/`
Expected: FAIL — the build fails with `undefined: run`, `undefined: Of` and `undefined: Names`

- [ ] **Step 13: Write `Of` and `Names`**

Create `closure/requisites.go`:

```go
package closure

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// run runs a command and returns what it wrote to standard output. An
// error of the command carries what it wrote to standard error. The tests
// replace it.
var run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// The command was killed because ctx ended: that is the cause.
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		if said := strings.TrimSpace(stderr.String()); said != "" {
			return nil, fmt.Errorf("%w: %s", err, said)
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

// Of returns the store paths of the closure of storePath, sorted, by
// `nix-store --query --requisites`. The closure of a store path holds the
// store path itself.
func Of(ctx context.Context, storePath string) ([]string, error) {
	out, err := run(ctx, "nix-store", "--query", "--requisites", storePath)
	if err != nil {
		return nil, fmt.Errorf("nix-store --query --requisites %s: %w", storePath, err)
	}
	var paths []string
	for line := range strings.Lines(string(out)) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, ok := strings.CutPrefix(line, storeDir+"/")
		if !ok || name == "" || strings.Contains(name, "/") {
			return nil, fmt.Errorf("nix-store --query --requisites %s: %q is no store path under %s", storePath, line, storeDir)
		}
		paths = append(paths, line)
	}
	slices.Sort(paths)
	return paths, nil
}

// Names returns the names of store paths: what follows /nix/store/.
func Names(storePaths []string) []string {
	names := make([]string, len(storePaths))
	for i, p := range storePaths {
		names[i] = strings.TrimPrefix(p, storeDir+"/")
	}
	return names
}
```

- [ ] **Step 14: Run the tests to see them pass**

Run: `go vet ./closure/ && go test ./closure/ -v`
Expected: PASS — every test of the package; `TestOfRealStore` passes where `nix-store` is on the `PATH` (it is in the repository's Nix shell on a machine with Nix) and is reported as skipped where it is not.

- [ ] **Step 15: Commit**

```bash
git add closure/
git commit -m "Closure: the closure of a store path, by nix-store"
```
