package sysutil

import "sync"

// Long jobs (downloading and slimming a dump, building indexes, keeping
// pictures) take hours; a computer that goes to sleep meanwhile stops
// them (the Windows laptop test lost a night that way). While any is
// running, NABU asks the system not to sleep when left alone. Closing a
// laptop's lid still puts it to sleep, as the reader would expect.

var awake struct {
	mu      sync.Mutex
	holds   int
	release func()
}

// KeepAwake keeps the computer from sleeping on its own until the
// returned function is called (holds add up; the last one lets go).
func KeepAwake() (done func()) {
	awake.mu.Lock()
	defer awake.mu.Unlock()
	awake.holds++
	if awake.holds == 1 {
		awake.release = inhibitSleep()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			awake.mu.Lock()
			defer awake.mu.Unlock()
			awake.holds--
			if awake.holds == 0 && awake.release != nil {
				awake.release()
				awake.release = nil
			}
		})
	}
}

// Awake reports whether NABU is keeping the computer awake now.
func Awake() bool {
	awake.mu.Lock()
	defer awake.mu.Unlock()
	return awake.holds > 0
}
