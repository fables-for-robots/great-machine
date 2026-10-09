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
