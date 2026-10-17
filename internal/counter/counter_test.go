package counter

import (
	"sync"
	"testing"
	"unsafe"

	"github.com/go-playground/cache/v2/internal/xruntime"
)

func TestValueSumsAllConcurrentAdds(t *testing.T) {
	c := New()
	const goroutines, perGoroutine = 16, 10_000
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range perGoroutine {
				c.Add(1)
			}
		})
	}
	wg.Wait()
	if got := c.Value(); got != goroutines*perGoroutine {
		t.Fatalf("got %d want %d", got, goroutines*perGoroutine)
	}
}

func TestStripesArePaddedToCacheLine(t *testing.T) {
	if s := unsafe.Sizeof(stripe{}); s != xruntime.CacheLine {
		t.Fatalf("stripe size %d, want %d", s, xruntime.CacheLine)
	}
}

func TestStripeCountIsPowerOfTwo(t *testing.T) {
	c := New()
	n := len(c.stripes)
	if n == 0 || n&(n-1) != 0 || uint32(n-1) != c.mask {
		t.Fatalf("invalid stripe count %d / mask %d", n, c.mask)
	}
}

func TestZeroAllocs(t *testing.T) {
	c := New()
	if allocs := testing.AllocsPerRun(1000, func() { c.Add(1) }); allocs != 0 {
		t.Fatalf("expected 0 allocs, got %v", allocs)
	}
}

func BenchmarkAddParallel(b *testing.B) {
	c := New()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Add(1)
		}
	})
}
