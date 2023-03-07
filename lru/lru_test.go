package lru

import (
	. "github.com/go-playground/assert/v2"
	syncext "github.com/go-playground/pkg/v5/sync"
	optionext "github.com/go-playground/pkg/v5/values/option"
	"strconv"
	"testing"
	"time"
)

func TestLRUBasics(t *testing.T) {
	c := New[string, int](3).MaxAge(time.Hour).Build()
	c.Set("1", 1)
	c.Set("2", 2)
	c.Set("3", 3)
	c.Set("1", 1) // resetting, not a mistake
	c.Set("4", 4)
	Equal(t, c.stats.Evictions, uint(1))
	Equal(t, c.stats.Capacity, 3)
	Equal(t, c.list.Len(), 3)
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
	Equal(t, c.list.Len(), 0)

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

func TestLRUMaxAge(t *testing.T) {
	c := New[string, int](3).MaxAge(time.Nanosecond).Build()
	c.Set("1", 1)
	Equal(t, c.stats.Capacity, 3)
	Equal(t, c.list.Len(), 1)
	time.Sleep(time.Second) // for windows :(
	Equal(t, c.Get("1"), optionext.None[int]())
	Equal(t, c.list.Len(), 0)
	Equal(t, c.stats.Evictions, uint(1))
}

func BenchmarkLRUCacheWithMaxAge(b *testing.B) {
	cache := New[string, string](100).MaxAge(time.Second).Build()

	for i := 0; i < b.N; i++ {
		cache.Set("a", "b")
		option := cache.Get("a")
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLRUCacheWithNoMaxAge(b *testing.B) {
	cache := New[string, string](100).Build()

	for i := 0; i < b.N; i++ {
		cache.Set("a", "b")
		option := cache.Get("a")
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLRUCacheGetsOnly(b *testing.B) {
	cache := New[string, string](100).Build()
	cache.Set("a", "b")

	for i := 0; i < b.N; i++ {
		option := cache.Get("a")
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLRUCacheSetsOnly(b *testing.B) {
	cache := New[string, string](100).Build()

	for i := 0; i < b.N; i++ {
		j := strconv.Itoa(i)
		cache.Set(j, "b")
	}
}

func BenchmarkLRUCacheSetGetDynamicWithEvictions(b *testing.B) {
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

func BenchmarkLRUCacheGetSetParallel(b *testing.B) {
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
