// Package human writes numbers, times and names as a person reads them:
// what the command line and the terminal UI print of a duration, a size,
// a rate, a time, an endpoint ID, a store path and an exit.
//
// Every function is total: whatever it is given, it returns words for a
// person. What is missing (the zero time, a figure that is no number) is
// written as None, and a length of time is never below zero.
package human

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// None stands where a value is missing.
const None = "-"

// Took writes how long a step took: tenths of a second up to a minute,
// which is where they matter, and whole seconds or minutes beyond.
func Took(d time.Duration) string {
	switch {
	case d < 0:
		return "0.0s"
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Truncate(100*time.Millisecond).Seconds())
	}
	return Age(d)
}

// Age writes for how long something has been so, in two units at most:
// 42s, 3m12s, 5h03m, 2d04h. A time below zero, which two clocks that
// differ give, is 0s.
func Age(d time.Duration) string {
	s := int64(max(d, 0) / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	case s < 24*3600:
		return fmt.Sprintf("%dh%02dm", s/3600, s/60%60)
	}
	return fmt.Sprintf("%dd%02dh", s/(24*3600), s/3600%24)
}

// Bytes writes a number of bytes with a binary unit: 512 B, 1.5 KiB,
// 120.5 MiB.
func Bytes(n uint64) string {
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n) / 1024
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// figure says whether v is a figure there is: a number, and not below
// zero. A rate below zero is what a counter that started again gives.
func figure(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

// Rate writes bytes per second: 22.4 KiB/s. A rate that is no figure is
// None.
func Rate(perSecond float64) string {
	if !figure(perSecond) {
		return None
	}
	if perSecond >= math.MaxUint64 {
		return Bytes(math.MaxUint64) + "/s"
	}
	return Bytes(uint64(perSecond+0.5)) + "/s"
}

// CPU writes cores as a percentage of one core: 0.75 is 75%, 2.5 is
// 250%, and less than a tenth of a core has a decimal: 4.2%.
func CPU(cores float64) string {
	if !figure(cores) {
		return None
	}
	p := cores * 100
	if p >= 10 {
		return strconv.FormatFloat(p, 'f', 0, 64) + "%"
	}
	return strconv.FormatFloat(p, 'f', 1, 64) + "%"
}

// Percent writes a share, 0 to 1, as a whole percentage.
func Percent(share float64) string {
	if !figure(share) {
		return None
	}
	return strconv.FormatFloat(share*100, 'f', 0, 64) + "%"
}

// coarse writes a length of time in its largest unit alone: 47s, 3m, 2h,
// 3d.
func coarse(d time.Duration) string {
	s := int64(d / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm", s/60)
	case s < 24*3600:
		return fmt.Sprintf("%dh", s/3600)
	}
	return fmt.Sprintf("%dd", s/(24*3600))
}

// Ago writes how long ago t was by the clock of now: "now", "47s ago",
// "3m ago". The zero time is None, and a time in the future, which a
// clock that is ahead gives, is "now".
func Ago(t, now time.Time) string {
	if t.IsZero() {
		return None
	}
	d := now.Sub(t)
	if d < time.Second {
		return "now"
	}
	return coarse(d) + " ago"
}

// Until writes how long it is until t: "in 6s", or "now" when it has
// come. The zero time is None.
func Until(t, now time.Time) string {
	if t.IsZero() {
		return None
	}
	d := t.Sub(now)
	if d < time.Second {
		return "now"
	}
	return "in " + coarse(d)
}

// Clock writes the time of day of t on the clock of the reader:
// 22:14:41. The zero time is None.
func Clock(t time.Time) string {
	if t.IsZero() {
		return None
	}
	return t.Local().Format("15:04:05")
}

// DateTime writes the day and the time of t on the clock of the reader:
// 2026-10-09 22:14:41. The zero time is None.
func DateTime(t time.Time) string {
	if t.IsZero() {
		return None
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// ShortID writes the first 8 characters of an endpoint ID, which is what
// `great-machine status` prints of one. An empty ID is None.
func ShortID(id wire.ID) string {
	switch {
	case id == "":
		return None
	case len(id) > 8:
		return string(id[:8])
	}
	return string(id)
}

// StoreName writes a path of the Nix store without the store and the
// hash: /nix/store/kzrv…-web-1.4.20/bin/web is web-1.4.20/bin/web. Any
// other path is itself, and an empty one is None.
func StoreName(path string) string {
	if path == "" {
		return None
	}
	const store, hash = "/nix/store/", 32
	rest, ok := strings.CutPrefix(path, store)
	if !ok || len(rest) < hash+2 || rest[hash] != '-' {
		return path
	}
	for i := range hash {
		if c := rest[i]; (c < '0' || c > '9') && (c < 'a' || c > 'z') {
			return path
		}
	}
	return rest[hash+1:]
}

// Exit writes how a service ended: "exit 3", "killed by SIGKILL", "did
// not start". No exit is None.
func Exit(e *wire.Exit) string {
	switch {
	case e == nil:
		return None
	case e.Error != "":
		return "did not start"
	case e.Signal != "":
		return "killed by " + e.Signal
	}
	return "exit " + strconv.Itoa(e.Status)
}

// Count writes a number with its noun: "1 zathras", "3 zathrases".
func Count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// Generation writes the number of a generation, or None for none (0).
func Generation(n uint64) string {
	if n == 0 {
		return None
	}
	return strconv.FormatUint(n, 10)
}
