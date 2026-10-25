package core

import (
	"runtime"
	"testing"
)

func TestModeFollowsAvailableProcessors(t *testing.T) {
	if modeForTesting != modeAuto {
		t.Skip("engine mode forced by CACHE_TEST_ENGINE")
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	if e, _ := newEngine(t, policies()[0], 10, nil); !e.Direct() {
		t.Fatal("one processor should select the direct mode")
	}
	runtime.GOMAXPROCS(directMaxProcs + 1)
	if e, _ := newEngine(t, policies()[0], 10, nil); e.Direct() {
		t.Fatal("more processors should select the concurrent mode")
	}
}

func TestSetModeForTestingOverridesSelection(t *testing.T) {
	prev := modeForTesting
	defer func() { modeForTesting = prev }()
	SetModeForTesting(true)
	if e, _ := newEngine(t, policies()[0], 10, nil); !e.Direct() {
		t.Fatal("forced direct mode ignored")
	}
	SetModeForTesting(false)
	if e, _ := newEngine(t, policies()[0], 10, nil); e.Direct() {
		t.Fatal("forced concurrent mode ignored")
	}
}

// TestDirectModeUpdatesInPlace checks the direct mode's main advantage: overwriting an existing key of the same weight
// reuses its entry instead of allocating.
func TestDirectModeUpdatesInPlace(t *testing.T) {
	prev := modeForTesting
	defer func() { modeForTesting = prev }()
	SetModeForTesting(true)
	for _, p := range policies() {
		t.Run(p.name, func(t *testing.T) {
			e, _ := newEngine(t, p, 100, nil)
			e.Set(1, 1)
			h := e.Hash(1)
			before := e.table.Get(h, 1)
			i := 0
			allocs := testing.AllocsPerRun(1000, func() {
				i++
				e.Set(1, i)
			})
			if allocs != 0 {
				t.Fatalf("overwrite allocated %v times", allocs)
			}
			if e.table.Get(h, 1) != before || e.Get(1).Unwrap() != i {
				t.Fatal("overwrite must update the existing entry in place")
			}
			checkInvariants(t, e)
		})
	}
}

func TestDirectModeReclaimsExpiredEntriesOnRead(t *testing.T) {
	prev := modeForTesting
	defer func() { modeForTesting = prev }()
	SetModeForTesting(true)
	var reclaimed []int
	e, clk := newEngine(t, policies()[0], 100, func(c *Config[int, int]) {
		c.ExpireAfterWrite(1)
		c.OnEviction(func(k, _ int, cause Cause) {
			if cause == CauseExpired {
				reclaimed = append(reclaimed, k)
			}
		})
	})
	e.Set(1, 1)
	clk.Advance(2)
	if e.Get(1).IsSome() {
		t.Fatal("expired entry returned")
	}
	if len(reclaimed) != 1 || e.table.Len() != 0 {
		t.Fatalf("expired entry must be reclaimed by the read that found it, reclaimed %v", reclaimed)
	}
}
