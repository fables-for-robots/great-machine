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
