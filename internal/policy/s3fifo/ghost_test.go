package s3fifo

import (
	"slices"
	"testing"
)

func ghostHashes(g *ghost) []uint64 {
	var hs []uint64
	for i := g.head; i != none; i = g.e[i].next {
		hs = append(hs, g.e[i].hash)
	}
	return hs
}

func assertGhost(t *testing.T, g *ghost, want ...uint64) {
	t.Helper()
	if got := ghostHashes(g); !slices.Equal(got, want) {
		t.Fatalf("ghost %v, want %v", got, want)
	}
	checkGhost(t, g)
}

func TestGhostTrimsOldestFirst(t *testing.T) {
	g := newGhost()
	for h := range uint64(5) {
		g.push(h, 3)
	}
	assertGhost(t, &g, 2, 3, 4)
	g.push(5, 1)
	assertGhost(t, &g, 5)
}

func TestGhostRemoveReportsMembership(t *testing.T) {
	g := newGhost()
	g.push(1, 4)
	g.push(2, 4)
	g.push(3, 4)
	if !g.remove(2) {
		t.Fatal("2 not found")
	}
	if g.remove(2) || g.remove(9) {
		t.Fatal("removed absent hash")
	}
	assertGhost(t, &g, 1, 3)
}

func TestGhostRemovesOldestOccurrenceOfDuplicates(t *testing.T) {
	g := newGhost()
	for _, h := range []uint64{7, 1, 7, 2, 7} {
		g.push(h, 10)
	}
	g.remove(7)
	assertGhost(t, &g, 1, 7, 2, 7)
	g.remove(7)
	assertGhost(t, &g, 1, 2, 7)
	g.push(3, 2)
	assertGhost(t, &g, 7, 3)
	if !g.remove(7) || g.remove(7) {
		t.Fatal("expected exactly one remaining occurrence of 7")
	}
}

func TestGhostReusesFreedEntries(t *testing.T) {
	g := newGhost()
	for h := range uint64(1000) {
		g.push(h, 4)
	}
	if len(g.e) > 5 {
		t.Fatalf("arena grew to %d entries for a ghost of 4", len(g.e))
	}
	assertGhost(t, &g, 996, 997, 998, 999)
}

func TestGhostReset(t *testing.T) {
	g := newGhost()
	g.push(1, 4)
	g.push(2, 4)
	g.reset()
	assertGhost(t, &g)
	if g.remove(1) {
		t.Fatal("reset ghost still contains 1")
	}
	g.push(3, 4)
	assertGhost(t, &g, 3)
}
