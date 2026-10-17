package readbuf

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestAddThenDrainReturnsEveryAddedEvent(t *testing.T) {
	b := New[int]()
	vals := make([]int, 8)
	added := 0
	for i := range vals {
		vals[i] = i
		if b.Add(&vals[i]) == Added {
			added++
		}
	}
	seen := map[int]bool{}
	b.Drain(func(x *int) { seen[*x] = true })
	if len(seen) != added {
		t.Fatalf("drained %d events, added %d", len(seen), added)
	}
}

func TestStripeReportsFullWhenSaturated(t *testing.T) {
	b := &Buffer[int]{stripes: make([]ring[int], 1), mask: 0}
	v := 1
	for range ringSize {
		if res := b.Add(&v); res != Added {
			t.Fatalf("expected Added before saturation, got %v", res)
		}
	}
	if res := b.Add(&v); res != Full {
		t.Fatalf("expected Full once saturated, got %v", res)
	}

	n := 0
	b.Drain(func(*int) { n++ })
	if n != ringSize {
		t.Fatalf("expected %d drained, got %d", ringSize, n)
	}
	if res := b.Add(&v); res != Added {
		t.Fatalf("expected Added after drain, got %v", res)
	}
}

func TestDrainOnEmptyBufferIsNoop(t *testing.T) {
	b := New[int]()
	b.Drain(func(*int) { t.Fatal("no events expected") })
}

func TestConcurrentProducersNeverDuplicateOrInventEvents(t *testing.T) {
	b := New[int64]()
	const producers, perProducer = 8, 20_000
	vals := make([]int64, producers*perProducer)
	for i := range vals {
		vals[i] = int64(i)
	}

	var added atomic.Int64
	var drained int64
	seen := make([]bool, len(vals))
	var consumer sync.Mutex
	drain := func() {
		consumer.Lock()
		b.Drain(func(x *int64) {
			if seen[*x] {
				t.Errorf("event %d drained twice", *x)
			}
			seen[*x] = true
			drained++
		})
		consumer.Unlock()
	}

	var wg sync.WaitGroup
	for p := range producers {
		wg.Go(func() {
			for i := range perProducer {
				switch b.Add(&vals[p*perProducer+i]) {
				case Added:
					added.Add(1)
				case Full:
					drain()
				}
			}
		})
	}
	wg.Wait()
	drain()

	if drained != added.Load() {
		t.Fatalf("drained %d events but %d were added", drained, added.Load())
	}
}

func TestNextPow2(t *testing.T) {
	for in, want := range map[uint32]uint32{0: 1, 1: 1, 2: 2, 3: 4, 16: 16, 17: 32} {
		if got := nextPow2(in); got != want {
			t.Errorf("nextPow2(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestAddDoesNotAllocate(t *testing.T) {
	b := New[int]()
	v := 1
	allocs := testing.AllocsPerRun(1000, func() {
		if b.Add(&v) == Full {
			b.Drain(func(*int) {})
		}
	})
	if allocs != 0 {
		t.Fatalf("expected 0 allocs, got %v", allocs)
	}
}

func BenchmarkAddParallel(b *testing.B) {
	buf := New[int]()
	v := 1
	var mu sync.Mutex
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if buf.Add(&v) == Full && mu.TryLock() {
				buf.Drain(func(*int) {})
				mu.Unlock()
			}
		}
	})
}
