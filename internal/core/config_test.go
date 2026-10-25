package core

import "testing"

func TestEntryCapacityIsClampedToMaxEntries(t *testing.T) {
	newPolicy := policies()[0].new
	if cfg := NewConfig[int, int](MaxEntries+1, nil, newPolicy); cfg.capacity != MaxEntries {
		t.Fatalf("entry capacity %d, want clamped to %d", cfg.capacity, uint64(MaxEntries))
	}
	if cfg := NewConfig[int, int](MaxEntries-1, nil, newPolicy); cfg.capacity != MaxEntries-1 {
		t.Fatal("capacities within the limit must be kept")
	}
	weigher := func(int, int) uint32 { return 1 }
	if cfg := NewConfig(MaxEntries*64, weigher, newPolicy); cfg.capacity != MaxEntries*64 {
		t.Fatal("weighted capacities are total weights and must not be clamped")
	}
}
