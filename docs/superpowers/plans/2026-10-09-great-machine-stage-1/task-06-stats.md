### Task 6: stats, the samples of a container

**Files:**
- Create: `stats/stats.go`
- Test: `stats/stats_test.go`

**Interfaces:**
- Consumes: from `wire` (Task 1), the type of a sample:

  ```go
  type Sample struct {
  	At          time.Time `json:"at"`
  	CPU         float64   `json:"cpu"`          // cores
  	Memory      uint64    `json:"memory"`       // bytes
  	MemoryLimit uint64    `json:"memory_limit"` // bytes; 0 for none
  	DiskRead    float64   `json:"disk_read"`    // bytes per second
  	DiskWrite   float64   `json:"disk_write"`
  	NetRx       float64   `json:"net_rx"`
  	NetTx       float64   `json:"net_tx"`
  }
  ```

- Produces:

  ```go
  package stats

  // Reader reads samples. The zero value is not usable.
  type Reader struct {
  	Cgroup string // the directory of the cgroup; NewReader: /sys/fs/cgroup
  	NetDev string // NewReader: /proc/net/dev
  	// unexported: the counters of the reading before
  }

  func NewReader() *Reader

  // Read returns the sample of now. The rates are over the time since the
  // reading before, and zero at the first. It returns false when a file is
  // missing or cannot be understood: this system gives no samples.
  func (r *Reader) Read(now time.Time) (wire.Sample, bool)
  ```

The reader has no build tag: it works on any system where the files are,
and the tests run on macOS and on Linux from files they write into
`t.TempDir()`. A `Reader` is used by one goroutine. An `io.stat` that is
empty is no error (a cgroup that has done no I/O has one): it is zero.
Every other file that is missing, empty or not understood makes `Read`
return false.

- [ ] **Step 1: Write the failing test**

`stats/stats_test.go`:

```go
package stats

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// The files of a container, as a kernel writes them.
const (
	cpuStat = `usage_usec 5000000
user_usec 3200000
system_usec 1800000
nr_periods 120
nr_throttled 3
throttled_usec 45000
nr_bursts 0
burst_usec 0
`
	// Two seconds later: a million microseconds more.
	cpuStatLater = `usage_usec 6000000
user_usec 3900000
system_usec 2100000
nr_periods 140
nr_throttled 3
throttled_usec 45000
nr_bursts 0
burst_usec 0
`
	memoryCurrent      = "104857600\n"
	memoryCurrentLater = "209715200\n"
	memoryMaxNone      = "max\n"
	memoryMaxSet       = "536870912\n"

	// Two devices, of which the second has no wbytes.
	ioStat = `8:0 rbytes=1000000 wbytes=2000000 rios=30 wios=40 dbytes=0 dios=0
253:0 rbytes=500000 rios=12
`
	// Read 4096 more on the first device and 4096 more on the second,
	// written 2048 more on the first.
	ioStatLater = `8:0 rbytes=1004096 wbytes=2002048 rios=31 wios=41 dbytes=0 dios=0
253:0 rbytes=504096 rios=13
`

	// lo, an eth0 whose name is glued to its first number, and one more.
	netDev = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:  999999    1234    0    0    0     0          0         0   999999    1234    0    0    0     0       0          0
  eth0:10000000    8000    0    0    0     0          0         0  2000000    6000    0    0    0     0       0          0
  net1:   50000     100    0    0    0     0          0         0    30000      90    0    0    0     0       0          0
`
	// lo has moved a great deal, which is not counted; eth0 received
	// 20000 more and sent 10000 more, net1 2000 and 1000.
	netDevLater = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 9999999    2234    0    0    0     0          0         0  9999999    2234    0    0    0     0       0          0
  eth0:10020000    8100    0    0    0     0          0         0  2010000    6050    0    0    0     0       0          0
  net1:   52000     110    0    0    0     0          0         0    31000      95    0    0    0     0       0          0
`
)

// files is the content of the five files of a reading, by name.
type files map[string]string

// first returns the files of a first reading.
func first() files {
	return files{
		"cpu.stat":       cpuStat,
		"memory.current": memoryCurrent,
		"memory.max":     memoryMaxNone,
		"io.stat":        ioStat,
		"net_dev":        netDev,
	}
}

// later returns the files two seconds after first.
func later() files {
	return files{
		"cpu.stat":       cpuStatLater,
		"memory.current": memoryCurrentLater,
		"memory.max":     memoryMaxSet,
		"io.stat":        ioStatLater,
		"net_dev":        netDevLater,
	}
}

// write puts the files into dir. One whose content is "-" is removed.
func write(t *testing.T, dir string, f files) {
	t.Helper()
	for name, content := range f {
		path := filepath.Join(dir, name)
		if content == "-" {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// reader returns a reader of the files in a new directory, and the
// directory.
func reader(t *testing.T) (*Reader, string) {
	t.Helper()
	dir := t.TempDir()
	return &Reader{Cgroup: dir, NetDev: filepath.Join(dir, "net_dev")}, dir
}

var t0 = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func TestNewReader(t *testing.T) {
	r := NewReader()
	if r.Cgroup != "/sys/fs/cgroup" || r.NetDev != "/proc/net/dev" {
		t.Fatalf("NewReader() = %+v", r)
	}
}

func TestFirstReading(t *testing.T) {
	r, dir := reader(t)
	write(t, dir, first())
	got, ok := r.Read(t0)
	if !ok {
		t.Fatal("Read() = false")
	}
	want := wire.Sample{At: t0, Memory: 104857600, MemoryLimit: 0}
	if got != want {
		t.Fatalf("Read() = %+v, want %+v", got, want)
	}
}

func TestRates(t *testing.T) {
	r, dir := reader(t)
	write(t, dir, first())
	if _, ok := r.Read(t0); !ok {
		t.Fatal("first Read() = false")
	}
	write(t, dir, later())
	now := t0.Add(2 * time.Second)
	got, ok := r.Read(now)
	if !ok {
		t.Fatal("second Read() = false")
	}
	want := wire.Sample{
		At:          now,
		CPU:         0.5, // 1 000 000 µs in 2 s
		Memory:      209715200,
		MemoryLimit: 536870912,
		DiskRead:    4096,  // 8192 bytes over both devices in 2 s
		DiskWrite:   1024,  // 2048 bytes in 2 s
		NetRx:       11000, // 20000 + 2000 bytes in 2 s, without lo
		NetTx:       5500,  // 10000 + 1000
	}
	if got != want {
		t.Fatalf("Read() = %+v, want %+v", got, want)
	}

	// A third reading with nothing changed: the figures stay, the rates
	// are zero.
	now = now.Add(2 * time.Second)
	got, ok = r.Read(now)
	want = wire.Sample{At: now, Memory: 209715200, MemoryLimit: 536870912}
	if !ok || got != want {
		t.Fatalf("third Read() = %+v, %v, want %+v", got, ok, want)
	}
}

func TestCountersWentBackwards(t *testing.T) {
	r, dir := reader(t)
	write(t, dir, later())
	if _, ok := r.Read(t0); !ok {
		t.Fatal("first Read() = false")
	}
	// The container was made anew: every counter is lower than it was.
	write(t, dir, first())
	now := t0.Add(2 * time.Second)
	got, ok := r.Read(now)
	want := wire.Sample{At: now, Memory: 104857600}
	if !ok || got != want {
		t.Fatalf("Read() = %+v, %v, want %+v", got, ok, want)
	}
	// From there on the rates are right again.
	write(t, dir, later())
	got, ok = r.Read(now.Add(time.Second))
	if !ok || got.CPU != 1 || got.NetRx != 22000 {
		t.Fatalf("Read() after = %+v, %v, want 1 core and 22000 bytes received a second", got, ok)
	}
}

func TestSameTime(t *testing.T) {
	r, dir := reader(t)
	write(t, dir, first())
	if _, ok := r.Read(t0); !ok {
		t.Fatal("first Read() = false")
	}
	write(t, dir, later())
	got, ok := r.Read(t0)
	want := wire.Sample{At: t0, Memory: 209715200, MemoryLimit: 536870912}
	if !ok || got != want {
		t.Fatalf("Read() = %+v, %v, want %+v", got, ok, want)
	}
}

func TestIOStatMayBeEmpty(t *testing.T) {
	// A cgroup that has done no I/O has an io.stat with nothing in it, and
	// a device may have a line with no figures.
	for _, content := range []string{"", "8:0\n"} {
		r, dir := reader(t)
		f := first()
		f["io.stat"] = content
		write(t, dir, f)
		if _, ok := r.Read(t0); !ok {
			t.Errorf("io.stat %q: Read() = false", content)
		}
	}
}

func TestNoSample(t *testing.T) {
	garbageNetDev := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
  eth0: what is this
`
	cases := []struct {
		name    string
		file    string
		content string // "-": the file is missing
	}{
		{"cpu.stat missing", "cpu.stat", "-"},
		{"cpu.stat empty", "cpu.stat", ""},
		{"cpu.stat without usage_usec", "cpu.stat", "user_usec 3200000\nsystem_usec 1800000\n"},
		{"cpu.stat garbage", "cpu.stat", "usage_usec many\n"},
		{"memory.current missing", "memory.current", "-"},
		{"memory.current empty", "memory.current", ""},
		{"memory.current garbage", "memory.current", "a lot\n"},
		{"memory.max missing", "memory.max", "-"},
		{"memory.max empty", "memory.max", ""},
		{"memory.max garbage", "memory.max", "unlimited\n"},
		{"io.stat missing", "io.stat", "-"},
		{"io.stat garbage", "io.stat", "8:0 rbytes=many wbytes=2\n"},
		{"io.stat no pairs", "io.stat", "8:0 rbytes 1 wbytes 2\n"},
		{"net_dev missing", "net_dev", "-"},
		{"net_dev empty", "net_dev", ""},
		{"net_dev garbage", "net_dev", "this is not /proc/net/dev\nat all\nreally\n"},
		{"net_dev short line", "net_dev", garbageNetDev},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, dir := reader(t)
			f := first()
			f[c.file] = c.content
			write(t, dir, f)
			if got, ok := r.Read(t0); ok {
				t.Fatalf("Read() = %+v, true, want false", got)
			}
		})
	}
}

func TestFileThatComesBack(t *testing.T) {
	// A reading that failed leaves the reading before it as it was.
	r, dir := reader(t)
	write(t, dir, first())
	if _, ok := r.Read(t0); !ok {
		t.Fatal("first Read() = false")
	}
	write(t, dir, files{"cpu.stat": "-"})
	if _, ok := r.Read(t0.Add(time.Second)); ok {
		t.Fatal("Read() without cpu.stat = true")
	}
	write(t, dir, later())
	got, ok := r.Read(t0.Add(2 * time.Second))
	if !ok || got.CPU != 0.5 {
		t.Fatalf("Read() = %+v, %v, want 0.5 cores", got, ok)
	}
}
```

- [ ] **Step 2: Run the test to see it fail**

Run: `go test ./stats/`
Expected: FAIL (the build fails) — `undefined: Reader`, `undefined: NewReader`

- [ ] **Step 3: Write the implementation**

`stats/stats.go`:

```go
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
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go vet ./stats/ && go test ./stats/ -v`
Expected: PASS, with every case of `TestNoSample` listed

- [ ] **Step 5: Commit**

```bash
git add stats/
git commit -m "Stats: samples from cgroup v2 and /proc/net/dev"
```
