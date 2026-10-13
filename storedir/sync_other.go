//go:build !linux

package storedir

import "os"

// syncFile is called on every file that was written, before it is closed.
// Where there is no syncfs, which is where a zathras is developed and
// tested and no pod runs, this is what makes a file durable.
func syncFile(f *os.File) error { return f.Sync() }

// syncTree does nothing here: every file was synced as it was written.
func syncTree(string) error { return nil }
