// Package benchmarks tests cache v2 against v1 and other Go caches implementing the same algorithms, on throughput,
// hit ratio and memory, to verify it holds up. Results are grouped by algorithm so like is compared with like.
//
// It is a separate module so the library itself stays free of these dependencies.
package benchmarks

import (
	"sync"

	"github.com/dgraph-io/ristretto/v2"
	lfuv1 "github.com/go-playground/cache/lfu"
	lruv1 "github.com/go-playground/cache/lru"
	hashicorp "github.com/hashicorp/golang-lru/v2"
	"github.com/maypok86/otter/v2"
	fifos3 "github.com/scalalang2/golang-fifo/s3fifo"
	fifosieve "github.com/scalalang2/golang-fifo/sieve"

	cache "github.com/go-playground/cache/v2"
	"github.com/go-playground/cache/v2/lfu"
	"github.com/go-playground/cache/v2/lru"
	"github.com/go-playground/cache/v2/s3fifo"
	"github.com/go-playground/cache/v2/tinylfu"
)

// Cache is the minimal surface every compared implementation is adapted to.
type Cache interface {
	Get(k int) bool
	Set(k, v int)
	// Flush makes asynchronous writes visible; a no-op for synchronous caches.
	Flush()
}

// Impl constructs a cache bounded to capacity entries.
type Impl struct {
	Name string
	// Algorithm groups implementations of the same eviction algorithm so results compare like with like.
	Algorithm string
	New       func(capacity int) Cache
	// Bounded is false for baselines that never evict, which are excluded from hit-ratio comparisons.
	Bounded bool
}

// Algorithms groups.
const (
	AlgLRU     = "LRU"
	AlgLFU     = "LFU"
	AlgTinyLFU = "TinyLFU"
	AlgS3FIFO  = "S3-FIFO"
	AlgSIEVE   = "SIEVE"
	AlgMap     = "unbounded map"
)

// Impls lists every compared implementation.
func Impls() []Impl {
	return []Impl{
		{"v2-lru", AlgLRU, func(c int) Cache { return v2{lru.New[int, int](uint64(c)).Build()} }, true},
		{"v1-lru", AlgLRU, func(c int) Cache { return v1lru{lruv1.New[int, int](c).BuildThreadSafe()} }, true},
		{"hashicorp-lru", AlgLRU, newHashicorp, true},
		{"v2-lfu", AlgLFU, func(c int) Cache { return v2{lfu.New[int, int](uint64(c)).Build()} }, true},
		{"v1-lfu", AlgLFU, func(c int) Cache { return v1lfu{lfuv1.New[int, int](c).BuildThreadSafe()} }, true},
		{"v2-tinylfu", AlgTinyLFU, func(c int) Cache { return v2{tinylfu.New[int, int](uint64(c)).Build()} }, true},
		{"otter", AlgTinyLFU, newOtter, true},
		{"ristretto", AlgTinyLFU, newRistretto, true},
		{"v2-s3fifo", AlgS3FIFO, func(c int) Cache { return v2{s3fifo.New[int, int](uint64(c)).Build()} }, true},
		{"golang-fifo-s3fifo", AlgS3FIFO, newFifoS3, true},
		{"golang-fifo-sieve", AlgSIEVE, newFifoSieve, true},
		{"sync.Map", AlgMap, func(int) Cache { return &syncMap{} }, false},
	}
}

type v2 struct{ c *cache.Cache[int, int] }

func (a v2) Get(k int) bool { return a.c.Get(k).IsSome() }
func (a v2) Set(k, v int)   { a.c.Set(k, v) }
func (a v2) Flush()         {}

type v1lru struct {
	c lruv1.ThreadSafeCache[int, int]
}

func (a v1lru) Get(k int) bool { return a.c.Get(k).IsSome() }
func (a v1lru) Set(k, v int)   { a.c.Set(k, v) }
func (a v1lru) Flush()         {}

type v1lfu struct {
	c lfuv1.ThreadSafeCache[int, int]
}

func (a v1lfu) Get(k int) bool { return a.c.Get(k).IsSome() }
func (a v1lfu) Set(k, v int)   { a.c.Set(k, v) }
func (a v1lfu) Flush()         {}

type otterCache struct{ c *otter.Cache[int, int] }

func newOtter(capacity int) Cache {
	return otterCache{otter.Must(&otter.Options[int, int]{MaximumSize: capacity})}
}

func (a otterCache) Get(k int) bool {
	_, ok := a.c.GetIfPresent(k)
	return ok
}
func (a otterCache) Set(k, v int) { a.c.Set(k, v) }
func (a otterCache) Flush()       {}

type ristrettoCache struct{ c *ristretto.Cache[int, int] }

func newRistretto(capacity int) Cache {
	c, err := ristretto.NewCache(&ristretto.Config[int, int]{
		NumCounters: int64(capacity) * 10,
		MaxCost:     int64(capacity),
		BufferItems: 64,
	})
	if err != nil {
		panic(err)
	}
	return ristrettoCache{c}
}

func (a ristrettoCache) Get(k int) bool {
	_, ok := a.c.Get(k)
	return ok
}
func (a ristrettoCache) Set(k, v int) { a.c.Set(k, v, 1) }
func (a ristrettoCache) Flush()       { a.c.Wait() }

type hashicorpCache struct{ c *hashicorp.Cache[int, int] }

func newHashicorp(capacity int) Cache {
	c, err := hashicorp.New[int, int](capacity)
	if err != nil {
		panic(err)
	}
	return hashicorpCache{c}
}

func (a hashicorpCache) Get(k int) bool {
	_, ok := a.c.Get(k)
	return ok
}
func (a hashicorpCache) Set(k, v int) { a.c.Add(k, v) }
func (a hashicorpCache) Flush()       {}

type fifoS3 struct{ c *fifos3.S3FIFO[int, int] }

func newFifoS3(capacity int) Cache { return fifoS3{fifos3.New[int, int](capacity, 0)} }

func (a fifoS3) Get(k int) bool {
	_, ok := a.c.Get(k)
	return ok
}
func (a fifoS3) Set(k, v int) { a.c.Set(k, v) }
func (a fifoS3) Flush()       {}

type fifoSieve struct{ c *fifosieve.Sieve[int, int] }

func newFifoSieve(capacity int) Cache { return fifoSieve{fifosieve.New[int, int](capacity, 0)} }

func (a fifoSieve) Get(k int) bool {
	_, ok := a.c.Get(k)
	return ok
}
func (a fifoSieve) Set(k, v int) { a.c.Set(k, v) }
func (a fifoSieve) Flush()       {}

type syncMap struct{ m sync.Map }

func (a *syncMap) Get(k int) bool {
	_, ok := a.m.Load(k)
	return ok
}
func (a *syncMap) Set(k, v int) { a.m.Store(k, v) }
func (a *syncMap) Flush()       {}
