package supervisor

import (
	"errors"
	"fmt"
	"os"
)

// ErrNotExecutable is in the chain of what Activate and CheckExecutable
// return for a binary that is no file this process may execute.
var ErrNotExecutable = errors.New("not an executable file")

// CheckExecutable returns nil when path, through its symbolic links, is a
// regular file with an execute bit.
func CheckExecutable(path string) error {
	info, err := os.Stat(path)
	switch {
	case err != nil:
		// os.Stat names the path in its error.
		return fmt.Errorf("%w: %w", ErrNotExecutable, err)
	case info.IsDir():
		return fmt.Errorf("%w: %s is a directory", ErrNotExecutable, path)
	case !info.Mode().IsRegular():
		return fmt.Errorf("%w: %s is no regular file", ErrNotExecutable, path)
	case info.Mode().Perm()&0o111 == 0:
		return fmt.Errorf("%w: %s has no execute bit", ErrNotExecutable, path)
	}
	return nil
}
