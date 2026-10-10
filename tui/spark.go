package tui

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

const (
	// window is how much of the past the sparklines show: what draal
	// keeps of a zathras.
	window = 15 * time.Minute
	// sparkWidest is how many cells a sparkline has at most: with more, a
	// cell would be less than the time between two samples and a pause
	// of the sampling, and every other cell a gap that is none.
	sparkWidest = 150
	// sampleAhead is how far ahead of the terminal UI's clock the time
	// of a sample may be.
	sampleAhead = time.Hour
	// byteMost and coreMost are the largest tops a scale has: figures
	// beyond them are no figures of a container, and are drawn as the
	// top.
	byteMost = 1 << 60
	coreMost = 1e12
)

// mergeSamples returns the samples a zathras view keeps: those it has
// and those that came, in the order of time, one for each time (a later
// one of the same time takes the place of the earlier), and none older
// than the window before the newest. A sample without a time is left
// out.
//
// The times are the zathras' own, and its clock may differ from the
// one of the terminal UI, which now is. A zathras whose clock is ahead
// by up to sampleAhead has its figures all the same: the window ends at
// its newest sample. A sample further ahead than that is left out: one
// such sample, of a clock that jumped or of a zathras that is wrong,
// would be the newest for as long as it is ahead, and the window before
// it would be empty.
func mergeSamples(have, more []wire.Sample, now time.Time) []wire.Sample {
	latest := now.Add(sampleAhead)
	all := make([]wire.Sample, 0, len(have)+len(more))
	for _, list := range [][]wire.Sample{have, more} {
		for _, s := range list {
			if !s.At.IsZero() && !s.At.After(latest) {
				all = append(all, s)
			}
		}
	}
	if len(all) == 0 {
		return nil
	}
	slices.SortStableFunc(all, func(a, b wire.Sample) int { return a.At.Compare(b.At) })
	out := all[:0]
	for _, s := range all {
		if n := len(out); n > 0 && out[n-1].At.Equal(s.At) {
			out[n-1] = s
			continue
		}
		out = append(out, s)
	}
	from := out[len(out)-1].At.Add(-window)
	first, _ := slices.BinarySearchFunc(out, from, func(s wire.Sample, t time.Time) int { return cmp.Compare(s.At.Compare(t), 0) })
	return slices.Clone(out[first:])
}

// figure says whether v is a figure a sparkline draws: a number that is
// not below zero. A rate below zero is what a counter that started
// again gives (the service was restarted): it is no figure, and the
// line has a gap there, never a block below the line.
func figure(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

// largest returns the largest figure of the samples; 0 when there is
// none.
func largest(samples []wire.Sample, pick func(wire.Sample) float64) float64 {
	top := 0.0
	for _, s := range samples {
		if v := pick(s); figure(v) && v > top {
			top = v
		}
	}
	return top
}

// byteTop returns the top of a sparkline of bytes, or of bytes a second,
// whose largest figure is most: the power of two at or above it, so
// that the scale is said in whole binary units, and 1 KiB at least.
func byteTop(most float64) float64 {
	if !figure(most) || most <= 1024 {
		return 1024
	}
	return min(math.Exp2(math.Ceil(math.Log2(most))), byteMost)
}

// coreTop returns the top of a sparkline of cores whose largest figure
// is most: 1, 2 or 5 times a power of ten at or above it, and a tenth
// of a core at least.
func coreTop(most float64) float64 {
	if !figure(most) || most <= 0.1 {
		return 0.1
	}
	if most >= coreMost {
		return coreMost
	}
	power := math.Pow(10, math.Floor(math.Log10(most)))
	for _, k := range []float64{1, 2, 5} {
		if k*power >= most*(1-1e-12) {
			// 0.1 + 0.2 is not 0.3: the top is rounded to what it stands for.
			return math.Round(k*power*1e9) / 1e9
		}
	}
	return math.Round(10*power*1e9) / 1e9
}

// blocks are what a sparkline is drawn with, from the lowest. The lowest
// is a figure of zero: a cell without a figure is empty.
var blocks = []rune("▁▂▃▄▅▆▇█")

// spark draws one figure of the samples as a line of width cells: the
// window that ends at the newest sample, each cell a part of it, with
// the block for the largest figure of that part on a scale from 0 to
// top. A cell in whose part there is no figure is a space: a gap is a
// gap. What is above top is drawn as the top.
func spark(samples []wire.Sample, pick func(wire.Sample) float64, width int, top float64) string {
	if width <= 0 {
		return ""
	}
	cells := make([]float64, width)
	for i := range cells {
		cells[i] = -1
	}
	if len(samples) > 0 {
		to := samples[len(samples)-1].At
		for _, s := range samples {
			v, back := pick(s), to.Sub(s.At)
			if !figure(v) || back < 0 || back >= window {
				continue
			}
			i := width - 1 - int(int64(back)*int64(width)/int64(window))
			cells[i] = max(cells[i], v)
		}
	}
	var b strings.Builder
	for _, v := range cells {
		switch {
		case v < 0:
			b.WriteByte(' ')
		case !figure(top) || top <= 0 || v <= 0:
			b.WriteRune(blocks[0])
		default:
			level := int(math.Ceil(v/top*float64(len(blocks)))) - 1
			b.WriteRune(blocks[min(max(level, 0), len(blocks)-1)])
		}
	}
	return b.String()
}
