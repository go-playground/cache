package table

import (
	"math/rand/v2"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-playground/cache/v2/internal/node"
)

type stressConfig struct {
	hash       func(int) uint64
	sharedKeys int
	ownedKeys  int
	ops        int
	wantResize bool
}

func TestConcurrentStress(t *testing.T) {
	scale := 1
	if testing.Short() {
		scale = 10
	}
	cases := map[string]stressConfig{
		"maphash":  {hash: hashOf[int], sharedKeys: 256, ownedKeys: 4096, ops: 20_000 / scale, wantResize: true},
		"weak":     {hash: func(k int) uint64 { return uint64(k & 15) }, sharedKeys: 64, ownedKeys: 128, ops: 4_000 / scale},
		"constant": {hash: func(int) uint64 { return 7 }, sharedKeys: 32, ownedKeys: 32, ops: 2_000 / scale},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) { runStress(t, cfg) })
	}
}

// runStress hammers one table from shared, owner and range goroutines, then checks quiescent consistency.
func runStress(t *testing.T, cfg stressConfig) {
	tbl := New[int, int](1)
	workers := max(4, runtime.GOMAXPROCS(0))
	expected := make([]map[int]*node.Node[int, int], workers)
	var done atomic.Bool
	var writers, readers sync.WaitGroup

	for w := range workers {
		writers.Go(func() { stressShared(t, tbl, cfg, uint64(w)) })
		writers.Go(func() { expected[w] = stressOwner(t, tbl, cfg, w) })
	}
	for range max(2, workers/4) {
		readers.Go(func() {
			for !done.Load() {
				stressRange(t, tbl, cfg)
			}
		})
	}
	writers.Wait()
	done.Store(true)
	readers.Wait()

	checkQuiescent(t, tbl, cfg, expected)
	if cfg.wantResize && tbl.resizes.Load() == 0 {
		t.Fatal("stress did not resize the table")
	}
}

// ownedKey maps the i-th key of worker w into a range disjoint from the shared keys and other workers.
func ownedKey(w, i int) int {
	return (w+1)<<20 | i
}

func checkKey(t *testing.T, cfg stressConfig, n *node.Node[int, int], key int) {
	if n == nil {
		return
	}
	if n.Key != key || n.Hash != cfg.hash(key) {
		t.Errorf("Get(%d) returned node for key %d", key, n.Key)
	}
}

func stressShared(t *testing.T, tbl *Table[int, int], cfg stressConfig, seed uint64) {
	r := rand.New(rand.NewPCG(seed, 99))
	recent := make([]*node.Node[int, int], 0, 16)
	for i := range cfg.ops {
		k := r.IntN(cfg.sharedKeys)
		h := cfg.hash(k)
		switch r.IntN(10) {
		case 0, 1, 2:
			n := node.New(k, i, h, 1)
			tbl.Store(n)
			if len(recent) == cap(recent) {
				recent = recent[:0]
			}
			recent = append(recent, n)
		case 3:
			checkKey(t, cfg, tbl.Delete(h, k), k)
		case 4:
			if len(recent) > 0 {
				tbl.DeleteNode(recent[r.IntN(len(recent))])
			}
		default:
			checkKey(t, cfg, tbl.Get(h, k), k)
		}
	}
}

// stressOwner is the only writer of its keys, so every read of them must observe its own last write.
func stressOwner(t *testing.T, tbl *Table[int, int], cfg stressConfig, w int) map[int]*node.Node[int, int] {
	r := rand.New(rand.NewPCG(uint64(w), 7))
	mine := make(map[int]*node.Node[int, int])
	for i := range cfg.ops {
		k := ownedKey(w, r.IntN(cfg.ownedKeys))
		h := cfg.hash(k)
		switch r.IntN(10) {
		case 0, 1, 2, 3, 4, 5, 6:
			n := node.New(k, i, h, 1)
			if old := tbl.Store(n); old != mine[k] {
				t.Errorf("owner Store(%d) replaced %p, want %p", k, old, mine[k])
			}
			mine[k] = n
		case 7:
			if old := tbl.Delete(h, k); old != mine[k] {
				t.Errorf("owner Delete(%d) = %p, want %p", k, old, mine[k])
			}
			delete(mine, k)
		case 8:
			if n := mine[k]; n != nil && !tbl.DeleteNode(n) {
				t.Errorf("owner DeleteNode(%d) of its own latest node failed", k)
			}
			delete(mine, k)
		}
		if got := tbl.Get(h, k); got != mine[k] {
			t.Errorf("owner Get(%d) = %p, want its last write %p", k, got, mine[k])
			return mine
		}
	}
	return mine
}

func stressRange(t *testing.T, tbl *Table[int, int], cfg stressConfig) {
	tbl.Range(func(n *node.Node[int, int]) bool {
		if n.Hash != cfg.hash(n.Key) {
			t.Errorf("Range visited node with hash %d for key %d", n.Hash, n.Key)
		}
		return true
	})
}

func checkQuiescent(t *testing.T, tbl *Table[int, int], cfg stressConfig, expected []map[int]*node.Node[int, int]) {
	visited := collect(t, tbl)
	if tbl.Len() != len(visited) {
		t.Fatalf("Len = %d, Range visited %d", tbl.Len(), len(visited))
	}
	found := 0
	for k, n := range visited {
		if tbl.Get(cfg.hash(k), k) != n {
			t.Fatalf("Get(%d) disagrees with Range", k)
		}
		found++
	}
	for k := range cfg.sharedKeys {
		if n := tbl.Get(cfg.hash(k), k); n != nil && visited[k] != n {
			t.Fatalf("Get(%d) found a node Range did not visit", k)
		}
	}
	if found != tbl.Len() {
		t.Fatalf("Get found %d keys, Len = %d", found, tbl.Len())
	}
	for w, mine := range expected {
		for i := range cfg.ownedKeys {
			k := ownedKey(w, i)
			if got := tbl.Get(cfg.hash(k), k); got != mine[k] {
				t.Fatalf("owner %d key %d = %p, want %p", w, k, got, mine[k])
			}
		}
	}
}
