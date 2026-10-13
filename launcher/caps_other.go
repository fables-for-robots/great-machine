//go:build !linux

package launcher

// boundingSet returns no capability: a process has a bounding set on
// Linux alone.
func boundingSet() (uint64, error) { return 0, nil }
