package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/vajra-wielder/nabu/internal/timer"
)

// progress prints one line that updates itself: what is being done, how
// far, how long so far and how long left.
type progress struct {
	label string
	start time.Time
	last  time.Time
	width int
}

func newProgress(label string) *progress {
	return &progress{label: label, start: time.Now()}
}

func (p *progress) update(done, total int64) {
	if time.Since(p.last) < time.Second {
		return
	}
	p.last = time.Now()
	pct := 0.0
	if total > 0 {
		pct = 100 * float64(done) / float64(total)
	}
	line := fmt.Sprintf("  %s: %.1f%% · %s", p.label, pct, timer.Line(time.Since(p.start), done, total))
	pad := ""
	if n := p.width - len([]rune(line)); n > 0 {
		pad = strings.Repeat(" ", n)
	}
	p.width = len([]rune(line))
	fmt.Print("\r" + line + pad)
}

// done ends the line, saying how long it took.
func (p *progress) done() string {
	if p.width > 0 {
		fmt.Print("\r" + strings.Repeat(" ", p.width) + "\r")
	}
	return timer.Elapsed(time.Since(p.start))
}
