package core

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-playground/cache/v2/internal/node"
	"github.com/go-playground/cache/v2/internal/policy"
	"github.com/go-playground/cache/v2/internal/policy/lfu"
	"github.com/go-playground/cache/v2/internal/policy/lru"
	"github.com/go-playground/cache/v2/internal/policy/s3fifo"
	"github.com/go-playground/cache/v2/internal/policy/tinylfu"
)

type policyCase struct {
	name string
	new  func(capacity uint64) policy.Policy[int, int]
}

func policies() []policyCase {
	return []policyCase{
		{"lru", func(c uint64) policy.Policy[int, int] { return lru.New[int, int](c) }},
		{"lfu", func(c uint64) policy.Policy[int, int] { return lfu.New[int, int](c, 0) }},
		{"tinylfu", func(c uint64) policy.Policy[int, int] { return tinylfu.New[int, int](c, true) }},
		{"s3fifo", func(c uint64) policy.Policy[int, int] { return s3fifo.New[int, int](c) }},
	}
}

type fakeClock struct{ now atomic.Int64 }

func (c *fakeClock) Now() int64              { return c.now.Load() }
func (c *fakeClock) Advance(d time.Duration) { c.now.Add(int64(d)) }

func newEngine(t testing.TB, p policyCase, capacity uint64, configure func(*Config[int, int])) (*Engine[int, int], *fakeClock) {
	t.Helper()
	cfg := NewConfig[int, int](capacity, nil, p.new)
	if configure != nil {
		configure(&cfg)
	}
	e := NewEngine(cfg)
	clk := &fakeClock{}
	clk.now.Store(1)
	e.SetClockForTesting(clk.Now)
	return e, clk
}

// checkInvariants verifies, after quiescence and a drain, that the table and policy agree exactly.
func checkInvariants(t testing.TB, e *Engine[int, int]) {
	t.Helper()
	e.Drain()
	var mapped int
	var weight uint64
	e.table.Range(func(n *node.Node[int, int]) bool {
		mapped++
		weight += uint64(n.Weight)
		if !n.IsAlive() {
			t.Fatalf("mapped node %d is not alive", n.Key)
		}
		if !n.InPolicy() {
			t.Fatalf("mapped node %d is not tracked by the policy", n.Key)
		}
		return true
	})
	if mapped != e.table.Len() {
		t.Fatalf("table Len %d but Range visited %d", e.table.Len(), mapped)
	}
	if weight != e.policy.Weight() {
		t.Fatalf("mapped weight %d != policy weight %d", weight, e.policy.Weight())
	}
	if weight > e.capacity {
		t.Fatalf("weight %d exceeds capacity %d", weight, e.capacity)
	}
}

func TestSetGetRemoveClear(t *testing.T) {
	for _, p := range policies() {
		t.Run(p.name, func(t *testing.T) {
			e, _ := newEngine(t, p, 100, nil)
			e.Set(1, 10)
			if v := e.Get(1); v.IsNone() || v.Unwrap() != 10 {
				t.Fatalf("got %v", v)
			}
			e.Set(1, 11)
			if v := e.Get(1); v.Unwrap() != 11 {
				t.Fatalf("update not visible: %v", v)
			}
			e.Remove(1)
			if v := e.Get(1); v.IsSome() {
				t.Fatal("removed key still visible")
			}
			e.Remove(1)
			for i := range 50 {
				e.Set(i, i)
			}
			e.Clear()
			for i := range 50 {
				if e.Get(i).IsSome() {
					t.Fatalf("key %d survived Clear", i)
				}
			}
			checkInvariants(t, e)
			if e.Stats().Len != 0 {
				t.Fatal("expected empty after Clear")
			}
		})
	}
}

func TestCapacityIsEnforcedAfterDrain(t *testing.T) {
	for _, p := range policies() {
		for _, capacity := range []uint64{0, 1, 2, 10, 100} {
			t.Run(fmt.Sprintf("%s/%d", p.name, capacity), func(t *testing.T) {
				e, _ := newEngine(t, p, capacity, nil)
				for i := range 1000 {
					e.Set(i, i)
					if i%7 == 0 {
						e.Get(i / 2)
					}
				}
				checkInvariants(t, e)
				s := e.Stats()
				if uint64(s.Len) > capacity {
					t.Fatalf("len %d exceeds capacity %d", s.Len, capacity)
				}
				if s.Evictions != uint64(1000-s.Len) {
					t.Fatalf("evictions %d, want %d", s.Evictions, 1000-s.Len)
				}
			})
		}
	}
}

func TestWeightedCapacityRejectsHeavyEntries(t *testing.T) {
	for _, p := range policies() {
		t.Run(p.name, func(t *testing.T) {
			cfg := NewConfig[int, int](10, func(_ int, v int) uint32 { return uint32(v) }, p.new)
			e := NewEngine(cfg)
			e.Set(1, 11)
			e.Drain()
			if e.Get(1).IsSome() {
				t.Fatal("entry heavier than capacity must not be retained")
			}
			e.Set(2, 4)
			e.Set(3, 4)
			e.Drain()
			if w := e.Stats().Weight; w != 8 {
				t.Fatalf("weight %d, want 8", w)
			}
			e.Set(4, 4)
			e.Drain()
			if w := e.Stats().Weight; w > 10 {
				t.Fatalf("weight %d exceeds capacity", w)
			}
		})
	}
}

func TestUpdateReplacesWeight(t *testing.T) {
	for _, p := range policies() {
		t.Run(p.name, func(t *testing.T) {
			cfg := NewConfig[int, int](100, func(_ int, v int) uint32 { return uint32(v) }, p.new)
			e := NewEngine(cfg)
			e.Set(1, 10)
			e.Set(1, 30)
			e.Drain()
			if w := e.Stats().Weight; w != 30 {
				t.Fatalf("weight %d, want 30", w)
			}
		})
	}
}

func TestExpireAfterWrite(t *testing.T) {
	for _, p := range policies() {
		t.Run(p.name, func(t *testing.T) {
			var causes []Cause
			e, clk := newEngine(t, p, 100, func(c *Config[int, int]) {
				c.ExpireAfterWrite(time.Second)
				c.OnEviction(func(_, _ int, cause Cause) { causes = append(causes, cause) })
			})
			e.Set(1, 1)
			clk.Advance(900 * time.Millisecond)
			if e.Get(1).IsNone() {
				t.Fatal("expired too early")
			}
			clk.Advance(100 * time.Millisecond)
			if e.Get(1).IsSome() {
				t.Fatal("reads must not extend expire-after-write")
			}
			e.Drain()
			if e.Stats().Len != 0 {
				t.Fatal("expired entry not reclaimed")
			}
			if !slices.Equal(causes, []Cause{CauseExpired}) {
				t.Fatalf("causes %v", causes)
			}
			checkInvariants(t, e)
		})
	}
}

func TestExpireAfterWriteResetsOnUpdate(t *testing.T) {
	e, clk := newEngine(t, policies()[0], 100, func(c *Config[int, int]) { c.ExpireAfterWrite(time.Second) })
	e.Set(1, 1)
	clk.Advance(800 * time.Millisecond)
	e.Set(1, 2)
	clk.Advance(800 * time.Millisecond)
	if v := e.Get(1); v.IsNone() || v.Unwrap() != 2 {
		t.Fatal("update must restart the expiry")
	}
}

func TestExpireAfterAccess(t *testing.T) {
	for _, p := range policies() {
		t.Run(p.name, func(t *testing.T) {
			e, clk := newEngine(t, p, 100, func(c *Config[int, int]) { c.ExpireAfterAccess(time.Second) })
			e.Set(1, 1)
			e.Set(2, 2)
			for range 5 {
				clk.Advance(600 * time.Millisecond)
				if e.Get(1).IsNone() {
					t.Fatal("reads must extend expire-after-access")
				}
			}
			if e.Get(2).IsSome() {
				t.Fatal("unread entry should have expired")
			}
			e.Drain()
			if e.Stats().Len != 1 {
				t.Fatalf("len %d, want 1", e.Stats().Len)
			}
			checkInvariants(t, e)
		})
	}
}

func TestExpireAfterCustom(t *testing.T) {
	for _, p := range policies() {
		t.Run(p.name, func(t *testing.T) {
			e, clk := newEngine(t, p, 100, func(c *Config[int, int]) {
				c.ExpireAfter(func(_, v int) time.Duration { return time.Duration(v) * time.Second })
			})
			e.Set(1, 1)
			e.Set(5, 5)
			e.Set(0, 0) // never expires
			clk.Advance(2 * time.Second)
			if e.Get(1).IsSome() || e.Get(5).IsNone() || e.Get(0).IsNone() {
				t.Fatal("per-entry expiry not honoured")
			}
			clk.Advance(time.Hour)
			if e.Get(5).IsSome() || e.Get(0).IsNone() {
				t.Fatal("per-entry expiry not honoured after an hour")
			}
			e.Drain()
			if e.Stats().Len != 1 {
				t.Fatalf("expired entries not reclaimed, len %d", e.Stats().Len)
			}
			checkInvariants(t, e)
		})
	}
}

func TestExpiryConfigDisabling(t *testing.T) {
	cases := map[string]func(*Config[int, int]){
		"write zero":     func(c *Config[int, int]) { c.ExpireAfterWrite(0) },
		"access neg":     func(c *Config[int, int]) { c.ExpireAfterAccess(-time.Second) },
		"custom nil":     func(c *Config[int, int]) { c.ExpireAfter(nil) },
		"last wins zero": func(c *Config[int, int]) { c.ExpireAfterWrite(time.Second); c.ExpireAfterWrite(0) },
	}
	for name, configure := range cases {
		t.Run(name, func(t *testing.T) {
			e, clk := newEngine(t, policies()[0], 10, configure)
			e.Set(1, 1)
			clk.Advance(1000 * time.Hour)
			if e.Get(1).IsNone() {
				t.Fatal("expiry should be disabled")
			}
		})
	}
}

func TestEvictionCallbackOnlyForSizeAndExpiry(t *testing.T) {
	var got []int
	e, _ := newEngine(t, policies()[0], 2, func(c *Config[int, int]) {
		c.OnEviction(func(k, _ int, cause Cause) {
			if cause != CauseSize {
				t.Errorf("unexpected cause %v", cause)
			}
			got = append(got, k)
		})
	})
	e.Set(1, 1)
	e.Set(2, 2)
	e.Set(3, 3)
	e.Remove(3)
	e.Clear()
	e.Drain()
	if !slices.Equal(got, []int{1}) {
		t.Fatalf("evicted %v, want [1]", got)
	}
}

func TestEvictionCallbackMayCallBackIntoTheCache(t *testing.T) {
	var e *Engine[int, int]
	e, _ = newEngine(t, policies()[0], 1, func(c *Config[int, int]) {
		c.OnEviction(func(k, _ int, _ Cause) {
			e.Get(k)
			e.Remove(k + 1000)
			_ = e.Stats()
		})
	})
	done := make(chan struct{})
	go func() {
		for i := range 100 {
			e.Set(i, i)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: eviction callback re-entering the cache")
	}
}

func TestStatsCounters(t *testing.T) {
	e, _ := newEngine(t, policies()[0], 10, func(c *Config[int, int]) { c.RecordStats() })
	e.Set(1, 1)
	e.Get(1)
	e.Get(2)
	e.RecordLoad(false)
	e.RecordLoad(true)
	s := e.Stats()
	if s.Hits != 1 || s.Misses != 1 || s.Sets != 1 || s.Loads != 2 || s.LoadErrors != 1 {
		t.Fatalf("unexpected stats %+v", s)
	}
}

func TestStatsDisabledByDefault(t *testing.T) {
	e, _ := newEngine(t, policies()[0], 10, nil)
	e.Set(1, 1)
	e.Get(1)
	e.RecordLoad(true)
	if s := e.Stats(); s.Hits != 0 || s.Sets != 0 || s.Loads != 0 || s.Len != 1 || s.Capacity != 10 {
		t.Fatalf("unexpected stats %+v", s)
	}
}

// TestOutOfOrderTasksNeverLeakOrResurrect replays task orderings that concurrent producers can create, checking the
// engine's state machine directly.
func TestOutOfOrderTasksNeverLeakOrResurrect(t *testing.T) {
	for _, p := range policies() {
		t.Run(p.name, func(t *testing.T) {
			e, _ := newEngine(t, p, 100, nil)
			mk := func(k int) *node.Node[int, int] { return node.New(k, k, e.Hash(k), 1) }

			// remove dequeued before its add
			n := mk(1)
			e.table.Store(n)
			e.table.Delete(n.Hash, n.Key)
			n.Retire()
			e.mu.Lock()
			e.remove(n)
			e.add(n)
			e.mu.Unlock()
			if n.InPolicy() || !n.IsDead() {
				t.Fatal("removed node must not be added")
			}

			// update dequeued before the add of the node it replaces
			old, repl := mk(2), mk(2)
			e.table.Store(old)
			e.table.Store(repl)
			old.Retire()
			e.mu.Lock()
			e.update(old, repl)
			e.add(old)
			e.mu.Unlock()
			if old.InPolicy() || !repl.InPolicy() {
				t.Fatal("replacement must be tracked, replaced node must not")
			}

			// update whose replacement was already removed
			a, b := mk(3), mk(3)
			e.table.Store(a)
			e.mu.Lock()
			e.add(a)
			e.mu.Unlock()
			e.table.Store(b)
			a.Retire()
			e.table.Delete(b.Hash, b.Key)
			b.Retire()
			e.mu.Lock()
			e.remove(b)
			e.update(a, b)
			e.mu.Unlock()
			if a.InPolicy() || b.InPolicy() {
				t.Fatal("neither node may remain tracked")
			}
			checkInvariants(t, e)
		})
	}
}

// TestConcurrentStress hammers every policy with mixed operations then verifies table/policy consistency.
func TestConcurrentStress(t *testing.T) {
	for _, p := range policies() {
		for _, withExpiry := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/expiry=%v", p.name, withExpiry), func(t *testing.T) {
				e, clk := newEngine(t, p, 64, func(c *Config[int, int]) {
					c.RecordStats()
					c.OnEviction(func(int, int, Cause) {})
					if withExpiry {
						c.ExpireAfterAccess(50 * time.Millisecond)
					}
				})
				var wg sync.WaitGroup
				for g := range 8 {
					wg.Go(func() {
						rng := rand.New(rand.NewPCG(uint64(g), 0))
						for range 20_000 {
							k := int(rng.Uint32() % 256)
							switch r := rng.IntN(100); {
							case r < 60:
								if v := e.Get(k); v.IsSome() && v.Unwrap() != k {
									t.Errorf("key %d returned value %d", k, v.Unwrap())
								}
							case r < 90:
								e.Set(k, k)
							case r < 98:
								e.Remove(k)
							case r < 99:
								clk.Advance(time.Millisecond)
							default:
								e.Clear()
							}
						}
					})
				}
				wg.Wait()
				checkInvariants(t, e)
				s := e.Stats()
				if s.Hits+s.Misses == 0 || s.Sets == 0 {
					t.Fatalf("stats not recorded: %+v", s)
				}
			})
		}
	}
}

// TestPerKeyLinearizability has each goroutine own a disjoint key range: its own writes and removes must be visible
// to its own subsequent reads despite concurrent maintenance by other goroutines.
func TestPerKeyLinearizability(t *testing.T) {
	for _, p := range policies() {
		t.Run(p.name, func(t *testing.T) {
			e, _ := newEngine(t, p, 1<<20, nil)
			var wg sync.WaitGroup
			for g := range 8 {
				wg.Go(func() {
					for i := range 5000 {
						k := g*1_000_000 + i%100
						e.Set(k, i)
						if v := e.Get(k); v.IsNone() || v.Unwrap() != i {
							t.Errorf("key %d: read %v after writing %d", k, v, i)
							return
						}
						if i%3 == 0 {
							e.Remove(k)
							if e.Get(k).IsSome() {
								t.Errorf("key %d visible after its removal", k)
								return
							}
						}
					}
				})
			}
			wg.Wait()
			checkInvariants(t, e)
		})
	}
}

// TestEngineMatchesExactLRUModelWhenDrainedEachOp drives the engine single-threaded with a drain after every op and
// compares it to an exact LRU model, proving the engine's task/read plumbing applies events faithfully.
func TestEngineMatchesExactLRUModelWhenDrainedEachOp(t *testing.T) {
	const capacity = 8
	e, _ := newEngine(t, policies()[0], capacity, nil)
	var model []int // front = most recent
	touch := func(k int) {
		model = slices.DeleteFunc(model, func(x int) bool { return x == k })
		model = append([]int{k}, model...)
	}
	rng := rand.New(rand.NewPCG(7, 7))
	for range 20_000 {
		k := int(rng.Uint32() % 20)
		switch rng.IntN(3) {
		case 0:
			got := e.Get(k).IsSome()
			want := slices.Contains(model, k)
			if got != want {
				t.Fatalf("get %d: engine %v model %v (model %v)", k, got, want, model)
			}
			if want {
				touch(k)
			}
		case 1:
			e.Set(k, k)
			touch(k)
			if len(model) > capacity {
				model = model[:capacity]
			}
		case 2:
			e.Remove(k)
			model = slices.DeleteFunc(model, func(x int) bool { return x == k })
		}
		e.Drain()
	}
	checkInvariants(t, e)
}

func TestZeroAllocReadPath(t *testing.T) {
	for _, p := range policies() {
		t.Run(p.name, func(t *testing.T) {
			e, _ := newEngine(t, p, 1000, func(c *Config[int, int]) { c.ExpireAfterAccess(time.Hour) })
			e.Set(1, 1)
			e.Drain()
			allocs := testing.AllocsPerRun(10_000, func() { e.Get(1) })
			if allocs != 0 {
				t.Fatalf("Get allocated %v times", allocs)
			}
		})
	}
}

func TestSetAllocatesOnlyTheNode(t *testing.T) {
	for _, p := range policies() {
		t.Run(p.name, func(t *testing.T) {
			e, _ := newEngine(t, p, 1000, nil)
			for i := range 2000 {
				e.Set(i, i)
			}
			i := 0
			allocs := testing.AllocsPerRun(10_000, func() {
				e.Set(i%1500, i)
				i++
			})
			if allocs > 1 {
				t.Fatalf("Set allocated %v times, want <= 1", allocs)
			}
		})
	}
}

// TestSweepReclaimsExpiredEntriesIncrementally checks that normal maintenance, without a full Drain, reclaims expired
// entries over successive sweeps, at most one sweep per sweepInterval.
func TestSweepReclaimsExpiredEntriesIncrementally(t *testing.T) {
	var expired atomic.Int64
	e, clk := newEngine(t, policies()[0], 1<<16, func(c *Config[int, int]) {
		c.ExpireAfterWrite(time.Second)
		c.OnEviction(func(_, _ int, cause Cause) {
			if cause == CauseExpired {
				expired.Add(1)
			}
		})
	})
	const n = 20_000
	for i := range n {
		e.Set(i, i)
	}
	clk.Advance(2 * time.Second)

	cursor := e.sweepCursor
	e.Set(-1, -1)
	e.Set(-2, -2)
	if e.sweepCursor == cursor {
		t.Fatal("first maintenance after expiry should sweep")
	}
	cursor = e.sweepCursor
	e.Set(-3, -3)
	if e.sweepCursor != cursor {
		t.Fatal("a second sweep within sweepInterval must not run")
	}

	maintenances := 0
	for expired.Load() < n {
		clk.Advance(time.Duration(sweepInterval))
		e.Set(-4, -4)
		maintenances++
		if maintenances > e.table.Buckets() {
			t.Fatalf("only %d of %d expired entries reclaimed after %d sweeps", expired.Load(), n, maintenances)
		}
	}
	if s := e.Stats(); s.Len > 4 {
		t.Fatalf("len %d, expected only the live entries", s.Len)
	}
	checkInvariants(t, e)
}

// TestSweepRunsExtraRoundsWhenMostlyExpired checks the adaptive rounds: a sweep that keeps finding expired entries
// continues, so a mostly expired cache is reclaimed in a handful of sweeps rather than one bucket run per sweep.
func TestSweepRunsExtraRoundsWhenMostlyExpired(t *testing.T) {
	e, clk := newEngine(t, policies()[0], 1<<14, func(c *Config[int, int]) { c.ExpireAfterWrite(time.Second) })
	for i := range 1 << 14 {
		e.Set(i, i)
	}
	clk.Advance(2 * time.Second)
	before := e.sweepCursor
	e.Set(-1, -1)
	if scanned := e.sweepCursor - before; scanned != sweepBuckets*sweepMaxRounds {
		t.Fatalf("scanned %d buckets, want %d when every round is mostly expired", scanned, sweepBuckets*sweepMaxRounds)
	}
}

func TestSweepSkipsLiveAndUnexpiredEntries(t *testing.T) {
	e, clk := newEngine(t, policies()[0], 100, func(c *Config[int, int]) { c.ExpireAfterWrite(time.Hour) })
	for i := range 50 {
		e.Set(i, i)
	}
	clk.Advance(time.Minute)
	e.Drain()
	if e.Stats().Len != 50 {
		t.Fatal("unexpired entries must survive sweeps")
	}
}

// TestSweepOfEntryNotYetInPolicy reclaims an expired entry whose add task has not been applied yet; the later add
// must then be skipped.
func TestSweepOfEntryNotYetInPolicy(t *testing.T) {
	e, clk := newEngine(t, policies()[0], 100, func(c *Config[int, int]) { c.ExpireAfterWrite(time.Second) })
	n := e.NewNode(e.Hash(1), 1, 1)
	e.table.Store(n)
	clk.Advance(2 * time.Second)
	e.mu.Lock()
	e.drainNow = clk.Now()
	e.onSweep(n)
	e.add(n)
	e.mu.Unlock()
	if n.InPolicy() || !n.IsDead() || e.table.Len() != 0 {
		t.Fatal("expired pending entry must be reclaimed and its add skipped")
	}
	checkInvariants(t, e)
}

// TestExpiredEntryIsReclaimedByTheReadThatFindsIt checks a read of an expired key frees it at once, in both engines,
// with a CauseExpired notification, rather than leaving it for the sweep.
func TestExpiredEntryIsReclaimedByTheReadThatFindsIt(t *testing.T) {
	for _, p := range policies() {
		t.Run(p.name, func(t *testing.T) {
			var expired []int
			e, clk := newEngine(t, p, 100, func(c *Config[int, int]) {
				c.ExpireAfterWrite(time.Second)
				c.OnEviction(func(k, _ int, cause Cause) {
					if cause == CauseExpired {
						expired = append(expired, k)
					}
				})
			})
			for k := range 10 {
				e.Set(k, k)
			}
			e.Drain()
			clk.Advance(2 * time.Second)
			if e.Get(3).IsSome() {
				t.Fatal("expired entry returned")
			}
			e.Drain()
			if e.table.Get(e.Hash(3), 3) != nil {
				t.Fatal("expired entry still mapped after the read that found it")
			}
			if !slices.Contains(expired, 3) {
				t.Fatalf("no CauseExpired notification for the read-reclaimed entry, got %v", expired)
			}
			checkInvariants(t, e)
		})
	}
}

// TestTryDrainStopsAfterMaxRoundsUnderSustainedRequests simulates a write racing every maintenance round. The caller
// must stop after maxDrainRounds rather than keep draining on behalf of others, and must not block on the lock.
func TestTryDrainStopsAfterMaxRoundsUnderSustainedRequests(t *testing.T) {
	e, _ := newEngine(t, policies()[0], 16, func(c *Config[int, int]) { c.ExpireAfterWrite(time.Hour) })
	release := make(chan struct{})
	var rounds atomic.Int32
	// Maintenance reads the clock once per round, after marking itself in progress.
	e.SetClockForTesting(func() int64 {
		n := rounds.Add(1)
		if n > maxDrainRounds {
			<-release
		}
		if n <= 2*maxDrainRounds {
			e.status.Store(drainRequired)
		}
		return 1
	})

	done := make(chan struct{})
	go func() {
		e.tryDrain()
		close(done)
	}()
	select {
	case <-done:
		if got := rounds.Load(); got != maxDrainRounds {
			t.Errorf("tryDrain ran %d maintenance rounds, want %d", got, maxDrainRounds)
		}
	case <-time.After(time.Second):
		t.Error("tryDrain kept draining past maxDrainRounds on the caller's goroutine")
	}
	close(release)
	<-done
	e.Drain()
}
