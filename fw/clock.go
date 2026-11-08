//go:build tinygo

package relay

import (
	"runtime/interrupt"
	_ "unsafe"
)

//go:linkname runtimeNanotime runtime.nanotime
func runtimeNanotime() int64

func monotonicNanoseconds() int64 {
	state := interrupt.Disable()
	now := runtimeNanotime()
	interrupt.Restore(state)
	return now
}
