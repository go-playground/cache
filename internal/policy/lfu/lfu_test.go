package lfu

import (
	"fmt"
	"hash/maphash"
	"math"
	"slices"
	"testing"

	"github.com/go-playground/cache/v2/internal/node"
	"github.com/go-playground/cache/v2/internal/policy"
)

var (
	_ policy.Policy[int, int] = (*Policy[int, int])(nil)

	seed = maphash.MakeSeed()
)

func mk(key int, weight uint32) *node.Node[int, int] {
	return node.New(key, key, maphash.Comparable(seed, key), weight)
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

// level is a frequency bucket's contents, front to back.
type level struct {
	freq uint32
	keys []int
}

func (l level) String() string {
	return fmt.Sprintf("%d:%v", l.freq, l.keys)
}

func equalLevels(a, b []level) bool {
	return slices.EqualFunc(a, b, func(x, y level) bool { return x.freq == y.freq && slices.Equal(x.keys, y.keys) })
}

// contents returns the buckets from lowest to highest frequency.
func contents(p *Policy[int, int]) []level {
	var levels []level
	for i := p.head; i != none; i = p.b[i].next {
		l := level{freq: p.b[i].freq}
		for n := p.b[i].q.Front(); n != nil; n = n.Next() {
			l.keys = append(l.keys, n.Key)
		}
		levels = append(levels, l)
	}
	return levels
}

// rootOf resolves a label without the path compression find performs.
func rootOf(t testing.TB, p *Policy[int, int], l int32) int32 {
	t.Helper()
	for range len(p.b) + 1 {
		if p.b[l].parent == l {
			return l
		}
		l = p.b[l].parent
	}
	t.Fatalf("label %d has a forwarding cycle", l)
	return none
}

// checkInvariants verifies the frequency list, node links, label reference counts, weight and capacity.
func checkInvariants(t testing.TB, p *Policy[int, int]) {
	t.Helper()
	free := checkFreeList(t, p)
	refs := make([]uint32, len(p.b))
	var weight uint64
	nodes := make(map[*node.Node[int, int]]bool)
	prev := none
	for i := p.head; i != none; i = p.b[i].next {
		b := &p.b[i]
		switch {
		case free[i]:
			t.Fatalf("bucket %d is linked and free", i)
		case b.prev != prev:
			t.Fatalf("bucket %d prev %d, want %d", i, b.prev, prev)
		case b.parent != i:
			t.Fatalf("linked bucket %d is not a root", i)
		case b.q.Len() == 0:
			t.Fatalf("bucket %d is empty", i)
		case b.freq == 0 || (prev != none && p.b[prev].freq >= b.freq):
			t.Fatalf("bucket %d frequency %d out of order", i, b.freq)
		}
		weight += checkBucket(t, p, i, nodes, refs)
		prev = i
	}
	if p.tail != prev {
		t.Fatalf("tail %d, want %d", p.tail, prev)
	}
	checkLabels(t, p, free, refs)
	if weight != p.Weight() || weight > p.capacity {
		t.Fatalf("weight %d, sum %d, capacity %d", p.Weight(), weight, p.capacity)
	}
}

func checkFreeList(t testing.TB, p *Policy[int, int]) map[int32]bool {
	t.Helper()
	free := make(map[int32]bool)
	for i := p.free; i != none; i = p.b[i].next {
		if free[i] {
			t.Fatalf("free list cycle at %d", i)
		}
		free[i] = true
	}
	return free
}

func checkBucket(t testing.TB, p *Policy[int, int], i int32, nodes map[*node.Node[int, int]]bool, refs []uint32) uint64 {
	t.Helper()
	var weight uint64
	var prev *node.Node[int, int]
	count := 0
	for n := p.b[i].q.Front(); n != nil; n = n.Next() {
		switch {
		case nodes[n]:
			t.Fatalf("node %d reachable twice", n.Key)
		case n.Prev() != prev:
			t.Fatalf("node %d prev link asymmetric", n.Key)
		case int(n.Slot()) >= len(p.b) || rootOf(t, p, int32(n.Slot())) != i:
			t.Fatalf("node %d slot %d does not resolve to bucket %d", n.Key, n.Slot(), i)
		case p.capacity == 0 || uint64(n.Weight) > p.capacity:
			t.Fatalf("node %d with weight %d retained at capacity %d", n.Key, n.Weight, p.capacity)
		}
		nodes[n] = true
		refs[n.Slot()]++
		weight += uint64(n.Weight)
		prev = n
		count++
	}
	if p.b[i].q.Back() != prev || p.b[i].q.Len() != count {
		t.Fatalf("bucket %d back or len inconsistent", i)
	}
	return weight
}

func checkLabels(t testing.TB, p *Policy[int, int], free map[int32]bool, refs []uint32) {
	t.Helper()
	for i := range int32(len(p.b)) {
		if free[i] {
			continue
		}
		if parent := p.b[i].parent; parent != i {
			refs[parent]++
			if p.b[i].q.Len() != 0 {
				t.Fatalf("forwarding label %d holds nodes", i)
			}
		}
	}
	for i := range int32(len(p.b)) {
		b := &p.b[i]
		switch {
		case free[i] && b.refs != 0:
			t.Fatalf("free label %d has %d refs", i, b.refs)
		case !free[i] && b.refs == 0:
			t.Fatalf("label %d leaked: allocated with no refs", i)
		case b.refs != refs[i]:
			t.Fatalf("label %d refs %d, counted %d", i, b.refs, refs[i])
		}
	}
}

func assertLevels(t *testing.T, p *Policy[int, int], want ...level) {
	t.Helper()
	if got := contents(p); !equalLevels(got, want) {
		t.Fatalf("contents %v, want %v", got, want)
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
		p.Access(n)
	}
}

func TestNewNodesStartAtFrequencyOne(t *testing.T) {
	p := New[int, int](5, 0)
	var r recorder
	addAll(p, &r, 1, 2, 3)
	assertLevels(t, p, level{1, []int{3, 2, 1}})
}

func TestAccessMovesToFrontOfNextBucket(t *testing.T) {
	p := New[int, int](5, 0)
	var r recorder
	nodes := addAll(p, &r, 1, 2, 3)
	p.Access(nodes[1])
	p.Access(nodes[2])
	assertLevels(t, p, level{1, []int{3}}, level{2, []int{2, 1}})
	p.Access(nodes[1])
	assertLevels(t, p, level{1, []int{3}}, level{2, []int{2}}, level{3, []int{1}})
	p.Access(nodes[3])
	assertLevels(t, p, level{2, []int{3, 2}}, level{3, []int{1}})
}

func TestAccessCreatesBucketBetweenGaps(t *testing.T) {
	p := New[int, int](5, 0)
	var r recorder
	nodes := addAll(p, &r, 1, 2)
	access(p, nodes[1], 3)
	p.Access(nodes[2])
	assertLevels(t, p, level{2, []int{2}}, level{4, []int{1}})
}

func TestVictimIsOldestOfLeastFrequent(t *testing.T) {
	p := New[int, int](3, 0)
	var r recorder
	nodes := addAll(p, &r, 1, 2, 3)
	access(p, nodes[1], 2)
	p.Access(nodes[2])
	p.Add(mk(4, 1), r.evict)
	assertKeys(t, "evicted", r.take(), []int{3})
	p.Add(mk(5, 1), r.evict)
	assertKeys(t, "evicted", r.take(), []int{4})
	p.Add(mk(6, 1), r.evict)
	assertKeys(t, "evicted", r.take(), []int{5})
	assertLevels(t, p, level{1, []int{6}}, level{2, []int{2}}, level{3, []int{1}})
}

func TestNewNodeIsNeverItsOwnVictim(t *testing.T) {
	p := New[int, int](2, 0)
	var r recorder
	nodes := addAll(p, &r, 1, 2)
	access(p, nodes[1], 1)
	access(p, nodes[2], 1)
	p.Add(mk(3, 1), r.evict)
	assertKeys(t, "evicted", r.take(), []int{1})
	assertLevels(t, p, level{1, []int{3}}, level{2, []int{2}})
}

func TestUpdateKeepsFrequencyAndMovesToFrontOfBucket(t *testing.T) {
	p := New[int, int](5, 0)
	var r recorder
	nodes := addAll(p, &r, 1, 2, 3)
	p.Access(nodes[1])
	p.Access(nodes[2])
	n := mk(1, 2)
	p.Update(nodes[1], n, r.evict)
	assertKeys(t, "evicted", r.take(), nil)
	assertLevels(t, p, level{1, []int{3}}, level{2, []int{1, 2}})
	if p.Weight() != 4 {
		t.Fatalf("weight %d, want 4", p.Weight())
	}
}

func TestUpdateOverCapacityMayEvictUpdatedNode(t *testing.T) {
	p := New[int, int](4, 0)
	var r recorder
	nodes := addAll(p, &r, 1, 2, 3)
	p.Access(nodes[2])
	p.Access(nodes[3])
	p.Update(nodes[1], mk(1, 3), r.evict)
	assertKeys(t, "evicted", r.take(), []int{1})
	assertLevels(t, p, level{2, []int{3, 2}})
}

func TestUpdateToHeavyEvictsNewNode(t *testing.T) {
	p := New[int, int](3, 0)
	var r recorder
	nodes := addAll(p, &r, 1, 2)
	p.Update(nodes[1], mk(1, 4), r.evict)
	assertKeys(t, "evicted", r.take(), []int{1})
	assertLevels(t, p, level{1, []int{2}})
}

func TestHeavyAddRejectedWithoutEvictingOthers(t *testing.T) {
	p := New[int, int](3, 0)
	var r recorder
	addAll(p, &r, 1, 2, 3)
	p.Add(mk(9, 4), r.evict)
	assertKeys(t, "evicted", r.take(), []int{9})
	assertLevels(t, p, level{1, []int{3, 2, 1}})
}

func TestCapacityZeroRetainsNothing(t *testing.T) {
	p := New[int, int](0, 1)
	var r recorder
	p.Add(mk(1, 1), r.evict)
	p.Add(mk(2, 0), r.evict)
	assertKeys(t, "evicted", r.take(), []int{1, 2})
	assertLevels(t, p)
}

func TestCapacityOneKeepsLatest(t *testing.T) {
	p := New[int, int](1, 0)
	var r recorder
	nodes := addAll(p, &r, 1)
	access(p, nodes[1], 5)
	p.Add(mk(2, 1), r.evict)
	assertKeys(t, "evicted", r.take(), []int{1})
	assertLevels(t, p, level{1, []int{2}})
}

func TestWeightedAddEvictsUntilFits(t *testing.T) {
	p := New[int, int](10, 0)
	var r recorder
	nodes := make([]*node.Node[int, int], 4)
	for k, w := range []uint32{3, 3, 3, 1} {
		nodes[k] = mk(k, w)
		p.Add(nodes[k], r.evict)
	}
	p.Access(nodes[0])
	p.Add(mk(9, 6), r.evict)
	assertKeys(t, "evicted", r.take(), []int{1, 2})
	assertLevels(t, p, level{1, []int{9, 3}}, level{2, []int{0}})
}

func TestFrequencySaturates(t *testing.T) {
	p := New[int, int](3, 0)
	var r recorder
	nodes := addAll(p, &r, 1, 2)
	p.Access(nodes[1])
	p.Access(nodes[2])
	p.b[p.head].freq = math.MaxUint32 - 1
	p.Access(nodes[1])
	p.Access(nodes[2])
	assertLevels(t, p, level{math.MaxUint32, []int{2, 1}})
	p.Access(nodes[1])
	assertLevels(t, p, level{math.MaxUint32, []int{1, 2}})
}

func TestAgingHalvesFrequencies(t *testing.T) {
	p := New[int, int](5, 9)
	var r recorder
	nodes := addAll(p, &r, 1, 2)
	access(p, nodes[1], 8)
	assertLevels(t, p, level{1, []int{2}}, level{9, []int{1}})
	p.Access(nodes[1])
	assertLevels(t, p, level{1, []int{2}}, level{5, []int{1}})
}

func TestAgingMergesWithHigherFrequencyInFront(t *testing.T) {
	p := New[int, int](10, 4)
	var r recorder
	nodes := addAll(p, &r, 1, 2, 3, 4, 5, 6)
	p.Access(nodes[2])
	p.Access(nodes[3])
	p.Access(nodes[4])
	assertLevels(t, p, level{1, []int{6, 5, 1}}, level{2, []int{4, 3, 2}})
	p.Access(nodes[4])
	assertLevels(t, p, level{1, []int{4, 3, 2, 6, 5, 1}})
}

func TestAgingMergesPairsIntoSameFrequency(t *testing.T) {
	p := New[int, int](10, 0)
	var r recorder
	nodes := addAll(p, &r, 1, 2, 3, 4, 5)
	for k, n := range []int{0, 1, 3, 4, 5} {
		access(p, nodes[k+1], n)
	}
	assertLevels(t, p, level{1, []int{1}}, level{2, []int{2}}, level{4, []int{3}}, level{5, []int{4}}, level{6, []int{5}})
	p.age()
	assertLevels(t, p, level{1, []int{2, 1}}, level{2, []int{4, 3}}, level{3, []int{5}})
	p.age()
	assertLevels(t, p, level{1, []int{5, 4, 3, 2, 1}})
}

func TestAgingCadence(t *testing.T) {
	p := New[int, int](5, 3)
	var r recorder
	nodes := addAll(p, &r, 1)
	access(p, nodes[1], 2)
	assertLevels(t, p, level{3, []int{1}})
	p.Access(nodes[1])
	assertLevels(t, p, level{2, []int{1}})
	access(p, nodes[1], 3)
	assertLevels(t, p, level{2, []int{1}})
}

func TestMergedLabelsForwardAndAreFreed(t *testing.T) {
	p := New[int, int](10, 0)
	var r recorder
	nodes := addAll(p, &r, 1, 2, 3)
	p.Access(nodes[2])
	p.Access(nodes[3])
	p.age()
	assertLevels(t, p, level{1, []int{3, 2, 1}})
	if nodes[1].Slot() == nodes[2].Slot() {
		t.Fatal("expected merge to leave one side forwarding")
	}
	p.Access(nodes[1])
	p.Access(nodes[2])
	p.Access(nodes[3])
	assertLevels(t, p, level{2, []int{3, 2, 1}})
	live := len(p.b)
	for i := p.free; i != none; i = p.b[i].next {
		live--
	}
	if live != 1 {
		t.Fatalf("%d labels allocated, want 1", live)
	}
}

func TestRemove(t *testing.T) {
	p := New[int, int](3, 0)
	var r recorder
	nodes := addAll(p, &r, 1, 2, 3)
	p.Access(nodes[2])
	p.Remove(nodes[2])
	assertLevels(t, p, level{1, []int{3, 1}})
	p.Remove(nodes[1])
	p.Add(mk(4, 1), r.evict)
	assertKeys(t, "evicted", r.take(), nil)
	assertLevels(t, p, level{1, []int{4, 3}})
}

func TestClearCallsFnForAllAndResets(t *testing.T) {
	p := New[int, int](5, 2)
	var r recorder
	nodes := addAll(p, &r, 1, 2, 3)
	p.Access(nodes[2])
	var cleared []int
	p.Clear(func(n *node.Node[int, int]) { cleared = append(cleared, n.Key) })
	assertKeys(t, "cleared", cleared, []int{3, 1, 2})
	if p.Weight() != 0 || p.accesses != 0 {
		t.Fatal("clear left state behind")
	}
	nodes = addAll(p, &r, 4)
	p.Access(nodes[4])
	assertLevels(t, p, level{2, []int{4}})
}

// steady returns a full policy of weight 1 nodes and a function which adds a spare node, evicting another, without
// allocating.
func steady(capacity int, agingEvery uint64) (*Policy[int, int], []*node.Node[int, int], func()) {
	p := New[int, int](uint64(capacity), agingEvery)
	spare := make([]*node.Node[int, int], 0, 4)
	evict := policy.EvictFunc[int, int](func(n *node.Node[int, int]) { spare = append(spare, n) })
	nodes := make([]*node.Node[int, int], capacity)
	for k := range nodes {
		nodes[k] = mk(k, 1)
		p.Add(nodes[k], evict)
	}
	spare = append(spare, mk(capacity, 1))
	return p, nodes, func() {
		n := spare[len(spare)-1]
		spare = spare[:len(spare)-1]
		p.Add(n, evict)
	}
}

func TestAllocs(t *testing.T) {
	_, _, add := steady(64, 0)
	add()
	if a := testing.AllocsPerRun(1000, add); a != 0 {
		t.Fatalf("Add with eviction allocated %v per op", a)
	}
	p, nodes, _ := steady(64, 7)
	i := 0
	accessNext := func() {
		p.Access(nodes[i%len(nodes)])
		i += 1 + i%3
	}
	for range 1000 {
		accessNext()
	}
	if a := testing.AllocsPerRun(1000, accessNext); a != 0 {
		t.Fatalf("Access allocated %v per op", a)
	}
	checkInvariants(t, p)
}

func BenchmarkAdd(b *testing.B) {
	_, _, add := steady(1024, 0)
	b.ReportAllocs()
	for b.Loop() {
		add()
	}
}

func BenchmarkAccess(b *testing.B) {
	p, nodes, _ := steady(1024, 1024*10)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		p.Access(nodes[i&1023])
		i++
	}
}
