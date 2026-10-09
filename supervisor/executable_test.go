package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckExecutable(t *testing.T) {
	dir := t.TempDir()
	path := func(name string) string { return filepath.Join(dir, name) }
	write := func(name string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path(name), []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
		// WriteFile's mode passes the umask; this one does not.
		if err := os.Chmod(path(name), mode); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, name string) {
		t.Helper()
		if err := os.Symlink(target, path(name)); err != nil {
			t.Fatal(err)
		}
	}
	write("program", 0o755)
	write("owner-only", 0o700)
	write("others-only", 0o601)
	write("data", 0o644)
	if err := os.Mkdir(path("directory"), 0o755); err != nil {
		t.Fatal(err)
	}
	link(path("program"), "link-to-program")
	link("program", "relative-link")
	link(path("link-to-program"), "link-to-link")
	link(path("data"), "link-to-data")
	link(path("directory"), "link-to-directory")
	link(path("nothing"), "dangling-link")

	tests := []struct {
		name string
		ok   bool
	}{
		{"program", true},
		{"owner-only", true},
		{"others-only", true},
		{"link-to-program", true},
		{"relative-link", true},
		{"link-to-link", true},
		{"data", false},
		{"link-to-data", false},
		{"directory", false},
		{"link-to-directory", false},
		{"dangling-link", false},
		{"missing", false},
		{"program/below-a-file", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckExecutable(path(tt.name))
			if tt.ok {
				if err != nil {
					t.Errorf("got %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, ErrNotExecutable) {
				t.Fatalf("got %v, want ErrNotExecutable in its chain", err)
			}
			if !strings.Contains(err.Error(), path(tt.name)) {
				t.Errorf("%q does not name the path", err)
			}
		})
	}
}
