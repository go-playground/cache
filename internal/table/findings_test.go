package table

import (
	"testing"
)

// These tests pin the review findings. They fail on the current code (the second only on 32-bit platforms) and pass
// once the suggested fixes are applied.

// TestFindingStripeMaskReachesEveryStripe: newState sizes stripes as min(n, nextPow2(GOMAXPROCS*4)), which is not a
// power of two whenever a non-power-of-two n is the smaller, yet indexes them with hash & (len-1). With n = 9 on a
// machine with 4+ procs, the mask is 0b1000 and only stripes 0 and 8 are ever used. Never out of bounds, but the
// size counter of small tables collapses onto one or two cache lines.
func TestFindingStripeMaskReachesEveryStripe(t *testing.T) {
	for n := minBuckets; n <= 300; n++ {
		s := newState[int, int](n)
		reach := make(map[uint64]bool)
		for h := range uint64(4 * len(s.stripes)) {
			reach[h&s.stripeMask] = true
		}
		if len(reach) != len(s.stripes) {
			t.Fatalf("n = %d: %d stripes allocated, mask %#b reaches only %d", n, len(s.stripes), s.stripeMask, len(reach))
		}
	}
}

// TestFindingBucketsForLargeHints: bucketsFor computes sizeHint*loadDen in int, which overflows on 32-bit platforms
// for hints from about 2^29, so NewBounded(MaxEntries, MaxEntries), what a cache built with capacity
// cache.MaxEntries asks for, gets 4 buckets and a bound of 4 and degrades to a few enormous chains. Passes on 64-bit;
// run with GOARCH=386 to see it fail.
func TestFindingBucketsForLargeHints(t *testing.T) {
	for _, hint := range []int{1<<29 - 4, 1 << 29, 600_000_000, 1<<30 - 1, MaxEntries} {
		n := bucketsFor(hint)
		if threshold := int64(n) * slotsPerBucket * loadNum / loadDen; threshold < int64(hint) {
			t.Errorf("bucketsFor(%d) = %d holds only %d before resizing", hint, n, threshold)
		}
	}
}
