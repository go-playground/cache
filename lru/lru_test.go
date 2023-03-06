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

	// test clear
	c.Clear()
	Equal(t, c.stats.Capacity, 3)
	Equal(t, c.list.Len(), 0)
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

func BenchmarkLRUCacheWithAllRegisteredFunctions(b *testing.B) {
	cache := syncext.NewMutex2(New[string, string](100).MaxAge(time.Second).Build())

	for i := 0; i < b.N; i++ {
		c := cache.Lock()
		c.Set("a", "b")
		option := c.Get("a")
		cache.Unlock()
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLRUCacheNoRegisteredFunctions(b *testing.B) {
	cache := syncext.NewMutex2(New[string, string](100).MaxAge(time.Second).Build())

	for i := 0; i < b.N; i++ {
		c := cache.Lock()
		c.Set("a", "b")
		option := c.Get("a")
		cache.Unlock()
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLRUCacheWithAllRegisteredFunctionsNoMaxAge(b *testing.B) {
	cache := syncext.NewMutex2(New[string, string](100).Build())

	for i := 0; i < b.N; i++ {
		c := cache.Lock()
		c.Set("a", "b")
		option := c.Get("a")
		cache.Unlock()
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLRUCacheNoRegisteredFunctionsNoMaxAge(b *testing.B) {
	cache := syncext.NewMutex2(New[string, string](100).Build())

	for i := 0; i < b.N; i++ {
		c := cache.Lock()
		c.Set("a", "b")
		option := c.Get("a")
		cache.Unlock()
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLRUCacheGetsOnly(b *testing.B) {
	cache := syncext.NewMutex2(New[string, string](100).Build())
	cache.Lock().Set("a", "b")
	cache.Unlock()

	for i := 0; i < b.N; i++ {
		option := cache.Lock().Get("a")
		cache.Unlock()
		if option.IsNone() || option.Unwrap() != "b" {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLRUCacheSetsOnly(b *testing.B) {
	cache := syncext.NewMutex2(New[string, string](100).Build())

	for i := 0; i < b.N; i++ {
		j := strconv.Itoa(i)
		cache.Lock().Set(j, "b")
		cache.Unlock()
	}
}

func BenchmarkLRUCacheSetGetDynamicWithEvictions(b *testing.B) {
	cache := syncext.NewMutex2(New[string, string](100).Build())

	for i := 0; i < b.N; i++ {
		j := strconv.Itoa(i)
		c := cache.Lock()
		c.Set(j, j)
		option := c.Get(j)
		cache.Unlock()
		if option.IsNone() || option.Unwrap() != j {
			panic("undefined behaviour")
		}
	}
}

func BenchmarkLRUCacheGetSetParallel(b *testing.B) {
	cache := syncext.NewMutex2(New[string, string](100).Build())
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c := cache.Lock()
			c.Set("a", "b")
			option := c.Get("a")
			cache.Unlock()
			if option.IsNone() || option.Unwrap() != "b" {
				panic("undefined behaviour")
			}
		}
	})
}
