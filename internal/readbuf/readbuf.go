// Package readbuf is a striped, lossy, multi-producer single-consumer buffer of read events.
//
// Producers never block: when a stripe is full or contended the event is dropped, which is acceptable because read
// events only inform eviction heuristics. Each processor (P) writes its own stripe, so cores rarely share cache lines.
package readbuf

import (
	"runtime"
	"sync/atomic"

	"github.com/go-playground/cache/v2/internal/xruntime"
)

const ringSize = 16

// Result is the outcome of Add.
type Result uint8

const (
	// Added means the event was recorded.
	Added Result = iota
	// Full means the stripe is full and the caller should schedule a drain.
	Full
	// Dropped means the event was discarded due to contention.
	Dropped
)

type ring[T any] struct {
	head atomic.Uint64
	_    [xruntime.CacheLine - 8]byte
	tail atomic.Uint64
	_    [xruntime.CacheLine - 8]byte
	buf  [ringSize]atomic.Pointer[T]
}

// Buffer is a striped lossy buffer of *T.
type Buffer[T any] struct {
	stripes []ring[T]
	mask    uint32
}

// New creates a buffer with one stripe per processor, rounded up to a power of two.
func New[T any]() *Buffer[T] {
	n := nextPow2(uint32(runtime.GOMAXPROCS(0)))
	return &Buffer[T]{stripes: make([]ring[T], n), mask: n - 1}
}

// Add records x into the calling processor's stripe.
func (b *Buffer[T]) Add(x *T) Result {
	r := &b.stripes[uint32(xruntime.Pid())&b.mask]
	t := r.tail.Load()
	if t-r.head.Load() >= ringSize {
		return Full
	}
	if !r.tail.CompareAndSwap(t, t+1) {
		return Dropped
	}
	r.buf[t&(ringSize-1)].Store(x)
	return Added
}

// Drain calls fn for every buffered event. Must only be called by one consumer at a time.
func (b *Buffer[T]) Drain(fn func(*T)) {
	for i := range b.stripes {
		b.stripes[i].drain(fn)
	}
}

func (r *ring[T]) drain(fn func(*T)) {
	h, t := r.head.Load(), r.tail.Load()
	for ; h != t; h++ {
		slot := &r.buf[h&(ringSize-1)]
		x := slot.Load()
		if x == nil {
			// A producer claimed the slot but has not yet published; resume from here next drain.
			break
		}
		slot.Store(nil)
		fn(x)
	}
	r.head.Store(h)
}

func nextPow2(v uint32) uint32 {
	if v <= 1 {
		return 1
	}
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	return v + 1
}
