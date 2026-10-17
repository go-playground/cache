package tinylfu

import (
	"math/rand/v2"
	"testing"

	"github.com/go-playground/cache/v2/internal/node"
)

type testNode = node.Node[int, int]

func hashOf(key int) uint64 {
	h := uint64(key) + 0x9e3779b97f4a7c15
	h = (h ^ h>>30) * 0xbf58476d1ce4e5b9
	h = (h ^ h>>27) * 0x94d049bb133111eb
	return h ^ h>>31
}

func newNode(key int, weight uint32) *testNode {
	return node.New(key, 0, hashOf(key), weight)
}

// checkInvariants asserts the structural and sizing invariants that hold after every public operation.
func checkInvariants(t testing.TB, p *Policy[int, int]) {
	t.Helper()
	seen := make(map[*testNode]bool)
	var total uint64
	count := 0
	for q := range p.queues {
		w, n := checkQueue(t, p, uint32(q), seen)
		if w != p.weights[q] {
			t.Fatalf("queue %d weight %d, tracked %d", q, w, p.weights[q])
		}
		total += w
		count += n
	}
	if total != p.total || count != p.count {
		t.Fatalf("total %d count %d, tracked %d %d", total, count, p.total, p.count)
	}
	checkSizing(t, p)
}

func checkQueue(t testing.TB, p *Policy[int, int], q uint32, seen map[*testNode]bool) (uint64, int) {
	t.Helper()
	d := &p.queues[q]
	var prev *testNode
	var w uint64
	n := 0
	for x := d.Front(); x != nil; x = x.Next(node.PolicyList) {
		if x.Prev(node.PolicyList) != prev {
			t.Fatalf("queue %d key %d: broken prev link", q, x.Key)
		}
		if x.Slot != q {
			t.Fatalf("queue %d key %d: slot %d", q, x.Key, x.Slot)
		}
		if seen[x] {
			t.Fatalf("key %d linked twice", x.Key)
		}
		seen[x] = true
		w += uint64(x.Weight)
		n++
		prev = x
	}
	if d.Back() != prev {
		t.Fatalf("queue %d: back is not the last linked node", q)
	}
	if d.Len() != n {
		t.Fatalf("queue %d: Len %d, linked %d", q, d.Len(), n)
	}
	return w, n
}

func checkSizing(t testing.TB, p *Policy[int, int]) {
	t.Helper()
	c := p.capacity
	if p.windowMax > percent(c, 80) || p.mainMax != c-p.windowMax || p.protectedMax != percent(p.mainMax, 80) {
		t.Fatalf("sizing window %d main %d protected %d for capacity %d", p.windowMax, p.mainMax, p.protectedMax, c)
	}
	if p.total > c || p.weights[window] > p.windowMax || p.weights[protected] > p.protectedMax {
		t.Fatalf("over limits: total %d/%d window %d/%d protected %d/%d",
			p.total, c, p.weights[window], p.windowMax, p.weights[protected], p.protectedMax)
	}
	if c == 0 && p.count != 0 {
		t.Fatalf("capacity 0 retains %d nodes", p.count)
	}
}

// queueKeys returns the keys of queue q from front to back.
func queueKeys(p *Policy[int, int], q uint32) []int {
	var keys []int
	for x := p.queues[q].Front(); x != nil; x = x.Next(node.PolicyList) {
		keys = append(keys, x.Key)
	}
	return keys
}

// zipfTrace returns n keys drawn from a Zipf distribution over keys [0, keys) with exponent s.
func zipfTrace(seed uint64, s float64, keys, n int) []int {
	z := rand.NewZipf(rand.New(rand.NewPCG(seed, seed)), s, 1, uint64(keys-1))
	trace := make([]int, n)
	for i := range trace {
		trace[i] = int(z.Uint64())
	}
	return trace
}

// scanTrace interleaves a Zipf trace with a sequential scan of scanLen never repeated keys every period requests.
func scanTrace(seed uint64, keys, n, period, scanLen int) []int {
	base := zipfTrace(seed, 1.01, keys, n)
	trace := make([]int, 0, n+n/period*scanLen)
	next := keys
	for i, k := range base {
		trace = append(trace, k)
		if (i+1)%period != 0 {
			continue
		}
		for range scanLen {
			trace = append(trace, next)
			next++
		}
	}
	return trace
}

// simulate replays trace against p as a cache of unit weight entries, returning the hit ratio. If check is set the
// invariants are asserted after every operation.
func simulate(t testing.TB, p *Policy[int, int], trace []int, check bool) float64 {
	t.Helper()
	nodes := make(map[int]*testNode)
	evict := func(n *testNode) { delete(nodes, n.Key) }
	hits := 0
	for _, k := range trace {
		if n, ok := nodes[k]; ok {
			hits++
			p.Access(n)
		} else {
			n := newNode(k, 1)
			nodes[k] = n
			p.Add(n, evict)
		}
		if check {
			checkInvariants(t, p)
		}
	}
	return float64(hits) / float64(len(trace))
}
