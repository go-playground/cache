package table

import (
	"github.com/go-playground/cache/v2/internal/xruntime"
	"math/bits"
	"runtime"
	"sync/atomic"

	"github.com/go-playground/cache/v2/internal/node"
)

const (
	minBuckets     = 4
	maxHint        = 1 << 30
	loadNum        = 3
	loadDen        = 4
	stripesPerProc = 4
)

// stripe is one cache-line padded slice of the size counter.
type stripe struct {
	n atomic.Int64
	_ [xruntime.CacheLine - 8]byte
}

// state is one generation of the table. It is resized at most once: frozen is set by its single resizer and resized
// is closed once the successor has been published.
type state[K comparable, V any] struct {
	buckets    []bucket[K, V]
	stripes    []stripe
	mask       uint64
	stripeMask uint64
	resizeAt   int64
	frozen     atomic.Bool
	resized    chan struct{}
}

func newState[K comparable, V any](n int) *state[K, V] {
	ns := min(n, nextPow2(runtime.GOMAXPROCS(0)*stripesPerProc))
	return &state[K, V]{
		buckets:    make([]bucket[K, V], n),
		stripes:    make([]stripe, ns),
		mask:       uint64(n - 1),
		stripeMask: uint64(ns - 1),
		resizeAt:   int64(n * slotsPerBucket * loadNum / loadDen),
		resized:    make(chan struct{}),
	}
}

// bucketsFor returns the smallest power of two root bucket count whose resize threshold is at least sizeHint.
func bucketsFor(sizeHint int) int {
	sizeHint = min(max(sizeHint, 0), maxHint)
	den := slotsPerBucket * loadNum
	need := (sizeHint*loadDen + den - 1) / den
	return nextPow2(max(need, minBuckets))
}

func nextPow2(n int) int {
	return 1 << bits.Len(uint(n-1))
}

func (s *state[K, V]) root(hash uint64) *bucket[K, V] {
	return &s.buckets[hash&s.mask]
}

// stripeFor returns the counter stripe of hash. stripeMask <= mask, so it only depends on the root bucket.
func (s *state[K, V]) stripeFor(hash uint64) *stripe {
	return &s.stripes[hash&s.stripeMask]
}

// size sums the stripes. Individual stripes may be negative; the sum is exact when quiescent.
func (s *state[K, V]) size() int64 {
	var sum int64
	for i := range s.stripes {
		sum += s.stripes[i].n.Load()
	}
	return sum
}

// store maps n.Key to n in the chain of root, which must be locked. It reports whether an overflow bucket was added.
func (s *state[K, V]) store(root *bucket[K, V], n *node.Node[K, V]) (old *node.Node[K, V], overflowed bool) {
	tag := tagOf(n.Hash)
	var free, last *bucket[K, V]
	freeSlot := 0
	for b := root; b != nil; b = b.next.Load() {
		meta := b.meta.Load()
		if i, e := b.find(meta, tag, n.Hash, n.Key); e != nil {
			b.slots[i].Store(n)
			return e, false
		}
		if m := matchEmpty(meta); free == nil && m != 0 {
			free, freeSlot = b, slotOf(m)
		}
		last = b
	}
	s.stripeFor(n.Hash).n.Add(1)
	if free != nil {
		free.publish(freeSlot, tag, n)
		return nil, false
	}
	nb := &bucket[K, V]{}
	nb.publish(0, tag, n)
	last.next.Store(nb)
	return nil, true
}

// delete removes key from the chain of root, which must be locked.
func (s *state[K, V]) delete(root *bucket[K, V], hash uint64, key K) *node.Node[K, V] {
	tag := tagOf(hash)
	for b := root; b != nil; b = b.next.Load() {
		if i, e := b.find(b.meta.Load(), tag, hash, key); e != nil {
			b.clear(i)
			s.stripeFor(hash).n.Add(-1)
			return e
		}
	}
	return nil
}

// deleteNode removes exactly n from the chain of root, which must be locked.
func (s *state[K, V]) deleteNode(root *bucket[K, V], n *node.Node[K, V]) bool {
	tag := tagOf(n.Hash)
	for b := root; b != nil; b = b.next.Load() {
		if i := b.findNode(b.meta.Load(), tag, n); i >= 0 {
			b.clear(i)
			s.stripeFor(n.Hash).n.Add(-1)
			return true
		}
	}
	return false
}

// copyChain copies the nodes of one locked chain of the previous, half sized, state into s, which is not yet
// reachable by anyone else. Every node of old root i lands in new root i or i+len(old) and no other old chain feeds
// those roots, so appending through one cursor per destination keeps the copy linear even for very long chains.
func (s *state[K, V]) copyChain(root *bucket[K, V]) {
	var lo, hi cursor[K, V]
	half := uint64(len(s.buckets) >> 1)
	for b := root; b != nil; b = b.next.Load() {
		for i := range b.slots {
			n := b.slots[i].Load()
			if n == nil {
				continue
			}
			c := &lo
			if n.Hash&half != 0 {
				c = &hi
			}
			c.append(s.root(n.Hash), n)
			s.stripeFor(n.Hash).n.Add(1)
		}
	}
}
