package table

import (
	"github.com/go-playground/cache/v2/internal/xruntime"
	"hash/maphash"
	"math/rand/v2"
	"strconv"
	"testing"
	"unsafe"

	"github.com/go-playground/cache/v2/internal/node"
)

var seed = maphash.MakeSeed()

func hashOf[K comparable](k K) uint64 {
	return maphash.Comparable(seed, k)
}

func newNode[K comparable](k K, v int, hash uint64) *node.Node[K, int] {
	return node.New(k, v, hash, 1)
}

func store[K comparable](t *Table[K, int], k K, v int) *node.Node[K, int] {
	n := newNode(k, v, hashOf(k))
	t.Store(n)
	return n
}

// collect returns every node Range visits, failing on a duplicate key.
func collect[K comparable](t testing.TB, tbl *Table[K, int]) map[K]*node.Node[K, int] {
	t.Helper()
	seen := make(map[K]*node.Node[K, int])
	tbl.Range(func(n *node.Node[K, int]) bool {
		if _, dup := seen[n.Key]; dup {
			t.Fatalf("Range visited key %v twice", n.Key)
		}
		seen[n.Key] = n
		return true
	})
	return seen
}

func TestStoreThenGetReturnsSameNode(t *testing.T) {
	tbl := New[string, int](0)
	n := newNode("a", 1, hashOf("a"))
	if old := tbl.Store(n); old != nil {
		t.Fatalf("Store of new key returned %v, want nil", old)
	}
	if got := tbl.Get(n.Hash, "a"); got != n {
		t.Fatalf("Get = %p, want %p", got, n)
	}
}

func TestGetMissingKeyReturnsNil(t *testing.T) {
	tbl := New[string, int](0)
	if got := tbl.Get(hashOf("a"), "a"); got != nil {
		t.Fatalf("Get on empty table = %v, want nil", got)
	}
	tbl.Store(newNode("a", 1, 7))
	if got := tbl.Get(7, "b"); got != nil {
		t.Fatalf("Get of a different key with the same hash = %v, want nil", got)
	}
	if got := tbl.Get(8, "a"); got != nil {
		t.Fatalf("Get with a different hash = %v, want nil", got)
	}
}

func TestStoreReplacesAndReturnsOld(t *testing.T) {
	tbl := New[string, int](0)
	first := store(tbl, "a", 1)
	second := newNode("a", 2, hashOf("a"))
	if old := tbl.Store(second); old != first {
		t.Fatalf("Store replace returned %p, want %p", old, first)
	}
	if got := tbl.Get(second.Hash, "a"); got != second {
		t.Fatalf("Get after replace = %p, want %p", got, second)
	}
	if tbl.Len() != 1 {
		t.Fatalf("Len after replace = %d, want 1", tbl.Len())
	}
}

func TestDeleteReturnsRemovedNode(t *testing.T) {
	tbl := New[string, int](0)
	n := store(tbl, "a", 1)
	store(tbl, "b", 2)
	if old := tbl.Delete(n.Hash, "a"); old != n {
		t.Fatalf("Delete = %p, want %p", old, n)
	}
	if got := tbl.Get(n.Hash, "a"); got != nil {
		t.Fatalf("Get after Delete = %v, want nil", got)
	}
	if old := tbl.Delete(n.Hash, "a"); old != nil {
		t.Fatalf("second Delete = %v, want nil", old)
	}
	if tbl.Len() != 1 {
		t.Fatalf("Len = %d, want 1", tbl.Len())
	}
}

func TestDeleteNode(t *testing.T) {
	tbl := New[string, int](0)
	n := store(tbl, "a", 1)
	if !tbl.DeleteNode(n) {
		t.Fatal("DeleteNode of the mapped node = false, want true")
	}
	if tbl.Get(n.Hash, "a") != nil || tbl.Len() != 0 {
		t.Fatal("DeleteNode did not remove the key")
	}
	if tbl.DeleteNode(n) {
		t.Fatal("second DeleteNode = true, want false")
	}

	stale := store(tbl, "b", 1)
	fresh := store(tbl, "b", 2)
	if tbl.DeleteNode(stale) {
		t.Fatal("DeleteNode of a replaced node = true, want false")
	}
	if got := tbl.Get(fresh.Hash, "b"); got != fresh {
		t.Fatalf("DeleteNode of a replaced node removed the replacement, Get = %v", got)
	}
	if tbl.DeleteNode(newNode("missing", 0, hashOf("missing"))) {
		t.Fatal("DeleteNode of a missing key = true, want false")
	}
}

func TestLenTracksDistinctKeys(t *testing.T) {
	tbl := New[int, int](0)
	for i := range 100 {
		store(tbl, i, i)
		store(tbl, i, i+1)
	}
	if tbl.Len() != 100 {
		t.Fatalf("Len = %d, want 100", tbl.Len())
	}
	for i := range 40 {
		tbl.Delete(hashOf(i), i)
	}
	if tbl.Len() != 60 {
		t.Fatalf("Len = %d, want 60", tbl.Len())
	}
}

func TestRangeVisitsEveryNodeOnce(t *testing.T) {
	tbl := New[int, int](0)
	want := make(map[int]*node.Node[int, int])
	for i := range 1000 {
		want[i] = store(tbl, i, i)
	}
	got := collect(t, tbl)
	if len(got) != len(want) {
		t.Fatalf("Range visited %d keys, want %d", len(got), len(want))
	}
	for k, n := range want {
		if got[k] != n {
			t.Fatalf("Range visited %p for key %d, want %p", got[k], k, n)
		}
	}
}

func TestRangeStopsWhenFnReturnsFalse(t *testing.T) {
	tbl := New[int, int](0)
	for i := range 100 {
		store(tbl, i, i)
	}
	calls := 0
	tbl.Range(func(*node.Node[int, int]) bool {
		calls++
		return calls < 10
	})
	if calls != 10 {
		t.Fatalf("Range called fn %d times, want 10", calls)
	}
}

func TestZeroValueKeysAndHash(t *testing.T) {
	ints := New[int, int](0)
	zi := newNode(0, 1, 0)
	ints.Store(zi)
	if ints.Get(0, 0) != zi {
		t.Fatal("zero int key with zero hash not found")
	}
	strs := New[string, int](0)
	zs := store(strs, "", 1)
	if strs.Get(zs.Hash, "") != zs {
		t.Fatal("empty string key not found")
	}
	if strs.Delete(zs.Hash, "") != zs || strs.Len() != 0 {
		t.Fatal("empty string key not deleted")
	}
}

// checkHashLayout fills a table whose hashes are all produced by h and checks every key is retrievable and deletable.
func checkHashLayout(t *testing.T, keys int, h func(int) uint64) {
	t.Helper()
	tbl := New[int, int](0)
	nodes := make([]*node.Node[int, int], keys)
	for i := range keys {
		nodes[i] = newNode(i, i, h(i))
		tbl.Store(nodes[i])
	}
	for i, n := range nodes {
		if got := tbl.Get(n.Hash, i); got != n {
			t.Fatalf("Get(%d) = %v, want %p", i, got, n)
		}
	}
	for i := 0; i < keys; i += 2 {
		if tbl.Delete(nodes[i].Hash, i) != nodes[i] {
			t.Fatalf("Delete(%d) did not return its node", i)
		}
	}
	if tbl.Len() != keys/2 {
		t.Fatalf("Len = %d, want %d", tbl.Len(), keys/2)
	}
	for i := 0; i < keys; i += 2 {
		nodes[i] = newNode(i, -i, h(i))
		if tbl.Store(nodes[i]) != nil {
			t.Fatalf("re-Store(%d) returned a node after delete", i)
		}
	}
	got := collect(t, tbl)
	if len(got) != keys || tbl.Len() != keys {
		t.Fatalf("Range visited %d, Len %d, want %d", len(got), tbl.Len(), keys)
	}
	for i, n := range nodes {
		if tbl.Get(n.Hash, i) != n || got[i] != n {
			t.Fatalf("key %d not mapped to its latest node", i)
		}
	}
}

func TestConstantHashLongChains(t *testing.T) {
	checkHashLayout(t, 2000, func(int) uint64 { return 42 })
}

func TestSameTagDifferentIndex(t *testing.T) {
	checkHashLayout(t, 1000, func(k int) uint64 { return uint64(k) })
}

func TestSameIndexDifferentTag(t *testing.T) {
	checkHashLayout(t, 128, func(k int) uint64 { return uint64(k) << 57 })
}

func TestGrowthRetainsAllEntries(t *testing.T) {
	const keys = 100_000
	tbl := New[int, int](0)
	for i := range keys {
		store(tbl, i, i)
	}
	if r := tbl.resizes.Load(); r < 10 {
		t.Fatalf("resizes = %d, want >= 10", r)
	}
	for i := range keys {
		n := tbl.Get(hashOf(i), i)
		if n == nil || n.Value != i {
			t.Fatalf("Get(%d) = %v after growth", i, n)
		}
	}
	if tbl.Len() != keys {
		t.Fatalf("Len = %d, want %d", tbl.Len(), keys)
	}
	if len(collect(t, tbl)) != keys {
		t.Fatal("Range lost entries after growth")
	}
}

func TestSizeHintAvoidsResize(t *testing.T) {
	for _, hint := range []int{1, 15, 16, 100, 1000, 12345, 100_000} {
		t.Run(strconv.Itoa(hint), func(t *testing.T) {
			tbl := New[int, int](hint)
			for i := range hint {
				store(tbl, i, i)
			}
			if r := tbl.resizes.Load(); r != 0 {
				t.Fatalf("filling to sizeHint resized %d times, want 0", r)
			}
			for i := hint; i < 4*hint+64; i++ {
				store(tbl, i, i)
			}
			if tbl.resizes.Load() == 0 {
				t.Fatal("filling far past sizeHint did not resize")
			}
		})
	}
}

func TestBucketsForIsMinimal(t *testing.T) {
	for _, hint := range []int{-5, 0, 1, 14, 15, 16, 31, 1000, 1 << 20} {
		n := bucketsFor(hint)
		if n&(n-1) != 0 || n < minBuckets {
			t.Fatalf("bucketsFor(%d) = %d, want a power of two >= %d", hint, n, minBuckets)
		}
		if threshold := n * slotsPerBucket * loadNum / loadDen; threshold < hint {
			t.Fatalf("bucketsFor(%d) = %d holds only %d before resizing", hint, n, threshold)
		}
		if half := n / 2; half >= minBuckets && half*slotsPerBucket*loadNum/loadDen >= hint {
			t.Fatalf("bucketsFor(%d) = %d, but %d buckets suffice", hint, n, half)
		}
	}
}

func TestBucketIsOneCacheLine(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("bucket layout targets 64-bit platforms")
	}
	if got := unsafe.Sizeof(bucket[string, int]{}); got != bucketSize {
		t.Fatalf("bucket is %d bytes, want %d", got, bucketSize)
	}
	if got := unsafe.Sizeof(stripe{}); got != xruntime.CacheLine {
		t.Fatalf("stripe is %d bytes, want %d", got, xruntime.CacheLine)
	}
}

func TestMatchTagHasNoFalseNegatives(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for range 100_000 {
		var meta uint64
		var tags [slotsPerBucket]uint64
		for i := range tags {
			if r.IntN(3) > 0 {
				tags[i] = tagOf(r.Uint64())
			}
			meta |= tags[i] << (i * 8)
		}
		tag := tagOf(r.Uint64())
		if r.IntN(2) == 0 && tags[0] != 0 {
			tag = tags[r.IntN(slotsPerBucket)] | 0x80
		}
		m := matchTag(meta, tag)
		firstEmpty := -1
		for i, got := range tags {
			if got == tag && m&(0x80<<(i*8)) == 0 {
				t.Fatalf("matchTag(%#x, %#x) missed slot %d", meta, tag, i)
			}
			if got == 0 && firstEmpty < 0 {
				firstEmpty = i
			}
		}
		e := matchEmpty(meta)
		if firstEmpty < 0 && e != 0 || firstEmpty >= 0 && slotOf(e) != firstEmpty {
			t.Fatalf("matchEmpty(%#x) lowest slot wrong, want %d", meta, firstEmpty)
		}
	}
}

func TestHotPathsDoNotAllocate(t *testing.T) {
	tbl := New[int, int](1024)
	nodes := make([]*node.Node[int, int], 1024)
	for i := range nodes {
		nodes[i] = store(tbl, i, i)
	}
	h := hashOf(512)
	cases := map[string]func(){
		"Get":              func() { tbl.Get(h, 512) },
		"Store replace":    func() { tbl.Store(nodes[512]) },
		"Delete and Store": func() { tbl.Delete(h, 512); tbl.Store(nodes[512]) },
		"DeleteNode":       func() { tbl.DeleteNode(nodes[512]); tbl.Store(nodes[512]) },
	}
	for name, fn := range cases {
		if a := testing.AllocsPerRun(1000, fn); a != 0 {
			t.Errorf("%s allocates %v times per call, want 0", name, a)
		}
	}
}
