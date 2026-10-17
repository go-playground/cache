package tinylfu

import (
	"math/rand/v2"
	"testing"
)

func TestSampleSize(t *testing.T) {
	tests := []struct{ capacity, want uint64 }{
		{0, 1}, {1, 10}, {1000, 10000}, {104858, 1048580}, {1<<64/10 - 1, (1<<64/10 - 1) * 10}, {1<<64 - 1, 1<<64 - 1},
	}
	for _, tt := range tests {
		if got := sampleSize(tt.capacity); got != tt.want {
			t.Errorf("sampleSize(%d) = %d, want %d", tt.capacity, got, tt.want)
		}
	}
}

// sample records hits and misses for one full sample at the given hit rate, returning the adjustment.
func sample(t *testing.T, c *climber, hitRate float64) float64 {
	t.Helper()
	hits := uint64(hitRate * float64(c.sampleSize))
	for i := range c.sampleSize - 1 {
		if amount := c.record(i < hits); amount != 0 {
			t.Fatalf("adjusted %v mid sample", amount)
		}
	}
	return c.record(hits == c.sampleSize)
}

func TestClimberFollowsHitRate(t *testing.T) {
	c := newClimber(100)
	steps := []struct {
		hitRate float64
		want    float64
	}{
		{0.50, 6.25},                      // improvement from 0 restarts the step upwards
		{0.52, 6.25},                      // small improvement keeps direction, then decays
		{0.53, 6.25 * 0.98},               // still improving
		{0.52, -6.25 * 0.98 * 0.98},       // small regression reverses
		{0.40, 6.25 * 0.98 * 0.98 * 0.98}, // large regression reverses again and restarts
		{0.41, 6.25},                      // restarted at full size in the direction just taken
	}
	for i, s := range steps {
		if got := sample(t, &c, s.hitRate); !near(got, s.want) {
			t.Fatalf("sample %d: adjustment %v, want %v", i, got, s.want)
		}
	}
}

func near(a, b float64) bool {
	d := a - b
	return d < 1e-9 && d > -1e-9
}

func TestClimberIsInertWhenNotAdaptive(t *testing.T) {
	p := New[int, int](100, false)
	simulate(t, p, zipfTrace(1, 1.01, 1000, 10000), false)
	if p.windowMax != 1 || p.climber != newClimber(100) {
		t.Errorf("static policy adapted: window %d climber %+v", p.windowMax, p.climber)
	}
}

// burstTrace references blocks of burst fresh keys passes times each, then never again: pure recency.
func burstTrace(blocks, burst, passes int) []int {
	trace := make([]int, 0, blocks*burst*passes)
	for b := range blocks {
		for range passes {
			for k := range burst {
				trace = append(trace, b*burst+k)
			}
		}
	}
	return trace
}

// climb replays trace against an adaptive policy, returning the hit ratio and the mean window over the second half.
func climb(t *testing.T, capacity uint64, seed uint64, trace []int) (float64, float64) {
	t.Helper()
	p := New[int, int](capacity, true)
	p.rnd = rand.New(rand.NewPCG(seed, seed)).Uint32
	nodes := make(map[int]*testNode)
	evict := func(n *testNode) { delete(nodes, n.Key) }
	hits := 0
	var windows uint64
	for i, k := range trace {
		if n, ok := nodes[k]; ok {
			hits++
			p.Access(n)
		} else {
			n := newNode(k, 1)
			nodes[k] = n
			p.Add(n, evict)
		}
		checkSizing(t, p)
		if i%1000 == 0 {
			checkInvariants(t, p)
		}
		if i >= len(trace)/2 {
			windows += p.windowMax
		}
	}
	return float64(hits) / float64(len(trace)), float64(windows) / float64(len(trace)-len(trace)/2)
}

func TestClimberGrowsWindowForRecency(t *testing.T) {
	const capacity = 500
	trace := burstTrace(1000, capacity/5, 4)
	hr, window := climb(t, capacity, 1, trace)
	static := simulate(t, New[int, int](capacity, false), trace, false)
	t.Logf("burst trace: adaptive %.3f static %.3f mean window %.0f", hr, static, window)
	if window < capacity/5 {
		t.Errorf("mean window %.0f, want >= burst %d", window, capacity/5)
	}
	if hr < static+0.25 {
		t.Errorf("adaptive hit ratio %.3f, want well above static %.3f", hr, static)
	}
}

func TestClimberKeepsWindowSmallForFrequency(t *testing.T) {
	const capacity, seeds = 500, 5
	var sum float64
	for seed := range uint64(seeds) {
		_, window := climb(t, capacity, seed, zipfTrace(seed, 1.01, 100_000, 400*capacity))
		t.Logf("zipf seed %d: mean window %.0f", seed, window)
		sum += window
	}
	if mean := sum / seeds; mean > capacity/4 {
		t.Errorf("mean window %.0f across seeds, want <= %d", mean, capacity/4)
	}
}
