package lru

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

func TestLRUStatsCadence(t *testing.T) {
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

func TestLRUBasics(t *testing.T) {
	c := New[string, int](3).MaxAge(time.Hour).Build(context.Background())
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

	// test clear
	c.Clear()
	Equal(t, c.stats.Capacity, 3)
	Equal(t, c.list.Len(), 0)
}

func TestLRUMaxAge(t *testing.T) {
	c := New[string, int](3).MaxAge(time.Nanosecond).Build(context.Background())
	c.Set("1", 1)
	Equal(t, c.stats.Capacity, 3)
	Equal(t, c.list.Len(), 1)
	time.Sleep(time.Second) // for windows :(
	Equal(t, c.Get("1"), optionext.None[int]())
	Equal(t, c.list.Len(), 0)
	Equal(t, c.stats.Evictions, uint(1))
}

func BenchmarkLRUCacheWithAllRegisteredFunctions(b *testing.B) {
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
