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
