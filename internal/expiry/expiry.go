// Package expiry tracks nodes for active expiration so expired entries release capacity without waiting for reads.
//
// Visibility never depends on these structures: readers always check node.IsExpired lazily. Trackers only reclaim.
// All trackers are single-threaded, used under the engine's policy lock, and own node.ExpiryList links and ExpirySlot.
// Nodes whose ExpiresAt is 0 (never) are not tracked.
package expiry

import "github.com/go-playground/cache/v2/internal/node"

// Tracker orders nodes for expiration.
type Tracker[K comparable, V any] interface {
	// Add starts tracking n if it has an expiry.
	Add(n *node.Node[K, V])

	// Access records a read of n, which is tracked or not.
	Access(n *node.Node[K, V])

	// Update replaces old, which is tracked or not, with n.
	Update(old, n *node.Node[K, V])

	// Remove stops tracking n, which is tracked or not.
	Remove(n *node.Node[K, V])

	// Expire stops tracking every node expired at now, calling fn for each.
	Expire(now int64, fn func(n *node.Node[K, V]))

	// Clear stops tracking every node.
	Clear()
}

const tracked = 1

func isTracked[K comparable, V any](n *node.Node[K, V]) bool {
	return n.ExpirySlot != 0
}
