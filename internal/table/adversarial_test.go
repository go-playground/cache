package table

import (
	"fmt"
	"math"
	"math/bits"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/go-playground/cache/v2/internal/node"
)

// Hash layout recap: index uses bits 0-31 (multiply-shift), the tag bits 57-63, and bits 32-56 only take part in the
// full n.Hash comparison. Each family below targets one of those, or several at once.

// advBoundaries holds low-32-bit values sitting exactly on, and one below, the first hash of every root of every
// bucket count from 4 to 160, where rounding in index and copyChain is most likely to go wrong.
var advBoundaries = func() []uint64 {
	seen := map[uint64]bool{}
	var out []uint64
	for n := uint64(4); n <= 160; n++ {
		for j := uint64(0); j <= n; j++ {
			b := (j<<32 + n - 1) / n
			for _, v := range []uint64{b - 1, b} {
				if v <= math.MaxUint32 && !seen[v] {
					seen[v] = true
					out = append(out, v)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}()

type advFamily struct {
	name string
	hash func(k int) uint64
	// colliding families put every key in one chain, so they use fewer keys to stay fast.
	chained bool
}

var advFamilies = []advFamily{
	{name: "mixed", hash: splitmix64},
	{name: "sameTag", hash: func(k int) uint64 { return splitmix64(k) & (1<<57 - 1) }},
	{name: "sameIndex", hash: func(k int) uint64 { return uint64(k%128)<<57 | uint64(k/128)<<32 | 0x9e3779b9 }, chained: true},
	{name: "sameIndexAndTag", hash: func(k int) uint64 { return uint64(k)<<32 | 0x12345678 }, chained: true},
	{name: "identical", hash: func(int) uint64 { return 0xdeadbeefcafef00d }, chained: true},
	{name: "pairs", hash: func(k int) uint64 { return splitmix64(k / 2) }},
	{name: "boundaries", hash: func(k int) uint64 {
		return splitmix64(k)&^math.MaxUint32 | advBoundaries[(k*7919)%len(advBoundaries)]
	}},
	{name: "lowBitsOnly", hash: func(k int) uint64 { return uint64(k) }, chained: true},
}

// advMaxEntriesFor returns a maxEntries for which bucketsFor yields exactly b buckets.
func advMaxEntriesFor(t testing.TB, b int) int {
	t.Helper()
	m := b * slotsPerBucket * loadNum / loadDen
	if got := bucketsFor(m); got != b {
		t.Fatalf("bucketsFor(%d) = %d, want %d", m, got, b)
	}
	return m
}

type advConfig struct {
	name  string
	build func() *Table[int, int]
	bound int // final bucket count of a bounded table, 0 when unbounded
}

// advConfigs covers doubling from power-of-two and non-power-of-two starts, bounded final steps from barely larger
// (N+1) to exactly doubling, a hint above the bound, and unsynchronized tables.
func advConfigs(t testing.TB) []advConfig {
	var cfgs []advConfig
	cfgs = append(cfgs,
		advConfig{name: "unbounded/hint0", build: func() *Table[int, int] { return New[int, int](0) }},
		advConfig{name: "unbounded/hint37", build: func() *Table[int, int] { return New[int, int](37) }},
		advConfig{name: "unsync/unbounded", build: func() *Table[int, int] { return NewUnsynchronized[int, int](0, 0) }},
	)
	for _, b := range []int{5, 6, 7, 9, 11, 15, 17, 23, 31, 33, 47, 63, 65, 97, 127, 129, 150} {
		m := advMaxEntriesFor(t, b)
		cfgs = append(cfgs, advConfig{
			name:  fmt.Sprintf("bounded/hint0/max%d", b),
			build: func() *Table[int, int] { return NewBounded[int, int](0, m) },
			bound: b,
		})
	}
	for _, b := range []int{13, 33, 150} {
		m := advMaxEntriesFor(t, b)
		cfgs = append(cfgs,
			advConfig{
				name:  fmt.Sprintf("bounded/hint40/max%d", b),
				build: func() *Table[int, int] { return NewBounded[int, int](40, m) },
				bound: b,
			},
			advConfig{
				name:  fmt.Sprintf("unsync/hint0/max%d", b),
				build: func() *Table[int, int] { return NewUnsynchronized[int, int](0, m) },
				bound: b,
			},
		)
	}
	cfgs = append(cfgs, advConfig{
		name:  "bounded/hintAboveBound",
		build: func() *Table[int, int] { return NewBounded[int, int](500, 20) },
		bound: bucketsFor(500),
	})
	return cfgs
}

// advCheckInvariants walks the current state and checks every structural invariant the lock-free readers rely on:
// a slot's tag byte is set exactly when its pointer is non-nil and equals tagOf of the node's hash, unused meta bytes
// stay zero, every node sits in the chain of its own index, no key or node appears twice, root locks are released and
// every stripe counts exactly its own nodes. It returns the mapped nodes by key.
func advCheckInvariants[K comparable](t testing.TB, tbl *Table[K, int]) map[K]*node.Node[K, int] {
	t.Helper()
	s := tbl.cur.Load()
	if s.frozen.Load() {
		t.Fatal("current state is frozen while quiescent")
	}
	if s.n != uint64(len(s.buckets)) {
		t.Fatalf("state n = %d, len(buckets) = %d", s.n, len(s.buckets))
	}
	byKey := make(map[K]*node.Node[K, int])
	perStripe := make([]int64, len(s.stripes))
	for j := range s.stripes {
		if !s.stripes[j].mu.TryLock() {
			t.Fatalf("stripe %d left locked", j)
		}
		s.stripes[j].mu.Unlock()
	}
	for j := range s.buckets {
		root := &s.buckets[j]
		for b := root; b != nil; b = b.next.Load() {
			meta := b.meta.Load()
			if meta>>(slotsPerBucket*8) != 0 {
				t.Fatalf("root %d: meta %#x has bits above the slot bytes", j, meta)
			}
			for i := range b.slots {
				tag := meta >> (i * 8) & 0xff
				n := b.slots[i].Load()
				if n == nil {
					if tag != 0 {
						t.Fatalf("root %d slot %d: empty slot has tag %#x", j, i, tag)
					}
					continue
				}
				if tag != tagOf(n.Hash) {
					t.Fatalf("root %d slot %d: tag %#x, want %#x", j, i, tag, tagOf(n.Hash))
				}
				if idx := s.index(n.Hash); idx != uint64(j) {
					t.Fatalf("key %v with hash %#x is in root %d, index says %d", n.Key, n.Hash, j, idx)
				}
				if _, dup := byKey[n.Key]; dup {
					t.Fatalf("key %v mapped twice", n.Key)
				}
				byKey[n.Key] = n
				perStripe[s.index(n.Hash)&s.stripeMask]++
			}
		}
	}
	for i := range s.stripes {
		if got := s.stripes[i].n.Load(); got != perStripe[i] {
			t.Fatalf("stripe %d counts %d, holds %d nodes", i, got, perStripe[i])
		}
	}
	if tbl.Len() != len(byKey) {
		t.Fatalf("Len = %d, table holds %d nodes", tbl.Len(), len(byKey))
	}
	return byKey
}

// advChainBuckets returns the total number of buckets, root and overflow, of the current state.
func advChainBuckets[K comparable](tbl *Table[K, int]) int {
	s := tbl.cur.Load()
	total := 0
	for j := range s.buckets {
		for b := &s.buckets[j]; b != nil; b = b.next.Load() {
			total++
		}
	}
	return total
}

type advScenario struct {
	t       *testing.T
	tbl     *Table[int, int]
	hash    func(int) uint64
	keys    int
	cur     map[int]*node.Node[int, int]
	history []*node.Node[int, int]
	seq     int
}

func (a *advScenario) put(k int) {
	a.t.Helper()
	a.seq++
	n := node.New(k, a.seq, a.hash(k), 1)
	if old := a.tbl.Store(n); old != a.cur[k] {
		a.t.Fatalf("Store(%d) returned %p, want %p", k, old, a.cur[k])
	}
	a.cur[k] = n
	a.history = append(a.history, n)
}

// verify checks the table against the oracle: every key and every absent key (which share hashes with present keys in
// the colliding families), probes of present keys with a hash differing only in index, middle or tag bits, Range and
// Len, plus the structural invariants.
func (a *advScenario) verify(stage string) {
	a.t.Helper()
	for k := range 2 * a.keys {
		h := a.hash(k)
		if got := a.tbl.Get(h, k); got != a.cur[k] {
			a.t.Fatalf("%s: Get(%d) = %p, want %p", stage, k, got, a.cur[k])
		}
		if k%3 != 0 {
			continue
		}
		for _, flip := range []uint64{1, 1 << 31, 1 << 40, 1 << 63} {
			if got := a.tbl.Get(h^flip, k); got != nil {
				a.t.Fatalf("%s: Get(%d) with hash ^ %#x found %p", stage, k, flip, got)
			}
		}
	}
	seen := make(map[int]*node.Node[int, int])
	a.tbl.Range(func(n *node.Node[int, int]) bool {
		if _, dup := seen[n.Key]; dup {
			a.t.Fatalf("%s: Range visited key %d twice", stage, n.Key)
		}
		seen[n.Key] = n
		return true
	})
	if len(seen) != len(a.cur) {
		a.t.Fatalf("%s: Range visited %d keys, want %d", stage, len(seen), len(a.cur))
	}
	for k, n := range a.cur {
		if seen[k] != n {
			a.t.Fatalf("%s: Range saw %p for key %d, want %p", stage, seen[k], k, n)
		}
	}
	if got := advCheckInvariants(a.t, a.tbl); len(got) != len(a.cur) {
		a.t.Fatalf("%s: table holds %d keys, want %d", stage, len(got), len(a.cur))
	}
}

func (a *advScenario) run(r *rand.Rand) {
	order := r.Perm(a.keys)
	buckets := a.tbl.Buckets()
	for _, k := range order {
		a.put(k)
		if b := a.tbl.Buckets(); b != buckets {
			advCheckInvariants(a.t, a.tbl)
			buckets = b
		}
	}
	a.verify("insert")

	for i, k := range order {
		if i%3 == 0 {
			a.put(k)
		}
	}
	a.verify("replace")

	for _, n := range a.history {
		if a.cur[n.Key] != n && a.tbl.DeleteNode(n) {
			a.t.Fatalf("DeleteNode of replaced node for key %d succeeded", n.Key)
		}
	}
	for i, k := range order {
		switch i % 4 {
		case 1:
			if got := a.tbl.Delete(a.hash(k), k); got != a.cur[k] {
				a.t.Fatalf("Delete(%d) = %p, want %p", k, got, a.cur[k])
			}
			delete(a.cur, k)
		case 2:
			if !a.tbl.DeleteNode(a.cur[k]) {
				a.t.Fatalf("DeleteNode of current node for key %d failed", k)
			}
			delete(a.cur, k)
		}
	}
	a.verify("delete")
	for i, k := range order {
		if i%4 == 1 && a.tbl.Delete(a.hash(k), k) != nil {
			a.t.Fatalf("second Delete(%d) found a node", k)
		}
	}

	for i, k := range order {
		if i%4 == 1 || i%4 == 2 {
			a.put(k)
		}
	}
	a.verify("reinsert")

	before, resizes := advChainBuckets(a.tbl), a.tbl.resizes.Load()
	for i := len(order) - 1; i >= 0; i-- {
		k := order[i]
		if got := a.tbl.Delete(a.hash(k), k); got != a.cur[k] {
			a.t.Fatalf("drain Delete(%d) = %p, want %p", k, got, a.cur[k])
		}
		delete(a.cur, k)
	}
	a.verify("empty")
	for _, k := range order {
		a.put(k)
	}
	a.verify("refill")
	if after := advChainBuckets(a.tbl); after != before || a.tbl.resizes.Load() != resizes {
		a.t.Fatalf("refilling the same keys grew the table: %d buckets -> %d, resizes %d -> %d",
			before, after, resizes, a.tbl.resizes.Load())
	}
}

// TestAdvCollisionFamiliesAcrossGrowth runs a full store/replace/delete/reinsert/drain/refill lifecycle for every
// collision family on every table configuration, against a map oracle and the structural invariants.
func TestAdvCollisionFamiliesAcrossGrowth(t *testing.T) {
	for _, fam := range advFamilies {
		keys := 700
		if fam.chained {
			keys = 300
		}
		for ci, cfg := range advConfigs(t) {
			t.Run(fam.name+"/"+cfg.name, func(t *testing.T) {
				tbl := cfg.build()
				a := &advScenario{t: t, tbl: tbl, hash: fam.hash, keys: keys, cur: map[int]*node.Node[int, int]{}}
				a.run(rand.New(rand.NewPCG(uint64(ci), 99)))
				if cfg.bound > 0 && tbl.Buckets() > cfg.bound && tbl.resizes.Load() > 0 {
					t.Fatalf("bounded table grew to %d buckets, bound %d", tbl.Buckets(), cfg.bound)
				}
				if cfg.bound > 0 && fam.name == "mixed" && tbl.Buckets() != cfg.bound {
					t.Fatalf("bounded table ended with %d buckets, want exactly %d", tbl.Buckets(), cfg.bound)
				}
			})
		}
	}
}

// TestAdvCopyChainPlacesEveryNodeExactlyOnce drives copyChain directly for every old bucket count 4..40 and every new
// count in (old, 2*old], with nodes on both edges of every old root's hash range, multi-bucket chains with holes, and
// checks every surviving node lands exactly once in the root Get would probe, chains are packed (the property resume
// relies on) and the stripes are rebuilt exactly. It also asserts the three-way split and shared destination roots are
// actually exercised.
func TestAdvCopyChainPlacesEveryNodeExactlyOnce(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 7))
	threeWay, shared := 0, 0
	for oldN := 4; oldN <= 40; oldN++ {
		for newN := oldN + 1; newN <= 2*oldN; newN++ {
			old := newState[int, int](oldN)
			want := make(map[*node.Node[int, int]]uint64)
			key := 0
			for i := range oldN {
				lo := (uint64(i)<<32 + uint64(oldN) - 1) / uint64(oldN)
				hi := (uint64(i+1)<<32+uint64(oldN)-1)/uint64(oldN) - 1
				h32s := []uint64{lo, lo, hi, hi, lo + (hi-lo)/2}
				for range 7 {
					h32s = append(h32s, lo+r.Uint64N(hi-lo+1))
				}
				var chain []*node.Node[int, int]
				for _, h32 := range h32s {
					h := r.Uint64()&^math.MaxUint32 | h32
					if old.index(h) != uint64(i) {
						t.Fatalf("oldN %d: hash %#x indexes %d, want %d", oldN, h, old.index(h), i)
					}
					n := node.New(key, key, h, 1)
					key++
					if prev, _ := old.store(&old.buckets[i], n); prev != nil {
						t.Fatal("distinct keys replaced each other")
					}
					chain = append(chain, n)
					want[n] = uint64(i)
				}
				for _, hole := range []int{3, 6} {
					n := chain[hole]
					if old.delete(&old.buckets[i], n.Hash, n.Key) != n {
						t.Fatal("delete of a middle chain node failed")
					}
					delete(want, n)
				}
			}

			ns := newState[int, int](newN)
			for i := range old.buckets {
				ns.copyChain(&old.buckets[i], uint64(i), uint64(oldN))
			}

			feeders := make(map[uint64]map[uint64]bool)
			seen := make(map[*node.Node[int, int]]bool)
			perStripe := make([]int64, len(ns.stripes))
			for j := range ns.buckets {
				for b := &ns.buckets[j]; b != nil; b = b.next.Load() {
					meta := b.meta.Load()
					filled := 0
					for i := range b.slots {
						n := b.slots[i].Load()
						if n == nil {
							if meta>>(i*8)&0xff != 0 {
								t.Fatalf("%d->%d: empty slot with a tag", oldN, newN)
							}
							continue
						}
						if filled != i {
							t.Fatalf("%d->%d root %d: copied chain has a hole before slot %d", oldN, newN, j, i)
						}
						filled++
						src, ok := want[n]
						if !ok || seen[n] {
							t.Fatalf("%d->%d: node %d copied twice or should not exist", oldN, newN, n.Key)
						}
						seen[n] = true
						if ns.index(n.Hash) != uint64(j) {
							t.Fatalf("%d->%d: node in root %d, Get would probe %d", oldN, newN, j, ns.index(n.Hash))
						}
						if meta>>(i*8)&0xff != tagOf(n.Hash) {
							t.Fatalf("%d->%d: wrong tag", oldN, newN)
						}
						first := src * uint64(newN) / uint64(oldN)
						if uint64(j)-first == 2 {
							threeWay++
						}
						if feeders[uint64(j)] == nil {
							feeders[uint64(j)] = map[uint64]bool{}
						}
						feeders[uint64(j)][src] = true
						perStripe[ns.index(n.Hash)&ns.stripeMask]++
					}
					if next := b.next.Load(); next != nil && filled != slotsPerBucket {
						t.Fatalf("%d->%d root %d: non-final bucket holds %d nodes", oldN, newN, j, filled)
					} else if next != nil && next.slots[0].Load() == nil {
						t.Fatalf("%d->%d root %d: copy linked an empty overflow bucket", oldN, newN, j)
					}
				}
			}
			if len(seen) != len(want) {
				t.Fatalf("%d->%d: copied %d nodes, want %d", oldN, newN, len(seen), len(want))
			}
			for j, src := range feeders {
				if len(src) > 2 {
					t.Fatalf("%d->%d: new root %d fed by %d old roots", oldN, newN, j, len(src))
				}
				if len(src) == 2 {
					shared++
				}
			}
			for i := range ns.stripes {
				if got := ns.stripes[i].n.Load(); got != perStripe[i] {
					t.Fatalf("%d->%d: stripe %d = %d, holds %d", oldN, newN, i, got, perStripe[i])
				}
			}
		}
	}
	if threeWay == 0 || shared == 0 {
		t.Fatalf("test did not exercise the hard cases: three-way splits %d, shared roots %d", threeWay, shared)
	}
}

// advFirstHash returns ceil(j * 2^32 / n), the first 32-bit hash of root j of n.
func advFirstHash(j, n uint64) uint64 {
	return (j<<32 + n - 1) / n
}

// TestAdvCopyBoundsExhaustive proves the copyChain bounds for every old count 4..300, every new count in (old, 2*old]
// and every old root, by checking both edges of each old root's hash range (index is monotonic, so the edges bound
// every hash in between): the destination is in [first, first+2], and every new root is fed by at most two
// neighbouring old roots, which is what lets cursor.resume append after the previous one.
func TestAdvCopyBoundsExhaustive(t *testing.T) {
	for oldN := uint64(4); oldN <= 300; oldN++ {
		o := &state[int, int]{n: oldN}
		for newN := oldN + 1; newN <= 2*oldN; newN++ {
			s := &state[int, int]{n: newN}
			for i := range oldN {
				lo, hi := advFirstHash(i, oldN), advFirstHash(i+1, oldN)-1
				if o.index(lo) != i || o.index(hi) != i {
					t.Fatalf("old %d root %d: edges index to %d, %d", oldN, i, o.index(lo), o.index(hi))
				}
				first := i * newN / oldN
				dLo, dHi := s.index(lo), s.index(hi)
				if dLo < first || dHi-first > 2 {
					t.Fatalf("%d->%d root %d: destinations [%d, %d], first %d", oldN, newN, i, dLo, dHi, first)
				}
				if newN == 2*oldN && (dLo != 2*i || dHi != 2*i+1) {
					t.Fatalf("doubling %d root %d: destinations [%d, %d], want [%d, %d]", oldN, i, dLo, dHi, 2*i, 2*i+1)
				}
			}
			for j := range newN {
				lo, hi := advFirstHash(j, newN), advFirstHash(j+1, newN)-1
				if lo > hi {
					t.Fatalf("new root %d of %d has an empty hash range", j, newN)
				}
				if o.index(hi)-o.index(lo) > 1 {
					t.Fatalf("%d->%d: new root %d fed by old roots %d..%d", oldN, newN, j, o.index(lo), o.index(hi))
				}
			}
		}
	}
}

// TestAdvRangeReductionAtScale checks index is in bounds, ignores the upper 32 bits and never overflows for bucket
// counts up to what MaxEntries needs, and samples the copy bounds for growth steps at that scale.
func TestAdvRangeReductionAtScale(t *testing.T) {
	maxB := uint64(bucketsFor(MaxEntries))
	if got := int64(maxB) * slotsPerBucket * loadNum / loadDen; got < MaxEntries {
		t.Fatalf("bucketsFor(MaxEntries) = %d holds only %d", maxB, got)
	}
	if bucketsFor(MaxEntries+12345) != int(maxB) || bucketsFor(math.MaxInt) != int(maxB) {
		t.Fatal("bucketsFor does not clamp hints above MaxEntries")
	}
	counts := []uint64{4, 5, 7, 255, 257, 1 << 20, 1<<20 + 1, 1 << 27, 1<<28 - 1, 1 << 28, maxB / 2, maxB - 1, maxB}
	for _, n := range counts {
		s := &state[int, int]{n: n}
		if hi, _ := bits.Mul64(math.MaxUint32, n); hi != 0 {
			t.Fatalf("n %d: (hash & MaxUint32) * n overflows uint64", n)
		}
		if s.index(0) != 0 || s.index(math.MaxUint32) != n-1 || s.index(math.MaxUint64) != n-1 {
			t.Fatalf("n %d: index edges %d, %d, %d", n, s.index(0), s.index(math.MaxUint32), s.index(math.MaxUint64))
		}
		r := rand.New(rand.NewPCG(n, 1))
		for range 2000 {
			h := r.Uint64()
			if s.index(h) != s.index(h&math.MaxUint32) || s.index(h) >= n {
				t.Fatalf("n %d: index(%#x) = %d depends on the upper bits or is out of range", n, h, s.index(h))
			}
		}
		for _, j := range []uint64{1, 2, n / 3, n / 2, n - 2, n - 1} {
			f := advFirstHash(j, n)
			if s.index(f) != j || s.index(f-1) != j-1 {
				t.Fatalf("n %d: root %d starts at %#x but index gives %d, %d", n, j, f, s.index(f-1), s.index(f))
			}
		}
	}

	// Growth steps near the maximum, derived from maxB so they stay valid whatever the bucket geometry. A doubling
	// step from n must start at ceil(n/2) so that newN <= 2*oldN holds for odd n.
	half := func(n uint64) uint64 { return (n + 1) / 2 }
	steps := [][2]uint64{
		{half(maxB), maxB}, {half(maxB) + 1, maxB}, {maxB - 1, maxB}, {maxB*2/3 + 1, maxB},
		{half(half(maxB)), half(maxB)}, {half(maxB) - 3, half(maxB) + 5},
	}
	for _, st := range steps {
		oldN, newN := st[0], st[1]
		if newN <= oldN || newN > 2*oldN {
			t.Fatalf("bad step %v", st)
		}
		o, s := &state[int, int]{n: oldN}, &state[int, int]{n: newN}
		r := rand.New(rand.NewPCG(oldN, newN))
		roots := []uint64{0, 1, 2, oldN / 2, oldN - 2, oldN - 1}
		for range 5000 {
			roots = append(roots, r.Uint64N(oldN))
		}
		for _, i := range roots {
			lo, hi := advFirstHash(i, oldN), advFirstHash(i+1, oldN)-1
			if o.index(lo) != i || o.index(hi) != i {
				t.Fatalf("%v root %d: edges index to %d, %d", st, i, o.index(lo), o.index(hi))
			}
			first := i * newN / oldN
			if d := s.index(lo); d < first {
				t.Fatalf("%v root %d: destination %d below first %d", st, i, d, first)
			}
			if d := s.index(hi); d-first > 2 {
				t.Fatalf("%v root %d: destination %d beyond first+2 (%d)", st, i, d, first)
			}
		}
	}
}

// TestAdvSWARExhaustive checks matchTag and matchEmpty on every meta built from adversarial tag bytes (including tags
// one bit apart, which is where borrow-induced false positives appear) against every possible query tag.
func TestAdvSWARExhaustive(t *testing.T) {
	bytesSet := []uint64{0x00, 0x80, 0x81, 0xc0, 0xfe, 0xff}
	var idx [slotsPerBucket]int
	for {
		var meta uint64
		var slots [slotsPerBucket]uint64
		for i, v := range idx {
			slots[i] = bytesSet[v]
			meta |= slots[i] << (i * 8)
		}
		advCheckMeta(t, meta, slots)

		i := 0
		for ; i < slotsPerBucket; i++ {
			idx[i]++
			if idx[i] < len(bytesSet) {
				break
			}
			idx[i] = 0
		}
		if i == slotsPerBucket {
			return
		}
	}
}

func advCheckMeta(t *testing.T, meta uint64, slots [slotsPerBucket]uint64) {
	e := matchEmpty(meta)
	if e&^slotMask != 0 {
		t.Fatalf("matchEmpty(%#x) reported bits outside the slots: %#x", meta, e)
	}
	for i, v := range slots {
		if got := e>>(i*8)&0x80 != 0; got != (v == 0) {
			t.Fatalf("matchEmpty(%#x) slot %d reported %v, slot byte %#x", meta, i, got, v)
		}
	}
	for tag := uint64(0x80); tag <= 0xff; tag++ {
		m := matchTag(meta, tag)
		if m&^slotMask != 0 {
			t.Fatalf("matchTag(%#x, %#x) reported bits outside the slots", meta, tag)
		}
		for i, v := range slots {
			reported := m>>(i*8)&0x80 != 0
			if v == tag && !reported {
				t.Fatalf("matchTag(%#x, %#x) missed slot %d", meta, tag, i)
			}
			if v == 0 && reported {
				t.Fatalf("matchTag(%#x, %#x) reported empty slot %d", meta, tag, i)
			}
		}
		if m != 0 && slots[slotOf(m)] != tag {
			t.Fatalf("matchTag(%#x, %#x): lowest reported slot %d holds %#x", meta, tag, slotOf(m), slots[slotOf(m)])
		}
	}
}

// TestAdvZeroBytesArbitraryBytes checks the documented zeroBytes contract on arbitrary byte pairs in every adjacent
// slot position: it never misses a zero byte and the lowest reported byte is always zero.
func TestAdvZeroBytesArbitraryBytes(t *testing.T) {
	for p := range slotsPerBucket - 1 {
		for a := range uint64(256) {
			for b := range uint64(256) {
				x := uint64(0xffffffffffffffff)
				x &^= 0xffff << (p * 8)
				x |= (a | b<<8) << (p * 8)
				m := zeroBytes(x)
				for i := range slotsPerBucket {
					if x>>(i*8)&0xff == 0 && m>>(i*8)&0x80 == 0 {
						t.Fatalf("zeroBytes(%#x) missed zero byte %d", x, i)
					}
				}
				if m != 0 && x>>(slotOf(m)*8)&0xff != 0 {
					t.Fatalf("zeroBytes(%#x) lowest reported byte %d is not zero", x, slotOf(m))
				}
			}
		}
	}
}

// TestAdvBucketFindIgnoresForeignTags builds buckets by hand whose tags collide with the probe but whose nodes differ
// in hash or key, and checks find and findNode only accept the exact match, in any slot.
func TestAdvBucketFindIgnoresForeignTags(t *testing.T) {
	const hash = 0xfe00000012345678
	tag := tagOf(hash)
	for target := range slotsPerBucket {
		var b bucket[int, int]
		var want *node.Node[int, int]
		for i := range slotsPerBucket {
			var n *node.Node[int, int]
			switch {
			case i == target:
				n = node.New(1, 0, hash, 1)
				want = n
			case i%2 == 0:
				n = node.New(1, 0, hash^1<<40, 1) // same key and tag, different hash
			default:
				n = node.New(2+i, 0, hash, 1) // same hash, different key
			}
			b.publish(i, tag, n)
		}
		if i, n := b.find(b.meta.Load(), tag, hash, 1); n != want || i != target {
			t.Fatalf("target %d: find returned slot %d node %p, want %p", target, i, n, want)
		}
		if i := b.findNode(b.meta.Load(), tag, want); i != target {
			t.Fatalf("target %d: findNode returned %d", target, i)
		}
		if _, n := b.find(b.meta.Load(), tag, hash, 999); n != nil {
			t.Fatal("find matched an absent key with a shared hash")
		}
		b.clear(target)
		if _, n := b.find(b.meta.Load(), tag, hash, 1); n != nil {
			t.Fatal("find matched a cleared slot")
		}
		if b.findNode(b.meta.Load(), tag, want) != -1 {
			t.Fatal("findNode matched a cleared slot")
		}
		if slotOf(matchEmpty(b.meta.Load())) != target {
			t.Fatalf("cleared slot %d not reported as the first empty slot", target)
		}
	}
}

// TestAdvRangeMutatingAcrossGrowth mirrors Clear: Range deletes every node it visits while fn also stores enough new
// keys to force a resize mid-Range. Every node present before Range must be visited exactly once and its DeleteNode,
// now applied to the successor state, must succeed.
func TestAdvRangeMutatingAcrossGrowth(t *testing.T) {
	for _, fam := range advFamilies {
		for _, cfg := range []advConfig{
			{name: "sync", build: func() *Table[int, int] { return New[int, int](0) }},
			{name: "unsync", build: func() *Table[int, int] { return NewUnsynchronized[int, int](0, 0) }},
			{name: "bounded", build: func() *Table[int, int] { return NewBounded[int, int](0, 400) }},
		} {
			t.Run(fam.name+"/"+cfg.name, func(t *testing.T) {
				tbl := cfg.build()
				const keys, extra = 40, 400
				orig := make(map[*node.Node[int, int]]int)
				for k := range keys {
					n := node.New(k, k, fam.hash(k), 1)
					tbl.Store(n)
					orig[n] = 0
				}
				resizes := tbl.resizes.Load()
				grown := false
				tbl.Range(func(n *node.Node[int, int]) bool {
					if _, ok := orig[n]; !ok {
						return true
					}
					orig[n]++
					if !grown {
						grown = true
						for k := keys; k < keys+extra; k++ {
							tbl.Store(node.New(k, k, fam.hash(k), 1))
						}
					}
					if !tbl.DeleteNode(n) {
						t.Fatalf("DeleteNode of visited node %d failed", n.Key)
					}
					return true
				})
				if tbl.resizes.Load() == resizes {
					t.Fatal("fn did not force a resize during Range")
				}
				for n, c := range orig {
					if c != 1 {
						t.Fatalf("node %d visited %d times", n.Key, c)
					}
				}
				got := advCheckInvariants(t, tbl)
				if len(got) != extra {
					t.Fatalf("table holds %d keys after Range, want %d", len(got), extra)
				}
				for k := keys; k < keys+extra; k++ {
					if n := tbl.Get(fam.hash(k), k); n == nil || n.Key != k {
						t.Fatalf("key %d stored during Range is missing", k)
					}
				}
			})
		}
	}
}

// TestAdvScanSweepDeletingInLongChains mirrors the expiry sweep: one full Scan cycle that deletes every other node
// visits every node exactly once even when the deletions punch holes in multi-bucket chains.
func TestAdvScanSweepDeletingInLongChains(t *testing.T) {
	for _, fam := range advFamilies {
		t.Run(fam.name, func(t *testing.T) {
			tbl := NewBounded[int, int](0, 300)
			const keys = 600
			for k := range keys {
				tbl.Store(node.New(k, k, fam.hash(k), 1))
			}
			visits := make(map[int]int)
			cursor := tbl.Scan(math.MaxUint64/3, tbl.Buckets(), func(n *node.Node[int, int]) {
				visits[n.Key]++
				if n.Key%2 == 0 && !tbl.DeleteNode(n) {
					t.Fatalf("DeleteNode(%d) during Scan failed", n.Key)
				}
			})
			if cursor != math.MaxUint64/3+uint64(tbl.Buckets()) {
				t.Fatalf("Scan returned cursor %d", cursor)
			}
			for k := range keys {
				if visits[k] != 1 {
					t.Fatalf("key %d visited %d times in one cycle", k, visits[k])
				}
				n := tbl.Get(fam.hash(k), k)
				if (k%2 == 0) != (n == nil) {
					t.Fatalf("key %d presence after sweep: %v", k, n != nil)
				}
			}
			advCheckInvariants(t, tbl)
		})
	}
}
