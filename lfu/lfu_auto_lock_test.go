package lfu

import (
	. "github.com/go-playground/assert/v2"
	optionext "github.com/go-playground/pkg/v5/values/option"
	"testing"
	"time"
)

func TestLFUAutoLockCache(t *testing.T) {
	c := New[string, int](3).MaxAge(time.Hour).BuildAutoLock()
	c.Set("1", 1)
	c.Set("2", 2)
	Equal(t, c.Get("1"), optionext.Some(1))

	c.Remove("2")
	Equal(t, c.Get("2"), optionext.None[int]())

	stats := c.Stats()
	Equal(t, stats.Capacity, 3)
	Equal(t, stats.Evictions, uint(0))
	Equal(t, stats.Gets, uint(2))
	Equal(t, stats.Hits, uint(1))
	Equal(t, stats.Len, 1)
	Equal(t, stats.Misses, uint(1))
	Equal(t, stats.Sets, uint(2))

	c.Clear()
	Equal(t, c.Get("1"), optionext.None[int]())
}

func BenchmarkLFUAutoLockCacheGetSetParallel(b *testing.B) {
	cache := New[string, string](100).BuildAutoLock()
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
