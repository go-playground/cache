package s3fifo

// none is the nil ghost arena index.
const none int32 = -1

// entry is a ghost arena slot. prev and next link the FIFO, next doubling as the free list link. same links to the
// next newer occurrence of the same hash.
type entry struct {
	hash uint64
	prev int32
	next int32
	same int32
}

// span locates the oldest and newest occurrence of a hash in the ghost FIFO.
type span struct {
	oldest int32
	newest int32
}

// ghost is a FIFO of recently evicted hashes with O(1) membership and O(1) removal of a hash's oldest occurrence.
//
// It is an intrusive list over a growable arena with a free list, so steady state operation does not allocate.
type ghost struct {
	e     []entry
	index map[uint64]span
	head  int32
	tail  int32
	free  int32
	len   int
}

func newGhost() ghost {
	return ghost{index: make(map[uint64]span), head: none, tail: none, free: none}
}

// push appends h as the newest entry, then drops the oldest entries until at most limit remain.
func (g *ghost) push(h uint64, limit int) {
	i := g.alloc(h)
	if g.tail == none {
		g.head = i
	} else {
		g.e[g.tail].next = i
		g.e[i].prev = g.tail
	}
	g.tail = i
	g.len++

	if s, ok := g.index[h]; ok {
		g.e[s.newest].same = i
		g.index[h] = span{oldest: s.oldest, newest: i}
	} else {
		g.index[h] = span{oldest: i, newest: i}
	}

	for g.len > limit {
		g.remove(g.e[g.head].hash)
	}
}

// remove drops the oldest occurrence of h, reporting whether h was present.
func (g *ghost) remove(h uint64) bool {
	s, ok := g.index[h]
	if !ok {
		return false
	}
	i := s.oldest
	if i == s.newest {
		delete(g.index, h)
	} else {
		g.index[h] = span{oldest: g.e[i].same, newest: s.newest}
	}
	g.unlink(i)
	g.e[i] = entry{next: g.free}
	g.free = i
	g.len--
	return true
}

// reset empties the ghost, retaining its storage.
func (g *ghost) reset() {
	clear(g.index)
	g.e = g.e[:0]
	g.head, g.tail, g.free, g.len = none, none, none, 0
}

func (g *ghost) alloc(h uint64) int32 {
	i := g.free
	if i == none {
		g.e = append(g.e, entry{})
		i = int32(len(g.e) - 1)
	} else {
		g.free = g.e[i].next
	}
	g.e[i] = entry{hash: h, prev: none, next: none, same: none}
	return i
}

func (g *ghost) unlink(i int32) {
	prev, next := g.e[i].prev, g.e[i].next
	if prev == none {
		g.head = next
	} else {
		g.e[prev].next = next
	}
	if next == none {
		g.tail = prev
	} else {
		g.e[next].prev = prev
	}
}
