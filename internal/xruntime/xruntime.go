// Package xruntime exposes the current processor (P) id for per-P striping of contended structures.
package xruntime

import _ "unsafe" // for go:linkname

// runtime.procPin and runtime.procUnpin are deliberately exported by the runtime for linkname use ("do not remove
// or change the type signature"), see runtime/proc.go.
//
//go:linkname procPin runtime.procPin
func procPin() int

//go:linkname procUnpin runtime.procUnpin
func procUnpin()

// Pid returns the id of the processor the calling goroutine is running on, in [0, GOMAXPROCS).
//
// The goroutine may migrate immediately after, so the id is only an affinity hint: callers must still synchronise.
// Striping by Pid keeps each core writing mostly its own cache lines, which random striping cannot.
func Pid() int {
	p := procPin()
	procUnpin()
	return p
}

// CacheLine is the padding used to keep independently written fields on separate cache lines.
//
// Measured on Apple silicon (128-byte lines): 64 performs the same as 128 on every workload, while no padding costs
// 10-18% on concurrent writes. Padding only applies to per-cache structures, never per entry.
const CacheLine = 64
