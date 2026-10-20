// Package table is a concurrent hash table from key to *node.Node with lock-free reads.
//
// The design follows CLHT: every bucket is one cache line holding a mutex, a word of one-byte tags and five node
// slots, chained to overflow buckets. Readers only perform atomic loads, so hot keys never bounce cache lines between
// reading cores. Writers lock the root bucket of the chain they modify. When an insert needs an overflow bucket while
// the table is above a 0.75 load factor, it grows: doubling, or less on the final step to its bound, never past
// MaxEntries, and never shrinking. Replaced tables are left intact so readers still holding them stay correct.
//
// Callers compute the hash: index uses its low bits and the tag its top 7 bits, so it must be well mixed.
package table

import (
	"sync/atomic"

	"github.com/go-playground/cache/v2/internal/node"
)

// Table is a concurrent map from key to *node.Node. The zero value is not usable, use New.
type Table[K comparable, V any] struct {
	cur        atomic.Pointer[state[K, V]]
	resizes    atomic.Uint64
	maxBuckets int
	unsync     bool
}

// New creates an unbounded table pre-sized so that sizeHint entries fit without resizing. sizeHint <= 0 means a small
// default.
func New[K comparable, V any](sizeHint int) *Table[K, V] {
	return NewBounded[K, V](sizeHint, 0)
}

// NewUnsynchronized is NewBounded for a caller that serialises every operation itself, e.g. with its own mutex. Its
// writes skip bucket locking.
func NewUnsynchronized[K comparable, V any](sizeHint, maxEntries int) *Table[K, V] {
	t := NewBounded[K, V](sizeHint, maxEntries)
	t.unsync = true
	return t
}

// NewBounded creates a table pre-sized for sizeHint entries that never grows beyond what maxEntries needs, so a table
// for a bounded cache ends exactly sized for its capacity. maxEntries <= 0 means unbounded.
func NewBounded[K comparable, V any](sizeHint, maxEntries int) *Table[K, V] {
	// Unbounded tables still stop at MaxEntries, keeping range reduction within 32 bits.
	t := &Table[K, V]{maxBuckets: bucketsFor(MaxEntries)}
	if maxEntries > 0 {
		t.maxBuckets = bucketsFor(maxEntries)
	}
	t.cur.Store(newState[K, V](bucketsFor(sizeHint)))
	return t
}

// Get returns the node mapped to key, or nil. Lock-free.
//
// A reader may still walk a state that has since been replaced. That state is frozen before it is copied and is
// never written again, so it equals the logical map from the freeze until the first write to its successor, which
// can only happen after the successor is published. Whatever the reader returns was therefore the mapping at some
// instant within its own call.
func (t *Table[K, V]) Get(hash uint64, key K) *node.Node[K, V] {
	s := t.cur.Load()
	tag := tagOf(hash)
	for b := s.root(hash); b != nil; b = b.next.Load() {
		if _, n := b.find(b.meta.Load(), tag, hash, key); n != nil {
			return n
		}
	}
	return nil
}

// Store maps n.Key to n, returning the node it replaced or nil.
func (t *Table[K, V]) Store(n *node.Node[K, V]) (old *node.Node[K, V]) {
	s, root := t.lockRoot(n.Hash)
	old, overflowed := s.store(root, n)
	t.unlock(root)
	if overflowed && len(s.buckets) < t.maxBuckets && s.size() > s.resizeAt {
		t.grow(s)
	}
	return old
}

// StoreIf maps n.Key to n only if the key currently maps to exactly expected (nil meaning unmapped), reporting whether
// it did. It lets a caller publish a value computed from an earlier lookup without overwriting a newer mapping.
func (t *Table[K, V]) StoreIf(expected, n *node.Node[K, V]) bool {
	s, root := t.lockRoot(n.Hash)
	cur := s.lookup(root, n.Hash, n.Key)
	if cur != expected {
		t.unlock(root)
		return false
	}
	_, overflowed := s.store(root, n)
	t.unlock(root)
	if overflowed && len(s.buckets) < t.maxBuckets && s.size() > s.resizeAt {
		t.grow(s)
	}
	return true
}

// Delete removes key, returning the removed node or nil.
func (t *Table[K, V]) Delete(hash uint64, key K) (old *node.Node[K, V]) {
	s, root := t.lockRoot(hash)
	old = s.delete(root, hash, key)
	t.unlock(root)
	return old
}

// DeleteNode removes n.Key only if it is currently mapped to exactly n (pointer equality). Reports whether it removed.
func (t *Table[K, V]) DeleteNode(n *node.Node[K, V]) bool {
	s, root := t.lockRoot(n.Hash)
	ok := s.deleteNode(root, n)
	t.unlock(root)
	return ok
}

// Range calls fn for every mapped node until fn returns false. Weakly consistent: safe concurrently with writers;
// nodes present for the whole Range are visited exactly once, concurrently changed ones may or may not be, and a key
// deleted and re-stored during the Range may be visited twice. fn may call any Table method.
func (t *Table[K, V]) Range(fn func(n *node.Node[K, V]) bool) {
	s := t.cur.Load()
	for i := range s.buckets {
		for b := &s.buckets[i]; b != nil; b = b.next.Load() {
			for j := range b.slots {
				if n := b.slots[j].Load(); n != nil && !fn(n) {
					return
				}
			}
		}
	}
}

// Scan visits the chains of n root buckets starting at cursor, calling fn for every mapped node, and returns the
// cursor to resume from; cursors wrap, so repeated scans cycle through the whole table. Weakly consistent like Range,
// and fn may call any Table method. Used for incremental sweeps that must bound their work per call.
func (t *Table[K, V]) Scan(cursor uint64, n int, fn func(n *node.Node[K, V])) uint64 {
	s := t.cur.Load()
	for range min(n, len(s.buckets)) {
		for b := &s.buckets[cursor%s.n]; b != nil; b = b.next.Load() {
			for j := range b.slots {
				if nd := b.slots[j].Load(); nd != nil {
					fn(nd)
				}
			}
		}
		cursor++
	}
	return cursor
}

// Buckets returns the number of root buckets, the length of one full Scan cycle.
func (t *Table[K, V]) Buckets() int {
	return len(t.cur.Load().buckets)
}

// Len returns the number of mapped nodes (may be momentarily stale under concurrency, exact when quiescent).
func (t *Table[K, V]) Len() int {
	return int(t.cur.Load().size())
}

// lockRoot returns the current state with the root bucket of hash locked, never a frozen state.
//
// frozen is checked under the root lock. A writer that locked the root before the resizer did either sees frozen
// unset and finishes its write before the resizer copies that root, or sees it set. A writer that locks the root after
// the resizer released it is ordered after the freeze by the mutex and sees it set. Writers seeing it set wait for the
// successor, which is published before resized is closed, so no write is ever lost.
func (t *Table[K, V]) lockRoot(hash uint64) (*state[K, V], *bucket[K, V]) {
	if t.unsync {
		s := t.cur.Load()
		return s, s.root(hash)
	}
	for {
		s := t.cur.Load()
		root := s.root(hash)
		root.mu.Lock()
		if !s.frozen.Load() {
			return s, root
		}
		root.mu.Unlock()
		<-s.resized
	}
}

func (t *Table[K, V]) unlock(root *bucket[K, V]) {
	if !t.unsync {
		root.mu.Unlock()
	}
}

// grow replaces s with a state twice its size, or exactly the size maxBuckets needs if that is smaller; a table at
// its bound never grows and absorbs any transient excess in overflow buckets. The goroutine that freezes s is its
// only resizer; everyone else returns immediately. The successor is unreachable until published, so its buckets need
// no locking while copying, and the atomic publish orders the copied contents before any use.
func (t *Table[K, V]) grow(s *state[K, V]) {
	n := min(2*len(s.buckets), t.maxBuckets)
	if !s.frozen.CompareAndSwap(false, true) {
		return
	}
	ns := newState[K, V](n)
	oldN := uint64(len(s.buckets))
	for i := range s.buckets {
		root := &s.buckets[i]
		if !t.unsync {
			root.mu.Lock()
		}
		ns.copyChain(root, uint64(i), oldN)
		t.unlock(root)
	}
	t.cur.Store(ns)
	t.resizes.Add(1)
	close(s.resized)
}
