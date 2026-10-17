// Package s3fifo implements the S3-FIFO eviction policy from "FIFO Queues are All You Need for Cache Eviction"
// (Yang et al., SOSP '23).
//
// The name stands for Simple, Scalable caching with three Static FIFO queues: a small FIFO which filters one-hit
// wonders, a main FIFO for entries that proved useful, and a ghost FIFO remembering the hashes of recently evicted
// small queue entries so they are readmitted straight into main. It is unrelated to Amazon S3.
//
// Read hits only increment a small saturating counter, so they are recorded inline and lock free.
package s3fifo

import (
	"github.com/go-playground/cache/v2/internal/deque"
	"github.com/go-playground/cache/v2/internal/node"
	"github.com/go-playground/cache/v2/internal/policy"
)

// Queue identifiers stored in node.Node.Slot().
const (
	small uint32 = iota
	main
)

// maxFreq is the saturation point of the per node frequency stored in node.Node.Meta.
const maxFreq = 3

// Policy is a weighted S3-FIFO policy. The front of each queue is the newest entry, the back the oldest.
//
// Not safe for concurrent use, except InlineAccess; see policy.Policy and policy.InlineAccessor.
type Policy[K comparable, V any] struct {
	small       deque.Deque[K, V]
	main        deque.Deque[K, V]
	ghost       ghost
	smallWeight uint64
	mainWeight  uint64
	smallMax    uint64
	capacity    uint64
}

// New returns an S3-FIFO policy retaining at most capacity weight, of which the small queue targets a tenth.
func New[K comparable, V any](capacity uint64) *Policy[K, V] {
	return &Policy[K, V]{
		small:    deque.New[K, V](),
		main:     deque.New[K, V](),
		smallMax: max(1, capacity/10),
		capacity: capacity,
	}
}

// Add tracks n with frequency 0 in the main queue if its hash is in the ghost queue, else in the small queue, then
// evicts until within capacity.
func (p *Policy[K, V]) Add(n *node.Node[K, V], evict policy.EvictFunc[K, V]) {
	if p.rejects(n) {
		evict(n)
		return
	}
	n.Meta.Store(0)
	if p.ghost.remove(n.Hash) {
		p.pushFront(n, main)
	} else {
		p.pushFront(n, small)
	}
	p.evictOverflow(evict)
}

// Access records a read hit of n.
func (p *Policy[K, V]) Access(n *node.Node[K, V]) {
	p.InlineAccess(n)
}

// InlineAccess records a read hit of n by incrementing its frequency, saturating at 3. Safe for concurrent use.
// Saturated nodes are only loaded, never written, so hot keys do not contend.
func (p *Policy[K, V]) InlineAccess(n *node.Node[K, V]) {
	for {
		f := n.Meta.Load()
		if f >= maxFreq {
			return
		}
		if n.Meta.CompareAndSwap(f, f+1) {
			return
		}
	}
}

// Update replaces old with n in place, n inheriting old's queue and its frequency plus one, then evicts until within
// capacity.
func (p *Policy[K, V]) Update(old, n *node.Node[K, V], evict policy.EvictFunc[K, V]) {
	p.queue(old.Slot()).Replace(old, n)
	n.SetSlot(old.Slot())
	n.Meta.Store(min(maxFreq, old.Meta.Load()+1))
	*p.queueWeight(n.Slot()) -= uint64(old.Weight)
	*p.queueWeight(n.Slot()) += uint64(n.Weight)
	if p.rejects(n) {
		p.Remove(n)
		evict(n)
	}
	p.evictOverflow(evict)
}

// Remove stops tracking n without recording it in the ghost queue.
func (p *Policy[K, V]) Remove(n *node.Node[K, V]) {
	p.queue(n.Slot()).Remove(n)
	*p.queueWeight(n.Slot()) -= uint64(n.Weight)
}

// Clear stops tracking every node, calling fn for each, small queue first, and forgets the ghost queue.
func (p *Policy[K, V]) Clear(fn func(n *node.Node[K, V])) {
	p.small.Clear(fn)
	p.main.Clear(fn)
	p.ghost.reset()
	p.smallWeight, p.mainWeight = 0, 0
}

// Weight returns the total weight of the tracked nodes.
func (p *Policy[K, V]) Weight() uint64 {
	return p.smallWeight + p.mainWeight
}

func (p *Policy[K, V]) rejects(n *node.Node[K, V]) bool {
	return p.capacity == 0 || uint64(n.Weight) > p.capacity
}

func (p *Policy[K, V]) queue(slot uint32) *deque.Deque[K, V] {
	if slot == main {
		return &p.main
	}
	return &p.small
}

func (p *Policy[K, V]) queueWeight(slot uint32) *uint64 {
	if slot == main {
		return &p.mainWeight
	}
	return &p.smallWeight
}

func (p *Policy[K, V]) pushFront(n *node.Node[K, V], slot uint32) {
	n.SetSlot(slot)
	p.queue(slot).PushFront(n)
	*p.queueWeight(slot) += uint64(n.Weight)
}

func (p *Policy[K, V]) evictOverflow(evict policy.EvictFunc[K, V]) {
	for p.Weight() > p.capacity {
		p.evictOne(evict)
	}
}

func (p *Policy[K, V]) evictOne(evict policy.EvictFunc[K, V]) {
	if p.small.Len() > 0 && (p.smallWeight >= p.smallMax || p.main.Len() == 0) && p.evictSmall(evict) {
		return
	}
	p.evictMain(evict)
}

// evictSmall promotes nodes accessed more than once from the back of the small queue into main until it evicts one,
// recording its hash in the ghost queue. It reports false if the small queue emptied without evicting.
func (p *Policy[K, V]) evictSmall(evict policy.EvictFunc[K, V]) bool {
	for t := p.small.Back(); t != nil; t = p.small.Back() {
		p.Remove(t)
		if t.Meta.Load() > 1 {
			t.Meta.Store(0)
			p.pushFront(t, main)
			continue
		}
		p.ghost.push(t.Hash, max(1, p.small.Len()+p.main.Len()))
		evict(t)
		return true
	}
	return false
}

// evictMain reinserts nodes with a non-zero frequency at the front of main, decrementing it, until it evicts one.
func (p *Policy[K, V]) evictMain(evict policy.EvictFunc[K, V]) {
	for {
		t := p.main.Back()
		if f := t.Meta.Load(); f > 0 {
			t.Meta.Store(f - 1)
			p.main.MoveToFront(t)
			continue
		}
		p.Remove(t)
		evict(t)
		return
	}
}
