package launcher

import (
	"errors"

	"golang.org/x/sys/unix"
)

// boundingSet returns the bounding set of this process: a bit for every
// capability a program that it executes can have at all. Linux keeps the
// set for every thread, and no thread of a zathras changes its own.
func boundingSet() (uint64, error) {
	var set uint64
	for n := range 64 {
		has, err := unix.PrctlRetInt(unix.PR_CAPBSET_READ, uintptr(n), 0, 0, 0)
		if errors.Is(err, unix.EINVAL) {
			// The kernel has no capability of this number, and none above.
			break
		}
		if err != nil {
			return 0, err
		}
		if has == 1 {
			set |= 1 << n
		}
	}
	return set, nil
}
