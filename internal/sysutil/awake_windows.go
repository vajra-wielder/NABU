//go:build windows

package sysutil

import (
	"runtime"
	"syscall"
)

// inhibitSleep asks Windows to stay awake: SetThreadExecutionState holds
// for as long as the thread that called it says so, so one thread is kept
// for it until released.
func inhibitSleep() func() {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("SetThreadExecutionState")
	if proc.Find() != nil {
		return func() {}
	}
	const continuous, systemRequired = 0x80000000, 0x00000001
	stop := make(chan struct{})
	started := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		proc.Call(uintptr(continuous | systemRequired))
		close(started)
		<-stop
		proc.Call(uintptr(continuous))
	}()
	<-started
	return func() { close(stop) }
}
