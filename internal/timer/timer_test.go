package timer

import (
	"testing"
	"time"
)

func TestTimer(t *testing.T) {
	for d, want := range map[time.Duration]string{45 * time.Second: "45 s", 12 * time.Minute: "12 min", 65 * time.Minute: "1 h 05 min", 51 * time.Hour: "2 days 3 h"} {
		if got := Elapsed(d); got != want {
			t.Errorf("Elapsed(%v) = %q", d, got)
		}
	}
	for d, want := range map[time.Duration]string{20 * time.Second: "less than a minute", 40 * time.Minute: "about 40 min", 150 * time.Minute: "about 2.5 hours", 30 * time.Hour: "about 30 hours", 100 * time.Hour: "about 4 days"} {
		if got := Left(d); got != want {
			t.Errorf("Left(%v) = %q", d, got)
		}
	}
	if got := Line(10*time.Minute, 25, 100); got != "10 min so far · about 30 min left" {
		t.Errorf("%q", got)
	}
	if got := Line(time.Second, 1, 100); got != "1 s so far" {
		t.Errorf("too early: %q", got)
	}
}
