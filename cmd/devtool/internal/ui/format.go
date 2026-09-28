package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Duration formats d the way a person reads elapsed time: 850ms, 12s,
// 3m05s, 1h02m.
func Duration(d time.Duration) string {
	switch {
	case d < time.Second:
		return d.Round(time.Millisecond).String()
	case d < time.Minute:
		return d.Round(time.Second).String()
	case d < time.Hour:
		d = d.Round(time.Second)
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		d = d.Round(time.Minute)
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// Count formats n with a metric suffix: 950, 12.3k, 4.56M.
func Count(n float64) string {
	switch {
	case n >= 1e9:
		return trim(n/1e9) + "G"
	case n >= 1e6:
		return trim(n/1e6) + "M"
	case n >= 1e3:
		return trim(n/1e3) + "k"
	default:
		return trim(n)
	}
}

// Remaining estimates the time left once progress, from 0 to 1, took
// elapsed, assuming the rest goes at the same pace. It's zero before any
// progress and once there's nothing left.
func Remaining(elapsed time.Duration, progress float64) time.Duration {
	if progress <= 0 || progress >= 1 {
		return 0
	}
	return time.Duration(float64(elapsed) / progress * (1 - progress))
}

// trim formats v with three significant digits and no trailing zeros.
func trim(v float64) string {
	prec := 2
	switch {
	case v >= 100:
		prec = 0
	case v >= 10:
		prec = 1
	}
	s := strconv.FormatFloat(v, 'f', prec, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}
