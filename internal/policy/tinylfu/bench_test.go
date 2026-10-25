package tinylfu

import (
	"math/rand/v2"
	"testing"
)

// pool is a fixed set of nodes recycled through a free list by the evict callback, so steady state Adds allocate
// nothing.
type pool struct {
	free  []*testNode
	evict func(n *testNode)
	next  int
}

func newPool(size int) *pool {
	p := &pool{free: make([]*testNode, 0, size)}
	for range size {
		p.free = append(p.free, newNode(0, 1))
	}
	p.evict = func(n *testNode) { p.free = append(p.free, n) }
	return p
}

// add tracks the next never seen key in tp using a recycled node.
func (p *pool) add(tp *Policy[int, int]) {
	n := p.free[len(p.free)-1]
	p.free = p.free[:len(p.free)-1]
	p.next++
	n.Key, n.Hash = p.next, hashOf(p.next)
	tp.Add(n, p.evict)
}

// filled returns an adaptive policy at capacity with every node in nodes.
func filled(capacity int) (*Policy[int, int], []*testNode) {
	p := New[int, int](uint64(capacity), true)
	nodes := make([]*testNode, capacity)
	for i := range nodes {
		nodes[i] = newNode(i, 1)
		p.Add(nodes[i], func(*testNode) { panic("evicted below capacity") })
	}
	return p, nodes
}

func TestAccessDoesNotAllocate(t *testing.T) {
	p, nodes := filled(1000)
	r := rand.New(rand.NewPCG(1, 2))
	if a := testing.AllocsPerRun(10000, func() { p.Access(nodes[r.IntN(len(nodes))]) }); a != 0 {
		t.Errorf("Access allocates %v", a)
	}
}

func TestSteadyStateAddDoesNotAllocate(t *testing.T) {
	const capacity = 1000
	p, nodes := New[int, int](capacity, true), newPool(capacity+1)
	for range 10 * capacity {
		nodes.add(p)
	}
	if a := testing.AllocsPerRun(10000, func() { nodes.add(p) }); a != 0 {
		t.Errorf("Add allocates %v", a)
	}
}

func BenchmarkAdd(b *testing.B) {
	for _, adaptive := range []bool{false, true} {
		b.Run(mode(adaptive), func(b *testing.B) {
			const capacity = 10_000
			p, nodes := New[int, int](capacity, adaptive), newPool(capacity+1)
			for range 2 * capacity {
				nodes.add(p)
			}
			b.ReportAllocs()
			for b.Loop() {
				nodes.add(p)
			}
		})
	}
}

func BenchmarkAccess(b *testing.B) {
	const capacity = 10_000
	p, nodes := filled(capacity)
	trace := zipfTrace(7, 1.01, capacity, 1<<16)
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		p.Access(nodes[trace[i&(len(trace)-1)]])
	}
}

func mode(adaptive bool) string {
	if adaptive {
		return "adaptive"
	}
	return "static"
}
