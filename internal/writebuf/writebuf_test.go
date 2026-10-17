package writebuf

import (
	"runtime"
	"sync"
	"testing"
)

func TestFIFOOrderForSingleProducer(t *testing.T) {
	q := New[int](8)
	for i := range 8 {
		if !q.Enqueue(i) {
			t.Fatalf("enqueue %d failed before capacity", i)
		}
	}
	for i := range 8 {
		v, ok := q.Dequeue()
		if !ok || v != i {
			t.Fatalf("dequeue %d: got (%d, %v)", i, v, ok)
		}
	}
	if _, ok := q.Dequeue(); ok {
		t.Fatal("expected empty queue")
	}
}

func TestEnqueueReportsFullAndRecoversAfterDequeue(t *testing.T) {
	q := New[int](4)
	for i := range 4 {
		q.Enqueue(i)
	}
	if q.Enqueue(99) {
		t.Fatal("expected full queue to reject")
	}
	if v, _ := q.Dequeue(); v != 0 {
		t.Fatalf("expected 0, got %d", v)
	}
	if !q.Enqueue(4) {
		t.Fatal("expected space after dequeue")
	}
	for want := 1; want <= 4; want++ {
		if v, _ := q.Dequeue(); v != want {
			t.Fatalf("expected %d, got %d", want, v)
		}
	}
}

func TestCapacityRoundsUpToPowerOfTwo(t *testing.T) {
	q := New[int](5)
	n := 0
	for q.Enqueue(n) {
		n++
	}
	if n != 8 {
		t.Fatalf("expected capacity 8, got %d", n)
	}
}

func TestDequeueClearsSlotForGC(t *testing.T) {
	q := New[*int](2)
	v := 1
	q.Enqueue(&v)
	q.Dequeue()
	if q.buf[0].val != nil {
		t.Fatal("dequeued slot should not retain the pointer")
	}
}

func TestConcurrentProducersDeliverEverythingExactlyOncePerProducerInOrder(t *testing.T) {
	const producers, perProducer = 8, 50_000
	type item struct{ p, i int }
	q := New[item](64)
	var consumer sync.Mutex
	next := make([]int, producers)
	received := 0
	drain := func() {
		consumer.Lock()
		defer consumer.Unlock()
		for {
			it, ok := q.Dequeue()
			if !ok {
				return
			}
			if it.i != next[it.p] {
				t.Errorf("producer %d: got %d want %d", it.p, it.i, next[it.p])
			}
			next[it.p]++
			received++
		}
	}

	var wg sync.WaitGroup
	for p := range producers {
		wg.Go(func() {
			for i := range perProducer {
				for !q.Enqueue(item{p, i}) {
					drain()
				}
			}
		})
	}
	wg.Wait()
	drain()
	if received != producers*perProducer {
		t.Fatalf("received %d, want %d", received, producers*perProducer)
	}
}

func TestZeroAllocs(t *testing.T) {
	q := New[int](16)
	allocs := testing.AllocsPerRun(1000, func() {
		q.Enqueue(1)
		q.Dequeue()
	})
	if allocs != 0 {
		t.Fatalf("expected 0 allocs, got %v", allocs)
	}
}

func TestStripedDeliversEverythingExactlyOnce(t *testing.T) {
	const producers, perProducer = 16, 20_000
	type item struct{ p, i int }
	s := NewStriped[item](8)
	var consumer sync.Mutex
	seen := make([][]bool, producers)
	for p := range seen {
		seen[p] = make([]bool, perProducer)
	}
	received := 0
	drain := func() {
		consumer.Lock()
		defer consumer.Unlock()
		s.Drain(func(it item) {
			if seen[it.p][it.i] {
				t.Errorf("item %v delivered twice", it)
			}
			seen[it.p][it.i] = true
			received++
		})
	}
	var wg sync.WaitGroup
	for p := range producers {
		wg.Go(func() {
			for i := range perProducer {
				for !s.Enqueue(item{p, i}) {
					drain()
				}
			}
		})
	}
	wg.Wait()
	drain()
	if received != producers*perProducer {
		t.Fatalf("received %d, want %d", received, producers*perProducer)
	}
}

func TestStripedReportsFullPerStripe(t *testing.T) {
	s := NewStriped[int](2)
	n := 0
	for s.Enqueue(n) && n < 1_000 {
		n++
	}
	if n == 1_000 {
		t.Fatal("a stripe must eventually report full")
	}
	got := 0
	s.Drain(func(int) { got++ })
	if got != n {
		t.Fatalf("drained %d, enqueued %d", got, n)
	}
}

func TestStripedSingleProducerNeverGrows(t *testing.T) {
	s := NewStriped[int](8)
	for i := range 100_000 {
		for !s.Enqueue(i) {
			s.Drain(func(int) {})
		}
	}
	if s.Stripes() != 1 {
		t.Fatalf("uncontended queue grew to %d stripes", s.Stripes())
	}
}

func TestStripedContentionGrowsUpToProcessors(t *testing.T) {
	s := NewStriped[int](8)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 4 * runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for i := range 50_000 {
				for !s.Enqueue(i) {
					mu.Lock()
					s.Drain(func(int) {})
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()
	if runtime.GOMAXPROCS(0) > 1 && s.Stripes() == 1 {
		t.Fatal("contended queue never grew")
	}
	if s.Stripes() > s.maxStripes {
		t.Fatalf("grew to %d stripes, max %d", s.Stripes(), s.maxStripes)
	}
}
