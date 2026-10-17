// Package deque is an intrusive, allocation free, doubly linked list over node.Node using one of its link sets.
//
// A node may be in at most one deque per link set at a time. Not safe for concurrent use.
package deque

import "github.com/go-playground/cache/v2/internal/node"

// Deque is an intrusive doubly linked list. Front is most recently added, Back is least recently added.
type Deque[K comparable, V any] struct {
	head, tail *node.Node[K, V]
	len        int
	list       node.List
}

// New returns an empty deque using the given link set.
func New[K comparable, V any](list node.List) Deque[K, V] {
	return Deque[K, V]{list: list}
}

// Len returns the number of nodes in the deque.
func (d *Deque[K, V]) Len() int {
	return d.len
}

// Front returns the first node or nil.
func (d *Deque[K, V]) Front() *node.Node[K, V] {
	return d.head
}

// Back returns the last node or nil.
func (d *Deque[K, V]) Back() *node.Node[K, V] {
	return d.tail
}

// PushFront links n at the front.
func (d *Deque[K, V]) PushFront(n *node.Node[K, V]) {
	n.SetPrev(d.list, nil)
	n.SetNext(d.list, d.head)
	if d.head == nil {
		d.tail = n
	} else {
		d.head.SetPrev(d.list, n)
	}
	d.head = n
	d.len++
}

// PushBack links n at the back.
func (d *Deque[K, V]) PushBack(n *node.Node[K, V]) {
	n.SetNext(d.list, nil)
	n.SetPrev(d.list, d.tail)
	if d.tail == nil {
		d.head = n
	} else {
		d.tail.SetNext(d.list, n)
	}
	d.tail = n
	d.len++
}

// Remove unlinks n, which must be in this deque.
func (d *Deque[K, V]) Remove(n *node.Node[K, V]) {
	prev, next := n.Prev(d.list), n.Next(d.list)
	if prev == nil {
		d.head = next
	} else {
		prev.SetNext(d.list, next)
	}
	if next == nil {
		d.tail = prev
	} else {
		next.SetPrev(d.list, prev)
	}
	n.SetPrev(d.list, nil)
	n.SetNext(d.list, nil)
	d.len--
}

// PopBack unlinks and returns the last node or nil.
func (d *Deque[K, V]) PopBack() *node.Node[K, V] {
	n := d.tail
	if n != nil {
		d.Remove(n)
	}
	return n
}

// MoveToFront moves n, which must be in this deque, to the front.
func (d *Deque[K, V]) MoveToFront(n *node.Node[K, V]) {
	if d.head == n {
		return
	}
	d.Remove(n)
	d.PushFront(n)
}

// MoveToBack moves n, which must be in this deque, to the back.
func (d *Deque[K, V]) MoveToBack(n *node.Node[K, V]) {
	if d.tail == n {
		return
	}
	d.Remove(n)
	d.PushBack(n)
}

// Replace puts n in old's position. old must be in this deque and n must not be in any deque using this link set.
func (d *Deque[K, V]) Replace(old, n *node.Node[K, V]) {
	prev, next := old.Prev(d.list), old.Next(d.list)
	n.SetPrev(d.list, prev)
	n.SetNext(d.list, next)
	if prev == nil {
		d.head = n
	} else {
		prev.SetNext(d.list, n)
	}
	if next == nil {
		d.tail = n
	} else {
		next.SetPrev(d.list, n)
	}
	old.SetPrev(d.list, nil)
	old.SetNext(d.list, nil)
}

// AppendBack moves every node of other, in order, to the back of d leaving other empty. Both must use the same link set.
func (d *Deque[K, V]) AppendBack(other *Deque[K, V]) {
	if other.head == nil {
		return
	}
	if d.tail == nil {
		d.head = other.head
	} else {
		d.tail.SetNext(d.list, other.head)
		other.head.SetPrev(d.list, d.tail)
	}
	d.tail = other.tail
	d.len += other.len
	other.head, other.tail, other.len = nil, nil, 0
}

// Clear unlinks every node, calling fn for each from front to back. fn may be nil.
func (d *Deque[K, V]) Clear(fn func(*node.Node[K, V])) {
	for n := d.head; n != nil; {
		next := n.Next(d.list)
		n.SetPrev(d.list, nil)
		n.SetNext(d.list, nil)
		if fn != nil {
			fn(n)
		}
		n = next
	}
	d.head, d.tail, d.len = nil, nil, 0
}
