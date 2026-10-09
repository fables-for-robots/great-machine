// Package stats reads the figures of a container: how much CPU, memory,
// disk and network it uses, from the files of cgroup v2 and from
// /proc/net/dev.
//
// A Reader keeps the counters of its last reading, and gives the rates
// over the time between two readings. It works wherever the files are, and
// gives no samples where one is missing (cgroup v1, a system that is not
// Linux).
package stats

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// Reader reads samples. The zero value is not usable: NewReader makes one
// for the container this process is in, and a test sets the two paths.
// A Reader is not safe for concurrent use.
type Reader struct {
	// Cgroup is the directory of the cgroup. NewReader: /sys/fs/cgroup.
	Cgroup string
	// NetDev is the file of the counters of the network interfaces.
	// NewReader: /proc/net/dev.
	NetDev string

	have bool      // whether there was a reading before
	at   time.Time // when it was
	last counters  // what it read
}

// counters is what the files hold at one time. Each of them only grows,
// unless the container was made anew.
type counters struct {
	cpuUsec   uint64
	diskRead  uint64
	diskWrite uint64
	netRx     uint64
	netTx     uint64
}

// NewReader returns a reader of the cgroup at /sys/fs/cgroup and of
// /proc/net/dev: in a container, the figures of the container.
func NewReader() *Reader {
	return &Reader{Cgroup: "/sys/fs/cgroup", NetDev: "/proc/net/dev"}
}

// Read returns the sample of now. The rates are over the time since the
// reading before, and zero at the first. It returns false when a file is
// missing or cannot be understood: this system gives no samples.
//
// A counter that is lower than it was (the container was made anew under
// the same reader) gives a rate of zero, and so do two readings at the
// same time.
func (r *Reader) Read(now time.Time) (wire.Sample, bool) {
	var cur counters
	var ok bool
	if cur.cpuUsec, ok = readCPU(filepath.Join(r.Cgroup, "cpu.stat")); !ok {
		return wire.Sample{}, false
	}
	memory, ok := readNumber(filepath.Join(r.Cgroup, "memory.current"))
	if !ok {
		return wire.Sample{}, false
	}
	limit, ok := readLimit(filepath.Join(r.Cgroup, "memory.max"))
	if !ok {
		return wire.Sample{}, false
	}
	if cur.diskRead, cur.diskWrite, ok = readIO(filepath.Join(r.Cgroup, "io.stat")); !ok {
		return wire.Sample{}, false
	}
	if cur.netRx, cur.netTx, ok = readNetDev(r.NetDev); !ok {
		return wire.Sample{}, false
	}

	s := wire.Sample{At: now, Memory: memory, MemoryLimit: limit}
	if seconds := now.Sub(r.at).Seconds(); r.have && seconds > 0 {
		// usage_usec is microseconds of CPU: a million of them in a second
		// is one core.
		s.CPU = rate(r.last.cpuUsec, cur.cpuUsec, seconds) / 1e6
		s.DiskRead = rate(r.last.diskRead, cur.diskRead, seconds)
		s.DiskWrite = rate(r.last.diskWrite, cur.diskWrite, seconds)
		s.NetRx = rate(r.last.netRx, cur.netRx, seconds)
		s.NetTx = rate(r.last.netTx, cur.netTx, seconds)
	}
	r.have, r.at, r.last = true, now, cur
	return s, true
}

// rate returns how much a counter grew in a second. One that went
// backwards has grown by nothing.
func rate(before, now uint64, seconds float64) float64 {
	if now < before {
		return 0
	}
	return float64(now-before) / seconds
}

// readCPU returns usage_usec of a cpu.stat, whose lines are a name and a
// number.
func readCPU(path string) (uint64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "usage_usec" {
			n, err := strconv.ParseUint(fields[1], 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// readNumber returns the one number a file such as memory.current holds.
func readNumber(path string) (uint64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(string(bytes.TrimSpace(data)), 10, 64)
	return n, err == nil
}

// readLimit returns what a memory.max holds: a number, or the word max
// for no limit, which is 0.
func readLimit(path string) (uint64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	if string(bytes.TrimSpace(data)) == "max" {
		return 0, true
	}
	n, err := strconv.ParseUint(string(bytes.TrimSpace(data)), 10, 64)
	return n, err == nil
}

// readIO returns the sums of rbytes and wbytes over the lines of an
// io.stat, each of which is a device and some key=value pairs:
//
//	8:0 rbytes=1 wbytes=2 rios=3 wios=4 dbytes=0 dios=0
//
// A device may lack a key. A cgroup that has done no I/O has an empty
// io.stat, which is zero and no error.
func readIO(path string) (read, written uint64, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		for _, field := range fields[1:] {
			key, value, found := strings.Cut(field, "=")
			if !found {
				return 0, 0, false
			}
			if key != "rbytes" && key != "wbytes" {
				continue
			}
			n, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return 0, 0, false
			}
			if key == "rbytes" {
				read += n
			} else {
				written += n
			}
		}
	}
	return read, written, true
}

// readNetDev returns the bytes received and sent over every interface of
// a /proc/net/dev but the loopback. The file has two lines of headings and
// then a line for every interface: its name, a colon, eight numbers of
// what was received, of which the first is the bytes, and eight of what
// was sent. The name may be glued to the first number.
func readNetDev(path string) (rx, tx uint64, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 2 || !strings.Contains(lines[0], "|") || !strings.Contains(lines[1], "|") {
		return 0, 0, false
	}
	for _, line := range lines[2:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		name, rest, found := strings.Cut(line, ":")
		fields := strings.Fields(rest)
		if !found || len(fields) < 16 {
			return 0, 0, false
		}
		received, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0, 0, false
		}
		sent, err := strconv.ParseUint(fields[8], 10, 64)
		if err != nil {
			return 0, 0, false
		}
		if strings.TrimSpace(name) == "lo" {
			continue
		}
		rx += received
		tx += sent
	}
	return rx, tx, true
}
