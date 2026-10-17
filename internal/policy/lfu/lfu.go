// Package lfu implements an exact O(1) least frequently used eviction policy with optional frequency aging.
package lfu

import (
	"math"

	"github.com/go-playground/cache/v2/internal/node"
	"github.com/go-playground/cache/v2/internal/policy"
)

// Policy is a weighted LFU policy.
//
// Nodes are grouped into buckets of equal frequency, ordered from lowest to highest. Within a bucket the front is the
// most recently added or accessed node. The victim is the back of the lowest frequency bucket. New nodes start at
// frequency 1.
//
// When agingEvery is non-zero, every agingEvery calls to Access halve all frequencies (minimum 1), merging buckets
// that collapse to the same frequency so that originally more frequent nodes are in front.
//
// Not safe for concurrent use; see policy.Policy.
type Policy[K comparable, V any] struct {
	b          []bucket[K, V]
	head       int32
	tail       int32
	free       int32
	capacity   uint64
	weight     uint64
	agingEvery uint64
	accesses   uint64
}

// New returns an LFU policy retaining at most capacity weight, aging frequencies every agingEvery accesses, 0
// disabling aging.
func New[K comparable, V any](capacity uint64, agingEvery uint64) *Policy[K, V] {
	return &Policy[K, V]{head: none, tail: none, free: none, capacity: capacity, agingEvery: agingEvery}
}

// Add tracks n at frequency 1. Victims are evicted before n is inserted, so n is never evicted to make room for itself.
func (p *Policy[K, V]) Add(n *node.Node[K, V], evict policy.EvictFunc[K, V]) {
	if p.rejects(n) {
		evict(n)
		return
	}
	for p.weight+uint64(n.Weight) > p.capacity {
		p.evictVictim(evict)
	}
	r := p.head
	if r == none || p.b[r].freq != 1 {
		r = p.insertAfter(none, 1)
	}
	p.b[r].q.PushFront(n)
	p.attach(n, r)
	p.weight += uint64(n.Weight)
}

// Access increments n's frequency, saturating at math.MaxUint32, and moves it to the front of its new bucket.
func (p *Policy[K, V]) Access(n *node.Node[K, V]) {
	r := p.find(n)
	if f := p.b[r].freq; f == math.MaxUint32 {
		p.b[r].q.MoveToFront(n)
	} else {
		p.promote(n, r, f+1)
	}
	p.tick()
}

// Update replaces old with n, which inherits old's frequency and moves to the front of its bucket, then evicts until
// within capacity.
func (p *Policy[K, V]) Update(old, n *node.Node[K, V], evict policy.EvictFunc[K, V]) {
	r := p.find(old)
	p.b[r].q.Replace(old, n)
	p.b[r].q.MoveToFront(n)
	n.Slot = uint32(r)
	p.weight -= uint64(old.Weight)
	p.weight += uint64(n.Weight)
	if p.rejects(n) {
		p.Remove(n)
		evict(n)
	}
	p.evictOverflow(evict)
}

// Remove stops tracking n.
func (p *Policy[K, V]) Remove(n *node.Node[K, V]) {
	r := p.find(n)
	p.b[r].q.Remove(n)
	p.release(r)
	p.weight -= uint64(n.Weight)
}

// Clear stops tracking every node, calling fn for each from lowest to highest frequency, and resets aging.
func (p *Policy[K, V]) Clear(fn func(n *node.Node[K, V])) {
	for i := p.head; i != none; i = p.b[i].next {
		p.b[i].q.Clear(fn)
	}
	clear(p.b)
	p.b = p.b[:0]
	p.head, p.tail, p.free = none, none, none
	p.weight, p.accesses = 0, 0
}

// Weight returns the total weight of the tracked nodes.
func (p *Policy[K, V]) Weight() uint64 {
	return p.weight
}

func (p *Policy[K, V]) rejects(n *node.Node[K, V]) bool {
	return p.capacity == 0 || uint64(n.Weight) > p.capacity
}

// promote moves n from root bucket r to the front of the bucket with frequency freq, creating it after r if needed.
func (p *Policy[K, V]) promote(n *node.Node[K, V], r int32, freq uint32) {
	next := p.b[r].next
	if next == none || p.b[next].freq != freq {
		next = p.insertAfter(r, freq)
	}
	p.b[r].q.Remove(n)
	p.b[next].q.PushFront(n)
	p.attach(n, next)
	p.release(r)
}

func (p *Policy[K, V]) evictOverflow(evict policy.EvictFunc[K, V]) {
	for p.weight > p.capacity {
		p.evictVictim(evict)
	}
}

func (p *Policy[K, V]) evictVictim(evict policy.EvictFunc[K, V]) {
	v := p.b[p.head].q.Back()
	p.Remove(v)
	evict(v)
}

func (p *Policy[K, V]) tick() {
	if p.agingEvery == 0 {
		return
	}
	p.accesses++
	if p.accesses < p.agingEvery {
		return
	}
	p.accesses = 0
	p.age()
}

// age halves every bucket's frequency, minimum 1, merging adjacent buckets which collapse to the same frequency.
func (p *Policy[K, V]) age() {
	kept := none
	for i := p.head; i != none; {
		next := p.b[i].next
		f := max(1, p.b[i].freq/2)
		p.b[i].freq = f
		if kept != none && p.b[kept].freq == f {
			kept = p.merge(kept, i)
		} else {
			kept = i
		}
		i = next
	}
}
