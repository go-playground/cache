package lru

import (
	. "github.com/go-playground/assert/v2"
	optionext "github.com/go-playground/pkg/v5/values/option"
	"testing"
	"time"
)

func TestLRUBasics(t *testing.T) {
	evictions := 0
	c := New[string, int]().Capacity(3).EvictFn(func(_ string, _ int) {
		evictions++
	}).Build()
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
	c := New[string, int]().Capacity(3).MaxAge(time.Nanosecond).EvictFn(func(_ string, _ int) {
		evictions++
	}).Build()
	c.Set("1", 1)
	Equal(t, c.Capacity(), 3)
	Equal(t, c.Len(), 1)
	Equal(t, c.Get("1"), optionext.None[int]())
	Equal(t, c.Len(), 0)
	Equal(t, evictions, 1)
}

func TestLRUFunctions(t *testing.T) {
	hits := 0
	misses := 0
	percentageFull := 0

	c := New[string, int]().Capacity(2).
		HitFn(func(_ string, _ int) {
			hits++
		}).
		MissFn(func(_ string) {
			misses++
		}).
		PercentageFullFn(func(pf int) {
			percentageFull = pf
		}).Build()
	c.Set("1", 1)
	Equal(t, percentageFull, 50)

	_ = c.Get("1")
	Equal(t, hits, 1)

	_ = c.Get("2")
	Equal(t, misses, 1)

	c.Clear()
	Equal(t, percentageFull, 0)
}

// TODO: dedicated test for Function to ensure proper key + value is sent
// TODO: dedicated test for Max Age
// TODO: Add benchmarks
