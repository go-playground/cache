package cache_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	resultext "github.com/go-playground/pkg/v5/values/result"

	cache "github.com/go-playground/cache/v2"
	"github.com/go-playground/cache/v2/lru"
	"github.com/go-playground/cache/v2/tinylfu"
)

// A Remove that returns while a finishing load is still running user code must not see the key resurrected.
func TestRemoveDuringLoadFinishNeverResurrects(t *testing.T) {
	weighing := make(chan struct{})
	release := make(chan struct{})
	c := lru.NewWeighted[string, int](100, func(_ string, v int) uint32 {
		if v == 42 {
			close(weighing)
			<-release
		}
		return 1
	}).Build()

	done := make(chan struct{})
	go func() {
		c.GetOrLoad(context.Background(), "k", func(context.Context, string) resultext.Result[int, error] { return ok(42) })
		close(done)
	}()
	<-weighing
	c.Remove("k")
	close(release)
	<-done
	if c.Get("k").IsSome() {
		t.Fatal("key resurrected after Remove returned")
	}
}

// A user weigher panicking while a load finishes must not wedge the cache.
func TestPanickingWeigherDuringLoadDoesNotWedge(t *testing.T) {
	c := lru.NewWeighted[string, int](100, func(_ string, v int) uint32 {
		if v < 0 {
			panic("bad value")
		}
		return 1
	}).Build()

	func() {
		defer func() { _ = recover() }()
		c.GetOrLoad(context.Background(), "k", func(context.Context, string) resultext.Result[int, error] { return ok(-1) })
	}()

	done := make(chan resultext.Result[int, error])
	go func() {
		done <- c.GetOrLoad(context.Background(), "k", func(context.Context, string) resultext.Result[int, error] { return ok(1) })
	}()
	select {
	case res := <-done:
		if res.Unwrap() != 1 {
			t.Fatalf("got %v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cache wedged after a panicking weigher")
	}
	c.Remove("other")
	c.Clear()
}

func TestSetDuringLoadIsNotOverwrittenByStaleLoad(t *testing.T) {
	c := newLoadCache()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		c.GetOrLoad(context.Background(), "k", func(context.Context, string) resultext.Result[int, error] {
			close(started)
			<-release
			return ok(0)
		})
		close(done)
	}()
	<-started
	c.Set("k", 1)
	close(release)
	<-done
	if v := c.Get("k"); v.Unwrap() != 1 {
		t.Fatalf("stale load overwrote a newer Set: got %v", v)
	}
}

func TestHugeTTLNeverWrapsToExpired(t *testing.T) {
	for name, c := range map[string]*cache.Cache[string, int]{
		"write":  lru.New[string, int](10).ExpireAfterWrite(math.MaxInt64).Build(),
		"access": lru.New[string, int](10).ExpireAfterAccess(math.MaxInt64).Build(),
		"custom": lru.New[string, int](10).ExpireAfter(func(string, int) time.Duration { return math.MaxInt64 }).Build(),
	} {
		c.Set("a", 1)
		if c.Get("a").IsNone() {
			t.Fatalf("%s: huge TTL treated as already expired", name)
		}
	}
}

// TestCapacityReservesTheIndexUpFront pins the documented cost of New: about 17 bytes of index per entry of
// capacity, allocated when the cache is built so it never grows.
func TestCapacityReservesTheIndexUpFront(t *testing.T) {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	c := lru.New[int, int](1_000_000).Build()
	runtime.ReadMemStats(&after)
	grew := after.TotalAlloc - before.TotalAlloc
	if grew < 16<<20 || grew > 20<<20 {
		t.Fatalf("building a 1M entry cache allocated %d bytes, want ~17MB of index", grew)
	}
	c.Set(1, 1)
}

func TestZeroWeightIsTreatedAsOne(t *testing.T) {
	c := lru.NewWeighted[string, int](10, func(string, int) uint32 { return 0 }).Build()
	for i := range 1000 {
		c.Set(fmt.Sprint(i), i)
	}
	if s := c.Stats(); s.Len > 10 || s.Weight > 10 {
		t.Fatalf("zero weights made the cache unbounded: %+v", s)
	}
}

func TestLoaderGoexitReleasesWaiters(t *testing.T) {
	c := newLoadCache()
	started := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		c.GetOrLoad(context.Background(), "k", func(context.Context, string) resultext.Result[int, error] {
			close(started)
			<-release
			runtime.Goexit()
			return ok(0)
		})
		t.Error("leader must not return normally after Goexit")
	})
	<-started
	waiter := make(chan resultext.Result[int, error])
	go func() {
		waiter <- c.GetOrLoad(context.Background(), "k", func(context.Context, string) resultext.Result[int, error] { return ok(1) })
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	if res := <-waiter; !errors.Is(res.Err(), cache.ErrLoaderExited) {
		t.Fatalf("waiter got %v, want ErrLoaderExited", res)
	}
	if res := c.GetOrLoad(context.Background(), "k", func(context.Context, string) resultext.Result[int, error] { return ok(2) }); res.Unwrap() != 2 {
		t.Fatal("cache must keep working after a Goexit load")
	}
}

func TestZeroValueBuilderPanicsWithGuidance(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "New or NewWeighted") {
			t.Fatalf("expected guidance panic, got %v", r)
		}
	}()
	var b tinylfu.Builder[string, int]
	b.Build()
}
