package tinylfu

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// fixture wraps a policy, tracking its nodes by key and recording evictions.
type fixture struct {
	t       *testing.T
	p       *Policy[int, int]
	nodes   map[int]*testNode
	evicted []int
}

func newFixture(t *testing.T, capacity uint64) *fixture {
	t.Helper()
	f := &fixture{t: t, p: New[int, int](capacity, false), nodes: make(map[int]*testNode)}
	f.p.rnd = func() uint32 {
		t.Fatal("rnd consulted")
		return 0
	}
	return f
}

func (f *fixture) evict(n *testNode) {
	f.evicted = append(f.evicted, n.Key)
	if f.nodes[n.Key] == n {
		delete(f.nodes, n.Key)
	}
}

func (f *fixture) add(key int, weight uint32) {
	f.t.Helper()
	n := newNode(key, weight)
	f.nodes[key] = n
	f.p.Add(n, f.evict)
	checkInvariants(f.t, f.p)
}

func (f *fixture) addAll(keys ...int) {
	f.t.Helper()
	for _, k := range keys {
		f.add(k, 1)
	}
}

func (f *fixture) access(keys ...int) {
	f.t.Helper()
	for _, k := range keys {
		f.p.Access(f.nodes[k])
		checkInvariants(f.t, f.p)
	}
}

func (f *fixture) update(key int, weight uint32) *testNode {
	f.t.Helper()
	old, n := f.nodes[key], newNode(key, weight)
	f.nodes[key] = n
	f.p.Update(old, n, f.evict)
	checkInvariants(f.t, f.p)
	return n
}

func (f *fixture) remove(key int) {
	f.t.Helper()
	f.p.Remove(f.nodes[key])
	delete(f.nodes, key)
	checkInvariants(f.t, f.p)
}

// bump increments the sketch for key until its estimated frequency is at least freq.
func (f *fixture) bump(key int, freq uint8) {
	for f.p.sketch.Frequency(hashOf(key)) < freq {
		f.p.sketch.Increment(hashOf(key))
	}
}

func (f *fixture) expect(q uint32, keys ...int) {
	f.t.Helper()
	if got := queueKeys(f.p, q); !slices.Equal(got, keys) {
		f.t.Fatalf("queue %d = %v, want %v", q, got, keys)
	}
}

func (f *fixture) expectEvicted(keys ...int) {
	f.t.Helper()
	if !slices.Equal(f.evicted, keys) {
		f.t.Fatalf("evicted %v, want %v", f.evicted, keys)
	}
}

func seq(from, to int) []int {
	var keys []int
	for k := from; k < to; k++ {
		keys = append(keys, k)
	}
	return keys
}

func reversed(keys []int) []int {
	keys = slices.Clone(keys)
	slices.Reverse(keys)
	return keys
}

func TestNewSizing(t *testing.T) {
	tests := []struct{ capacity, window, main, protected uint64 }{
		{0, 0, 0, 0},
		{1, 0, 1, 0},
		{99, 0, 99, 79},
		{100, 1, 99, 79},
		{1000, 10, 990, 792},
		{1<<64 - 1, 184467440737095516, 18262276632972456099, 14609821306377964879},
	}
	for _, tt := range tests {
		p := New[int, int](tt.capacity, true)
		if p.windowMax != tt.window || p.mainMax != tt.main || p.protectedMax != tt.protected {
			t.Errorf("capacity %d: window %d main %d protected %d, want %d %d %d",
				tt.capacity, p.windowMax, p.mainMax, p.protectedMax, tt.window, tt.main, tt.protected)
		}
	}
}

func TestAddEntersWindow(t *testing.T) {
	f := newFixture(t, 1000)
	f.addAll(1, 2, 3)
	f.expect(window, 3, 2, 1)
	f.expect(probation)
}

func TestWindowOverflowMovesToProbationFront(t *testing.T) {
	f := newFixture(t, 100)
	f.addAll(1, 2, 3)
	f.expect(window, 3)
	f.expect(probation, 2, 1)
	f.expectEvicted()
}

func TestAccessReorders(t *testing.T) {
	f := newFixture(t, 1000)
	f.addAll(1, 2, 3)
	f.access(1)
	f.expect(window, 1, 3, 2)
}

func TestAccessPromotesProbationToProtected(t *testing.T) {
	f := newFixture(t, 100)
	f.addAll(1, 2, 3, 4)
	f.access(2, 1)
	f.expect(probation, 3)
	f.expect(protected, 1, 2)
	f.access(2)
	f.expect(protected, 2, 1)
}

func TestProtectedOverflowDemotesToProbationFront(t *testing.T) {
	f := newFixture(t, 100)
	f.addAll(seq(0, 81)...)
	f.access(seq(0, 79)...)
	f.expect(probation, 79)
	f.access(79)
	f.expect(probation, 0)
	f.expect(protected, reversed(seq(1, 80))...)
	f.expect(window, 80)
}

func TestAdmission(t *testing.T) {
	tests := []struct {
		name    string
		hot     int
		evicted int
	}{
		{"popular candidate displaces victim", 99, 0},
		{"unpopular candidate is rejected", 0, 99},
		{"ties favour the victim", -1, 99},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, 100)
			f.addAll(seq(0, 100)...)
			if tt.hot >= 0 {
				f.bump(tt.hot, 4)
			}
			f.add(100, 1)
			f.expectEvicted(tt.evicted)
		})
	}
}

func TestCandidatesAreTriedOldestFirst(t *testing.T) {
	tests := []struct {
		name      string
		hot       []int
		evicted   []int
		probation []int
	}{
		{"both admitted", []int{99, 100}, []int{0, 1}, append([]int{100, 99}, reversed(seq(2, 99))...)},
		{"older rejected", []int{100}, []int{99, 0}, append([]int{100}, reversed(seq(1, 99))...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, 100)
			f.addAll(seq(0, 100)...)
			for _, k := range tt.hot {
				f.bump(k, 4)
			}
			f.add(100, 2)
			f.expectEvicted(tt.evicted...)
			f.expect(window)
			f.expect(probation, tt.probation...)
		})
	}
}

func TestHashFloodDefence(t *testing.T) {
	tests := []struct {
		name    string
		rnd     uint32
		evicted int
	}{
		{"low bits zero admits", 0x80, 0},
		{"low bits set rejects", 0x81, 99},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, 100)
			f.addAll(seq(0, 100)...)
			f.bump(99, hashFloodThreshold)
			f.bump(0, 10)
			calls := 0
			f.p.rnd = func() uint32 {
				calls++
				return tt.rnd
			}
			f.add(100, 1)
			f.expectEvicted(tt.evicted)
			if calls != 1 {
				t.Errorf("rnd called %d times, want 1", calls)
			}
		})
	}
}

func TestColdCandidateNeverConsultsRandom(t *testing.T) {
	f := newFixture(t, 100)
	f.addAll(seq(0, 100)...)
	f.bump(99, hashFloodThreshold-1)
	f.bump(0, 10)
	f.add(100, 1)
	f.expectEvicted(99)
}

func TestHeavyAddIsRejected(t *testing.T) {
	f := newFixture(t, 10)
	f.add(1, 5)
	f.add(2, 11)
	f.expectEvicted(2)
	f.expect(probation, 1)
	if f.p.Weight() != 5 {
		t.Errorf("Weight = %d, want 5", f.p.Weight())
	}
}

func TestHeavyUpdateIsRejected(t *testing.T) {
	f := newFixture(t, 10)
	f.addAll(1, 2)
	f.update(1, 11)
	f.expectEvicted(1)
	f.expect(probation, 2)
	f.expect(protected)
	if f.p.Weight() != 1 {
		t.Errorf("Weight = %d, want 1", f.p.Weight())
	}
}

func TestCapacityZeroRetainsNothing(t *testing.T) {
	f := newFixture(t, 0)
	f.add(1, 0)
	f.add(2, 1)
	f.expectEvicted(1, 2)
}

func TestCapacityOne(t *testing.T) {
	f := newFixture(t, 1)
	f.addAll(1, 2)
	f.expectEvicted(2)
	f.access(1)
	f.expect(probation, 1)
	f.bump(3, 4)
	f.add(3, 1)
	f.expectEvicted(2, 1)
	f.expect(probation, 3)
}

func TestSmallCapacitiesHaveNoWindow(t *testing.T) {
	for c := uint64(2); c < 100; c++ {
		p := New[int, int](c, false)
		if p.windowMax != 0 || p.mainMax != c || p.protectedMax != c*80/100 {
			t.Fatalf("capacity %d: window %d main %d protected %d", c, p.windowMax, p.mainMax, p.protectedMax)
		}
		simulate(t, p, zipfTrace(c, 1.1, 200, 500), true)
	}
}

func TestWeightedNodes(t *testing.T) {
	f := newFixture(t, 10)
	f.add(1, 3)
	f.add(2, 3)
	f.add(3, 3)
	f.expect(probation, 3, 2, 1)
	f.bump(4, 4)
	f.add(4, 4)
	f.expectEvicted(1)
	f.expect(probation, 4, 3, 2)
	f.add(5, 0)
	f.expectEvicted(1)
	if f.p.Weight() != 10 {
		t.Errorf("Weight = %d, want 10", f.p.Weight())
	}
}

func TestUpdateInWindowStaysInWindow(t *testing.T) {
	f := newFixture(t, 1000)
	f.addAll(1, 2, 3)
	n := f.update(1, 4)
	f.expect(window, 1, 3, 2)
	if n.Slot() != window || f.p.weights[window] != 6 {
		t.Errorf("slot %d window weight %d, want %d 6", n.Slot(), f.p.weights[window], window)
	}
}

func TestUpdatePromotesFromProbation(t *testing.T) {
	f := newFixture(t, 100)
	f.addAll(1, 2, 3)
	f.update(1, 2)
	f.expect(protected, 1)
	f.expect(probation, 2)
	if f.p.weights[protected] != 2 || f.p.Weight() != 4 {
		t.Errorf("protected weight %d total %d, want 2 4", f.p.weights[protected], f.p.Weight())
	}
}

func TestUpdateInProtectedMovesToFront(t *testing.T) {
	f := newFixture(t, 100)
	f.addAll(1, 2, 3)
	f.access(1, 2)
	f.update(1, 1)
	f.expect(protected, 1, 2)
}

func TestUpdateRecordsFrequency(t *testing.T) {
	f := newFixture(t, 100)
	f.addAll(1)
	before := f.p.sketch.Frequency(hashOf(1))
	f.update(1, 1)
	if got := f.p.sketch.Frequency(hashOf(1)); got != before+1 {
		t.Errorf("frequency %d, want %d", got, before+1)
	}
}

func TestUpdateGrowingWeightEvicts(t *testing.T) {
	f := newFixture(t, 10)
	f.addAll(1, 2, 3)
	f.update(3, 9)
	f.expectEvicted(1)
	f.expect(protected)
	f.expect(probation, 3, 2)
}

func TestRemove(t *testing.T) {
	f := newFixture(t, 100)
	f.addAll(1, 2, 3, 4)
	f.access(1)
	f.remove(1)
	f.remove(2)
	f.remove(4)
	f.expect(probation, 3)
	if f.p.Weight() != 1 || f.p.count != 1 {
		t.Errorf("Weight %d count %d, want 1 1", f.p.Weight(), f.p.count)
	}
}

func TestClear(t *testing.T) {
	f := newFixture(t, 100)
	f.addAll(1, 2, 3, 4)
	f.access(1, 2)
	f.p.climber.hits, f.p.climber.misses, f.p.climber.prevHitRate, f.p.climber.step = 5, 5, 0.5, 1
	f.p.windowTarget = 50
	f.p.setWindow(50)

	var cleared []int
	f.p.Clear(func(n *testNode) { cleared = append(cleared, n.Key) })
	checkInvariants(t, f.p)

	if want := []int{4, 3, 2, 1}; !slices.Equal(cleared, want) {
		t.Errorf("cleared %v, want %v", cleared, want)
	}
	if f.p.Weight() != 0 || f.p.count != 0 {
		t.Errorf("Weight %d count %d after Clear", f.p.Weight(), f.p.count)
	}
	if f.p.climber != newClimber(100) || f.p.windowMax != 1 {
		t.Errorf("climber %+v window %d not reset", f.p.climber, f.p.windowMax)
	}
	if f.p.sketch.Frequency(hashOf(1)) == 0 {
		t.Error("Clear reset the sketch")
	}
}

func TestSketchGrowsWithCount(t *testing.T) {
	p := New[int, int](1<<20, false)
	if p.sketch.Capacity() != initialSketch {
		t.Fatalf("initial sketch %d, want %d", p.sketch.Capacity(), initialSketch)
	}
	nodes := make(map[int]*testNode)
	for k := range 5000 {
		n := newNode(k, 1)
		nodes[k] = n
		p.Add(n, func(n *testNode) { delete(nodes, n.Key) })
	}
	if p.sketch.Capacity() != 8192 {
		t.Errorf("sketch %d, want 8192", p.sketch.Capacity())
	}
}

func TestRandomOperationsKeepInvariants(t *testing.T) {
	for _, adaptive := range []bool{false, true} {
		for _, c := range []uint64{0, 1, 2, 7, 50, 333} {
			p := New[int, int](c, adaptive)
			r := rand.New(rand.NewPCG(c, 9))
			p.rnd = r.Uint32
			nodes := make(map[int]*testNode)
			evict := func(n *testNode) {
				if nodes[n.Key] == n {
					delete(nodes, n.Key)
				}
			}
			for range 20000 {
				k := r.IntN(int(c)*2 + 4)
				w := uint32(r.IntN(4))
				old, ok := nodes[k]
				switch {
				case !ok:
					n := newNode(k, w)
					nodes[k] = n
					p.Add(n, evict)
				case r.IntN(10) == 0:
					n := newNode(k, w)
					nodes[k] = n
					p.Update(old, n, evict)
				case r.IntN(20) == 0:
					p.Remove(old)
					delete(nodes, k)
				default:
					p.Access(old)
				}
				checkInvariants(t, p)
			}
		}
	}
}
