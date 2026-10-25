package benchmarks

import (
	"math"
	"math/rand/v2"
	"slices"
)

// Zipf generates keys in [0, n) where key i has probability proportional to 1/(i+1)^s, for any s > 0, by inverse
// CDF lookup. Key identities are shuffled so popularity is unrelated to key order.
type Zipf struct {
	cdf  []float64
	perm []int
	rng  *rand.Rand
}

// NewZipf creates a Zipf generator over n keys with skew s and a deterministic seed.
func NewZipf(n int, s float64, seed uint64) *Zipf {
	cdf := make([]float64, n)
	sum := 0.0
	for i := range n {
		sum += 1 / math.Pow(float64(i+1), s)
		cdf[i] = sum
	}
	for i := range cdf {
		cdf[i] /= sum
	}
	rng := rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))
	return &Zipf{cdf: cdf, perm: rng.Perm(n), rng: rng}
}

// Next returns the next key.
func (z *Zipf) Next() int {
	i, _ := slices.BinarySearch(z.cdf, z.rng.Float64())
	return z.perm[min(i, len(z.perm)-1)]
}

// ZipfTrace returns length keys drawn from Zipf(n, s).
func ZipfTrace(length, n int, s float64, seed uint64) []int {
	z := NewZipf(n, s, seed)
	trace := make([]int, length)
	for i := range trace {
		trace[i] = z.Next()
	}
	return trace
}

// ScanTrace interleaves a Zipf workload with one-off sequential scans of scanLen never-repeated keys every period
// operations, the classic pattern that flushes recency-only caches.
func ScanTrace(length, n int, s float64, period, scanLen int, seed uint64) []int {
	z := NewZipf(n, s, seed)
	trace := make([]int, 0, length)
	next := n
	for len(trace) < length {
		for i := 0; i < period && len(trace) < length; i++ {
			trace = append(trace, z.Next())
		}
		for i := 0; i < scanLen && len(trace) < length; i++ {
			trace = append(trace, next)
			next++
		}
	}
	return trace
}

// LoopTrace cycles through n keys in order, the worst case for LRU when n exceeds capacity.
func LoopTrace(length, n int) []int {
	trace := make([]int, length)
	for i := range trace {
		trace[i] = i % n
	}
	return trace
}

// ShiftingTrace is a Zipf workload whose popular keys move to a fresh key range every phase operations, rewarding
// policies that forget stale popularity.
func ShiftingTrace(length, n int, s float64, phase int, seed uint64) []int {
	z := NewZipf(n, s, seed)
	trace := make([]int, length)
	for i := range trace {
		trace[i] = z.Next() + (i/phase)*n
	}
	return trace
}
