package table

import (
	"math/rand/v2"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-playground/cache/v2/internal/node"
)

// advConcurrentFamilies are the families whose chains stay short enough for concurrent churn to finish quickly, plus
// one fully colliding family with a smaller key space.
func advConcurrentFamilies() []advFamily {
	var out []advFamily
	for _, f := range advFamilies {
		switch f.name {
		case "mixed", "sameTag", "boundaries", "pairs", "sameIndexAndTag":
			out = append(out, f)
		}
	}
	return out
}

func advConcurrentConfigs(t testing.TB) []advConfig {
	m33, m150 := advMaxEntriesFor(t, 33), advMaxEntriesFor(t, 150)
	return []advConfig{
		{name: "unbounded", build: func() *Table[int, int] { return New[int, int](0) }},
		{name: "bounded33", build: func() *Table[int, int] { return NewBounded[int, int](0, m33) }},
		{name: "bounded150", build: func() *Table[int, int] { return NewBounded[int, int](0, m150) }},
	}
}

func advChurnKeys(f advFamily) int {
	if f.name == "sameIndexAndTag" {
		return 250
	}
	return 1500
}

// TestAdvReadersDuringGrowth checks lock-free readers while writers force repeated (including non-doubling) resizes:
// keys present for the whole test are never missed and always return their exact node, and a key replaced by a single
// writer with increasing values is never observed going backwards (a reader cannot see an older state's value after a
// newer one, which would break linearizability across a resize).
func TestAdvReadersDuringGrowth(t *testing.T) {
	for _, fam := range advConcurrentFamilies() {
		for _, cfg := range advConcurrentConfigs(t) {
			t.Run(fam.name+"/"+cfg.name, func(t *testing.T) {
				runAdvReadersDuringGrowth(t, cfg.build(), fam)
			})
		}
	}
}

func runAdvReadersDuringGrowth(t *testing.T, tbl *Table[int, int], fam advFamily) {
	const stable = 48
	churn := advChurnKeys(fam)
	counterKey := stable + 2*churn + 1
	stableNodes := make([]*node.Node[int, int], stable)
	for k := range stable {
		stableNodes[k] = node.New(k, k, fam.hash(k), 1)
		tbl.Store(stableNodes[k])
	}
	tbl.Store(node.New(counterKey, 0, fam.hash(counterKey), 1))

	var stop atomic.Bool
	var readers, writers sync.WaitGroup
	for range max(2, runtime.GOMAXPROCS(0)/2) {
		readers.Go(func() {
			last := -1
			for !stop.Load() {
				for k, want := range stableNodes {
					if got := tbl.Get(want.Hash, k); got != want {
						t.Errorf("Get(%d) = %p, want stable node %p", k, got, want)
						return
					}
				}
				n := tbl.Get(fam.hash(counterKey), counterKey)
				if n == nil || n.Key != counterKey {
					t.Errorf("counter key lost: %v", n)
					return
				}
				if n.Value < last {
					t.Errorf("counter went backwards: %d after %d", n.Value, last)
					return
				}
				last = n.Value
			}
		})
	}
	writers.Go(func() {
		for v := 1; !stop.Load(); v++ {
			tbl.Store(node.New(counterKey, v, fam.hash(counterKey), 1))
		}
	})
	var churners sync.WaitGroup
	for w := range 2 {
		churners.Go(func() {
			base := stable + w*churn
			nodes := make([]*node.Node[int, int], churn)
			for round := range 2 {
				for i := range churn {
					k := base + i
					nodes[i] = node.New(k, round, fam.hash(k), 1)
					tbl.Store(nodes[i])
				}
				for i := range churn {
					k := base + i
					if i%2 == 0 {
						if got := tbl.Delete(fam.hash(k), k); got != nodes[i] {
							t.Errorf("churn Delete(%d) = %p, want %p", k, got, nodes[i])
						}
					} else if !tbl.DeleteNode(nodes[i]) {
						t.Errorf("churn DeleteNode(%d) failed", k)
					}
				}
			}
		})
	}
	churners.Wait()
	stop.Store(true)
	writers.Wait()
	readers.Wait()

	got := advCheckInvariants(t, tbl)
	if len(got) != stable+1 {
		t.Fatalf("table holds %d keys, want %d", len(got), stable+1)
	}
	for k, n := range stableNodes {
		if got[k] != n {
			t.Fatalf("stable key %d lost", k)
		}
	}
	if tbl.resizes.Load() == 0 {
		t.Fatal("churn did not resize the table")
	}
}

// TestAdvRacingInsertsOfSameKeys has many goroutines insert the same key set, each with its own nodes and in its own
// order, while the table grows. Exactly one Store per key may return nil (the first insert), no key may be mapped
// twice and every key must end mapped to one of the nodes stored for it.
func TestAdvRacingInsertsOfSameKeys(t *testing.T) {
	for _, fam := range advConcurrentFamilies() {
		for _, cfg := range advConcurrentConfigs(t) {
			t.Run(fam.name+"/"+cfg.name, func(t *testing.T) {
				tbl := cfg.build()
				keys := advChurnKeys(fam)
				workers := max(4, runtime.GOMAXPROCS(0))
				var firsts atomic.Int64
				var wg sync.WaitGroup
				for w := range workers {
					wg.Go(func() {
						r := rand.New(rand.NewPCG(uint64(w), 3))
						for _, k := range r.Perm(keys) {
							if tbl.Store(node.New(k, w, fam.hash(k), 1)) == nil {
								firsts.Add(1)
							}
						}
					})
				}
				wg.Wait()
				if got := firsts.Load(); got != int64(keys) {
					t.Fatalf("%d Stores returned nil, want exactly %d (one per key)", got, keys)
				}
				got := advCheckInvariants(t, tbl)
				if len(got) != keys {
					t.Fatalf("table holds %d keys, want %d", len(got), keys)
				}
				for k := range keys {
					if n := tbl.Get(fam.hash(k), k); n == nil || n != got[k] || n.Value < 0 || n.Value >= workers {
						t.Fatalf("key %d mapped to %v", k, n)
					}
				}
			})
		}
	}
}

// TestAdvRacingDeletesRemoveOnce has goroutines race Delete and DeleteNode over the same keys while another goroutine
// forces resizes with unrelated keys. Each key must be removed exactly once.
func TestAdvRacingDeletesRemoveOnce(t *testing.T) {
	for _, fam := range advConcurrentFamilies() {
		for _, cfg := range advConcurrentConfigs(t) {
			t.Run(fam.name+"/"+cfg.name, func(t *testing.T) {
				tbl := cfg.build()
				keys := advChurnKeys(fam) / 2
				nodes := make([]*node.Node[int, int], keys)
				for k := range keys {
					nodes[k] = node.New(k, k, fam.hash(k), 1)
					tbl.Store(nodes[k])
				}
				workers := max(4, runtime.GOMAXPROCS(0))
				var removed atomic.Int64
				var wg sync.WaitGroup
				wg.Go(func() {
					for k := keys; k < 3*keys; k++ {
						tbl.Store(node.New(k, k, fam.hash(k), 1))
					}
				})
				for w := range workers {
					wg.Go(func() {
						r := rand.New(rand.NewPCG(uint64(w), 5))
						for _, k := range r.Perm(keys) {
							if w%2 == 0 {
								if n := tbl.Delete(fam.hash(k), k); n != nil {
									if n != nodes[k] {
										t.Errorf("Delete(%d) returned a foreign node", k)
									}
									removed.Add(1)
								}
							} else if tbl.DeleteNode(nodes[k]) {
								removed.Add(1)
							}
						}
					})
				}
				wg.Wait()
				if got := removed.Load(); got != int64(keys) {
					t.Fatalf("%d removals succeeded, want exactly %d", got, keys)
				}
				got := advCheckInvariants(t, tbl)
				if len(got) != 2*keys {
					t.Fatalf("table holds %d keys, want %d", len(got), 2*keys)
				}
				for k := range keys {
					if tbl.Get(fam.hash(k), k) != nil {
						t.Fatalf("deleted key %d still mapped", k)
					}
				}
			})
		}
	}
}
