package s3fifo

import (
	"slices"
	"testing"
)

// encoded returns the ghost's occupied slots as sorted (slot<<32 | fingerprint) values, its observable state.
func (g *ghost) encoded() []uint64 {
	var out []uint64
	for i, fp := range g.slots {
		if fp != 0 {
			out = append(out, uint64(i)<<32|uint64(fp))
		}
	}
	return out
}

// ghostOf returns the encoding of a ghost sized like p's after pushing hashes in order.
func ghostOf(p *Policy[int, int], hashes ...uint64) []uint64 {
	g := ghost{slots: make([]uint32, len(p.ghost.slots)), mask: p.ghost.mask}
	for _, h := range hashes {
		g.slots[h&g.mask] = fingerprint(h)
	}
	return g.encoded()
}

func TestGhostRemembersAndForgets(t *testing.T) {
	var g ghost
	if g.remove(1) {
		t.Fatal("empty ghost must not remember anything")
	}
	g.push(0x1111, 8)
	g.push(0x2222, 8)
	if !g.remove(0x1111) || g.remove(0x1111) {
		t.Fatal("remove must report a remembered hash exactly once")
	}
	if !g.remove(0x2222) {
		t.Fatal("other hashes must be unaffected")
	}
}

func TestGhostCollisionOverwritesOlder(t *testing.T) {
	var g ghost
	g.push(0x10, 4)
	g.push(0x20, 4) // same slot, different fingerprint
	if g.remove(0x10) {
		t.Fatal("older colliding ghost must have been overwritten")
	}
	if !g.remove(0x20) {
		t.Fatal("newer colliding ghost must be remembered")
	}
}

func TestGhostDistinguishesHashesSharingASlot(t *testing.T) {
	var g ghost
	g.push(42, 1)
	if g.remove(7) {
		t.Fatal("a different hash in the same slot must not match")
	}
}

func TestGhostGrowsToTrackedCountAndStartsOver(t *testing.T) {
	var g ghost
	g.push(1, 3)
	if len(g.slots) != 4 {
		t.Fatalf("size %d, want nextPow2(3) = 4", len(g.slots))
	}
	g.push(2, 2)
	if len(g.slots) != 4 {
		t.Fatal("ghost must never shrink")
	}
	g.push(3, 5)
	if len(g.slots) != 8 || g.remove(1) || g.remove(2) || !g.remove(3) {
		t.Fatal("growing must start over with only the new hash")
	}
}

func TestGhostReset(t *testing.T) {
	var g ghost
	g.push(1, 4)
	g.reset()
	if len(g.slots) != 4 || len(g.encoded()) != 0 {
		t.Fatal("reset must forget everything but keep the table")
	}
}

func TestGhostIsFourBytesPerSlotAndAllocationFree(t *testing.T) {
	var g ghost
	g.push(1, 1024)
	if cap(g.slots) != 1024 {
		t.Fatalf("cap %d", cap(g.slots))
	}
	h := uint64(0)
	if allocs := testing.AllocsPerRun(1000, func() {
		h += 0x9E3779B97F4A7C15
		g.push(h, 1024)
		g.remove(h)
	}); allocs != 0 {
		t.Fatalf("steady state allocated %v times", allocs)
	}
	if !slices.Equal(g.encoded(), nil) {
		t.Fatal("expected empty after paired push/remove")
	}
}
