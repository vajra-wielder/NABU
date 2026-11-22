// Package timer writes how long a job has taken and how long it has
// left, the same way everywhere NABU shows progress.
package timer

import (
	"fmt"
	"time"
)

// Elapsed is time spent: "45 s", "12 min", "1 h 05 min", "2 days 3 h".
func Elapsed(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h %02d min", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%d days %d h", int(d.Hours())/24, int(d.Hours())%24)
}

// Left is time still to go, rounded as an estimate should be.
func Left(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < 90*time.Minute:
		return fmt.Sprintf("about %d min", int(d.Minutes()+0.5))
	case d < 48*time.Hour:
		h := d.Hours()
		if h < 10 {
			return fmt.Sprintf("about %.1f hours", h)
		}
		return fmt.Sprintf("about %d hours", int(h+0.5))
	}
	return fmt.Sprintf("about %d days", int(d.Hours()/24+0.5))
}

// Remaining guesses the time left from the share done so far (zero
// when it is too early to tell).
func Remaining(elapsed time.Duration, done, total int64) time.Duration {
	if done <= 0 || total <= done || elapsed < 3*time.Second {
		return 0
	}
	return time.Duration(float64(elapsed) * float64(total-done) / float64(done))
}

// Line is "12 min so far · about 40 min left" (or only the first part
// while it is too early to tell).
func Line(elapsed time.Duration, done, total int64) string {
	s := Elapsed(elapsed) + " so far"
	if left := Remaining(elapsed, done, total); left > 0 {
		s += " · " + Left(left) + " left"
	}
	return s
}
