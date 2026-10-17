package expiry

import (
	"github.com/go-playground/cache/v2/internal/deque"
	"github.com/go-playground/cache/v2/internal/node"
)

// accessSkipBudget bounds how many unexpired nodes Expire reorders per call in access order mode. Reads may be dropped
// by the lossy read buffer (or never buffered, e.g. S3-FIFO), leaving a recently read node at the back with a bumped
// expiry; moving it to the front lets the scan continue to genuinely expired nodes behind it.
const accessSkipBudget = 8

// Order tracks nodes in a single queue ordered by write time (expire after write) or access time (expire after access).
// With a fixed time-to-live this order is also expiry order, giving O(1) operations.
type Order[K comparable, V any] struct {
	q        deque.Deque[K, V]
	onAccess bool
}

// NewWriteOrder returns a tracker for a fixed time-to-live measured from the last write.
func NewWriteOrder[K comparable, V any]() *Order[K, V] {
	return &Order[K, V]{q: deque.New[K, V](node.ExpiryList)}
}

// NewAccessOrder returns a tracker for a fixed time-to-live measured from the last read or write.
func NewAccessOrder[K comparable, V any]() *Order[K, V] {
	return &Order[K, V]{q: deque.New[K, V](node.ExpiryList), onAccess: true}
}

// Add starts tracking n if it has an expiry.
func (o *Order[K, V]) Add(n *node.Node[K, V]) {
	if n.ExpiresAt() == 0 {
		return
	}
	n.ExpirySlot = tracked
	o.q.PushFront(n)
}

// Access moves n to the front in access order mode.
func (o *Order[K, V]) Access(n *node.Node[K, V]) {
	if !o.onAccess || !isTracked(n) {
		return
	}
	o.q.MoveToFront(n)
}

// Update replaces old with n at the front.
func (o *Order[K, V]) Update(old, n *node.Node[K, V]) {
	o.Remove(old)
	o.Add(n)
}

// Remove stops tracking n.
func (o *Order[K, V]) Remove(n *node.Node[K, V]) {
	if !isTracked(n) {
		return
	}
	o.q.Remove(n)
	n.ExpirySlot = 0
}

// Expire removes expired nodes from the back, calling fn for each.
func (o *Order[K, V]) Expire(now int64, fn func(n *node.Node[K, V])) {
	skips := 0
	for n := o.q.Back(); n != nil; n = o.q.Back() {
		if n.IsExpired(now) {
			o.Remove(n)
			fn(n)
			continue
		}
		if !o.onAccess || skips == accessSkipBudget {
			return
		}
		skips++
		o.q.MoveToFront(n)
	}
}

// Clear stops tracking every node.
func (o *Order[K, V]) Clear() {
	o.q.Clear(func(n *node.Node[K, V]) { n.ExpirySlot = 0 })
}

// Len returns the number of tracked nodes.
func (o *Order[K, V]) Len() int {
	return o.q.Len()
}
