package table

import (
	"math/rand/v2"
	"strconv"
	"sync"
	"testing"

	"github.com/go-playground/cache/v2/internal/node"
)

// benchTable is the API shared by Table and refTable.
type benchTable[K comparable] interface {
	Get(hash uint64, key K) *node.Node[K, int]
	Store(n *node.Node[K, int]) *node.Node[K, int]
}

type fixture[K comparable] struct {
	keys   []K
	hashes []uint64
	nodes  []*node.Node[K, int]
	tables map[string]benchTable[K]
}

var (
	fixturesMu sync.Mutex
	fixtures   = map[string]any{}
)

// loadFixture builds, once per name, presized table and reference instances holding n keys.
func loadFixture[K comparable](name string, n int, key func(int) K) *fixture[K] {
	fixturesMu.Lock()
	defer fixturesMu.Unlock()
	if f, ok := fixtures[name]; ok {
		return f.(*fixture[K])
	}
	f := &fixture[K]{
		keys:   make([]K, n),
		hashes: make([]uint64, n),
		nodes:  make([]*node.Node[K, int], n),
		tables: map[string]benchTable[K]{"table": New[K, int](n), "ref": newRefTable[K, int](n)},
	}
	for i := range n {
		f.keys[i] = key(i)
		f.hashes[i] = hashOf(f.keys[i])
		f.nodes[i] = node.New(f.keys[i], i, f.hashes[i], 1)
		for _, t := range f.tables {
			t.Store(f.nodes[i])
		}
	}
	fixtures[name] = f
	return f
}

var (
	impls = []string{"table", "ref"}
	sizes = []struct {
		name string
		n    int
	}{{"100k", 100_000}, {"1M", 1_000_000}}
)

func intKey(i int) int       { return i }
func stringKey(i int) string { return "key:" + strconv.Itoa(i) }

func benchGet[K comparable](b *testing.B, tbl benchTable[K], keys []K, hashes []uint64) {
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := rand.IntN(len(keys))
		misses := 0
		for pb.Next() {
			if tbl.Get(hashes[i], keys[i]) == nil {
				misses++
			}
			if i++; i == len(keys) {
				i = 0
			}
		}
		if misses > 0 {
			b.Errorf("%d misses", misses)
		}
	})
}

func BenchmarkGet(b *testing.B) {
	for _, sz := range sizes {
		for _, impl := range impls {
			b.Run("int/"+sz.name+"/"+impl, func(b *testing.B) {
				f := loadFixture("int/"+sz.name, sz.n, intKey)
				benchGet(b, f.tables[impl], f.keys, f.hashes)
			})
			b.Run("string/"+sz.name+"/"+impl, func(b *testing.B) {
				f := loadFixture("string/"+sz.name, sz.n, stringKey)
				benchGet(b, f.tables[impl], f.keys, f.hashes)
			})
		}
	}
}

// BenchmarkGetHot has every goroutine read the same 16 keys, where reader-side writes bounce cache lines.
func BenchmarkGetHot(b *testing.B) {
	for _, impl := range impls {
		b.Run(impl, func(b *testing.B) {
			f := loadFixture("int/100k", 100_000, intKey)
			benchGet(b, f.tables[impl], f.keys[:16], f.hashes[:16])
		})
	}
}

// BenchmarkStore replaces existing keys with prebuilt nodes in parallel.
func BenchmarkStore(b *testing.B) {
	for _, impl := range impls {
		b.Run(impl, func(b *testing.B) {
			f := loadFixture("int/100k", 100_000, intKey)
			tbl := f.tables[impl]
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := rand.IntN(len(f.nodes))
				for pb.Next() {
					tbl.Store(f.nodes[i])
					if i++; i == len(f.nodes) {
						i = 0
					}
				}
			})
		})
	}
}

// BenchmarkInsertGrow inserts 100k fresh keys into a default sized table per op, including every resize.
func BenchmarkInsertGrow(b *testing.B) {
	f := loadFixture("int/100k", 100_000, intKey)
	b.ReportAllocs()
	for b.Loop() {
		tbl := New[int, int](0)
		for _, n := range f.nodes {
			tbl.Store(n)
		}
	}
}

// BenchmarkMixed is 90% Get and 10% Store over 100k int keys.
func BenchmarkMixed(b *testing.B) {
	for _, impl := range impls {
		b.Run(impl, func(b *testing.B) {
			f := loadFixture("int/100k", 100_000, intKey)
			tbl := f.tables[impl]
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := rand.IntN(len(f.nodes))
				for op := 0; pb.Next(); op++ {
					if op%10 == 0 {
						tbl.Store(f.nodes[i])
					} else {
						tbl.Get(f.hashes[i], f.keys[i])
					}
					if i++; i == len(f.nodes) {
						i = 0
					}
				}
			})
		})
	}
}
