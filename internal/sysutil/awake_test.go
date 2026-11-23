package sysutil

import "testing"

// Holds add up: the computer is kept awake until the last one lets go,
// and letting go twice does no harm.
func TestKeepAwake(t *testing.T) {
	if Awake() {
		t.Fatal("awake before anything asked")
	}
	a := KeepAwake()
	b := KeepAwake()
	a()
	a()
	if !Awake() {
		t.Fatal("let go while a job still runs")
	}
	b()
	if Awake() {
		t.Fatal("still held after the last job")
	}
}
