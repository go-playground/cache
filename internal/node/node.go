// Package node defines the cache entry shared by the table, policies, buffers and expiry structures.
package node

import "sync/atomic"

// List identifies which intrusive link set of a Node a deque uses.
type List uint8

const (
	// PolicyList links are owned by the eviction policy.
	PolicyList List = iota
	// ExpiryList links are owned by the expiry structures.
	ExpiryList
)

const (
	alive uint32 = iota
	retired
	dead
)

type links[K comparable, V any] struct {
	prev, next *Node[K, V]
}

// Node is a single cache entry.
//
// Key, Value, Hash and Weight are immutable once the node is published to the table. Updates to a key publish a new
// node and retire the old one, so readers never race on the value.
type Node[K comparable, V any] struct {
	Key   K
	Value V
	Hash  uint64

	// Weight is the entry's weight towards capacity.
	Weight uint32

	// Slot is policy owned state, only accessed under the policy lock.
	Slot uint32

	// Meta is policy owned state that may be accessed concurrently, such as S3-FIFO's inline frequency.
	Meta atomic.Uint32

	// InPolicy reports whether the node is currently tracked by the policy. Only accessed under the policy lock.
	InPolicy bool

	// ExpirySlot is expiry owned state, 0 meaning untracked. Only accessed under the policy lock.
	ExpirySlot uint32

	expiresAt atomic.Int64
	state     atomic.Uint32
	links     [2]links[K, V]
}

// New creates a new alive node.
func New[K comparable, V any](key K, value V, hash uint64, weight uint32) *Node[K, V] {
	return &Node[K, V]{Key: key, Value: value, Hash: hash, Weight: weight}
}

// Prev returns the previous node in the given list.
func (n *Node[K, V]) Prev(l List) *Node[K, V] {
	return n.links[l].prev
}

// Next returns the next node in the given list.
func (n *Node[K, V]) Next(l List) *Node[K, V] {
	return n.links[l].next
}

// SetPrev sets the previous node in the given list.
func (n *Node[K, V]) SetPrev(l List, p *Node[K, V]) {
	n.links[l].prev = p
}

// SetNext sets the next node in the given list.
func (n *Node[K, V]) SetNext(l List, p *Node[K, V]) {
	n.links[l].next = p
}

// ExpiresAt returns the monotonic expiry instant in nanoseconds, 0 meaning never.
func (n *Node[K, V]) ExpiresAt() int64 {
	return n.expiresAt.Load()
}

// SetExpiresAt sets the monotonic expiry instant in nanoseconds, 0 meaning never.
func (n *Node[K, V]) SetExpiresAt(at int64) {
	n.expiresAt.Store(at)
}

// IsExpired reports whether the node has expired at the given monotonic instant.
func (n *Node[K, V]) IsExpired(now int64) bool {
	at := n.expiresAt.Load()
	return at != 0 && at <= now
}

// IsAlive reports whether the node is still mapped in the table.
func (n *Node[K, V]) IsAlive() bool {
	return n.state.Load() == alive
}

// IsDead reports whether the node has been removed from both the table and the policy.
func (n *Node[K, V]) IsDead() bool {
	return n.state.Load() == dead
}

// Retire marks the node as removed from the table but potentially still tracked by the policy.
func (n *Node[K, V]) Retire() {
	n.state.CompareAndSwap(alive, retired)
}

// Die marks the node as removed from both the table and the policy.
func (n *Node[K, V]) Die() {
	n.state.Store(dead)
}
