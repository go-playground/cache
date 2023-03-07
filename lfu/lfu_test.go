package lfu

import (
	. "github.com/go-playground/assert/v2"
	syncext "github.com/go-playground/pkg/v5/sync"
	optionext "github.com/go-playground/pkg/v5/values/option"
	"strconv"
	"testing"
	"time"
)

func TestLFUBasics(t *testing.T) {
	c := New[string, int](3).MaxAge(time.Hour).Build()
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

	stats := c.Stats()
	Equal(t, stats.Hits, uint(3))
	Equal(t, stats.Misses, uint(2))
	Equal(t, stats.Gets, uint(5))
	Equal(t, stats.Sets, uint(5))
	Equal(t, stats.Evictions, uint(1))
	Equal(t, stats.Len, 2)
	Equal(t, stats.Capacity, 3)

	// test clear
	c.Clear()
	Equal(t, c.stats.Capacity, 3)
	Equal(t, len(c.entries), 0)

	// test after clear
	stats = c.Stats()
	Equal(t, stats.Hits, uint(0))
	Equal(t, stats.Misses, uint(0))
	Equal(t, stats.Gets, uint(0))
	Equal(t, stats.Sets, uint(0))
	Equal(t, stats.Evictions, uint(0))
	Equal(t, stats.Len, 0)
	Equal(t, stats.Capacity, 3)
}

func TestLFUMaxAge(t *testing.T) {
	c := New[string, int](3).MaxAge(time.Nanosecond).Build()
	c.Set("1", 1)
	Equal(t, c.stats.Capacity, 3)
	Equal(t, len(c.entries), 1)
	time.Sleep(time.Second) // for windows :(
	Equal(t, c.Get("1"), optionext.None[int]())
	Equal(t, len(c.entries), 0)
	Equal(t, c.stats.Evictions, uint(1))
}

func TestLFUEdgeFrequencySplitAndRecombine(t *testing.T) {
	c := New[string, int](2).MaxAge(time.Hour).Build()
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
	c := New[string, int](2).MaxAge(time.Hour).Build()
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
	c := New[string, int](2).MaxAge(time.Hour).Build()
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

func BenchmarkLFUCacheWithMaxAge(b *testing.B) {
	cache := New[string, string](100).MaxAge(time.Second).Build()

	for i := 0; i < b.N; i++ {
		cache.Set("a", "b")
		option := cache.Get("a")
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLFUCacheWithNoMaxAge(b *testing.B) {
	cache := New[string, string](100).Build()

	for i := 0; i < b.N; i++ {
		cache.Set("a", "b")
		option := cache.Get("a")
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLFUCacheGetsOnly(b *testing.B) {
	cache := New[string, string](100).Build()
	cache.Set("a", "b")

	for i := 0; i < b.N; i++ {
		option := cache.Get("a")
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLFUCacheSetsOnly(b *testing.B) {
	cache := New[string, string](100).Build()

	for i := 0; i < b.N; i++ {
		j := strconv.Itoa(i)
		cache.Set(j, "b")
	}
}

func BenchmarkLFUCacheSetGetDynamic(b *testing.B) {
	cache := New[string, string](100).Build()

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
	cache := syncext.NewMutex2(New[string, string](100).Build())
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			guard := cache.Lock()
			guard.T.Set("a", "b")
			option := guard.T.Get("a")
			guard.Unlock()
			if option.IsNone() || option.Unwrap() != "b" {
				panic("undefined behaviour")
			}
		}
	})
}
