package tinylfu

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/go-playground/cache/v2/internal/sketch"
)

// entry is the model's view of a node.
type entry struct {
	key    int
	weight uint32
}

// model is a naive slice based W-TinyLFU: index 0 of each queue is the most recently used entry. Window candidates
// are the first `candidates` entries of probation and the deepest of them is tried first.
type model struct {
	capacity     uint64
	queues       [3][]entry
	windowTarget float64
	windowMax    uint64
	mainMax      uint64
	protectedMax uint64
	sketch       *sketch.Sketch
	rnd          func() uint32
	adaptive     bool
	climber      climber
}

func newModel(capacity uint64, adaptive bool, rnd func() uint32) *model {
	m := &model{
		capacity: capacity,
		sketch:   newSketch(capacity),
		rnd:      rnd,
		adaptive: adaptive,
		climber:  newClimber(capacity),
	}
	m.resetWindow()
	return m
}

func (m *model) resetWindow() {
	m.windowTarget = float64(m.capacity / 100)
	m.sizeWindow(m.capacity / 100)
}

func (m *model) sizeWindow(w uint64) {
	m.windowMax = w
	m.mainMax = m.capacity - w
	m.protectedMax = m.mainMax * 80 / 100
}

func (m *model) record(hit bool) {
	if !m.adaptive {
		return
	}
	amount := m.climber.record(hit, uint64(m.count()))
	if amount == 0 {
		return
	}
	limit := m.capacity * 80 / 100
	m.windowTarget = min(max(m.windowTarget+amount, 0), float64(limit))
	m.sizeWindow(min(uint64(m.windowTarget), limit))
	if amount > 0 {
		m.growWindow()
	}
}

func (m *model) growWindow() {
	for quota := uint64(windowTransferQuota); quota > 0; {
		q := uint32(probation)
		if len(m.queues[probation]) == 0 {
			q = protected
		}
		last := len(m.queues[q]) - 1
		if last < 0 || m.weight(window)+uint64(m.queues[q][last].weight) > m.windowMax {
			return
		}
		e := m.take(q, last)
		m.pushFront(window, e)
		quota -= min(quota, uint64(e.weight))
	}
}

func (m *model) weight(q uint32) uint64 {
	var w uint64
	for _, e := range m.queues[q] {
		w += uint64(e.weight)
	}
	return w
}

func (m *model) total() uint64 {
	return m.weight(window) + m.weight(probation) + m.weight(protected)
}

func (m *model) count() int {
	return len(m.queues[window]) + len(m.queues[probation]) + len(m.queues[protected])
}

func (m *model) find(key int) (uint32, int) {
	for q := range m.queues {
		if i := slices.IndexFunc(m.queues[q], func(e entry) bool { return e.key == key }); i >= 0 {
			return uint32(q), i
		}
	}
	panic("model: key not tracked")
}

func (m *model) take(q uint32, i int) entry {
	e := m.queues[q][i]
	m.queues[q] = slices.Delete(m.queues[q], i, i+1)
	return e
}

func (m *model) pushFront(q uint32, e entry) {
	m.queues[q] = slices.Insert(m.queues[q], 0, e)
}

func (m *model) rejects(e entry) bool {
	return m.capacity == 0 || uint64(e.weight) > m.capacity
}

func (m *model) add(e entry) []int {
	if m.count()+1 > m.sketch.Capacity() {
		m.sketch.EnsureCapacity(m.count() + 1)
		if m.adaptive && m.sketch.Capacity() < int(min(m.capacity, 1<<62)) {
			m.climber.resample(uint64(m.sketch.Capacity()))
		}
	}
	m.sketch.Increment(hashOf(e.key))
	m.record(false)
	var evicted []int
	if m.rejects(e) {
		evicted = append(evicted, e.key)
	} else {
		m.pushFront(window, e)
	}
	return m.evictEntries(evicted)
}

func (m *model) access(key int) {
	m.sketch.Increment(hashOf(key))
	m.record(true)
	m.reorder(m.find(key))
	m.demote()
	m.drain()
}

func (m *model) update(e entry) []int {
	q, i := m.find(e.key)
	m.queues[q][i] = e
	m.sketch.Increment(hashOf(e.key))
	m.record(true)
	q, i = m.find(e.key) // the climber may have moved it into the window
	var evicted []int
	if m.rejects(e) {
		m.take(q, i)
		evicted = append(evicted, e.key)
	} else {
		m.reorder(q, i)
	}
	return m.evictEntries(evicted)
}

func (m *model) remove(key int) {
	m.take(m.find(key))
}

func (m *model) clear() []int {
	var keys []int
	for q := range m.queues {
		for _, e := range m.queues[q] {
			keys = append(keys, e.key)
		}
		m.queues[q] = nil
	}
	m.climber.reset()
	m.resetWindow()
	return keys
}

func (m *model) reorder(q uint32, i int) {
	e := m.take(q, i)
	if q == probation {
		q = protected
	}
	m.pushFront(q, e)
}

func (m *model) demote() {
	for m.weight(protected) > m.protectedMax {
		m.pushFront(probation, m.take(protected, len(m.queues[protected])-1))
	}
}

func (m *model) drain() int {
	n := 0
	for m.weight(window) > m.windowMax {
		m.pushFront(probation, m.take(window, len(m.queues[window])-1))
		n++
	}
	return n
}

func (m *model) evictEntries(evicted []int) []int {
	m.demote()
	candidates := m.drain()
	for m.total() > m.capacity {
		candidates = min(candidates, len(m.queues[probation]))
		vq := m.victimQueue()
		vi := len(m.queues[vq]) - 1
		if candidates == 0 || (vq == probation && vi == candidates-1) {
			evicted = append(evicted, m.take(vq, vi).key)
			continue
		}
		ci := candidates - 1
		candidates--
		if m.admit(m.queues[probation][ci], m.queues[vq][vi]) {
			evicted = append(evicted, m.take(vq, vi).key)
		} else {
			evicted = append(evicted, m.take(probation, ci).key)
		}
	}
	return evicted
}

func (m *model) victimQueue() uint32 {
	for _, q := range []uint32{probation, protected} {
		if len(m.queues[q]) > 0 {
			return q
		}
	}
	return window
}

func (m *model) admit(candidate, victim entry) bool {
	cf, vf := m.sketch.Frequency(hashOf(candidate.key)), m.sketch.Frequency(hashOf(victim.key))
	switch {
	case cf > vf:
		return true
	case cf < 6:
		return false
	default:
		return m.rnd()&127 == 0
	}
}

// differential drives a policy and a model with the same operations, comparing them after each.
type differential struct {
	t       testing.TB
	p       *Policy[int, int]
	m       *model
	nodes   map[int]*testNode
	evicted []int
}

func newDifferential(t testing.TB, capacity uint64, adaptive bool, seed uint64) *differential {
	d := &differential{
		t:     t,
		p:     New[int, int](capacity, adaptive),
		m:     newModel(capacity, adaptive, rand.New(rand.NewPCG(seed, seed)).Uint32),
		nodes: make(map[int]*testNode),
	}
	d.p.rnd = rand.New(rand.NewPCG(seed, seed)).Uint32
	return d
}

func (d *differential) evict(n *testNode) {
	d.evicted = append(d.evicted, n.Key)
	if d.nodes[n.Key] == n {
		delete(d.nodes, n.Key)
	}
}

// apply runs op on key: 0 add or update, 1 access, 2 update, 3 remove, 4 clear.
func (d *differential) apply(op, key int, weight uint32) {
	d.t.Helper()
	d.evicted = d.evicted[:0]
	var want []int
	old, tracked := d.nodes[key]
	switch {
	case op == 4:
		d.p.Clear(d.evict)
		want = d.m.clear()
	case !tracked && op <= 2:
		n := newNode(key, weight)
		d.nodes[key] = n
		d.p.Add(n, d.evict)
		want = d.m.add(entry{key, weight})
	case !tracked:
		return
	case op == 1:
		d.p.Access(old)
		d.m.access(key)
	case op == 3:
		d.p.Remove(old)
		delete(d.nodes, key)
		d.m.remove(key)
	default:
		n := newNode(key, weight)
		d.nodes[key] = n
		d.p.Update(old, n, d.evict)
		want = d.m.update(entry{key, weight})
	}
	d.compare(want)
}

func (d *differential) compare(evicted []int) {
	d.t.Helper()
	checkInvariants(d.t, d.p)
	if !slices.Equal(d.evicted, evicted) {
		d.t.Fatalf("evicted %v, model %v", d.evicted, evicted)
	}
	for q := range d.p.queues {
		var keys []int
		for _, e := range d.m.queues[q] {
			keys = append(keys, e.key)
		}
		if got := queueKeys(d.p, uint32(q)); !slices.Equal(got, keys) {
			d.t.Fatalf("queue %d = %v, model %v", q, got, keys)
		}
		if d.p.weights[q] != d.m.weight(uint32(q)) {
			d.t.Fatalf("queue %d weight %d, model %d", q, d.p.weights[q], d.m.weight(uint32(q)))
		}
	}
	if d.p.windowMax != d.m.windowMax || d.p.mainMax != d.m.mainMax || d.p.protectedMax != d.m.protectedMax {
		d.t.Fatalf("sizing %d/%d/%d, model %d/%d/%d", d.p.windowMax, d.p.mainMax, d.p.protectedMax,
			d.m.windowMax, d.m.mainMax, d.m.protectedMax)
	}
}

// weightFor maps b to a weight in [0, 4] or one heavier than capacity.
func weightFor(b byte, capacity uint64) uint32 {
	switch b % 16 {
	case 0:
		return 0
	case 15:
		return uint32(capacity) + 1
	default:
		return uint32(1 + b%4)
	}
}

func TestPolicyMatchesModel(t *testing.T) {
	for _, c := range []uint64{0, 1, 2, 3, 5, 10, 50, 100, 257, 1000} {
		for seed := range uint64(4) {
			for _, adaptive := range []bool{false, true} {
				d := newDifferential(t, c, adaptive, seed)
				r := rand.New(rand.NewPCG(seed, c))
				keys := int(c)*3/2 + 4
				for range 5000 {
					op := []int{0, 0, 0, 1, 1, 1, 1, 2, 3}[r.IntN(9)]
					if r.IntN(2000) == 0 {
						op = 4
					}
					d.apply(op, r.IntN(keys), weightFor(byte(r.Uint32()), c))
				}
			}
		}
	}
}

// FuzzTinyLFUAgainstModel decodes data as a capacity byte, a flags byte and then 3-byte operations of kind, key and
// weight, replaying them against the policy and the model.
func FuzzTinyLFUAgainstModel(f *testing.F) {
	f.Add([]byte{10, 0, 0, 1, 1, 0, 2, 1, 0, 3, 1, 1, 1, 0, 3, 2, 0})
	f.Add([]byte{1, 1, 0, 1, 1, 0, 2, 1, 1, 1, 0, 2, 2, 15, 3, 1, 0})
	f.Add([]byte{0, 0, 0, 1, 0, 0, 2, 1, 1, 1, 0})
	f.Add([]byte{3, 1, 0, 1, 1, 0, 2, 2, 0, 3, 3, 1, 1, 0, 4, 1, 2, 2, 4, 3, 255, 0, 0})
	seed := []byte{20, 1}
	for i := range 300 {
		seed = append(seed, byte(i*7), byte(i*13%29), byte(i*5))
	}
	f.Add(seed)

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 2 {
			return
		}
		capacity, adaptive := uint64(data[0]), data[1]&1 == 1
		d := newDifferential(t, capacity, adaptive, uint64(data[1]))
		for ops := data[2:]; len(ops) >= 3; ops = ops[3:] {
			op := int(ops[0] % 4)
			if ops[0] == 255 {
				op = 4
			}
			d.apply(op, int(ops[1]%64), weightFor(ops[2], capacity))
		}
	})
}
