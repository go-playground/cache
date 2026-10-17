package table

import (
	"math/rand/v2"
	"testing"

	"github.com/go-playground/cache/v2/internal/node"
)

const (
	opStore = iota
	opDelete
	opDeleteNode
	opGet
	opRange
	opLen
	numOps

	fuzzKeys = 64
)

// splitmix64 is a deterministic well-mixed hash, so fuzz crashers reproduce across processes (maphash seeds do not).
func splitmix64(k int) uint64 {
	z := uint64(k) + 0x9e3779b97f4a7c15
	z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
	z = (z ^ z>>27) * 0x94d049bb133111eb
	return z ^ z>>31
}

// hashModes are selected by the first fuzz byte. All but the first deliberately collide.
var hashModes = []func(int) uint64{
	splitmix64,
	func(k int) uint64 { return uint64(k & 7) },   // same tag, few indexes
	func(int) uint64 { return 42 },                // one chain
	func(k int) uint64 { return uint64(k) << 57 }, // one index, distinct tags
}

type differential struct {
	t       testing.TB
	hash    func(int) uint64
	tbl     *Table[int, int]
	ref     *refTable[int, int]
	history map[int][]*node.Node[int, int]
	seq     int
}

// runDifferential decodes data into ops and checks Table against refTable after each one.
// data[0] selects the hash mode (low 2 bits), whether growth is bounded (bit 2, bounding it to the sizeHint plus a
// margin so the final growth step is less than doubling) and the sizeHint (high 4 bits); each following byte pair is
// (op and argument, key).
func runDifferential(t testing.TB, data []byte) {
	if len(data) == 0 {
		return
	}
	d := &differential{
		t:       t,
		hash:    hashModes[int(data[0])%len(hashModes)],
		tbl:     newFuzzTable(data[0]),
		ref:     newRefTable[int, int](0),
		history: make(map[int][]*node.Node[int, int]),
	}
	for i := 1; i+1 < len(data); i += 2 {
		d.apply(data[i], int(data[i+1])%fuzzKeys)
	}
	d.checkAllKeys()
}

func (d *differential) apply(b byte, k int) {
	arg := int(b >> 3)
	switch int(b) % numOps {
	case opStore:
		d.store(k)
	case opDelete:
		if a, b := d.tbl.Delete(d.hash(k), k), d.ref.Delete(d.hash(k), k); a != b {
			d.t.Fatalf("Delete(%d) = %p, want %p", k, a, b)
		}
	case opDeleteNode:
		n := d.pick(k, arg)
		if a, b := d.tbl.DeleteNode(n), d.ref.DeleteNode(n); a != b {
			d.t.Fatalf("DeleteNode(%d) = %v, want %v", k, a, b)
		}
	case opGet:
		if a, b := d.tbl.Get(d.hash(k), k), d.ref.Get(d.hash(k), k); a != b {
			d.t.Fatalf("Get(%d) = %p, want %p", k, a, b)
		}
	case opRange:
		d.checkRange(arg)
	case opLen:
		if a, b := d.tbl.Len(), d.ref.Len(); a != b {
			d.t.Fatalf("Len = %d, want %d", a, b)
		}
	}
}

func (d *differential) store(k int) {
	d.seq++
	n := node.New(k, d.seq, d.hash(k), 1)
	d.history[k] = append(d.history[k], n)
	if a, b := d.tbl.Store(n), d.ref.Store(n); a != b {
		d.t.Fatalf("Store(%d) = %p, want %p", k, a, b)
	}
}

// pick returns a previously stored node of k, newest first, or a never stored one.
func (d *differential) pick(k, back int) *node.Node[int, int] {
	h := d.history[k]
	if len(h) == 0 {
		return node.New(k, -1, d.hash(k), 1)
	}
	return h[len(h)-1-back%len(h)]
}

// checkRange visits up to limit nodes (0 = all), each of which must be mapped in the reference.
func (d *differential) checkRange(limit int) {
	seen := make(map[*node.Node[int, int]]struct{})
	d.tbl.Range(func(n *node.Node[int, int]) bool {
		if _, dup := seen[n]; dup {
			d.t.Fatalf("Range visited %d twice", n.Key)
		}
		if d.ref.Get(n.Hash, n.Key) != n {
			d.t.Fatalf("Range visited unmapped node for key %d", n.Key)
		}
		seen[n] = struct{}{}
		return limit == 0 || len(seen) < limit
	})
	want := d.ref.Len()
	if limit > 0 {
		want = min(want, limit)
	}
	if len(seen) != want {
		d.t.Fatalf("Range(limit %d) visited %d, want %d", limit, len(seen), want)
	}
}

func (d *differential) checkAllKeys() {
	for k := range fuzzKeys {
		if a, b := d.tbl.Get(d.hash(k), k), d.ref.Get(d.hash(k), k); a != b {
			d.t.Fatalf("final Get(%d) = %p, want %p", k, a, b)
		}
	}
	if a, b := d.tbl.Len(), d.ref.Len(); a != b {
		d.t.Fatalf("final Len = %d, want %d", a, b)
	}
}

// randomOps returns a deterministic op stream for the given hash mode and seed.
func randomOps(mode, n int, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, uint64(mode)))
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(r.Uint32())
	}
	data[0] = data[0]&0xf0 | byte(mode)
	return data
}

// growthOps stores every key in the key space, then mixes deletes and re-stores.
func growthOps(mode byte) []byte {
	data := []byte{mode}
	for k := range fuzzKeys {
		data = append(data, opStore, byte(k))
	}
	for k := range fuzzKeys {
		data = append(data, byte(k%3)*opDelete|byte(k)<<3, byte(k), opGet, byte(k))
	}
	return append(data, opRange, 0, opLen, 0)
}

func FuzzTableAgainstReference(f *testing.F) {
	for mode := range byte(len(hashModes)) {
		f.Add(growthOps(mode))
		f.Add(randomOps(int(mode), 512, 1))
	}
	f.Add([]byte{0, opStore, 1, opStore, 1, opDeleteNode | 1<<3, 1, opGet, 1, opDeleteNode, 1, opGet, 1, opLen, 0})
	f.Add([]byte{2, opStore, 0, opStore, 1, opDelete, 0, opStore, 2, opRange | 1<<3, 0, opRange, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		runDifferential(t, data)
	})
}

func TestTableAgainstReference(t *testing.T) {
	seeds, size := 50, 4000
	if testing.Short() {
		seeds = 10
	}
	for mode := range hashModes {
		for s := range seeds {
			runDifferential(t, randomOps(mode, size, uint64(s)))
		}
	}
}

func newFuzzTable(b byte) *Table[int, int] {
	hint := int(b >> 4)
	if b&4 == 0 {
		return New[int, int](hint)
	}
	return NewBounded[int, int](hint, hint*3+17)
}
