package lru

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/go-playground/cache/v2/internal/node"
)

// entry is the model's view of a node.
type entry struct {
	key    int
	weight uint32
}

// model is a naive LRU: q[0] is the most recently used entry.
type model struct {
	capacity uint64
	weight   uint64
	q        []entry
}

func (m *model) rejects(e entry) bool {
	return m.capacity == 0 || uint64(e.weight) > m.capacity
}

func (m *model) index(key int) int {
	return slices.IndexFunc(m.q, func(e entry) bool { return e.key == key })
}

func (m *model) add(e entry) []int {
	if m.rejects(e) {
		return []int{e.key}
	}
	m.q = slices.Insert(m.q, 0, e)
	m.weight += uint64(e.weight)
	return m.evictOverflow(nil)
}

func (m *model) access(key int) {
	i := m.index(key)
	e := m.q[i]
	m.q = slices.Delete(m.q, i, i+1)
	m.q = slices.Insert(m.q, 0, e)
}

func (m *model) update(e entry) []int {
	m.remove(e.key)
	m.q = slices.Insert(m.q, 0, e)
	m.weight += uint64(e.weight)
	var evicted []int
	if m.rejects(e) {
		m.remove(e.key)
		evicted = append(evicted, e.key)
	}
	return m.evictOverflow(evicted)
}

func (m *model) remove(key int) {
	i := m.index(key)
	m.weight -= uint64(m.q[i].weight)
	m.q = slices.Delete(m.q, i, i+1)
}

func (m *model) clear() []int {
	keys := m.keys()
	m.q, m.weight = nil, 0
	return keys
}

func (m *model) evictOverflow(evicted []int) []int {
	for m.weight > m.capacity {
		e := m.q[len(m.q)-1]
		m.remove(e.key)
		evicted = append(evicted, e.key)
	}
	return evicted
}

func (m *model) keys() []int {
	var keys []int
	for _, e := range m.q {
		keys = append(keys, e.key)
	}
	return keys
}

type opKind uint8

const (
	opAdd opKind = iota
	opAccess
	opUpdate
	opRemove
	opClear
)

type op struct {
	kind   opKind
	key    int
	weight uint32
}

const keySpace = 16

// decodeOps turns every 3 bytes into an op: kind, key and weight. About 5% of weights exceed capacity and a few are 0.
func decodeOps(data []byte, capacity uint64) []op {
	ops := make([]op, 0, len(data)/3)
	for ; len(data) >= 3; data = data[3:] {
		ops = append(ops, op{kind: decodeKind(data[0]), key: int(data[1]) % keySpace, weight: decodeWeight(data[2], capacity)})
	}
	return ops
}

func decodeKind(b byte) opKind {
	switch {
	case b == 255:
		return opClear
	case b%16 < 6:
		return opAdd
	case b%16 < 11:
		return opAccess
	case b%16 < 14:
		return opUpdate
	default:
		return opRemove
	}
}

func decodeWeight(b byte, capacity uint64) uint32 {
	switch {
	case b < 12:
		return uint32(capacity) + 1 + uint32(b%3)
	case b < 16:
		return 0
	default:
		return 1 + uint32(b%4)
	}
}

// harness drives the policy and the model with the same ops, comparing them after each.
type harness struct {
	t       testing.TB
	p       *Policy[int, int]
	m       *model
	nodes   map[int]*node.Node[int, int]
	evicted []int
}

func newHarness(t testing.TB, capacity uint64) *harness {
	return &harness{t: t, p: New[int, int](capacity), m: &model{capacity: capacity}, nodes: make(map[int]*node.Node[int, int])}
}

func (h *harness) evict(n *node.Node[int, int]) {
	h.evicted = append(h.evicted, n.Key)
	delete(h.nodes, n.Key)
}

func (h *harness) run(ops []op) {
	for i, o := range ops {
		h.evicted = nil
		want := h.apply(o)
		if !slices.Equal(h.evicted, want) {
			h.t.Fatalf("op %d %+v: evicted %v, model %v", i, o, h.evicted, want)
		}
		if got, want := contents(h.p), h.m.keys(); !slices.Equal(got, want) {
			h.t.Fatalf("op %d %+v: contents %v, model %v", i, o, got, want)
		}
		if h.p.Weight() != h.m.weight {
			h.t.Fatalf("op %d %+v: weight %d, model %d", i, o, h.p.Weight(), h.m.weight)
		}
		checkInvariants(h.t, h.p)
	}
}

func (h *harness) apply(o op) []int {
	old, tracked := h.nodes[o.key]
	e := entry{key: o.key, weight: o.weight}
	switch {
	case o.kind == opClear:
		h.p.Clear(h.evict)
		return h.m.clear()
	case o.kind == opAccess && tracked:
		h.p.Access(old)
		h.m.access(o.key)
	case o.kind == opRemove && tracked:
		h.p.Remove(old)
		delete(h.nodes, o.key)
		h.m.remove(o.key)
	case (o.kind == opAdd || o.kind == opUpdate) && tracked:
		n := mk(o.key, o.weight)
		h.nodes[o.key] = n
		h.p.Update(old, n, h.evict)
		return h.m.update(e)
	case o.kind == opAdd || o.kind == opUpdate:
		n := mk(o.key, o.weight)
		h.nodes[o.key] = n
		h.p.Add(n, h.evict)
		return h.m.add(e)
	}
	return nil
}

var capacities = []uint64{0, 1, 2, 5, 10, 37}

func randomBytes(seed, stream uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, stream))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

func TestMatchesModel(t *testing.T) {
	for _, c := range capacities {
		for s := range uint64(10) {
			newHarness(t, c).run(decodeOps(randomBytes(s, c, 3*5000), c))
		}
	}
}

func FuzzLRUAgainstModel(f *testing.F) {
	f.Add([]byte{3, 0, 1, 20, 0, 2, 20, 6, 1, 20, 0, 3, 200})
	f.Add([]byte{1, 0, 1, 5, 11, 1, 30, 14, 1, 0, 255, 0, 0})
	f.Add(randomBytes(1, 1, 301))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		c := capacities[int(data[0])%len(capacities)]
		newHarness(t, c).run(decodeOps(data[1:], c))
	})
}
