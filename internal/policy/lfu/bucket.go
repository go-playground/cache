package lfu

import (
	"github.com/go-playground/cache/v2/internal/deque"
	"github.com/go-playground/cache/v2/internal/node"
)

// none is the nil arena index.
const none int32 = -1

// bucket is an arena entry acting as a label which node.Node.Slot refers to.
//
// A root label (parent == itself) is a live frequency bucket linked into the frequency list via prev and next.
// A forwarding label has been merged into another bucket during aging; its deque is empty and parent leads towards
// the root holding its nodes. Forwarding lets aging merge buckets in O(1) without relabelling their nodes.
//
// refs counts the nodes whose Slot is this label plus the labels whose parent is this label. A label is freed when
// refs reaches zero, which for a root happens exactly when its deque becomes empty.
type bucket[K comparable, V any] struct {
	q      deque.Deque[K, V]
	freq   uint32
	refs   uint32
	prev   int32
	next   int32
	parent int32
}

// alloc returns an unlinked root label with the given frequency, reusing freed labels first.
func (p *Policy[K, V]) alloc(freq uint32) int32 {
	i := p.free
	if i == none {
		p.b = append(p.b, bucket[K, V]{})
		i = int32(len(p.b) - 1)
	} else {
		p.free = p.b[i].next
	}
	p.b[i] = bucket[K, V]{q: deque.New[K, V](node.PolicyList), freq: freq, prev: none, next: none, parent: i}
	return i
}

// insertAfter allocates a root bucket with the given frequency and links it after at, or at the head when at is none.
func (p *Policy[K, V]) insertAfter(at int32, freq uint32) int32 {
	i := p.alloc(freq)
	next := p.head
	if at != none {
		next = p.b[at].next
		p.b[at].next = i
	}
	p.b[i].prev, p.b[i].next = at, next
	if at == none {
		p.head = i
	}
	if next == none {
		p.tail = i
	} else {
		p.b[next].prev = i
	}
	return i
}

// unlink removes the root bucket i from the frequency list.
func (p *Policy[K, V]) unlink(i int32) {
	prev, next := p.b[i].prev, p.b[i].next
	if prev == none {
		p.head = next
	} else {
		p.b[prev].next = next
	}
	if next == none {
		p.tail = prev
	} else {
		p.b[next].prev = prev
	}
}

// release drops one reference to label l, freeing it and cascading to its parent when unreferenced.
func (p *Policy[K, V]) release(l int32) {
	for {
		p.b[l].refs--
		if p.b[l].refs > 0 {
			return
		}
		parent := p.b[l].parent
		if parent == l {
			p.unlink(l)
		}
		p.b[l] = bucket[K, V]{next: p.free}
		p.free = l
		if parent == l {
			return
		}
		l = parent
	}
}

// attach points n at the root bucket r.
func (p *Policy[K, V]) attach(n *node.Node[K, V], r int32) {
	n.Slot = uint32(r)
	p.b[r].refs++
}

// find returns the root bucket holding n, repointing n.Slot directly at it.
func (p *Policy[K, V]) find(n *node.Node[K, V]) int32 {
	l := int32(n.Slot)
	r := l
	for p.b[r].parent != r {
		r = p.b[r].parent
	}
	if r != l {
		p.attach(n, r)
		p.release(l)
	}
	return r
}

// merge combines the adjacent root buckets lo and hi, which now share a frequency, returning the surviving root.
// hi's nodes, being originally more frequent, end up in front of lo's. The larger bucket survives to keep
// forwarding chains shallow.
func (p *Policy[K, V]) merge(lo, hi int32) int32 {
	s, o := hi, lo
	if p.b[lo].q.Len() > p.b[hi].q.Len() {
		s, o = lo, hi
	}
	p.b[hi].q.AppendBack(&p.b[lo].q)
	if s == lo {
		p.b[lo].q, p.b[hi].q = p.b[hi].q, p.b[lo].q
	}
	p.unlink(o)
	p.b[o].parent = s
	p.b[s].refs++
	return s
}
