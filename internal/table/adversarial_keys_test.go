package table

import (
	"math"
	"testing"

	"github.com/go-playground/cache/v2/internal/node"
	"github.com/go-playground/cache/v2/internal/xhash"
)

// TestAdvFloatKeysFollowMapSemantics runs float keys hashed by xhash through the table and checks the result matches
// Go map semantics: +0 and -0 are the same key, while every NaN is a distinct key that can be stored but never found
// by key, only removed by node.
func TestAdvFloatKeysFollowMapSemantics(t *testing.T) {
	h := xhash.New[float64]()
	tbl := New[float64, int](0)
	negZero := math.Copysign(0, -1)

	pos := node.New(0.0, 1, h.Hash(0.0), 1)
	tbl.Store(pos)
	if got := tbl.Get(h.Hash(negZero), negZero); got != pos {
		t.Fatalf("Get(-0) = %v, want the +0 node", got)
	}
	neg := node.New(negZero, 2, h.Hash(negZero), 1)
	if old := tbl.Store(neg); old != pos {
		t.Fatalf("Store(-0) replaced %v, want the +0 node", old)
	}
	if tbl.Len() != 1 {
		t.Fatalf("Len = %d after storing +0 and -0, want 1", tbl.Len())
	}

	nan := math.NaN()
	var nans []*node.Node[float64, int]
	for i := range 3 {
		n := node.New(nan, i, h.Hash(nan), 1)
		if old := tbl.Store(n); old != nil {
			t.Fatalf("Store(NaN) replaced %v; NaN != NaN, so every NaN is a new key", old)
		}
		nans = append(nans, n)
	}
	if tbl.Len() != 4 {
		t.Fatalf("Len = %d, want 4 (one zero, three NaNs)", tbl.Len())
	}
	for _, n := range nans {
		if tbl.Get(n.Hash, nan) != nil || tbl.Delete(n.Hash, nan) != nil {
			t.Fatal("a NaN key was found by key")
		}
	}
	for _, n := range nans {
		if !tbl.DeleteNode(n) {
			t.Fatal("DeleteNode of a NaN node failed")
		}
	}
	advCheckInvariants(t, tbl)
	if tbl.Len() != 1 || tbl.Get(h.Hash(0.0), 0.0) != neg {
		t.Fatal("removing the NaN nodes disturbed the zero key")
	}
}

// advExhaustiveIntKeys stores every value of a small integer type, hashed by xhash, and checks each is found, distinct
// and deletable.
func advExhaustiveIntKeys[K comparable](t *testing.T, values []K) {
	t.Helper()
	h := xhash.New[K]()
	tbl := New[K, int](0)
	hashes := make(map[uint64]K, len(values))
	for i, v := range values {
		hv := h.Hash(v)
		if prev, dup := hashes[hv]; dup {
			t.Fatalf("%v and %v share a 64-bit hash", prev, v)
		}
		hashes[hv] = v
		if tbl.Store(node.New(v, i, hv, 1)) != nil {
			t.Fatalf("Store(%v) replaced a node", v)
		}
	}
	got := advCheckInvariants(t, tbl)
	if len(got) != len(values) {
		t.Fatalf("table holds %d keys, want %d", len(got), len(values))
	}
	for i, v := range values {
		if n := tbl.Get(h.Hash(v), v); n == nil || n.Value != i {
			t.Fatalf("Get(%v) = %v", v, n)
		}
	}
	for _, v := range values {
		if tbl.Delete(h.Hash(v), v) == nil {
			t.Fatalf("Delete(%v) missed", v)
		}
	}
	if tbl.Len() != 0 {
		t.Fatalf("Len = %d after deleting everything", tbl.Len())
	}
}

type advSmallEnum int8

func TestAdvExhaustiveSmallIntegerKeys(t *testing.T) {
	var i8 []int8
	var u8 []uint8
	var e8 []advSmallEnum
	for v := range 256 {
		i8 = append(i8, int8(v))
		u8 = append(u8, uint8(v))
		e8 = append(e8, advSmallEnum(v))
	}
	var i16 []int16
	var u16 []uint16
	for v := range 1 << 16 {
		i16 = append(i16, int16(v))
		u16 = append(u16, uint16(v))
	}
	t.Run("int8", func(t *testing.T) { advExhaustiveIntKeys(t, i8) })
	t.Run("uint8", func(t *testing.T) { advExhaustiveIntKeys(t, u8) })
	t.Run("named int8", func(t *testing.T) { advExhaustiveIntKeys(t, e8) })
	t.Run("int16", func(t *testing.T) { advExhaustiveIntKeys(t, i16) })
	t.Run("uint16", func(t *testing.T) { advExhaustiveIntKeys(t, u16) })
}
