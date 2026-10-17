package s3fifo

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/go-playground/cache/v2/internal/node"
)

// mentry is the model's view of a node.
type mentry struct {
	key    int
	hash   uint64
	weight uint32
	freq   uint32
}

// model is a naive S3-FIFO: queue index 0 is the front (newest), ghost index 0 is the oldest hash.
type model struct {
	capacity uint64
	smallMax uint64
	small    []mentry
	main     []mentry
	ghost    []uint64
}

func newModel(capacity uint64) *model {
	return &model{capacity: capacity, smallMax: max(1, capacity/10)}
}

func sumWeight(q []mentry) uint64 {
	var w uint64
	for _, e := range q {
		w += uint64(e.weight)
	}
	return w
}

func (m *model) weight() uint64 {
	return sumWeight(m.small) + sumWeight(m.main)
}

func (m *model) rejects(e mentry) bool {
	return m.capacity == 0 || uint64(e.weight) > m.capacity
}

// locate returns the queue holding key and its index.
func (m *model) locate(key int) (*[]mentry, int) {
	for _, q := range []*[]mentry{&m.small, &m.main} {
		if i := slices.IndexFunc(*q, func(e mentry) bool { return e.key == key }); i >= 0 {
			return q, i
		}
	}
	panic("model: key not tracked")
}

func (m *model) add(e mentry) []int {
	if m.rejects(e) {
		return []int{e.key}
	}
	e.freq = 0
	if i := slices.Index(m.ghost, e.hash); i >= 0 {
		m.ghost = slices.Delete(m.ghost, i, i+1)
		m.main = slices.Insert(m.main, 0, e)
	} else {
		m.small = slices.Insert(m.small, 0, e)
	}
	return m.evictOverflow(nil)
}

func (m *model) access(key int) {
	q, i := m.locate(key)
	(*q)[i].freq = min(3, (*q)[i].freq+1)
}

func (m *model) update(e mentry) []int {
	q, i := m.locate(e.key)
	e.freq = min(3, (*q)[i].freq+1)
	(*q)[i] = e
	var evicted []int
	if m.rejects(e) {
		m.remove(e.key)
		evicted = append(evicted, e.key)
	}
	return m.evictOverflow(evicted)
}

func (m *model) remove(key int) {
	q, i := m.locate(key)
	*q = slices.Delete(*q, i, i+1)
}

func (m *model) clear() []int {
	var keys []int
	for _, e := range append(slices.Clone(m.small), m.main...) {
		keys = append(keys, e.key)
	}
	m.small, m.main, m.ghost = nil, nil, nil
	return keys
}

func (m *model) evictOverflow(evicted []int) []int {
	for m.weight() > m.capacity {
		if len(m.small) > 0 && (sumWeight(m.small) >= m.smallMax || len(m.main) == 0) {
			if k, ok := m.evictSmall(); ok {
				evicted = append(evicted, k)
				continue
			}
		}
		evicted = append(evicted, m.evictMain())
	}
	return evicted
}

func (m *model) evictSmall() (int, bool) {
	for len(m.small) > 0 {
		t := m.small[len(m.small)-1]
		m.small = m.small[:len(m.small)-1]
		if t.freq > 1 {
			t.freq = 0
			m.main = slices.Insert(m.main, 0, t)
			continue
		}
		m.ghost = append(m.ghost, t.hash)
		if limit := max(1, len(m.small)+len(m.main)); len(m.ghost) > limit {
			m.ghost = m.ghost[len(m.ghost)-limit:]
		}
		return t.key, true
	}
	return 0, false
}

func (m *model) evictMain() int {
	for {
		t := m.main[len(m.main)-1]
		m.main = m.main[:len(m.main)-1]
		if t.freq > 0 {
			t.freq--
			m.main = slices.Insert(m.main, 0, t)
			continue
		}
		return t.key
	}
}

func (m *model) snapshot() snapshot {
	s := snapshot{ghost: slices.Clone(m.ghost)}
	for _, e := range m.small {
		s.small = append(s.small, kf{e.key, e.freq})
	}
	for _, e := range m.main {
		s.main = append(s.main, kf{e.key, e.freq})
	}
	return s
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

const keySpace = 24

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
	hash    func(int) uint64
	nodes   map[int]*node.Node[int, int]
	evicted []int
}

func newHarness(t testing.TB, capacity uint64, hash func(int) uint64) *harness {
	return &harness{t: t, p: New[int, int](capacity), m: newModel(capacity), hash: hash, nodes: make(map[int]*node.Node[int, int])}
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
		if got, want := contents(h.p), h.m.snapshot(); !got.equal(want) {
			h.t.Fatalf("op %d %+v: contents %+v, model %+v", i, o, got, want)
		}
		if h.p.Weight() != h.m.weight() {
			h.t.Fatalf("op %d %+v: weight %d, model %d", i, o, h.p.Weight(), h.m.weight())
		}
		checkInvariants(h.t, h.p)
	}
}

func (h *harness) apply(o op) []int {
	old, tracked := h.nodes[o.key]
	e := mentry{key: o.key, hash: h.hash(o.key), weight: o.weight}
	switch {
	case o.kind == opClear:
		h.p.Clear(h.evict)
		return h.m.clear()
	case o.kind == opAccess && tracked:
		h.p.InlineAccess(old)
		h.m.access(o.key)
	case o.kind == opRemove && tracked:
		h.p.Remove(old)
		delete(h.nodes, o.key)
		h.m.remove(o.key)
	case (o.kind == opAdd || o.kind == opUpdate) && tracked:
		n := node.New(o.key, o.key, e.hash, o.weight)
		h.nodes[o.key] = n
		h.p.Update(old, n, h.evict)
		return h.m.update(e)
	case o.kind == opAdd || o.kind == opUpdate:
		n := node.New(o.key, o.key, e.hash, o.weight)
		h.nodes[o.key] = n
		h.p.Add(n, h.evict)
		return h.m.add(e)
	}
	return nil
}

var (
	capacities = []uint64{0, 1, 2, 5, 10, 37}
	hashers    = []func(int) uint64{hashOf, func(k int) uint64 { return uint64(k % 3) }}
)

func randomBytes(seed, stream uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, stream))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

func TestMatchesModel(t *testing.T) {
	for hi, hash := range hashers {
		for _, c := range capacities {
			for s := range uint64(10) {
				newHarness(t, c, hash).run(decodeOps(randomBytes(s, c<<1|uint64(hi), 3*5000), c))
			}
		}
	}
}

func FuzzS3FIFOAgainstModel(f *testing.F) {
	f.Add([]byte{3, 0, 0, 1, 20, 0, 2, 20, 6, 1, 20, 6, 1, 20, 0, 3, 200})
	f.Add([]byte{2, 1, 0, 1, 5, 11, 1, 30, 14, 1, 0, 255, 0, 0})
	f.Add(randomBytes(1, 1, 302))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 2 {
			return
		}
		c := capacities[int(data[0])%len(capacities)]
		hash := hashers[int(data[1])%len(hashers)]
		newHarness(t, c, hash).run(decodeOps(data[2:], c))
	})
}
