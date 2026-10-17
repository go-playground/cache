// Package node defines the cache entry shared by the table, policies and buffers.
//
// Entries are deliberately compact: K + V + 48 bytes, so an int→int entry is exactly one 64-byte allocation.
package node

import "sync/atomic"

const (
	alive uint32 = iota
	retired
	dead
)

const inPolicyBit = 1 << 31

// Node is a single cache entry.
//
// Key, Value, Hash and Weight are immutable once the node is published to the table. Updates to a key publish a new
// node and retire the old one, so readers never race on the value.
type Node[K comparable, V any] struct {
	Key   K
	Value V
	Hash  uint64

	prev, next *Node[K, V]
	expiresAt  atomic.Int64

	// Weight is the entry's weight towards capacity.
	Weight uint32

	// slot holds the policy owned slot in its low 31 bits and the engine owned in-policy flag in its top bit. Only
	// accessed under the policy lock.
	slot uint32

	// Meta is policy owned state that may be accessed concurrently, such as S3-FIFO's inline frequency.
	Meta atomic.Uint32

	state atomic.Uint32
}

// New creates a new alive node.
func New[K comparable, V any](key K, value V, hash uint64, weight uint32) *Node[K, V] {
	return &Node[K, V]{Key: key, Value: value, Hash: hash, Weight: weight}
}

// Prev returns the previous node in the policy's list.
func (n *Node[K, V]) Prev() *Node[K, V] {
	return n.prev
}

// Next returns the next node in the policy's list.
func (n *Node[K, V]) Next() *Node[K, V] {
	return n.next
}

// SetPrev sets the previous node in the policy's list.
func (n *Node[K, V]) SetPrev(p *Node[K, V]) {
	n.prev = p
}

// SetNext sets the next node in the policy's list.
func (n *Node[K, V]) SetNext(p *Node[K, V]) {
	n.next = p
}

// Slot returns the policy owned slot, a value below 1<<31.
func (n *Node[K, V]) Slot() uint32 {
	return n.slot &^ inPolicyBit
}

// SetSlot sets the policy owned slot, which must be below 1<<31.
func (n *Node[K, V]) SetSlot(s uint32) {
	n.slot = n.slot&inPolicyBit | s
}

// InPolicy reports whether the node is tracked by the policy. Engine owned.
func (n *Node[K, V]) InPolicy() bool {
	return n.slot&inPolicyBit != 0
}

// SetInPolicy records whether the node is tracked by the policy. Engine owned.
func (n *Node[K, V]) SetInPolicy(in bool) {
	if in {
		n.slot |= inPolicyBit
		return
	}
	n.slot &^= inPolicyBit
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
