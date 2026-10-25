package xhash

import (
	"hash/maphash"
	"math"
	"math/bits"
	"strconv"
	"testing"
)

type userID int32

type compositeKey struct {
	tenant string
	id     int
}

func TestHashIsDeterministicPerHasher(t *testing.T) {
	hs := New[string]()
	a1, a2, b := hs.Hash("a"), hs.Hash("a"), hs.Hash("b")
	if a1 != a2 {
		t.Fatal("same key must hash identically")
	}
	if a1 == b {
		t.Fatal("distinct keys should not collide")
	}

	hc := New[compositeKey]()
	c1, c2 := hc.Hash(compositeKey{"t", 1}), hc.Hash(compositeKey{"t", 1})
	if c1 != c2 {
		t.Fatal("same composite key must hash identically")
	}
}

func TestHashSeedsDifferBetweenHashers(t *testing.T) {
	h1, h2 := New[string](), New[string]()
	if h1.Hash("key") == h2.Hash("key") {
		t.Fatal("independent hashers should use independent seeds")
	}
}

func TestKindSelection(t *testing.T) {
	cases := []struct {
		name string
		got  kind
		want kind
	}{
		{"string", kindOf[string](), kindComparable},
		{"int", kindOf[int](), kindInt},
		{"named int32", kindOf[userID](), kindInt},
		{"uint8", kindOf[uint8](), kindInt},
		{"struct", kindOf[compositeKey](), kindComparable},
		{"pointer", kindOf[*int](), kindComparable},
		{"float", kindOf[float64](), kindComparable},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got kind %d want %d", c.name, c.got, c.want)
		}
	}
}

func TestIntHashSpreadsSequentialKeys(t *testing.T) {
	h := New[int]()
	const n = 1 << 16
	const buckets = 1 << 10
	var counts [buckets]int
	var highCounts [buckets]int
	for i := range n {
		v := h.Hash(i)
		counts[v&(buckets-1)]++
		highCounts[v>>(64-bits.Len(buckets-1))]++
	}
	assertUniform(t, "low bits", counts[:], n/buckets)
	assertUniform(t, "high bits", highCounts[:], n/buckets)
}

func TestSmallIntegersUseOnlyTheirOwnBytes(t *testing.T) {
	h := New[int8]()
	if h.Hash(-1) == h.Hash(1) {
		t.Fatal("distinct int8 values should not collide")
	}
	five1, five2 := h.Hash(5), h.Hash(5)
	if five1 != five2 {
		t.Fatal("int8 hashing must be deterministic")
	}
}

// assertUniform checks counts with a chi-square goodness-of-fit test. With k buckets the statistic has mean k-1 and
// standard deviation sqrt(2(k-1)); a well mixed hash essentially never exceeds 6 standard deviations, while a poor one
// (e.g. identity on sequential keys) exceeds it by orders of magnitude.
func assertUniform(t *testing.T, name string, counts []int, expected int) {
	t.Helper()
	var chi2 float64
	for _, c := range counts {
		d := float64(c - expected)
		chi2 += d * d / float64(expected)
	}
	dof := float64(len(counts) - 1)
	if limit := dof + 6*math.Sqrt(2*dof); chi2 > limit {
		t.Fatalf("%s: chi-square %.0f exceeds %.0f, distribution is not uniform", name, chi2, limit)
	}
}

func TestZeroAllocs(t *testing.T) {
	hs, hi, hc := New[string](), New[int](), New[compositeKey]()
	k := compositeKey{"tenant", 42}
	allocs := testing.AllocsPerRun(100, func() {
		_ = hs.Hash("some-key")
		_ = hi.Hash(42)
		_ = hc.Hash(k)
	})
	if allocs != 0 {
		t.Fatalf("expected 0 allocs, got %v", allocs)
	}
}

var sink uint64

func BenchmarkHashString(b *testing.B) {
	h := New[string]()
	keys := make([]string, 1024)
	for i := range keys {
		keys[i] = "key-" + strconv.Itoa(i)
	}
	b.ResetTimer()
	for i := range b.N {
		sink = h.Hash(keys[i&1023])
	}
}

func BenchmarkMaphashComparableString(b *testing.B) {
	seed := maphash.MakeSeed()
	keys := make([]string, 1024)
	for i := range keys {
		keys[i] = "key-" + strconv.Itoa(i)
	}
	b.ResetTimer()
	for i := range b.N {
		sink = maphash.Comparable(seed, keys[i&1023])
	}
}

func BenchmarkHashInt(b *testing.B) {
	h := New[int]()
	for i := range b.N {
		sink = h.Hash(i)
	}
}

func BenchmarkMaphashComparableInt(b *testing.B) {
	seed := maphash.MakeSeed()
	for i := range b.N {
		sink = maphash.Comparable(seed, i)
	}
}
