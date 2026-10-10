package tui

import (
	"math"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// sampleStart is where the samples of the tests begin.
var sampleStart = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// cpuAt returns a sample of so many cores, so many seconds after
// sampleStart.
func cpuAt(seconds int, cores float64) wire.Sample {
	return wire.Sample{At: sampleStart.Add(time.Duration(seconds) * time.Second), CPU: cores}
}

func cpuOf(s wire.Sample) float64 { return s.CPU }

func TestMergeSamples(t *testing.T) {
	seconds := func(samples []wire.Sample) []int {
		var out []int
		for _, s := range samples {
			out = append(out, int(s.At.Sub(sampleStart)/time.Second))
		}
		return out
	}
	equal := func(a, b []int) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	if got := mergeSamples(nil, nil, sampleStart.Add(time.Hour)); got != nil {
		t.Errorf("nothing and nothing is %v", got)
	}
	// In the order of time, whatever order they came in.
	got := mergeSamples([]wire.Sample{cpuAt(4, 1), cpuAt(0, 1)}, []wire.Sample{cpuAt(2, 1), cpuAt(6, 1)}, sampleStart.Add(time.Hour))
	if !equal(seconds(got), []int{0, 2, 4, 6}) {
		t.Errorf("merged: %v", seconds(got))
	}
	// The sample of the fleet is the last one of the history again: one
	// for each time, and the later one stands.
	got = mergeSamples([]wire.Sample{cpuAt(0, 1), cpuAt(2, 1)}, []wire.Sample{cpuAt(2, 7), cpuAt(4, 1)}, sampleStart.Add(time.Hour))
	if !equal(seconds(got), []int{0, 2, 4}) || got[1].CPU != 7 {
		t.Errorf("merged with one of the same time: %v, the second with %v cores", seconds(got), got[1].CPU)
	}
	// None older than the window before the newest; one at its edge stays.
	got = mergeSamples([]wire.Sample{cpuAt(0, 1), cpuAt(2, 1), cpuAt(4, 1)}, []wire.Sample{cpuAt(902, 1)}, sampleStart.Add(time.Hour))
	if !equal(seconds(got), []int{2, 4, 902}) {
		t.Errorf("merged over the window: %v", seconds(got))
	}
	// A sample without a time is none.
	got = mergeSamples([]wire.Sample{{CPU: 3}}, []wire.Sample{cpuAt(2, 1), {}}, sampleStart.Add(time.Hour))
	if !equal(seconds(got), []int{2}) {
		t.Errorf("merged with samples without a time: %v", seconds(got))
	}
	// What was given is not changed.
	have := []wire.Sample{cpuAt(0, 1), cpuAt(2, 1)}
	mergeSamples(have, []wire.Sample{cpuAt(2000, 1)}, sampleStart.Add(time.Hour))
	if len(have) != 2 || have[0].CPU != 1 {
		t.Errorf("the samples that were given are now %v", have)
	}
}

// The times of samples are the zathras' own. A clock that is ahead by
// less than sampleAhead has its window; a sample further ahead is none,
// and cannot empty the window of the others.
func TestMergeSamplesOfAClockThatIsAhead(t *testing.T) {
	now := sampleStart.Add(20 * time.Second)
	var have []wire.Sample
	for i := range 10 {
		have = append(have, cpuAt(2*i, 1))
	}
	got := mergeSamples(have, []wire.Sample{{At: now.Add(2 * time.Hour), CPU: 9}, {At: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), CPU: 9}}, now)
	if len(got) != 10 || got[9].CPU != 1 {
		t.Errorf("with a sample two hours ahead and one of the year 9999, %d samples are kept, the newest with %v cores; want the ten there were", len(got), got[len(got)-1].CPU)
	}
	// A zathras whose clock is half an hour ahead: its samples are its
	// figures, windowed by its own newest.
	var ahead []wire.Sample
	for i := range 600 {
		ahead = append(ahead, wire.Sample{At: now.Add(30*time.Minute + time.Duration(2*i)*time.Second - 20*time.Minute), CPU: 2})
	}
	got = mergeSamples(nil, ahead, now)
	if len(got) != 451 || !got[len(got)-1].At.Equal(ahead[599].At) {
		t.Errorf("of a clock half an hour ahead %d samples are kept, want the 451 of its last fifteen minutes", len(got))
	}
}

func TestSpark(t *testing.T) {
	// One sample every 2 seconds for 15 minutes, drawn in 15 cells: a
	// minute each.
	var steady, rising, restarted, paused []wire.Sample
	for i := range 450 {
		steady = append(steady, cpuAt(2*i, 0.5))
		rising = append(rising, cpuAt(2*i, float64(i)/450))
		// The counter of the network starts again in the eighth minute:
		// a rate below zero, which is no figure.
		rate := 100.0
		if i/30 == 7 {
			rate = -4e9
		}
		restarted = append(restarted, wire.Sample{At: sampleStart.Add(time.Duration(2*i) * time.Second), NetRx: rate})
		// Nothing was sampled from the fourth to the sixth minute.
		if i/30 < 3 || i/30 > 5 {
			paused = append(paused, cpuAt(2*i, 0.5))
		}
	}
	netOf := func(s wire.Sample) float64 { return s.NetRx }
	for _, tt := range []struct {
		name    string
		samples []wire.Sample
		pick    func(wire.Sample) float64
		width   int
		top     float64
		want    string
	}{
		{"no samples", nil, cpuOf, 8, 1, "        "},
		{"no width", steady, cpuOf, 0, 1, ""},
		{"a steady half", steady, cpuOf, 15, 1, "▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄"},
		{"rising", rising, cpuOf, 15, 1, "▁▂▂▃▃▄▄▅▅▆▆▇▇██"},
		{"above the top is the top", steady, cpuOf, 5, 0.25, "█████"},
		{"zero is the lowest block, and no gap", []wire.Sample{cpuAt(0, 0), cpuAt(450, 0), cpuAt(898, 0)}, cpuOf, 3, 1, "▁▁▁"},
		{"a top of zero", steady, cpuOf, 3, 0, "▁▁▁"},
		{"a top that is no number", steady, cpuOf, 3, math.NaN(), "▁▁▁"},
		{"a gap is a gap", paused, cpuOf, 15, 1, "▄▄▄   ▄▄▄▄▄▄▄▄▄"},
		{"a counter that started again is a gap, never below the line", restarted, netOf, 15, 100, "███████ ███████"},
		{"a zathras that has just come: its samples are at the right", steady[:60], cpuOf, 15, 1, "             ▄▄"},
		{"one sample", steady[:1], cpuOf, 4, 1, "   ▄"},
	} {
		got := spark(tt.samples, tt.pick, tt.width, tt.top)
		if got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
		if width(got) != tt.width {
			t.Errorf("%s: %d cells wide, want %d", tt.name, width(got), tt.width)
		}
	}
	// Whatever the figures are, a sparkline is blocks and spaces.
	odd := []wire.Sample{cpuAt(0, math.NaN()), cpuAt(2, math.Inf(1)), cpuAt(4, math.Inf(-1)), cpuAt(6, -0.0), cpuAt(8, 1e308), cpuAt(10, -1)}
	for _, r := range spark(odd, cpuOf, sparkWidest, 1) {
		if r != ' ' && (r < '▁' || r > '█') {
			t.Errorf("a sparkline of figures that are none has %q in it", r)
		}
	}
}

func TestTopsOfTheScales(t *testing.T) {
	for _, tt := range []struct{ most, bytes, cores float64 }{
		{0, 1024, 0.1},
		{-5, 1024, 0.1},
		{math.NaN(), 1024, 0.1},
		{0.05, 1024, 0.1},
		{0.1, 1024, 0.1},
		{0.12, 1024, 0.2},
		{0.3, 1024, 0.5},
		{0.75, 1024, 1},
		{1, 1024, 1},
		{2.5, 1024, 5},
		{1025, 2048, 2000},
		{300 << 20, 512 << 20, 500000000},
		{512 << 20, 512 << 20, 1000000000},
		// Figures that are none of a container: the top is the largest
		// there is, and a number.
		{1e308, byteMost, coreMost},
		{math.MaxFloat64, byteMost, coreMost},
		{math.Inf(1), 1024, 0.1},
	} {
		if got := byteTop(tt.most); got != tt.bytes {
			t.Errorf("byteTop(%v) = %v, want %v", tt.most, got, tt.bytes)
		}
		if got := coreTop(tt.most); got != tt.cores {
			t.Errorf("coreTop(%v) = %v, want %v", tt.most, got, tt.cores)
		}
	}
	samples := []wire.Sample{cpuAt(0, 0.2), cpuAt(2, math.NaN()), cpuAt(4, 0.7), cpuAt(6, -3)}
	if got := largest(samples, cpuOf); got != 0.7 {
		t.Errorf("largest = %v, want 0.7", got)
	}
	if got := largest(nil, cpuOf); got != 0 {
		t.Errorf("largest of nothing = %v", got)
	}
}
