package tinylfu

import (
	"container/list"
	"math/rand/v2"
	"testing"
)

// lruHitRatio replays trace against a plain LRU of capacity entries.
func lruHitRatio(capacity int, trace []int) float64 {
	l := list.New()
	index := make(map[int]*list.Element, capacity)
	hits := 0
	for _, k := range trace {
		if e, ok := index[k]; ok {
			hits++
			l.MoveToFront(e)
			continue
		}
		index[k] = l.PushFront(k)
		if l.Len() > capacity {
			delete(index, l.Remove(l.Back()).(int))
		}
	}
	return float64(hits) / float64(len(trace))
}

// compareHitRatios asserts TinyLFU's hit ratio is at least gain times LRU's, static and adaptive.
func compareHitRatios(t *testing.T, name string, capacity int, trace []int, gain float64) {
	t.Helper()
	lru := lruHitRatio(capacity, trace)
	for _, adaptive := range []bool{false, true} {
		p := New[int, int](uint64(capacity), adaptive)
		p.rnd = rand.New(rand.NewPCG(1, 1)).Uint32
		hr := simulate(t, p, trace, false)
		t.Logf("%s adaptive=%t: tinylfu %.4f lru %.4f", name, adaptive, hr, lru)
		if hr < lru*gain {
			t.Errorf("%s adaptive=%t: tinylfu %.4f, want >= %.2f x lru %.4f", name, adaptive, hr, gain, lru)
		}
	}
}

func TestHitRatioZipf(t *testing.T) {
	compareHitRatios(t, "zipf", 1000, zipfTrace(42, 1.01, 100_000, 500_000), 1.1)
}

func TestHitRatioScanPolluted(t *testing.T) {
	compareHitRatios(t, "zipf+scans", 1000, scanTrace(42, 100_000, 500_000, 2000, 2000), 1.25)
}
