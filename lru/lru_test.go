package lru

import (
	"context"
	. "github.com/go-playground/assert/v2"
	optionext "github.com/go-playground/pkg/v5/values/option"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestLRUPercentageFullCadence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var count uint32
	c := New[string, int](2).PercentageFullFn(func(percentageFull float64) {
		atomic.AddUint32(&count, 1)
	}).PercentageFullReportCadence(time.Millisecond * 500).Build(ctx)
	c.Set("a", 1)
	Equal(t, atomic.LoadUint32(&count), uint32(1))
	time.Sleep(time.Second)
	Equal(t, atomic.LoadUint32(&count) > 1, true)
}

func TestLRUBasics(t *testing.T) {
	evictions := 0
	c := New[string, int](3).MaxAge(time.Hour).EvictFn(func(_ string, _ int) {
		evictions++
	}).Build(context.Background())
	c.Set("1", 1)
	c.Set("2", 2)
	c.Set("3", 3)
	c.Set("1", 1) // resetting, not a mistake
	c.Set("4", 4)
	Equal(t, evictions, 1)
	Equal(t, c.Capacity(), 3)
	Equal(t, c.Len(), 3)
	Equal(t, c.Get("1"), optionext.Some(1))
	Equal(t, c.Get("2"), optionext.None[int]())
	Equal(t, c.Get("3"), optionext.Some(3))
	Equal(t, c.Get("4"), optionext.Some(4))

	// test remove
	c.Remove("3")
	Equal(t, c.Get("3"), optionext.None[int]())

	// test clear
	c.Clear()
	Equal(t, c.Capacity(), 3)
	Equal(t, c.Len(), 0)
}

func TestLRUMaxAge(t *testing.T) {
	evictions := 0
	c := New[string, int](3).MaxAge(time.Nanosecond).EvictFn(func(_ string, _ int) {
		evictions++
	}).Build(context.Background())
	c.Set("1", 1)
	Equal(t, c.Capacity(), 3)
	Equal(t, c.Len(), 1)
	time.Sleep(time.Second) // for windows :(
	Equal(t, c.Get("1"), optionext.None[int]())
	Equal(t, c.Len(), 0)
	Equal(t, evictions, 1)
}

func TestLRUFunctions(t *testing.T) {
	hits := 0
	misses := 0
	percentageFull := float64(0)

	c := New[string, int](2).
		HitFn(func(_ string, _ int) {
			hits++
		}).
		MissFn(func(_ string) {
			misses++
		}).
		PercentageFullFn(func(pf float64) {
			percentageFull = pf
		}).Build(context.Background())
	c.Set("1", 1)
	Equal(t, percentageFull, float64(50))

	_ = c.Get("1")
	Equal(t, hits, 1)

	_ = c.Get("2")
	Equal(t, misses, 1)

	c.Clear()
	Equal(t, percentageFull, float64(0))
}

func BenchmarkLRUCacheWithAllRegisteredFunctions(b *testing.B) {
	var hits int64 = 0
	var misses int64 = 0
	var evictions int64 = 0
	var pf uint32 = 0

	cache := New[string, string](100).MaxAge(time.Second).HitFn(func(_ string, _ string) {
		atomic.AddInt64(&hits, 1)
	}).MissFn(func(_ string) {
		atomic.AddInt64(&misses, 1)
	}).EvictFn(func(_ string, _ string) {
		atomic.AddInt64(&evictions, 1)
	}).PercentageFullFn(func(percentageFull float64) {
		atomic.StoreUint32(&pf, uint32(percentageFull))
	}).PercentageFullReportCadence(time.Minute).Build(context.Background())

	for i := 0; i < b.N; i++ {
		cache.Set("a", "b")
		option := cache.Get("a")
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLRUCacheNoRegisteredFunctions(b *testing.B) {

	cache := New[string, string](100).MaxAge(time.Second).Build(context.Background())

	for i := 0; i < b.N; i++ {
		cache.Set("a", "b")
		option := cache.Get("a")
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLRUCacheWithAllRegisteredFunctionsNoMaxAge(b *testing.B) {
	var hits int64 = 0
	var misses int64 = 0
	var evictions int64 = 0
	var pf uint32 = 0

	cache := New[string, string](100).HitFn(func(_ string, _ string) {
		atomic.AddInt64(&hits, 1)
	}).MissFn(func(_ string) {
		atomic.AddInt64(&misses, 1)
	}).EvictFn(func(_ string, _ string) {
		atomic.AddInt64(&evictions, 1)
	}).PercentageFullFn(func(percentageFull float64) {
		atomic.StoreUint32(&pf, uint32(percentageFull))
	}).PercentageFullReportCadence(time.Minute).Build(context.Background())

	for i := 0; i < b.N; i++ {
		cache.Set("a", "b")
		option := cache.Get("a")
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLRUCacheNoRegisteredFunctionsNoMaxAge(b *testing.B) {
	cache := New[string, string](100).Build(context.Background())

	for i := 0; i < b.N; i++ {
		cache.Set("a", "b")
		option := cache.Get("a")
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLRUCacheGetsOnly(b *testing.B) {
	cache := New[string, string](100).Build(context.Background())
	cache.Set("a", "b")

	for i := 0; i < b.N; i++ {
		option := cache.Get("a")
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLRUCacheSetsOnly(b *testing.B) {
	cache := New[string, string](100).Build(context.Background())

	for i := 0; i < b.N; i++ {
		j := strconv.Itoa(i)
		cache.Set(j, "b")
	}
}

func BenchmarkLRUCacheSetGetDynamicWithEvictions(b *testing.B) {
	cache := New[string, string](100).Build(context.Background())

	for i := 0; i < b.N; i++ {
		j := strconv.Itoa(i)
		cache.Set(j, j)
		option := cache.Get(j)
		if option.IsNone() || option.Unwrap() != j {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLRUCacheGetSetParallel(b *testing.B) {
	cache := New[string, string](100).Build(context.Background())
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cache.Set("a", "b")
			option := cache.Get("a")
			if option.IsNone() || option.Unwrap() != "b" {
				panic("undefined behaviour")
			}
		}
	})
}
