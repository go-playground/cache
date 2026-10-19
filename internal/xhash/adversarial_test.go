package xhash

import (
	"math"
	"math/rand/v2"
	"runtime"
	"strings"
	"testing"
)

type (
	advI8   int8
	advU16  uint16
	advI32  int32
	advU64  uint64
	advUptr uintptr
)

// advIntMatches checks the integer fast path hashes exactly the key's own bytes, zero-extended: any byte read beyond
// the key would make the hash differ from mix(intSeed ^ want) or vary between calls.
func advIntMatches[K comparable](t *testing.T, values []K, bitsOf func(K) uint64) {
	t.Helper()
	h := New[K]()
	if h.kind != kindInt {
		t.Fatalf("%T is not on the integer path", values[0])
	}
	for _, v := range values {
		want := mix(h.intSeed ^ bitsOf(v))
		if got := h.Hash(v); got != want {
			t.Fatalf("Hash(%T(%v)) = %#x, want mix(seed ^ %#x) = %#x", v, v, got, bitsOf(v), want)
		}
	}
}

// TestAdvIntegerPathReadsExactlyTheKeyBytes covers every integer kind, signed extremes and named types.
func TestAdvIntegerPathReadsExactlyTheKeyBytes(t *testing.T) {
	advIntMatches(t, []int8{math.MinInt8, -1, 0, 1, math.MaxInt8}, func(v int8) uint64 { return uint64(uint8(v)) })
	advIntMatches(t, []int16{math.MinInt16, -1, 0, 1, math.MaxInt16}, func(v int16) uint64 { return uint64(uint16(v)) })
	advIntMatches(t, []int32{math.MinInt32, -1, 0, 1, math.MaxInt32}, func(v int32) uint64 { return uint64(uint32(v)) })
	advIntMatches(t, []int64{math.MinInt64, -1, 0, 1, math.MaxInt64}, func(v int64) uint64 { return uint64(v) })
	advIntMatches(t, []int{math.MinInt, -1, 0, 1, math.MaxInt}, func(v int) uint64 { return uint64(uint(v)) })
	advIntMatches(t, []uint8{0, 1, math.MaxUint8}, func(v uint8) uint64 { return uint64(v) })
	advIntMatches(t, []uint16{0, 1, math.MaxUint16}, func(v uint16) uint64 { return uint64(v) })
	advIntMatches(t, []uint32{0, 1, math.MaxUint32}, func(v uint32) uint64 { return uint64(v) })
	advIntMatches(t, []uint64{0, 1, math.MaxUint64}, func(v uint64) uint64 { return v })
	advIntMatches(t, []uint{0, 1, math.MaxUint}, func(v uint) uint64 { return uint64(v) })
	advIntMatches(t, []uintptr{0, 1, ^uintptr(0)}, func(v uintptr) uint64 { return uint64(v) })
	advIntMatches(t, []advI8{-128, -1, 0, 127}, func(v advI8) uint64 { return uint64(uint8(v)) })
	advIntMatches(t, []advU16{0, 0xffff}, func(v advU16) uint64 { return uint64(v) })
	advIntMatches(t, []advI32{-1, 7}, func(v advI32) uint64 { return uint64(uint32(v)) })
	advIntMatches(t, []advU64{0, math.MaxUint64}, func(v advU64) uint64 { return uint64(v) })
	advIntMatches(t, []advUptr{0, ^advUptr(0)}, func(v advUptr) uint64 { return uint64(v) })
}

// TestAdvSmallIntegersNeverCollide hashes every int8, uint8, int16 and uint16 and requires distinct 64-bit hashes.
func TestAdvSmallIntegersNeverCollide(t *testing.T) {
	advDistinct(t, "int8", 1<<8, func(h Hasher[int8], i int) uint64 { return h.Hash(int8(i)) })
	advDistinct(t, "uint8", 1<<8, func(h Hasher[uint8], i int) uint64 { return h.Hash(uint8(i)) })
	advDistinct(t, "int16", 1<<16, func(h Hasher[int16], i int) uint64 { return h.Hash(int16(i)) })
	advDistinct(t, "uint16", 1<<16, func(h Hasher[uint16], i int) uint64 { return h.Hash(uint16(i)) })
}

func advDistinct[K comparable](t *testing.T, name string, n int, hash func(Hasher[K], int) uint64) {
	t.Helper()
	h := New[K]()
	seen := make(map[uint64]int, n)
	for i := range n {
		v := hash(h, i)
		if j, dup := seen[v]; dup {
			t.Fatalf("%s: values %d and %d share hash %#x", name, j, i, v)
		}
		seen[v] = i
	}
}

// advUnmix inverts mix, proving it is a bijection on uint64: distinct integer keys (after the injective zero-extension
// and the bijective seed xor) can never share a 64-bit hash.
func advUnmix(z uint64) uint64 {
	z ^= z>>31 ^ z>>62
	z *= advInverse(0x94d049bb133111eb)
	z ^= z>>27 ^ z>>54
	z *= advInverse(0xbf58476d1ce4e5b9)
	z ^= z>>30 ^ z>>60
	return z
}

// advInverse returns the multiplicative inverse of odd c modulo 2^64 by Newton iteration.
func advInverse(c uint64) uint64 {
	x := c
	for range 5 {
		x *= 2 - c*x
	}
	return x
}

func TestAdvMixIsABijection(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 1))
	inputs := []uint64{0, 1, math.MaxUint64, 1 << 63, 0x9E3779B97F4A7C15}
	for range 100_000 {
		inputs = append(inputs, r.Uint64())
	}
	for _, x := range inputs {
		if got := advUnmix(mix(x)); got != x {
			t.Fatalf("unmix(mix(%#x)) = %#x", x, got)
		}
	}
}

type advFloatKey struct {
	f float64
	s string
}

// TestAdvFloatZeroesHashEqually: +0 == -0 in Go, so every key type containing a float must hash them equally, as the
// table finds keys by hash before comparing them.
func TestAdvFloatZeroesHashEqually(t *testing.T) {
	neg64 := math.Copysign(0, -1)
	neg32 := float32(neg64)
	if h := New[float64](); h.Hash(0) != h.Hash(neg64) {
		t.Fatal("float64 +0 and -0 hash differently")
	}
	if h := New[float32](); h.Hash(0) != h.Hash(neg32) {
		t.Fatal("float32 +0 and -0 hash differently")
	}
	if h := New[complex128](); h.Hash(complex(0, 0)) != h.Hash(complex(neg64, neg64)) {
		t.Fatal("complex128 zeroes hash differently")
	}
	if h := New[advFloatKey](); h.Hash(advFloatKey{0, "k"}) != h.Hash(advFloatKey{neg64, "k"}) {
		t.Fatal("struct with +0 and -0 field hashes differently")
	}
	if h := New[[2]float32](); h.Hash([2]float32{0, 1}) != h.Hash([2]float32{neg32, 1}) {
		t.Fatal("array with +0 and -0 element hashes differently")
	}
	if h := New[any](); h.Hash(any(0.0)) != h.Hash(any(neg64)) {
		t.Fatal("interface holding +0 and -0 hashes differently")
	}
	h := New[float64]()
	posInf1, posInf2, negInf := h.Hash(math.Inf(1)), h.Hash(math.Inf(1)), h.Hash(math.Inf(-1))
	if posInf1 == negInf || posInf1 != posInf2 {
		t.Fatal("infinities hash inconsistently")
	}
}

// TestAdvNaNHashesAreRandomised: NaN != NaN, so like a Go map each NaN is a distinct key; its hash is random per call,
// which also keeps repeated NaN inserts from piling into one chain.
func TestAdvNaNHashesAreRandomised(t *testing.T) {
	nan := math.NaN()
	h := New[float64]()
	hk := New[advFloatKey]()
	seen, seenKey := map[uint64]bool{}, map[uint64]bool{}
	for range 16 {
		seen[h.Hash(nan)] = true
		seenKey[hk.Hash(advFloatKey{nan, "k"})] = true
	}
	if len(seen) < 2 || len(seenKey) < 2 {
		t.Fatalf("NaN hashes are not randomised: %d and %d distinct of 16", len(seen), len(seenKey))
	}
}

// TestAdvUncomparableInterfaceKeyPanics: an interface key holding an uncomparable value panics in Hash, exactly like
// using it as a Go map key, before the table (and any bucket lock) is touched.
func TestAdvUncomparableInterfaceKeyPanics(t *testing.T) {
	for _, k := range []any{[]int{1}, map[int]int{}, func() {}, struct{ s []int }{}} {
		func() {
			defer func() {
				r := recover()
				err, ok := r.(runtime.Error)
				if !ok || !strings.Contains(err.Error(), "unhashable") {
					t.Fatalf("Hash(%T) recovered %v, want a runtime unhashable type error", k, r)
				}
			}()
			New[any]().Hash(k)
		}()
	}
}

type advIface interface{ id() int }

type advImpl struct{ v int }

func (a advImpl) id() int { return a.v }

// TestAdvEqualKeysHashEqually checks the property the table depends on, k1 == k2 implies Hash(k1) == Hash(k2), for
// comparable kinds that take the maphash path, with equal values built from distinct memory.
func TestAdvEqualKeysHashEqually(t *testing.T) {
	hs := New[string]()
	a := strings.Repeat("ab", 50)
	b := string([]byte(strings.Repeat("ab", 50)))
	if hs.Hash(a) != hs.Hash(b) || hs.Hash("") == hs.Hash("\x00") {
		t.Fatal("string hashing is inconsistent")
	}
	x, y := new(int), new(int)
	hp := New[*int]()
	hx1, hx2, hy := hp.Hash(x), hp.Hash(x), hp.Hash(y)
	if hx1 != hx2 || hx1 == hy {
		t.Fatal("pointer hashing is inconsistent")
	}
	ha := New[any]()
	i1, i2 := ha.Hash(any(int8(3))), ha.Hash(any(int8(3)))
	if i1 != i2 || ha.Hash(any(a)) != ha.Hash(any(b)) {
		t.Fatal("interface hashing is inconsistent")
	}
	hi := New[advIface]()
	if first, second := hi.Hash(advImpl{4}), hi.Hash(advImpl{4}); first != second {
		t.Fatal("non-empty interface hashing is inconsistent")
	}
	hb := New[bool]()
	if hb.kind != kindComparable || hb.Hash(true) == hb.Hash(false) {
		t.Fatal("bool hashing is inconsistent")
	}
	if New[[4]byte]().kind != kindComparable || New[any]().kind != kindComparable || New[advUptr]().kind != kindInt {
		t.Fatal("unexpected kind selection")
	}
}
