package cache

import (
	"math"
	"testing"
)

// TestClampWeightNeverWrapsLargeSums: lengths summed for WeighByKeyAndLen must clamp, not wrap, past MaxInt32.
func TestClampWeightNeverWrapsLargeSums(t *testing.T) {
	if got := clampWeight(int64(math.MaxInt32) + int64(math.MaxInt32)); got != math.MaxUint32-1 {
		t.Fatalf("clampWeight(2*MaxInt32) = %d, want %d", got, uint32(math.MaxUint32-1))
	}
	if got := clampWeight(math.MaxInt64); got != math.MaxUint32 {
		t.Fatalf("clampWeight(MaxInt64) = %d, want %d", got, uint32(math.MaxUint32))
	}
	if got := clampWeight(-5); got != 1 {
		t.Fatalf("clampWeight(-5) = %d, want 1", got)
	}
}
