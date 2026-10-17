package s3fifo

import (
	"hash/maphash"
	"slices"
	"sync"
	"testing"

	"github.com/go-playground/cache/v2/internal/deque"
	"github.com/go-playground/cache/v2/internal/node"
	"github.com/go-playground/cache/v2/internal/policy"
)

var (
	_ policy.Policy[int, int]         = (*Policy[int, int])(nil)
	_ policy.InlineAccessor[int, int] = (*Policy[int, int])(nil)

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

// kf is a queued key and its frequency.
type kf struct {
	key  int
	freq uint32
}

// snapshot is the observable state: both queues front to back and the ghost's occupied slots (see ghost.encoded).
type snapshot struct {
	small []kf
	main  []kf
	ghost []uint64
}

func (s snapshot) equal(o snapshot) bool {
	return slices.Equal(s.small, o.small) && slices.Equal(s.main, o.main) && slices.Equal(s.ghost, o.ghost)
}

func queueContents(q *deque.Deque[int, int]) []kf {
	var keys []kf
	for n := q.Front(); n != nil; n = n.Next() {
		keys = append(keys, kf{n.Key, n.Meta.Load()})
	}
	return keys
}

func contents(p *Policy[int, int]) snapshot {
	return snapshot{small: queueContents(&p.small), main: queueContents(&p.main), ghost: p.ghost.encoded()}
}

// checkInvariants verifies queue links, slots, frequencies, per queue weights, capacity and the ghost structure.
func checkInvariants(t testing.TB, p *Policy[int, int]) {
	t.Helper()
	seen := make(map[*node.Node[int, int]]bool)
	sw := checkQueue(t, p, &p.small, small, seen)
	mw := checkQueue(t, p, &p.main, main, seen)
	if sw != p.smallWeight || mw != p.mainWeight {
		t.Fatalf("weights small %d main %d, sums %d %d", p.smallWeight, p.mainWeight, sw, mw)
	}
	if p.Weight() > p.capacity {
		t.Fatalf("weight %d over capacity %d", p.Weight(), p.capacity)
	}
	checkGhost(t, &p.ghost)
}

func checkQueue(t testing.TB, p *Policy[int, int], q *deque.Deque[int, int], slot uint32, seen map[*node.Node[int, int]]bool) uint64 {
	t.Helper()
	var weight uint64
	var prev *node.Node[int, int]
	count := 0
	for n := q.Front(); n != nil; n = n.Next() {
		switch {
		case seen[n]:
			t.Fatalf("node %d reachable twice", n.Key)
		case n.Prev() != prev:
			t.Fatalf("node %d prev link asymmetric", n.Key)
		case n.Slot() != slot:
			t.Fatalf("node %d slot %d in queue %d", n.Key, n.Slot(), slot)
		case n.Meta.Load() > maxFreq:
			t.Fatalf("node %d frequency %d", n.Key, n.Meta.Load())
		case p.capacity == 0 || uint64(n.Weight) > p.capacity:
			t.Fatalf("node %d with weight %d retained at capacity %d", n.Key, n.Weight, p.capacity)
		}
		seen[n] = true
		weight += uint64(n.Weight)
		prev = n
		count++
	}
	if q.Back() != prev || q.Len() != count {
		t.Fatalf("queue %d back or len inconsistent", slot)
	}
	return weight
}

func checkGhost(t testing.TB, g *ghost) {
	t.Helper()
	if n := len(g.slots); n&(n-1) != 0 || (n > 0 && g.mask != uint64(n-1)) {
		t.Fatalf("ghost size %d mask %x inconsistent", n, g.mask)
	}
	for i, fp := range g.slots {
		if fp != 0 && fp&1 == 0 {
			t.Fatalf("ghost slot %d holds invalid fingerprint %x", i, fp)
		}
	}
}

func assertContents(t *testing.T, p *Policy[int, int], want snapshot) {
	t.Helper()
	if got := contents(p); !got.equal(want) {
		t.Fatalf("contents %+v, want %+v", got, want)
	}
	checkInvariants(t, p)
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

func access(p *Policy[int, int], n *node.Node[int, int], times int) {
	for range times {
		p.InlineAccess(n)
	}
}

func TestSmallQueueTargetsTenthOfCapacity(t *testing.T) {
	for c, want := range map[uint64]uint64{0: 1, 5: 1, 10: 1, 25: 2, 100: 10} {
		if got := New[int, int](c).smallMax; got != want {
			t.Errorf("capacity %d: smallMax %d, want %d", c, got, want)
		}
	}
}

func TestNewNodesEnterSmallAtFrequencyZero(t *testing.T) {
	p := New[int, int](10)
	var r recorder
	n := mk(1, 1)
	n.Meta.Store(3)
	p.Add(n, r.evict)
	addAll(p, &r, 2)
	assertContents(t, p, snapshot{small: []kf{{2, 0}, {1, 0}}})
}

func TestSmallEvictsOneHitWondersIntoGhost(t *testing.T) {
	p := New[int, int](3)
	var r recorder
	nodes := addAll(p, &r, 1, 2, 3)
	access(p, nodes[1], 1)
	p.Add(mk(4, 1), r.evict)
	assertKeys(t, "evicted", r.take(), []int{1})
	assertContents(t, p, snapshot{small: []kf{{4, 0}, {3, 0}, {2, 0}}, ghost: ghostOf(p, hashOf(1))})
}

func TestGhostHitReadmitsIntoMain(t *testing.T) {
	p := New[int, int](3)
	var r recorder
	addAll(p, &r, 1, 2, 3, 4)
	assertKeys(t, "evicted", r.take(), []int{1})
	p.Add(mk(1, 1), r.evict)
	assertKeys(t, "evicted", r.take(), []int{2})
	assertContents(t, p, snapshot{small: []kf{{4, 0}, {3, 0}}, main: []kf{{1, 0}}, ghost: ghostOf(p, hashOf(2))})
}

func TestPromotionToMainRequiresFrequencyAboveOne(t *testing.T) {
	p := New[int, int](3)
	var r recorder
	nodes := addAll(p, &r, 1, 2, 3)
	access(p, nodes[1], 1)
	access(p, nodes[2], 2)
	p.Add(mk(4, 1), r.evict)
	assertKeys(t, "evicted", r.take(), []int{1})
	p.Add(mk(5, 1), r.evict)
	assertKeys(t, "evicted", r.take(), []int{3})
	assertContents(t, p, snapshot{
		small: []kf{{5, 0}, {4, 0}},
		main:  []kf{{2, 0}},
		ghost: ghostOf(p, hashOf(1), hashOf(3)),
	})
}

func TestMainReinsertsAccessedNodesWithDecrement(t *testing.T) {
	p := New[int, int](30)
	var r recorder
	nodes := make(map[int]*node.Node[int, int])
	for _, k := range []int{1, 2, 3} {
		nodes[k] = mk(k, 10)
		p.Add(nodes[k], r.evict)
		access(p, nodes[k], 2)
	}
	p.Add(mk(4, 1), r.evict)
	assertKeys(t, "evicted", r.take(), []int{4})
	assertContents(t, p, snapshot{main: []kf{{3, 0}, {2, 0}, {1, 0}}, ghost: ghostOf(p, hashOf(4))})

	access(p, nodes[1], 1)
	access(p, nodes[2], 2)
	p.Add(mk(5, 1), r.evict)
	assertKeys(t, "evicted", r.take(), []int{3})
	assertContents(t, p, snapshot{small: []kf{{5, 0}}, main: []kf{{2, 1}, {1, 0}}, ghost: ghostOf(p, hashOf(4))})
}

func TestFrequencySaturatesAtThree(t *testing.T) {
	p := New[int, int](3)
	var r recorder
	nodes := addAll(p, &r, 1)
	access(p, nodes[1], 5)
	p.Access(nodes[1])
	if f := nodes[1].Meta.Load(); f != 3 {
		t.Fatalf("frequency %d, want 3", f)
	}
}

func TestInlineAccessIsConcurrencySafe(t *testing.T) {
	p := New[int, int](3)
	var r recorder
	nodes := addAll(p, &r, 1)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { access(p, nodes[1], 100) })
	}
	wg.Wait()
	if f := nodes[1].Meta.Load(); f != 3 {
		t.Fatalf("frequency %d, want 3", f)
	}
}

func TestUpdateReplacesInPlaceAndCountsAsAccess(t *testing.T) {
	p := New[int, int](10)
	var r recorder
	nodes := addAll(p, &r, 1, 2, 3)
	n := mk(2, 2)
	p.Update(nodes[2], n, r.evict)
	assertContents(t, p, snapshot{small: []kf{{3, 0}, {2, 1}, {1, 0}}})
	access(p, n, 3)
	m := mk(2, 1)
	p.Update(n, m, r.evict)
	assertContents(t, p, snapshot{small: []kf{{3, 0}, {2, 3}, {1, 0}}})
	if p.Weight() != 3 {
		t.Fatalf("weight %d, want 3", p.Weight())
	}
}

func TestUpdateInMainStaysInMain(t *testing.T) {
	p := New[int, int](2)
	var r recorder
	addAll(p, &r, 1, 2, 3)
	nodes := addAll(p, &r, 1)
	assertKeys(t, "evicted", r.take(), []int{1, 2})
	p.Update(nodes[1], mk(1, 1), r.evict)
	assertContents(t, p, snapshot{small: []kf{{3, 0}}, main: []kf{{1, 1}}, ghost: ghostOf(p, hashOf(2))})
}

func TestUpdateOverCapacityEvicts(t *testing.T) {
	p := New[int, int](4)
	var r recorder
	nodes := addAll(p, &r, 1, 2, 3)
	p.Update(nodes[3], mk(3, 3), r.evict)
	assertKeys(t, "evicted", r.take(), []int{1})
	assertContents(t, p, snapshot{small: []kf{{3, 1}, {2, 0}}, ghost: ghostOf(p, hashOf(1))})
}

func TestUpdateToHeavyEvictsNewNode(t *testing.T) {
	p := New[int, int](3)
	var r recorder
	nodes := addAll(p, &r, 1, 2)
	p.Update(nodes[1], mk(1, 4), r.evict)
	assertKeys(t, "evicted", r.take(), []int{1})
	assertContents(t, p, snapshot{small: []kf{{2, 0}}})
}

func TestHeavyAddRejectedWithoutEvictingOthers(t *testing.T) {
	p := New[int, int](3)
	var r recorder
	addAll(p, &r, 1, 2, 3)
	p.Add(mk(9, 4), r.evict)
	assertKeys(t, "evicted", r.take(), []int{9})
	assertContents(t, p, snapshot{small: []kf{{3, 0}, {2, 0}, {1, 0}}})
}

func TestCapacityZeroRetainsNothing(t *testing.T) {
	p := New[int, int](0)
	var r recorder
	p.Add(mk(1, 1), r.evict)
	p.Add(mk(2, 0), r.evict)
	assertKeys(t, "evicted", r.take(), []int{1, 2})
	assertContents(t, p, snapshot{})
}

func TestCapacityOneKeepsLatest(t *testing.T) {
	p := New[int, int](1)
	var r recorder
	addAll(p, &r, 1, 2, 3)
	assertKeys(t, "evicted", r.take(), []int{1, 2})
	assertContents(t, p, snapshot{small: []kf{{3, 0}}, ghost: ghostOf(p, hashOf(2))})
}

func TestWeightedAddEvictsUntilFits(t *testing.T) {
	p := New[int, int](10)
	var r recorder
	for k, w := range []uint32{3, 3, 3, 1} {
		p.Add(mk(k, w), r.evict)
	}
	p.Add(mk(9, 6), r.evict)
	assertKeys(t, "evicted", r.take(), []int{0, 1})
	assertContents(t, p, snapshot{small: []kf{{9, 0}, {3, 0}, {2, 0}}, ghost: ghostOf(p, hashOf(0), hashOf(1))})
}

// TestGhostIsBoundedByTrackedCount checks the ghost never holds more than nextPow2 of the tracked entry count, so its
// memory is 4-8 bytes per entry regardless of how many keys have been evicted.
func TestGhostIsBoundedByTrackedCount(t *testing.T) {
	p := New[int, int](100)
	var r recorder
	for k := range 100_000 {
		p.Add(mk(k, 1), r.evict)
	}
	if size := len(p.ghost.slots); size > 128 {
		t.Fatalf("ghost has %d slots for 100 tracked entries", size)
	}
	if n := len(p.ghost.encoded()); n == 0 || n > len(p.ghost.slots) {
		t.Fatalf("ghost holds %d entries in %d slots", n, len(p.ghost.slots))
	}
}

func TestGhostMatchesByHash(t *testing.T) {
	p := New[int, int](1)
	var r recorder
	p.Add(node.New(1, 1, 42, 1), r.evict)
	p.Add(node.New(2, 2, 7, 1), r.evict)
	p.Add(node.New(3, 3, 42, 1), r.evict)
	assertKeys(t, "evicted", r.take(), []int{1, 2})
	assertContents(t, p, snapshot{main: []kf{{3, 0}}, ghost: ghostOf(p, 7)})
}

func TestRemoveDoesNotEnterGhost(t *testing.T) {
	p := New[int, int](3)
	var r recorder
	nodes := addAll(p, &r, 1, 2, 3)
	p.Remove(nodes[2])
	assertContents(t, p, snapshot{small: []kf{{3, 0}, {1, 0}}})
	addAll(p, &r, 2)
	assertKeys(t, "evicted", r.take(), nil)
	assertContents(t, p, snapshot{small: []kf{{2, 0}, {3, 0}, {1, 0}}})
}

func TestClearCallsFnForAllAndForgetsGhost(t *testing.T) {
	p := New[int, int](2)
	var r recorder
	addAll(p, &r, 1, 2, 3)
	addAll(p, &r, 1)
	assertContents(t, p, snapshot{small: []kf{{3, 0}}, main: []kf{{1, 0}}, ghost: ghostOf(p, hashOf(2))})
	var cleared []int
	p.Clear(func(n *node.Node[int, int]) { cleared = append(cleared, n.Key) })
	assertKeys(t, "cleared", cleared, []int{3, 1})
	if p.Weight() != 0 {
		t.Fatalf("weight %d after clear", p.Weight())
	}
	assertContents(t, p, snapshot{})
	addAll(p, &r, 2)
	assertContents(t, p, snapshot{small: []kf{{2, 0}}})
}

// steady returns a full policy of weight 1 nodes and a function which adds a spare node, evicting another, without
// allocating once warm.
func steady(capacity int) (*Policy[int, int], []*node.Node[int, int], func()) {
	p := New[int, int](uint64(capacity))
	spare := make([]*node.Node[int, int], 0, 4)
	evict := policy.EvictFunc[int, int](func(n *node.Node[int, int]) { spare = append(spare, n) })
	nodes := make([]*node.Node[int, int], capacity+1)
	for k := range nodes {
		nodes[k] = mk(k, 1)
		p.Add(nodes[k], evict)
	}
	i := 0
	return p, nodes, func() {
		n := spare[len(spare)-1]
		spare = spare[:len(spare)-1]
		if i++; i%3 == 0 {
			p.InlineAccess(p.small.Front())
			p.InlineAccess(p.small.Front())
		}
		p.Add(n, evict)
	}
}

func TestAllocs(t *testing.T) {
	p, nodes, add := steady(64)
	for range 10_000 {
		add()
	}
	if a := testing.AllocsPerRun(10_000, add); a != 0 {
		t.Fatalf("Add with eviction allocated %v per op", a)
	}
	checkInvariants(t, p)
	n := nodes[0]
	if a := testing.AllocsPerRun(1000, func() { p.Access(n) }); a != 0 {
		t.Fatalf("Access allocated %v per op", a)
	}
}

func BenchmarkAdd(b *testing.B) {
	_, _, add := steady(1024)
	b.ReportAllocs()
	for b.Loop() {
		add()
	}
}

func BenchmarkAccess(b *testing.B) {
	p, nodes, _ := steady(1024)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		p.InlineAccess(nodes[i&1023])
		i++
	}
}

func BenchmarkInlineAccessParallelHot(b *testing.B) {
	p, nodes, _ := steady(1024)
	n := nodes[0]
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			p.InlineAccess(n)
		}
	})
}
