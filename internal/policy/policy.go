// Package policy defines the eviction policy contract used by the cache engine.
package policy

import "github.com/go-playground/cache/v2/internal/node"

// EvictFunc is invoked by a policy for every node it evicts. The node has already been unlinked from the policy.
type EvictFunc[K comparable, V any] func(n *node.Node[K, V])

// Policy is a single-threaded eviction policy. Every method is only ever called while holding the engine's policy
// lock, so implementations must not synchronise internally.
//
// Policies own node.Node's PolicyList links and its Slot field, and may use Meta. They must not touch InPolicy,
// which the engine manages. Capacity and all accounting are in terms of node.Node.Weight.
// A node heavier than capacity is never retained: Add and Update evict it immediately.
type Policy[K comparable, V any] interface {
	// Add starts tracking n, then evicts until total weight is within capacity, possibly evicting n itself.
	Add(n *node.Node[K, V], evict EvictFunc[K, V])

	// Access records a read hit of n.
	Access(n *node.Node[K, V])

	// Update replaces old with n, inheriting old's queue and frequency, and records the write as an access in the
	// policy's own terms. It then evicts until total weight is within capacity, possibly evicting n itself.
	// old is tracked by the policy and n is not; after Update old is no longer tracked and evict is not called for it.
	Update(old, n *node.Node[K, V], evict EvictFunc[K, V])

	// Remove stops tracking n, which is tracked by the policy.
	Remove(n *node.Node[K, V])

	// Clear stops tracking every node, calling fn for each.
	Clear(fn func(n *node.Node[K, V]))

	// Weight returns the total weight of the tracked nodes.
	Weight() uint64
}

// InlineAccessor is optionally implemented by policies which record read hits directly on the read path instead of
// via the read buffer, such as S3-FIFO.
//
// InlineAccess is called concurrently, without the policy lock, and must only touch n.Meta atomically.
// When implemented the engine never calls Access.
type InlineAccessor[K comparable, V any] interface {
	InlineAccess(n *node.Node[K, V])
}
