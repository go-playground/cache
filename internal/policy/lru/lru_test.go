package lru

import (
	"hash/maphash"
	"slices"
	"testing"

	"github.com/go-playground/cache/v2/internal/node"
	"github.com/go-playground/cache/v2/internal/policy"
)

var (
	_ policy.Policy[int, int] = (*Policy[int, int])(nil)

	seed = maphash.MakeSeed()
)

func hashOf(key int) uint64 {
	return maphash.Comparable(seed, key)
}

func mk(key int, weight uint32) *node.Node[int, int] {
	return node.New(key, key, hashOf(key), weight)
}

// recorder collects the keys of evicted nodes in order.
type recorder struct {
	keys []int
}

func (r *recorder) evict(n *node.Node[int, int]) {
	r.keys = append(r.keys, n.Key)
}

func (r *recorder) take() []int {
	k := r.keys
	r.keys = nil
	return k
}

// contents returns the tracked keys from most to least recently used.
func contents(p *Policy[int, int]) []int {
	var keys []int
	for n := p.q.Front(); n != nil; n = n.Next(node.PolicyList) {
		keys = append(keys, n.Key)
	}
	return keys
}

// checkInvariants verifies weight accounting, link symmetry, reachability and capacity.
func checkInvariants(t testing.TB, p *Policy[int, int]) {
	t.Helper()
	seen := make(map[*node.Node[int, int]]bool)
	var weight uint64
	var prev *node.Node[int, int]
	for n := p.q.Front(); n != nil; n = n.Next(node.PolicyList) {
		if seen[n] {
			t.Fatalf("node %d reachable twice", n.Key)
		}
		seen[n] = true
		if n.Prev(node.PolicyList) != prev {
			t.Fatalf("node %d prev link asymmetric", n.Key)
		}
		if p.capacity == 0 || uint64(n.Weight) > p.capacity {
			t.Fatalf("node %d with weight %d retained at capacity %d", n.Key, n.Weight, p.capacity)
		}
		weight += uint64(n.Weight)
		prev = n
	}
	if p.q.Back() != prev {
		t.Fatal("back does not match last reachable node")
	}
	if p.q.Len() != len(seen) {
		t.Fatalf("len %d, reachable %d", p.q.Len(), len(seen))
	}
	if weight != p.Weight() || weight > p.capacity {
		t.Fatalf("weight %d, sum %d, capacity %d", p.Weight(), weight, p.capacity)
	}
}

func assertKeys(t *testing.T, what string, got, want []int) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

func addAll(p *Policy[int, int], r *recorder, keys ...int) map[int]*node.Node[int, int] {
	nodes := make(map[int]*node.Node[int, int])
	for _, k := range keys {
		nodes[k] = mk(k, 1)
		p.Add(nodes[k], r.evict)
	}
	return nodes
}

func TestEvictsLeastRecentlyAdded(t *testing.T) {
	p := New[int, int](3)
	var r recorder
	addAll(p, &r, 1, 2, 3, 4, 5)
	assertKeys(t, "evicted", r.take(), []int{1, 2})
	assertKeys(t, "contents", contents(p), []int{5, 4, 3})
	checkInvariants(t, p)
}

func TestAccessProtectsFromEviction(t *testing.T) {
	p := New[int, int](3)
	var r recorder
	nodes := addAll(p, &r, 1, 2, 3)
	p.Access(nodes[1])
	assertKeys(t, "contents", contents(p), []int{1, 3, 2})
	p.Add(mk(4, 1), r.evict)
	assertKeys(t, "evicted", r.take(), []int{2})
	checkInvariants(t, p)
}

func TestUpdateReplacesAndMovesToFront(t *testing.T) {
	p := New[int, int](4)
	var r recorder
	nodes := addAll(p, &r, 1, 2, 3)
	n := mk(1, 2)
	p.Update(nodes[1], n, r.evict)
	assertKeys(t, "contents", contents(p), []int{1, 3, 2})
	if p.q.Front() != n {
		t.Fatal("new node not at front")
	}
	if p.Weight() != 4 {
		t.Fatalf("weight %d, want 4", p.Weight())
	}
	assertKeys(t, "evicted", r.take(), nil)
	checkInvariants(t, p)
}

func TestUpdateGrowingWeightEvictsOthers(t *testing.T) {
	p := New[int, int](4)
	var r recorder
	nodes := addAll(p, &r, 1, 2, 3, 4)
	p.Update(nodes[2], mk(2, 3), r.evict)
	assertKeys(t, "evicted", r.take(), []int{1, 3})
	assertKeys(t, "contents", contents(p), []int{2, 4})
	checkInvariants(t, p)
}

func TestUpdateToHeavyEvictsNewNode(t *testing.T) {
	p := New[int, int](3)
	var r recorder
	nodes := addAll(p, &r, 1, 2)
	p.Update(nodes[1], mk(1, 4), r.evict)
	assertKeys(t, "evicted", r.take(), []int{1})
	assertKeys(t, "contents", contents(p), []int{2})
	if p.Weight() != 1 {
		t.Fatalf("weight %d, want 1", p.Weight())
	}
	checkInvariants(t, p)
}

func TestHeavyAddRejectedWithoutDisturbingOthers(t *testing.T) {
	p := New[int, int](3)
	var r recorder
	addAll(p, &r, 1, 2)
	p.Add(mk(9, 4), r.evict)
	assertKeys(t, "evicted", r.take(), []int{9})
	assertKeys(t, "contents", contents(p), []int{2, 1})
	checkInvariants(t, p)
}

func TestCapacityZeroRetainsNothing(t *testing.T) {
	p := New[int, int](0)
	var r recorder
	p.Add(mk(1, 1), r.evict)
	p.Add(mk(2, 0), r.evict)
	assertKeys(t, "evicted", r.take(), []int{1, 2})
	if p.q.Len() != 0 || p.Weight() != 0 {
		t.Fatal("capacity 0 retained a node")
	}
}

func TestCapacityOneKeepsLatest(t *testing.T) {
	p := New[int, int](1)
	var r recorder
	addAll(p, &r, 1, 2, 3)
	assertKeys(t, "evicted", r.take(), []int{1, 2})
	assertKeys(t, "contents", contents(p), []int{3})
	checkInvariants(t, p)
}

func TestWeightedAddEvictsUntilFits(t *testing.T) {
	p := New[int, int](10)
	var r recorder
	for k, w := range []uint32{3, 3, 3, 1} {
		p.Add(mk(k, w), r.evict)
	}
	p.Add(mk(9, 6), r.evict)
	assertKeys(t, "evicted", r.take(), []int{0, 1})
	assertKeys(t, "contents", contents(p), []int{9, 3, 2})
	if p.Weight() != 10 {
		t.Fatalf("weight %d, want 10", p.Weight())
	}
	checkInvariants(t, p)
}

func TestRemove(t *testing.T) {
	p := New[int, int](3)
	var r recorder
	nodes := addAll(p, &r, 1, 2, 3)
	p.Remove(nodes[2])
	assertKeys(t, "contents", contents(p), []int{3, 1})
	if p.Weight() != 2 {
		t.Fatalf("weight %d, want 2", p.Weight())
	}
	p.Add(mk(4, 1), r.evict)
	assertKeys(t, "evicted", r.take(), nil)
	checkInvariants(t, p)
}

func TestClearCallsFnForAllAndResetsWeight(t *testing.T) {
	p := New[int, int](3)
	var r recorder
	addAll(p, &r, 1, 2, 3)
	var cleared []int
	p.Clear(func(n *node.Node[int, int]) { cleared = append(cleared, n.Key) })
	assertKeys(t, "cleared", cleared, []int{3, 2, 1})
	if p.Weight() != 0 || p.q.Len() != 0 {
		t.Fatal("clear left state behind")
	}
	addAll(p, &r, 4)
	assertKeys(t, "contents", contents(p), []int{4})
	checkInvariants(t, p)
}

// steady returns a full policy of weight 1 nodes and a function which adds a spare node, evicting another, without
// allocating.
func steady(capacity int) (*Policy[int, int], func()) {
	p := New[int, int](uint64(capacity))
	spare := make([]*node.Node[int, int], 0, 4)
	evict := policy.EvictFunc[int, int](func(n *node.Node[int, int]) { spare = append(spare, n) })
	for k := range capacity {
		p.Add(mk(k, 1), evict)
	}
	spare = append(spare, mk(capacity, 1))
	return p, func() {
		n := spare[len(spare)-1]
		spare = spare[:len(spare)-1]
		p.Add(n, evict)
	}
}

func TestAllocs(t *testing.T) {
	p, add := steady(64)
	if a := testing.AllocsPerRun(1000, add); a != 0 {
		t.Fatalf("Add with eviction allocated %v per op", a)
	}
	n := p.q.Back()
	if a := testing.AllocsPerRun(1000, func() {
		p.Access(n)
		n = p.q.Back()
	}); a != 0 {
		t.Fatalf("Access allocated %v per op", a)
	}
}

func BenchmarkAdd(b *testing.B) {
	_, add := steady(1024)
	b.ReportAllocs()
	for b.Loop() {
		add()
	}
}

func BenchmarkAccess(b *testing.B) {
	p, _ := steady(1024)
	b.ReportAllocs()
	for b.Loop() {
		p.Access(p.q.Back())
	}
}
