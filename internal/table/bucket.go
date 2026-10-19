package table

import (
	"math/bits"
	"sync/atomic"

	"github.com/go-playground/cache/v2/internal/node"
)

const (
	slotsPerBucket = 6
	bucketSize     = 64

	lsb      = 0x0101010101010101
	slotMask = 0x8080808080808080 >> ((8 - slotsPerBucket) * 8)
)

// bucket is exactly one 64-byte cache line on 64-bit platforms: a word of tags, six node slots and the overflow link.
// Writes to a chain are guarded by the lock of the root's stripe (see state.lock), not by the bucket, which is what
// makes room for the sixth slot. meta holds one tag byte per slot, 0 meaning empty, and its unused upper bytes are
// always 0.
//
// Invariant: a slot's tag byte is set whenever its pointer is non-nil. Inserts set the tag before storing the pointer
// and deletes nil the pointer before clearing the tag, while replacements (same key, therefore same hash and tag) only
// swap the pointer in place. The pointer store is thus the linearization point of every write and a reader whose meta
// load follows it always sees the tag, so readers never miss a key that was present for their whole call. A stale or
// foreign tag is harmless because readers verify the loaded node's own Hash and Key.
type bucket[K comparable, V any] struct {
	meta  atomic.Uint64
	slots [slotsPerBucket]atomic.Pointer[node.Node[K, V]]
	next  atomic.Pointer[bucket[K, V]]
}

// tagOf returns the top 7 bits of hash with the high bit set, so a tag is never 0.
func tagOf(hash uint64) uint64 {
	return hash>>57 | 0x80
}

// zeroBytes sets the high bit of every slot byte of x that is zero. It never misses a zero byte; bytes above a true
// zero may be falsely reported because of borrow propagation, but the lowest reported byte is always exact.
func zeroBytes(x uint64) uint64 {
	return (x - lsb) &^ x & slotMask
}

// matchTag reports candidate slots whose tag equals tag.
func matchTag(meta, tag uint64) uint64 {
	return zeroBytes(meta ^ (lsb * tag))
}

// matchEmpty reports empty slots; only the lowest reported slot is guaranteed to be empty.
func matchEmpty(meta uint64) uint64 {
	return zeroBytes(meta)
}

// slotOf returns the slot index of the lowest reported byte in m.
func slotOf(m uint64) int {
	return bits.TrailingZeros64(m) >> 3
}

// find returns the slot and node mapped to key in b, or nil.
func (b *bucket[K, V]) find(meta, tag, hash uint64, key K) (int, *node.Node[K, V]) {
	for m := matchTag(meta, tag); m != 0; m &= m - 1 {
		i := slotOf(m)
		if n := b.slots[i].Load(); n != nil && n.Hash == hash && n.Key == key {
			return i, n
		}
	}
	return 0, nil
}

// findNode returns the slot holding exactly n in b, or -1.
func (b *bucket[K, V]) findNode(meta, tag uint64, n *node.Node[K, V]) int {
	for m := matchTag(meta, tag); m != 0; m &= m - 1 {
		i := slotOf(m)
		if b.slots[i].Load() == n {
			return i
		}
	}
	return -1
}

// publish stores n into empty slot i, tag first. Requires the chain's lock.
func (b *bucket[K, V]) publish(i int, tag uint64, n *node.Node[K, V]) {
	b.meta.Store(b.meta.Load() | tag<<(i*8))
	b.slots[i].Store(n)
}

// clear empties slot i, pointer first. Requires the chain's lock.
func (b *bucket[K, V]) clear(i int) {
	b.slots[i].Store(nil)
	b.meta.Store(b.meta.Load() &^ (0xff << (i * 8)))
}

// cursor appends nodes to a chain that nobody else can reach yet.
type cursor[K comparable, V any] struct {
	b *bucket[K, V]
	i int
}

func (c *cursor[K, V]) append(root *bucket[K, V], n *node.Node[K, V]) {
	if c.b == nil {
		c.resume(root)
	}
	if c.i == slotsPerBucket {
		nb := &bucket[K, V]{}
		c.b.next.Store(nb)
		c.b, c.i = nb, 0
	}
	c.b.publish(c.i, tagOf(n.Hash), n)
	c.i++
}

// resume positions the cursor after the last node already in root's chain. When growth is less than doubling, two
// adjacent old chains can share a destination root, and the second must append after the first. Cursors fill slots
// contiguously, so the first empty slot of the last bucket is the end.
func (c *cursor[K, V]) resume(root *bucket[K, V]) {
	c.b = root
	for next := c.b.next.Load(); next != nil; next = c.b.next.Load() {
		c.b = next
	}
	c.i = 0
	for c.i < slotsPerBucket && c.b.slots[c.i].Load() != nil {
		c.i++
	}
}
