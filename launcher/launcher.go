// Package launcher starts the processes of a zathras: the service and the
// shells.
//
// A zathras serves the store of its container itself, over NFS, and
// mounts it, which takes CAP_SYS_ADMIN. Two things follow for whatever it
// starts.
//
// What it starts is not to have that capability. On Linux a Launcher has
// a thread of its own that has given it up, and every process is started
// from that thread: a child is a copy of the thread that started it.
//
// And nothing is executed from the store by the zathras itself. Go starts
// a process with vfork: the thread that starts it stands still until the
// child has executed its program, and until then the runtime cannot stop
// the world, which every garbage collection begins with. A program in the
// store is read through the NFS server of this same process, whose
// goroutines a collection that begins meanwhile holds: the child waits
// for the server, the server for the collection, the collection for the
// child. So what a Launcher starts is the program itself, which is a file
// of the image, with Arg: started so, it replaces itself with the program
// that was meant (Replace), as a process of its own that holds nobody.
package launcher

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// Arg is the first argument of the program when it is started to replace
// itself with another:
//
//	great-machine exec-in-place PATH ARGV0 [ARG...]
const Arg = "exec-in-place"

// The exit status of a program that was to replace itself and could not,
// as a shell has them.
const (
	statusNotExecuted = 126 // the program could not be executed
	statusNotThere    = 127 // there is no such program
)

// Replace is called first thing in main, and in the TestMain of a test
// that starts processes through a Launcher. When args are not those of
// Arg it returns (0, false) at once. When they are, it replaces the
// process with the program at PATH, with the arguments from ARGV0 on and
// the environment of the process, and does not return; when that fails it
// says so on standard error and returns the exit status, 127 for a program
// that is not there and 126 for any other failure, and true.
func Replace(args []string) (status int, was bool) {
	if len(args) < 2 || args[1] != Arg {
		return 0, false
	}
	if len(args) < 4 {
		fmt.Fprintf(os.Stderr, "great-machine: %s is how a zathras starts a program: it takes PATH ARGV0 [ARG...]\n", Arg)
		return statusNotExecuted, true
	}
	path := args[2]
	err := syscall.Exec(path, args[3:], os.Environ())
	if !errors.Is(err, syscall.ENOENT) {
		fmt.Fprintf(os.Stderr, "great-machine: executing %s: %v\n", path, err)
		return statusNotExecuted, true
	}
	// The kernel says "no such file" also of a program that is there
	// when what the program is to be loaded with is not: its interpreter,
	// which for a program of a Nix store is in another store path.
	if _, statErr := os.Stat(path); statErr == nil {
		fmt.Fprintf(os.Stderr, "great-machine: executing %s: %v: the file is there, so what is missing is what it is loaded with, its interpreter\n", path, err)
	} else {
		fmt.Fprintf(os.Stderr, "great-machine: executing %s: %v\n", path, err)
	}
	return statusNotThere, true
}

// User is who a process runs as.
type User struct{ UID, GID uint32 }

// ParseUser reads "UID" or "UID:GID", both numbers; without a GID it is
// the UID.
func ParseUser(text string) (User, error) {
	uid, gid, both := strings.Cut(text, ":")
	u, err := parseID(uid)
	if err != nil {
		return User{}, fmt.Errorf("%q is no user: want UID or UID:GID, both numbers: %w", text, err)
	}
	if !both {
		return User{UID: u, GID: u}, nil
	}
	g, err := parseID(gid)
	if err != nil {
		return User{}, fmt.Errorf("%q is no user: want UID or UID:GID, both numbers: %w", text, err)
	}
	return User{UID: u, GID: g}, nil
}

// parseID reads the ID of a user or of a group: digits alone, and not the
// largest number an ID holds, which stands for "no ID" wherever one is
// set.
func parseID(text string) (uint32, error) {
	for _, c := range text {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%q is no number", text)
		}
	}
	n, err := strconv.ParseUint(text, 10, 32)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return 0, fmt.Errorf("%s is more than an ID holds", text)
		}
		return 0, fmt.Errorf("%q is no number", text)
	}
	if n == math.MaxUint32 {
		return 0, fmt.Errorf("%s is no ID: it stands for none", text)
	}
	return uint32(n), nil
}
