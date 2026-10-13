package storedir

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/amber-store/core/fstree"
	"github.com/amber-store/core/key"
	"golang.org/x/sys/unix"
)

// The modes of what is written out, which are those of a Nix store: a
// directory and a file that can be executed are for everybody to read and
// to execute, any other file for everybody to read, and nothing is for
// anybody to write.
const (
	modeDir  = 0o555
	modeExec = 0o555
	modeFile = 0o444
)

// storeTime is the modification time of what no reference records one
// for, the root of a store path: one second after the epoch, which Nix
// gives everything in a store.
var storeTime = time.Unix(1, 0)

// accept returns nil for a root that can be written out: a tree, or the
// content of a file.
func accept(root key.Key) error {
	switch root.Type() {
	case key.DirLeaf, key.DirNode, key.Blob, key.FileNode:
		return nil
	}
	return fmt.Errorf("its root is a %v, which is neither a tree nor the content of a file", root.Type())
}

// writeOut writes the reference whose root is root out at partial, which
// is not there and whose directory is, reading its objects with get, and
// syncs what it wrote. It writes what a Nix store holds and nothing else:
//
//   - a directory, with the mode 0555;
//   - a regular file, with the mode 0555 when its owner may execute it
//     and 0444 otherwise;
//   - a symbolic link, with its target as it is;
//   - for a root that is the content of a file, a file with the mode
//     0555: no mode is recorded for it.
//
// The owner and the group are those of this process, whatever the
// reference records, and no setuid, setgid or sticky bit and no extended
// attribute is set. The modification times are those the reference
// records, and storeTime for the root. An entry of any other type, a
// device, a FIFO or a socket, is an error that names it.
//
// Nothing is written through what the tree holds: every directory is made
// here and every file is made new, so that a symbolic link of the tree is
// made and never followed.
//
// When it fails, with ctx or otherwise, nothing is left at partial.
func writeOut(ctx context.Context, get func(key.Key) ([]byte, error), root key.Key, partial string) (err error) {
	if err := accept(root); err != nil {
		return err
	}
	defer func() {
		if err == nil {
			return
		}
		if rerr := removeTree(partial); rerr != nil {
			err = errors.Join(err, fmt.Errorf("removing what was written of it: %w", rerr))
		}
	}()
	w := &writer{ctx: ctx, get: get}
	switch root.Type() {
	case key.DirLeaf, key.DirNode:
		err = w.dir(partial, "", root, storeTime)
	default:
		err = w.file(partial, root, modeExec, storeTime)
	}
	if err != nil {
		return err
	}
	// The last object may have been read after ctx was done.
	if err := ctx.Err(); err != nil {
		return err
	}
	return syncTree(partial)
}

// writer writes the tree of one reference.
type writer struct {
	ctx context.Context
	get func(key.Key) ([]byte, error)
}

// fetch is what the tree is read with. Nothing Core reads with knows of a
// context, so this is where the end of one reaches the writing: between
// two objects.
func (w *writer) fetch(k key.Key) ([]byte, error) {
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	return w.get(k)
}

// dir makes the directory at path, which is rel in the reference ("" for
// its root), writes the entries of k into it, and gives it its mode and
// its time last: a directory nobody may write to does not stand in the
// way of its own content, and what is made in it does not change the time
// it was given.
func (w *writer) dir(path, rel string, k key.Key, at time.Time) error {
	if err := os.Mkdir(path, 0o700); err != nil {
		return err
	}
	entries, err := fstree.CollectEntries(k, w.fetch)
	if err != nil {
		return fmt.Errorf("reading the directory %s: %w", shown(rel), err)
	}
	for _, ent := range entries {
		if err := w.entry(path, rel, ent); err != nil {
			return err
		}
	}
	// The mode asked of Mkdir is narrowed by the umask; this one is not.
	if err := os.Chmod(path, modeDir); err != nil {
		return err
	}
	return setTime(path, at)
}

// entry writes one entry of the directory at dir, which is rel in the
// reference.
func (w *writer) entry(dir, rel string, ent fstree.Entry) error {
	name := string(ent.Name)
	if !oneName(name) {
		return fmt.Errorf("the directory %s has an entry named %q, which is no name of a file", shown(rel), name)
	}
	target, rel := filepath.Join(dir, name), path.Join(rel, name)
	at := time.Unix(0, ent.Mtime)
	switch ent.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		k, err := key.Parse(ent.ContentKey)
		if err != nil {
			return fmt.Errorf("the directory %s: %w", rel, err)
		}
		return w.dir(target, rel, k, at)
	case unix.S_IFREG:
		k, err := key.Parse(ent.ContentKey)
		if err != nil {
			return fmt.Errorf("the file %s: %w", rel, err)
		}
		mode := os.FileMode(modeFile)
		if ent.Mode&unix.S_IXUSR != 0 {
			mode = modeExec
		}
		return w.file(target, k, mode, at)
	case unix.S_IFLNK:
		if err := os.Symlink(string(ent.LinkTarget), target); err != nil {
			return err
		}
		return setTime(target, at)
	}
	return fmt.Errorf("%s is %s, which a store path does not hold", rel, kind(ent.Mode))
}

// file writes the content at k to a new file at path, and gives the file
// the mode and the time.
func (w *writer) file(path string, k key.Key, mode os.FileMode, at time.Time) (err error) {
	// A new file and nothing else: names are unique in a directory, so
	// something at path already is a file system that folds two names
	// into one, and writing through it would mix two files.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			f.Close()
		}
	}()
	if err := fstree.WriteContent(f, k, w.fetch); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := syncFile(f); err != nil {
		return err
	}
	// The mode asked of OpenFile is narrowed by the umask; this one is
	// not.
	if err := f.Chmod(mode); err != nil {
		return err
	}
	closed = true
	if err := f.Close(); err != nil {
		return err
	}
	return setTime(path, at)
}

// setTime gives path the modification time at. It does not follow path
// when it is a symbolic link.
func setTime(path string, at time.Time) error {
	ts := unix.NsecToTimespec(at.UnixNano())
	if err := unix.UtimesNanoAt(unix.AT_FDCWD, path, []unix.Timespec{ts, ts}, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("setting the time of %s: %w", path, err)
	}
	return nil
}

// oneName reports whether name can be the name of one file in a
// directory: any bytes but those that would make it a path, or no name at
// all.
func oneName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\x00")
}

// shown is rel as an error names it: the root of a reference has no name
// of its own.
func shown(rel string) string {
	if rel == "" {
		return "at its root"
	}
	return rel
}

// kind says what an entry with the mode is, for the refusal of one that a
// store path does not hold.
func kind(mode uint64) string {
	switch mode & unix.S_IFMT {
	case unix.S_IFIFO:
		return "a FIFO"
	case unix.S_IFCHR:
		return "a character device"
	case unix.S_IFBLK:
		return "a block device"
	case unix.S_IFSOCK:
		return "a socket"
	}
	return fmt.Sprintf("an entry of a type that is not known (%#o)", mode&unix.S_IFMT)
}

// removeTree removes the file or the tree at path, also one whose
// directories nobody may write to, as those of a store path are. What is
// not there is removed already.
func removeTree(path string) error {
	if os.RemoveAll(path) == nil {
		return nil
	}
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(path)
}
