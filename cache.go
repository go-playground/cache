// Package cache provides fast, concurrent, bounded in-memory caches with pluggable eviction policies.
//
// Caches are created through a policy package's builder, each producing the same *Cache type:
//
//	lru.New[string, User](10_000).Build()     // least recently used
//	lfu.New[string, User](10_000).Build()     // least frequently used
//	tinylfu.New[string, User](10_000).Build() // W-TinyLFU: best general purpose hit ratio
//	s3fifo.New[string, User](10_000).Build()  // S3-FIFO: fastest reads, scan resistant
//
// All methods are safe for concurrent use, and a cache needs no background goroutine and no Close.
//
// A cache picks its engine automatically when built, from the processors available to the program (GOMAXPROCS):
//   - With one processor, goroutines never run in parallel, so it uses a single mutex, applies eviction bookkeeping
//     immediately and updates existing entries in place without allocating.
//   - With more, reads are lock-free and eviction bookkeeping is buffered and applied in batches by whichever caller
//     acquires the lock, so cores rarely contend.
//
// Both engines offer the same API and guarantees: an expired entry is never returned, capacity is enforced, and every
// operation is safe for concurrent use. They differ in timing: with more than one processor, eviction bookkeeping is
// applied a little after the operation that caused it, so recency and frequency are approximate and the cache may
// briefly hold a few entries over capacity until its buffers drain. The choice is made once per cache: if
// GOMAXPROCS changes afterwards, existing caches keep their engine.
package cache

import (
	"sync/atomic"

	optionext "github.com/go-playground/pkg/v5/values/option"

	"github.com/go-playground/cache/v2/internal/core"
)

// Cache is a concurrent, bounded, in-memory cache. Create one with a policy package's builder, e.g. lru.New.
type Cache[K comparable, V any] struct {
	e *core.Engine[K, V]

	// loads tracks in-flight GetOrLoad calls. Allocated on first use so caches that never load pay nothing for it.
	loads atomic.Pointer[inflight[K, V]]
}

// New builds a Cache from a policy builder's configuration. It is used by the policy packages; use their builders.
func New[K comparable, V any](cfg core.Config[K, V]) *Cache[K, V] {
	return &Cache[K, V]{e: core.NewEngine(cfg)}
}

func (c *Cache[K, V]) engine() *core.Engine[K, V] {
	if c.e == nil {
		panic("cache: Cache must be created via a policy builder, e.g. lru.New[K, V](capacity).Build()")
	}
	return c.e
}

// Get returns the value for key, or None if it is absent or expired.
func (c *Cache[K, V]) Get(key K) optionext.Option[V] {
	return c.engine().Get(key)
}

// Set maps key to value, replacing any existing value. A GetOrLoad for key that is in flight will not overwrite it.
func (c *Cache[K, V]) Set(key K, value V) {
	e := c.engine()
	h := e.Hash(key)
	c.invalidate(h, key)
	e.Notify(e.SetHashed(h, key, value))
}

// Remove removes key if present. A GetOrLoad for key that is in flight will not store its result.
func (c *Cache[K, V]) Remove(key K) {
	e := c.engine()
	h := e.Hash(key)
	c.invalidate(h, key)
	e.RemoveHashed(h, key)
}

// Clear removes every entry. GetOrLoad calls in flight will not store their results.
func (c *Cache[K, V]) Clear() {
	e := c.engine()
	if f := c.loads.Load(); f != nil {
		f.invalidateAll()
	}
	e.Clear()
}

// Stats returns cumulative statistics. Hits, Misses, Sets, Loads and LoadErrors are only recorded when the cache
// was built with RecordStats.
func (c *Cache[K, V]) Stats() Stats {
	s := c.engine().Stats()
	return Stats{
		Capacity:   s.Capacity,
		Len:        s.Len,
		Weight:     s.Weight,
		Hits:       s.Hits,
		Misses:     s.Misses,
		Sets:       s.Sets,
		Evictions:  s.Evictions,
		Loads:      s.Loads,
		LoadErrors: s.LoadErrors,
	}
}

func (c *Cache[K, V]) invalidate(h uint64, key K) {
	if f := c.loads.Load(); f != nil {
		f.invalidate(h, key)
	}
}

// inflight returns the in-flight load tracker, creating it on first use.
func (c *Cache[K, V]) inflight() *inflight[K, V] {
	if f := c.loads.Load(); f != nil {
		return f
	}
	c.loads.CompareAndSwap(nil, newInflight[K, V]())
	return c.loads.Load()
}
