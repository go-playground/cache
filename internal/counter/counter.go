// Package counter is a striped, cache-line padded counter for write-heavy concurrent statistics.
package counter

import (
	"runtime"
	"sync/atomic"

	"github.com/go-playground/cache/v2/internal/xruntime"
)

type stripe struct {
	v atomic.Uint64
	_ [xruntime.CacheLine - 8]byte
}

// Counter spreads increments over per-processor stripes so concurrent writers rarely share a cache line.
type Counter struct {
	stripes []stripe
	mask    uint32
}

// New creates a counter with nextPow2(GOMAXPROCS) stripes.
func New() *Counter {
	n := uint32(1)
	for n < uint32(runtime.GOMAXPROCS(0)) {
		n <<= 1
	}
	return &Counter{stripes: make([]stripe, n), mask: n - 1}
}

// Add adds delta.
func (c *Counter) Add(delta uint64) {
	c.stripes[uint32(xruntime.Pid())&c.mask].v.Add(delta)
}

// Value returns the sum of all stripes.
func (c *Counter) Value() uint64 {
	var sum uint64
	for i := range c.stripes {
		sum += c.stripes[i].v.Load()
	}
	return sum
}
