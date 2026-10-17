// Package lru builds caches that evict the least recently used entry.
//
// Under concurrency recency is approximate: reads are recorded through a lossy buffer, so a small fraction of reads
// may not refresh an entry's position. LRU suits recency-biased workloads; for most workloads tinylfu has a higher
// hit ratio.
package lru

import (
	"time"

	cache "github.com/go-playground/cache/v2"
	"github.com/go-playground/cache/v2/internal/builder"
	"github.com/go-playground/cache/v2/internal/core"
	"github.com/go-playground/cache/v2/internal/policy"
	plru "github.com/go-playground/cache/v2/internal/policy/lru"
)

// Builder configures an LRU cache. Every setting except capacity is optional.
type Builder[K comparable, V any] struct {
	cfg core.Config[K, V]
}

// New returns a builder for an LRU cache holding at most capacity entries.
// A capacity of 0 creates a cache that retains nothing.
func New[K comparable, V any](capacity uint64) *Builder[K, V] {
	return NewWeighted[K, V](capacity, nil)
}

// NewWeighted returns a builder for an LRU cache whose entries' total weight, as reported by weigher, is at
// most capacity. Use a built-in weigher such as cache.WeighByLen or cache.WeighBySize, or any func(K, V) uint32.
// A nil weigher weighs every entry as 1. An entry heavier than capacity is never retained.
func NewWeighted[K comparable, V any](capacity uint64, weigher cache.Weigher[K, V]) *Builder[K, V] {
	b := &Builder[K, V]{}
	b.cfg = core.NewConfig(capacity, weigher, b.newPolicy)
	return b
}

// ExpireAfterWrite expires entries d after they were last written. d <= 0 disables expiry.
// Replaces any previously configured expiry.
func (b *Builder[K, V]) ExpireAfterWrite(d time.Duration) *Builder[K, V] {
	b.cfg.ExpireAfterWrite(d)
	return b
}

// ExpireAfterAccess expires entries d after they were last read or written. d <= 0 disables expiry.
// Replaces any previously configured expiry.
func (b *Builder[K, V]) ExpireAfterAccess(d time.Duration) *Builder[K, V] {
	b.cfg.ExpireAfterAccess(d)
	return b
}

// ExpireAfter expires each entry after the duration fn returns when the entry is written; a result <= 0 means the
// entry never expires. A nil fn disables expiry. Replaces any previously configured expiry.
func (b *Builder[K, V]) ExpireAfter(fn func(key K, value V) time.Duration) *Builder[K, V] {
	b.cfg.ExpireAfter(fn)
	return b
}

// OnEviction sets a callback invoked for every entry evicted due to capacity or expiry, but not for Remove, Clear or
// replacement by Set (including of an expired entry not yet reclaimed). It is called outside of any cache lock, on
// whichever goroutine performed the maintenance, possibly concurrently and in no particular order across keys, and
// may safely call back into the cache. It should not block for long. A nil fn disables it.
func (b *Builder[K, V]) OnEviction(fn func(key K, value V, cause cache.Cause)) *Builder[K, V] {
	builder.OnEviction(&b.cfg, fn)
	return b
}

// RecordStats enables recording of hits, misses, sets and loads, reported by Cache.Stats. Disabled by default because
// counting every read adds a shared write to the read path.
func (b *Builder[K, V]) RecordStats() *Builder[K, V] {
	b.cfg.RecordStats()
	return b
}

// Build creates the cache. It never fails; the builder may be reused to build further independent caches.
func (b *Builder[K, V]) Build() *cache.Cache[K, V] {
	return cache.New(b.cfg)
}

func (b *Builder[K, V]) newPolicy(capacity uint64) policy.Policy[K, V] {
	return plru.New[K, V](capacity)
}
