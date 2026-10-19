package table

import (
	"math"
	"math/bits"
	"runtime"
	"sync/atomic"

	"github.com/go-playground/cache/v2/internal/node"
	"github.com/go-playground/cache/v2/internal/xruntime"
)

const (
	minBuckets     = 4
	maxHint        = MaxEntries
	loadNum        = 3
	loadDen        = 4
	stripesPerProc = 4
)

// MaxEntries is the most entries a table is sized for; beyond it buckets overflow into ever longer chains.
const MaxEntries = 1 << 30

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
	n          uint64
	stripeMask uint64
	resizeAt   int64
	frozen     atomic.Bool
	resized    chan struct{}
}

func newState[K comparable, V any](n int) *state[K, V] {
	// A power of two no larger than n, so the mask reaches every stripe and small tables are not over-striped.
	ns := min(1<<(bits.Len(uint(n))-1), nextPow2(runtime.GOMAXPROCS(0)*stripesPerProc))
	return &state[K, V]{
		buckets:    make([]bucket[K, V], n),
		stripes:    make([]stripe, ns),
		n:          uint64(n),
		stripeMask: uint64(ns - 1),
		resizeAt:   int64(n) * slotsPerBucket * loadNum / loadDen,
		resized:    make(chan struct{}),
	}
}

// bucketsFor returns the smallest root bucket count whose resize threshold is at least sizeHint. Bucket counts need
// not be powers of two, so a table sized for a capacity wastes no memory to rounding.
func bucketsFor(sizeHint int) int {
	hint := int64(min(max(sizeHint, 0), maxHint))
	const den = slotsPerBucket * loadNum
	need := (hint*loadDen + den - 1) / den // int64: hint*loadDen overflows a 32-bit int
	return max(int(need), minBuckets)
}

func nextPow2(n int) int {
	return 1 << bits.Len(uint(n-1))
}

// index maps hash to a root bucket by multiply-shift range reduction of its low 32 bits, which works for any bucket
// count. The top bits, used for tags, are independent of the index.
func (s *state[K, V]) index(hash uint64) uint64 {
	return (hash & math.MaxUint32) * s.n >> 32
}

func (s *state[K, V]) root(hash uint64) *bucket[K, V] {
	return &s.buckets[s.index(hash)]
}

// stripeFor returns the counter stripe of hash.
func (s *state[K, V]) stripeFor(hash uint64) *stripe {
	return &s.stripes[hash&s.stripeMask]
}

// size sums the stripes. A node's increment and decrement always land on the same stripe of the same state, so no
// stripe is negative; the sum is exact when quiescent.
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

// copyChain copies the nodes of locked root i of the previous state, which had oldN roots, into s, which is not yet
// reachable by anyone else and has at most twice as many roots.
//
// Range reduction is monotonic in the hash, so old root i covers one contiguous hash range, which maps onto at most 3
// consecutive new roots starting at i*n/oldN. With exact doubling no other old chain feeds them; with a smaller final
// step a boundary root may also be fed by the neighbouring old chain, so cursors resume after existing nodes. Appending
// through one cursor per destination keeps the copy linear even for very long chains.
func (s *state[K, V]) copyChain(root *bucket[K, V], i, oldN uint64) {
	var cursors [3]cursor[K, V]
	first := i * s.n / oldN
	for b := root; b != nil; b = b.next.Load() {
		for j := range b.slots {
			n := b.slots[j].Load()
			if n == nil {
				continue
			}
			dst := s.index(n.Hash)
			cursors[dst-first].append(&s.buckets[dst], n)
			s.stripeFor(n.Hash).n.Add(1)
		}
	}
}
