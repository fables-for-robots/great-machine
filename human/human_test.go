package human

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

func TestTookAndAge(t *testing.T) {
	for _, tt := range []struct {
		d         time.Duration
		took, age string
	}{
		{-5 * time.Second, "0.0s", "0s"},
		{0, "0.0s", "0s"},
		{1349 * time.Millisecond, "1.3s", "1s"},
		{42 * time.Second, "42.0s", "42s"},
		{59999 * time.Millisecond, "59.9s", "59s"},
		{time.Minute, "1m00s", "1m00s"},
		{3*time.Minute + 12*time.Second, "3m12s", "3m12s"},
		{5*time.Hour + 3*time.Minute, "5h03m", "5h03m"},
		{52 * time.Hour, "2d04h", "2d04h"},
		{math.MaxInt64, "106751d23h", "106751d23h"},
		{math.MinInt64, "0.0s", "0s"},
	} {
		if got := Took(tt.d); got != tt.took {
			t.Errorf("Took(%d) = %q, want %q", tt.d, got, tt.took)
		}
		if got := Age(tt.d); got != tt.age {
			t.Errorf("Age(%d) = %q, want %q", tt.d, got, tt.age)
		}
	}
}

func TestBytes(t *testing.T) {
	for _, tt := range []struct {
		n    uint64
		want string
	}{
		{0, "0 B"}, {1023, "1023 B"}, {1536, "1.5 KiB"}, {126353408, "120.5 MiB"}, {3 << 30, "3.0 GiB"},
		{5 << 40, "5.0 TiB"}, {math.MaxUint64, "16777216.0 TiB"},
	} {
		if got := Bytes(tt.n); got != tt.want {
			t.Errorf("Bytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestRateCPUAndPercent(t *testing.T) {
	for _, tt := range []struct {
		v                  float64
		rate, cpu, percent string
	}{
		{0, "0 B/s", "0.0%", "0%"},
		{0.042, "0 B/s", "4.2%", "4%"},
		{0.75, "1 B/s", "75%", "75%"},
		{2.5, "3 B/s", "250%", "250%"},
		{22937.6, "22.4 KiB/s", "2293760%", "2293760%"},
		{-1, None, None, None}, // a counter that started again
		{math.NaN(), None, None, None},
		{math.Inf(1), None, None, None},
		{math.Inf(-1), None, None, None},
	} {
		if got := Rate(tt.v); got != tt.rate {
			t.Errorf("Rate(%v) = %q, want %q", tt.v, got, tt.rate)
		}
		if got := CPU(tt.v); got != tt.cpu {
			t.Errorf("CPU(%v) = %q, want %q", tt.v, got, tt.cpu)
		}
		if got := Percent(tt.v); got != tt.percent {
			t.Errorf("Percent(%v) = %q, want %q", tt.v, got, tt.percent)
		}
	}
	if got := Rate(1e300); got != "16777216.0 TiB/s" {
		t.Errorf("Rate(1e300) = %q", got)
	}
}

func TestAgoAndUntil(t *testing.T) {
	now := time.Date(2026, 10, 9, 22, 14, 41, 0, time.UTC)
	for _, tt := range []struct {
		t          time.Time
		ago, until string
	}{
		{time.Time{}, None, None},
		{now, "now", "now"},
		{now.Add(-999 * time.Millisecond), "now", "now"},
		{now.Add(-47 * time.Second), "47s ago", "now"},
		{now.Add(-200 * time.Second), "3m ago", "now"},
		{now.Add(-3 * time.Hour), "3h ago", "now"},
		{now.Add(-80 * time.Hour), "3d ago", "now"},
		{now.Add(6 * time.Second), "now", "in 6s"}, // a clock that is ahead
		{now.Add(90 * time.Minute), "now", "in 1h"},
	} {
		if got := Ago(tt.t, now); got != tt.ago {
			t.Errorf("Ago(%v) = %q, want %q", tt.t, got, tt.ago)
		}
		if got := Until(tt.t, now); got != tt.until {
			t.Errorf("Until(%v) = %q, want %q", tt.t, got, tt.until)
		}
	}
}

func TestClockAndDateTime(t *testing.T) {
	at := time.Date(2026, 10, 9, 22, 4, 1, 999, time.Local)
	if got := Clock(at); got != "22:04:01" {
		t.Errorf("Clock = %q", got)
	}
	if got := DateTime(at); got != "2026-10-09 22:04:01" {
		t.Errorf("DateTime = %q", got)
	}
	// On the clock of the reader, whatever zone the time came in.
	if got := Clock(at.UTC()); got != "22:04:01" {
		t.Errorf("Clock of the same time in UTC = %q", got)
	}
	if Clock(time.Time{}) != None || DateTime(time.Time{}) != None {
		t.Errorf("the zero time is %q and %q, want %q", Clock(time.Time{}), DateTime(time.Time{}), None)
	}
}

func TestShortIDAndStoreName(t *testing.T) {
	id := wire.ID(strings.Repeat("ab12", 16))
	if got := ShortID(id); got != "ab12ab12" {
		t.Errorf("ShortID = %q", got)
	}
	if ShortID("") != None || ShortID("abc") != "abc" {
		t.Errorf("ShortID of nothing = %q, of abc = %q", ShortID(""), ShortID("abc"))
	}
	for path, want := range map[string]string{
		"": None,
		"/nix/store/kzrvqcn6yd2p8bqlhbqm8wl5bh7f0r1s-web-1.4.20/bin/web": "web-1.4.20/bin/web",
		"/nix/store/kzrvqcn6yd2p8bqlhbqm8wl5bh7f0r1s-web-1.4.20":         "web-1.4.20",
		"/nix/store/short-web/bin/web":                                   "/nix/store/short-web/bin/web",
		"/nix/store/KZRVQCN6YD2P8BQLHBQM8WL5BH7F0R1S-web/bin/web":        "/nix/store/KZRVQCN6YD2P8BQLHBQM8WL5BH7F0R1S-web/bin/web",
		"/nix/store/kzrvqcn6yd2p8bqlhbqm8wl5bh7f0r1s-":                   "/nix/store/kzrvqcn6yd2p8bqlhbqm8wl5bh7f0r1s-",
		"/usr/bin/env": "/usr/bin/env",
	} {
		if got := StoreName(path); got != want {
			t.Errorf("StoreName(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestExitCountAndGeneration(t *testing.T) {
	for _, tt := range []struct {
		e    *wire.Exit
		want string
	}{
		{nil, None},
		{&wire.Exit{Status: 0}, "exit 0"},
		{&wire.Exit{Status: 3}, "exit 3"},
		{&wire.Exit{Status: -1, Signal: "SIGKILL"}, "killed by SIGKILL"},
		{&wire.Exit{Status: -1, Error: "exec format error"}, "did not start"},
		{&wire.Exit{Status: -1}, "exit -1"},
	} {
		if got := Exit(tt.e); got != tt.want {
			t.Errorf("Exit(%+v) = %q, want %q", tt.e, got, tt.want)
		}
	}
	if got := Count(1, "zathras", "zathrases") + ", " + Count(0, "zathras", "zathrases") + ", " + Count(3, "zathras", "zathrases"); got != "1 zathras, 0 zathrases, 3 zathrases" {
		t.Errorf("Count = %q", got)
	}
	if Generation(0) != None || Generation(20) != "20" {
		t.Errorf("Generation(0) = %q, Generation(20) = %q", Generation(0), Generation(20))
	}
}
