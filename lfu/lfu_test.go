package lfu

import (
	"context"
	. "github.com/go-playground/assert/v2"
	optionext "github.com/go-playground/pkg/v5/values/option"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLFUStatsCadence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stats atomic.Value
	var store sync.Once

	c := New[string, int](2).Stats(time.Millisecond*750, func(s Stats) {
		store.Do(func() {
			stats.Store(s)
		})
	}).Build(ctx)
	c.Set("a", 1)
	_ = c.Get("a")
	_ = c.Get("b")
	time.Sleep(time.Second)
	s := stats.Load().(Stats)
	Equal(t, s.Hits, uint(1))
	Equal(t, s.Misses, uint(1))
	Equal(t, s.Gets, uint(2))
	Equal(t, s.Sets, uint(1))
	Equal(t, s.Evictions, uint(0))
	Equal(t, s.Capacity, 2)
	Equal(t, s.Len, 1)
}

func TestLFUBasics(t *testing.T) {
	c := New[string, int](3).MaxAge(time.Hour).Build(context.Background())
	c.Set("1", 1)
	c.Set("2", 2)
	c.Set("3", 3)
	c.Set("1", 1) // resetting, not a mistake
	c.Set("4", 4)
	Equal(t, c.stats.Evictions, uint(1))
	Equal(t, c.stats.Capacity, 3)
	Equal(t, len(c.entries), 3)
	Equal(t, c.Get("1"), optionext.Some(1))
	Equal(t, c.Get("2"), optionext.None[int]())
	Equal(t, c.Get("3"), optionext.Some(3))
	Equal(t, c.Get("4"), optionext.Some(4))

	// test remove
	c.Remove("3")
	Equal(t, c.Get("3"), optionext.None[int]())

	// test clear
	c.Clear()
	Equal(t, c.stats.Capacity, 3)
	Equal(t, len(c.entries), 0)
}

func TestLFUMaxAge(t *testing.T) {
	c := New[string, int](3).MaxAge(time.Nanosecond).Build(context.Background())
	c.Set("1", 1)
	Equal(t, c.stats.Capacity, 3)
	Equal(t, len(c.entries), 1)
	time.Sleep(time.Second) // for windows :(
	Equal(t, c.Get("1"), optionext.None[int]())
	Equal(t, len(c.entries), 0)
	Equal(t, c.stats.Evictions, uint(1))
}

func TestLFUEdgeFrequencySplitAndRecombine(t *testing.T) {
	c := New[string, int](2).MaxAge(time.Hour).Build(context.Background())
	c.Set("1", 1)
	c.Set("2", 2)

	Equal(t, c.Get("1"), optionext.Some(1))
	Equal(t, c.Get("1"), optionext.Some(1))
	Equal(t, c.Get("2"), optionext.Some(2))
	Equal(t, c.frequencies.Len(), 2)

	c.Set("3", 3)

	Equal(t, c.Get("1"), optionext.Some(1))
	Equal(t, c.Get("2"), optionext.None[int]()) // evicted
	Equal(t, c.Get("3"), optionext.Some(3))

	// test clear
	c.Clear()
	Equal(t, c.stats.Capacity, 2)
	Equal(t, len(c.entries), 0)
}

func TestLFUEdgeCases(t *testing.T) {

	// testing when we add an entry which causes us to go over capacity
	// and the last one added caused a new base frequency to be created.
	c := New[string, int](2).MaxAge(time.Hour).Build(context.Background())
	c.Set("1", 1)
	c.Set("2", 2)
	Equal(t, c.frequencies.Len(), 1)
	Equal(t, c.frequencies.Front().Value.entries.Len(), 2)

	Equal(t, c.Get("1"), optionext.Some(1))
	Equal(t, c.frequencies.Len(), 2)
	Equal(t, c.frequencies.Front().Value.entries.Len(), 1)
	Equal(t, c.frequencies.Back().Value.entries.Len(), 1)
	Equal(t, c.frequencies.Front().Value.count, 2)
	Equal(t, c.frequencies.Back().Value.count, 1)

	Equal(t, c.Get("1"), optionext.Some(1))
	Equal(t, c.frequencies.Len(), 2)
	Equal(t, c.frequencies.Front().Value.entries.Len(), 1)
	Equal(t, c.frequencies.Back().Value.entries.Len(), 1)
	Equal(t, c.frequencies.Front().Value.count, 3)
	Equal(t, c.frequencies.Back().Value.count, 1)

	Equal(t, c.Get("2"), optionext.Some(2))
	Equal(t, c.frequencies.Len(), 2)
	Equal(t, c.frequencies.Front().Value.entries.Len(), 1)
	Equal(t, c.frequencies.Back().Value.entries.Len(), 1)
	Equal(t, c.frequencies.Front().Value.count, 3)
	Equal(t, c.frequencies.Back().Value.count, 2)
	Equal(t, c.Get("2"), optionext.Some(2))
	Equal(t, c.frequencies.Len(), 1)
	Equal(t, c.frequencies.Front().Value.entries.Len(), 2)
	Equal(t, c.frequencies.Front().Value.entries.Front().Value.value, 2)
	Equal(t, c.frequencies.Front().Value.entries.Back().Value.value, 1)

	Equal(t, c.Get("1"), optionext.Some(1))
	Equal(t, c.frequencies.Len(), 2)
	Equal(t, c.Get("1"), optionext.Some(1))
	Equal(t, c.frequencies.Len(), 2)
	Equal(t, c.Get("2"), optionext.Some(2))
	Equal(t, c.frequencies.Len(), 2)
	Equal(t, c.Get("2"), optionext.Some(2))
	Equal(t, c.frequencies.Len(), 1)
	c.Set("3", 3)

	Equal(t, c.Get("1"), optionext.None[int]()) // evicted
	Equal(t, c.Get("2"), optionext.Some(2))
	Equal(t, c.Get("3"), optionext.Some(3))

	// test clear
	c.Clear()
	Equal(t, c.stats.Capacity, 2)
	Equal(t, len(c.entries), 0)

	// Test when frequency count goes beyond int max value
	// we don't want to place it back to the beginning, leave it as the head

	maxInt := int(^uint(0) >> 1)
	c.Set("1", 1)
	Equal(t, c.frequencies.Len(), 1)

	// hacking by explicitly setting frequency to max value to save us looping for the test
	c.frequencies.Front().Value.count = maxInt
	_ = c.Get("1")
	Equal(t, c.frequencies.Len(), 1)
	Equal(t, c.frequencies.Front().Value.count, maxInt)
}

func TestLFULFU(t *testing.T) {
	c := New[string, int](2).MaxAge(time.Hour).Build(context.Background())
	c.Set("1", 1)
	c.Set("2", 2)

	for i := 0; i < 1_000; i++ {
		_ = c.Get("1")
	}

	for i := 0; i < 100; i++ {
		_ = c.Get("2")
	}

	// should cause `2` to be evicted even though it;s the most recently used, it isn't the most frequently used.
	c.Set("3", 3)
	Equal(t, c.Get("1"), optionext.Some(1))
	Equal(t, c.Get("2"), optionext.None[int]())
	Equal(t, c.Get("3"), optionext.Some(3))
}

func BenchmarkLFUCacheWithAllRegisteredFunctions(b *testing.B) {
	var stats atomic.Value

	cache := New[string, string](100).MaxAge(time.Second).Stats(time.Second, func(s Stats) {
		stats.Store(s)
	}).Build(context.Background())

	for i := 0; i < b.N; i++ {
		cache.Set("a", "b")
		option := cache.Get("a")
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLFUCacheNoRegisteredFunctions(b *testing.B) {
	cache := New[string, string](100).MaxAge(time.Second).Build(context.Background())

	for i := 0; i < b.N; i++ {
		cache.Set("a", "b")
		option := cache.Get("a")
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLFUCacheWithAllRegisteredFunctionsNoMaxAge(b *testing.B) {
	var stats atomic.Value

	cache := New[string, string](100).Stats(time.Second, func(s Stats) {
		stats.Store(s)
	}).Build(context.Background())

	for i := 0; i < b.N; i++ {
		cache.Set("a", "b")
		option := cache.Get("a")
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLFUCacheNoRegisteredFunctionsNoMaxAge(b *testing.B) {
	cache := New[string, string](100).Build(context.Background())

	for i := 0; i < b.N; i++ {
		cache.Set("a", "b")
		option := cache.Get("a")
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLFUCacheGetsOnly(b *testing.B) {
	cache := New[string, string](100).Build(context.Background())
	cache.Set("a", "b")

	for i := 0; i < b.N; i++ {
		option := cache.Get("a")
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLFUCacheSetsOnly(b *testing.B) {
	cache := New[string, string](100).Build(context.Background())

	for i := 0; i < b.N; i++ {
		j := strconv.Itoa(i)
		cache.Set(j, "b")
	}
}

func BenchmarkLFUCacheSetGetDynamicWithEvictions(b *testing.B) {
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

func BenchmarkLFUCacheGetSetParallel(b *testing.B) {
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
