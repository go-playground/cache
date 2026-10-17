package cache_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	resultext "github.com/go-playground/pkg/v5/values/result"

	cache "github.com/go-playground/cache/v2"
	"github.com/go-playground/cache/v2/tinylfu"
)

var errBoom = errors.New("boom")

func newLoadCache() *cache.Cache[string, int] {
	return tinylfu.New[string, int](100).RecordStats().Build()
}

func ok(v int) resultext.Result[int, error] { return resultext.Ok[int, error](v) }

func TestGetOrLoadHitDoesNotCallLoader(t *testing.T) {
	c := newLoadCache()
	c.Set("a", 1)
	res := c.GetOrLoad(context.Background(), "a", func(context.Context, string) resultext.Result[int, error] {
		t.Fatal("loader called on hit")
		return ok(0)
	})
	if res.Unwrap() != 1 {
		t.Fatalf("got %v", res)
	}
}

func TestGetOrLoadStoresLoadedValue(t *testing.T) {
	c := newLoadCache()
	res := c.GetOrLoad(context.Background(), "a", func(_ context.Context, k string) resultext.Result[int, error] {
		return ok(len(k))
	})
	if res.Unwrap() != 1 || c.Get("a").Unwrap() != 1 {
		t.Fatal("loaded value not returned and stored")
	}
	if s := c.Stats(); s.Loads != 1 || s.LoadErrors != 0 || s.Sets != 1 {
		t.Fatalf("unexpected stats %+v", s)
	}
}

func TestGetOrLoadCoalescesConcurrentMisses(t *testing.T) {
	c := newLoadCache()
	var calls atomic.Int64
	release := make(chan struct{})
	var wg sync.WaitGroup
	const callers = 50
	results := make([]int, callers)
	for i := range callers {
		wg.Go(func() {
			results[i] = c.GetOrLoad(context.Background(), "k", func(context.Context, string) resultext.Result[int, error] {
				calls.Add(1)
				<-release
				return ok(42)
			}).Unwrap()
		})
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("loader called %d times, want 1", calls.Load())
	}
	for i, r := range results {
		if r != 42 {
			t.Fatalf("caller %d got %d", i, r)
		}
	}
}

func TestGetOrLoadErrorsAreSharedButNotCached(t *testing.T) {
	c := newLoadCache()
	res := c.GetOrLoad(context.Background(), "a", func(context.Context, string) resultext.Result[int, error] {
		return resultext.Err[int](errBoom)
	})
	if !errors.Is(res.Err(), errBoom) {
		t.Fatalf("got %v", res)
	}
	if c.Get("a").IsSome() {
		t.Fatal("error result must not be cached")
	}
	res = c.GetOrLoad(context.Background(), "a", func(context.Context, string) resultext.Result[int, error] { return ok(2) })
	if res.Unwrap() != 2 {
		t.Fatal("next call must load again")
	}
	if s := c.Stats(); s.Loads != 2 || s.LoadErrors != 1 {
		t.Fatalf("unexpected stats %+v", s)
	}
}

func TestGetOrLoadWaiterCancellationDoesNotAffectLoad(t *testing.T) {
	c := newLoadCache()
	release := make(chan struct{})
	started := make(chan struct{})
	leaderDone := make(chan resultext.Result[int, error])
	go func() {
		leaderDone <- c.GetOrLoad(context.Background(), "k", func(context.Context, string) resultext.Result[int, error] {
			close(started)
			<-release
			return ok(1)
		})
	}()
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	waiterDone := make(chan resultext.Result[int, error])
	go func() {
		waiterDone <- c.GetOrLoad(ctx, "k", func(context.Context, string) resultext.Result[int, error] {
			t.Error("waiter must not load")
			return ok(0)
		})
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if res := <-waiterDone; !errors.Is(res.Err(), context.Canceled) {
		t.Fatalf("waiter got %v, want context.Canceled", res)
	}
	close(release)
	if res := <-leaderDone; res.Unwrap() != 1 {
		t.Fatalf("leader got %v", res)
	}
	if c.Get("k").Unwrap() != 1 {
		t.Fatal("load must still be stored")
	}
}

func TestGetOrLoadWaitersRetryWhenLeaderIsCanceled(t *testing.T) {
	c := newLoadCache()
	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	started := make(chan struct{})
	var loads atomic.Int64
	leaderDone := make(chan resultext.Result[int, error])
	go func() {
		leaderDone <- c.GetOrLoad(leaderCtx, "k", func(ctx context.Context, _ string) resultext.Result[int, error] {
			loads.Add(1)
			close(started)
			<-ctx.Done()
			return resultext.Err[int](ctx.Err())
		})
	}()
	<-started

	waiterDone := make(chan resultext.Result[int, error])
	go func() {
		waiterDone <- c.GetOrLoad(context.Background(), "k", func(context.Context, string) resultext.Result[int, error] {
			loads.Add(1)
			return ok(7)
		})
	}()
	time.Sleep(20 * time.Millisecond)
	cancelLeader()

	if res := <-leaderDone; !errors.Is(res.Err(), context.Canceled) {
		t.Fatalf("leader got %v", res)
	}
	if res := <-waiterDone; res.IsErr() || res.Unwrap() != 7 {
		t.Fatalf("waiter must retry and load, got %v", res)
	}
	if loads.Load() != 2 {
		t.Fatalf("loads %d, want 2", loads.Load())
	}
}

func TestGetOrLoadPanicPropagates(t *testing.T) {
	c := newLoadCache()
	started := make(chan struct{})
	release := make(chan struct{})
	waiterDone := make(chan resultext.Result[int, error])

	leaderPanic := make(chan any)
	go func() {
		defer func() { leaderPanic <- recover() }()
		c.GetOrLoad(context.Background(), "k", func(context.Context, string) resultext.Result[int, error] {
			close(started)
			<-release
			panic("kaboom")
		})
	}()
	<-started
	go func() {
		waiterDone <- c.GetOrLoad(context.Background(), "k", func(context.Context, string) resultext.Result[int, error] {
			return ok(0)
		})
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)

	if r := <-leaderPanic; r != "kaboom" {
		t.Fatalf("leader must re-panic with the original value, got %v", r)
	}
	res := <-waiterDone
	var pe *cache.LoadPanicError
	if !errors.As(res.Err(), &pe) || pe.Value != "kaboom" || len(pe.Stack) == 0 {
		t.Fatalf("waiter must receive *LoadPanicError, got %v", res)
	}
	if pe.Error() == "" {
		t.Fatal("LoadPanicError must describe itself")
	}
	if c.Get("k").IsSome() {
		t.Fatal("panicked load must not store")
	}
}

func TestGetOrLoadIsNotStoredWhenRemovedOrClearedInFlight(t *testing.T) {
	for name, invalidate := range map[string]func(*cache.Cache[string, int]){
		"remove": func(c *cache.Cache[string, int]) { c.Remove("k") },
		"clear":  func(c *cache.Cache[string, int]) { c.Clear() },
	} {
		t.Run(name, func(t *testing.T) {
			c := newLoadCache()
			started := make(chan struct{})
			release := make(chan struct{})
			done := make(chan resultext.Result[int, error])
			go func() {
				done <- c.GetOrLoad(context.Background(), "k", func(context.Context, string) resultext.Result[int, error] {
					close(started)
					<-release
					return ok(1)
				})
			}()
			<-started
			invalidate(c)
			close(release)
			if res := <-done; res.Unwrap() != 1 {
				t.Fatalf("caller must still receive the loaded value, got %v", res)
			}
			if c.Get("k").IsSome() {
				t.Fatal("value loaded before removal must not be stored")
			}
		})
	}
}

func TestGetOrLoadConcurrentWithRemoveNeverResurrects(t *testing.T) {
	c := newLoadCache()
	for i := range 2000 {
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			<-start
			c.GetOrLoad(context.Background(), "k", func(context.Context, string) resultext.Result[int, error] { return ok(i) })
		})
		removed := make(chan struct{})
		wg.Go(func() {
			<-start
			c.Remove("k")
			close(removed)
		})
		close(start)
		wg.Wait()
		<-removed
		c.Remove("k")
	}
	if c.Get("k").IsSome() {
		t.Fatal("key resurrected")
	}
}
