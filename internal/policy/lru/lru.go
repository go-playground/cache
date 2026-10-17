// Package lru implements a least recently used eviction policy.
package lru

import (
	"github.com/go-playground/cache/v2/internal/deque"
	"github.com/go-playground/cache/v2/internal/node"
	"github.com/go-playground/cache/v2/internal/policy"
)

// Policy is a weighted LRU policy. Front of the queue is the most recently used node, the back is the next victim.
//
// Not safe for concurrent use; see policy.Policy.
type Policy[K comparable, V any] struct {
	q        deque.Deque[K, V]
	capacity uint64
	weight   uint64
}

// New returns an LRU policy retaining at most capacity weight.
func New[K comparable, V any](capacity uint64) *Policy[K, V] {
	return &Policy[K, V]{q: deque.New[K, V](node.PolicyList), capacity: capacity}
}

// Add tracks n as the most recently used node, then evicts least recently used nodes until within capacity.
func (p *Policy[K, V]) Add(n *node.Node[K, V], evict policy.EvictFunc[K, V]) {
	if p.rejects(n) {
		evict(n)
		return
	}
	p.q.PushFront(n)
	p.weight += uint64(n.Weight)
	p.evictOverflow(evict)
}

// Access marks n as the most recently used node.
func (p *Policy[K, V]) Access(n *node.Node[K, V]) {
	p.q.MoveToFront(n)
}

// Update replaces old with n and marks n as the most recently used node, then evicts until within capacity.
func (p *Policy[K, V]) Update(old, n *node.Node[K, V], evict policy.EvictFunc[K, V]) {
	p.q.Replace(old, n)
	p.q.MoveToFront(n)
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
	p.q.Remove(n)
	p.weight -= uint64(n.Weight)
}

// Clear stops tracking every node, calling fn for each from most to least recently used.
func (p *Policy[K, V]) Clear(fn func(n *node.Node[K, V])) {
	p.q.Clear(fn)
	p.weight = 0
}

// Weight returns the total weight of the tracked nodes.
func (p *Policy[K, V]) Weight() uint64 {
	return p.weight
}

func (p *Policy[K, V]) rejects(n *node.Node[K, V]) bool {
	return p.capacity == 0 || uint64(n.Weight) > p.capacity
}

func (p *Policy[K, V]) evictOverflow(evict policy.EvictFunc[K, V]) {
	for p.weight > p.capacity {
		v := p.q.PopBack()
		p.weight -= uint64(v.Weight)
		evict(v)
	}
}
