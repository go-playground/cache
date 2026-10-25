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

// Width is how many counters a Set holds per stripe: as many 8-byte counters as fit in one cache line.
const Width = xruntime.CacheLine / 8

type setStripe struct {
	v [Width]atomic.Uint64
}

// Set is up to Width related counters sharing one cache line per processor, so a cache recording several statistics
// costs one line per processor rather than one per statistic.
type Set struct {
	stripes []setStripe
	mask    uint32
}

// NewSet creates a Set with nextPow2(GOMAXPROCS) stripes.
func NewSet() *Set {
	n := uint32(1)
	for n < uint32(runtime.GOMAXPROCS(0)) {
		n <<= 1
	}
	return &Set{stripes: make([]setStripe, n), mask: n - 1}
}

// Add adds delta to counter i.
func (s *Set) Add(i int, delta uint64) {
	s.stripes[uint32(xruntime.Pid())&s.mask].v[i].Add(delta)
}

// Value returns the sum of counter i over all stripes.
func (s *Set) Value(i int) uint64 {
	var sum uint64
	for j := range s.stripes {
		sum += s.stripes[j].v[i].Load()
	}
	return sum
}
