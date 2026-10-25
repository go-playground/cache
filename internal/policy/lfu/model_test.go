package lfu

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

// mbucket is a model frequency bucket; q[0] is the front.
type mbucket struct {
	freq uint32
	q    []entry
}

// model is a naive LFU over a slice of buckets sorted by ascending frequency.
type model struct {
	capacity   uint64
	agingEvery uint64
	accesses   uint64
	weight     uint64
	b          []mbucket
}

func (m *model) rejects(e entry) bool {
	return m.capacity == 0 || uint64(e.weight) > m.capacity
}

func (m *model) find(key int) (int, int) {
	for bi, b := range m.b {
		if ei := slices.IndexFunc(b.q, func(e entry) bool { return e.key == key }); ei >= 0 {
			return bi, ei
		}
	}
	panic("model: key not tracked")
}

func (m *model) add(e entry) []int {
	if m.rejects(e) {
		return []int{e.key}
	}
	var evicted []int
	for m.weight+uint64(e.weight) > m.capacity {
		evicted = m.evictVictim(evicted)
	}
	if len(m.b) == 0 || m.b[0].freq != 1 {
		m.b = slices.Insert(m.b, 0, mbucket{freq: 1})
	}
	m.b[0].q = slices.Insert(m.b[0].q, 0, e)
	m.weight += uint64(e.weight)
	return evicted
}

func (m *model) access(key int) {
	bi, ei := m.find(key)
	e := m.b[bi].q[ei]
	f := m.b[bi].freq
	m.b[bi].q = slices.Delete(m.b[bi].q, ei, ei+1)
	switch {
	case f == 1<<32-1:
		m.b[bi].q = slices.Insert(m.b[bi].q, 0, e)
	case bi+1 < len(m.b) && m.b[bi+1].freq == f+1:
		m.b[bi+1].q = slices.Insert(m.b[bi+1].q, 0, e)
	default:
		m.b = slices.Insert(m.b, bi+1, mbucket{freq: f + 1, q: []entry{e}})
	}
	if len(m.b[bi].q) == 0 {
		m.b = slices.Delete(m.b, bi, bi+1)
	}
	if m.agingEvery == 0 {
		return
	}
	m.accesses++
	if m.accesses == m.agingEvery {
		m.accesses = 0
		m.age()
	}
}

func (m *model) age() {
	var aged []mbucket
	for _, b := range m.b {
		b.freq = max(1, b.freq/2)
		if n := len(aged); n > 0 && aged[n-1].freq == b.freq {
			aged[n-1].q = append(slices.Clone(b.q), aged[n-1].q...)
			continue
		}
		aged = append(aged, b)
	}
	m.b = aged
}

func (m *model) update(e entry) []int {
	bi, ei := m.find(e.key)
	m.weight -= uint64(m.b[bi].q[ei].weight)
	m.b[bi].q = slices.Delete(m.b[bi].q, ei, ei+1)
	m.b[bi].q = slices.Insert(m.b[bi].q, 0, e)
	m.weight += uint64(e.weight)
	var evicted []int
	if m.rejects(e) {
		m.remove(e.key)
		evicted = append(evicted, e.key)
	}
	for m.weight > m.capacity {
		evicted = m.evictVictim(evicted)
	}
	return evicted
}

func (m *model) evictVictim(evicted []int) []int {
	q := m.b[0].q
	k := q[len(q)-1].key
	m.remove(k)
	return append(evicted, k)
}

func (m *model) remove(key int) {
	bi, ei := m.find(key)
	m.weight -= uint64(m.b[bi].q[ei].weight)
	m.b[bi].q = slices.Delete(m.b[bi].q, ei, ei+1)
	if len(m.b[bi].q) == 0 {
		m.b = slices.Delete(m.b, bi, bi+1)
	}
}

func (m *model) clear() []int {
	var keys []int
	for _, l := range m.levels() {
		keys = append(keys, l.keys...)
	}
	m.b, m.weight, m.accesses = nil, 0, 0
	return keys
}

func (m *model) levels() []level {
	var levels []level
	for _, b := range m.b {
		l := level{freq: b.freq}
		for _, e := range b.q {
			l.keys = append(l.keys, e.key)
		}
		levels = append(levels, l)
	}
	return levels
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
	case b%16 < 5:
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

func newHarness(t testing.TB, capacity, agingEvery uint64) *harness {
	return &harness{
		t:     t,
		p:     New[int, int](capacity, agingEvery),
		m:     &model{capacity: capacity, agingEvery: agingEvery},
		nodes: make(map[int]*node.Node[int, int]),
	}
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
		if got, want := contents(h.p), h.m.levels(); !equalLevels(got, want) {
			h.t.Fatalf("op %d %+v: contents %v, model %v", i, o, got, want)
		}
		if h.p.Weight() != h.m.weight || h.p.accesses != h.m.accesses {
			h.t.Fatalf("op %d %+v: weight %d accesses %d, model %d %d", i, o, h.p.Weight(), h.p.accesses, h.m.weight, h.m.accesses)
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

var (
	capacities = []uint64{0, 1, 2, 5, 10, 37}
	agings     = []uint64{0, 1, 3, 7, 50}
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
	for _, c := range capacities {
		for _, a := range agings {
			for s := range uint64(10) {
				newHarness(t, c, a).run(decodeOps(randomBytes(s, c<<8|a, 3*5000), c))
			}
		}
	}
}

func FuzzLFUAgainstModel(f *testing.F) {
	f.Add([]byte{4, 2, 0, 1, 20, 0, 2, 20, 6, 1, 20, 6, 1, 20, 0, 3, 200})
	f.Add([]byte{3, 1, 0, 1, 5, 11, 1, 30, 14, 1, 0, 255, 0, 0})
	f.Add(randomBytes(1, 1, 302))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 2 {
			return
		}
		c := capacities[int(data[0])%len(capacities)]
		a := agings[int(data[1])%len(agings)]
		newHarness(t, c, a).run(decodeOps(data[2:], c))
	})
}
