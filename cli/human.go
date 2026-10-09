package cli

import (
	"fmt"
	"time"
)

// took writes how long a step took: tenths of a second up to a minute,
// which is where they matter, and whole seconds or minutes beyond.
func took(d time.Duration) string {
	switch {
	case d < 0:
		return "0.0s"
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Truncate(100*time.Millisecond).Seconds())
	}
	return age(d)
}

// age writes for how long something has been so, in two units at most:
// 42s, 3m12s, 5h03m, 2d04h.
func age(d time.Duration) string {
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

// byteSize writes a number of bytes with a binary unit: 512 B, 1.5 KiB,
// 120.5 MiB.
func byteSize(n uint64) string {
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
