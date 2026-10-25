package s3fifo

import "math/bits"

// ghost remembers the hashes of entries recently evicted from the small queue, in 4 bytes per slot.
//
// It is a direct-mapped table of 32-bit fingerprints: a hash's slot is chosen by its low bits and holds a fingerprint
// of the whole hash. Pushing overwrites whatever occupied the slot, so older ghosts age out through collisions rather
// than strict FIFO order, an approximation that keeps memory at 4-8 bytes per tracked entry instead of a queue plus a
// map. The table sizes itself to the number of tracked entries, as the S3-FIFO paper sizes its ghost queue to the
// main queue, and starts over when it grows since fingerprints cannot be rehashed.
type ghost struct {
	slots []uint32
	mask  uint64
}

// fingerprint mixes both halves of h into a non-zero 32-bit value; 0 marks an empty slot.
func fingerprint(h uint64) uint32 {
	return uint32(h^h>>32) | 1
}

// push records h, first growing the table to nextPow2(limit) slots if it is smaller.
func (g *ghost) push(h uint64, limit int) {
	if limit > len(g.slots) {
		n := 1 << bits.Len(uint(limit-1))
		g.slots, g.mask = make([]uint32, n), uint64(n-1)
	}
	g.slots[h&g.mask] = fingerprint(h)
}

// remove forgets h, reporting whether it was remembered.
func (g *ghost) remove(h uint64) bool {
	if len(g.slots) == 0 {
		return false
	}
	i := h & g.mask
	if g.slots[i] != fingerprint(h) {
		return false
	}
	g.slots[i] = 0
	return true
}

// reset forgets everything, retaining the table.
func (g *ghost) reset() {
	clear(g.slots)
}
