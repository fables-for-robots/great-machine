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
