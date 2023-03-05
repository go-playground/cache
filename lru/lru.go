package lru

import (
	"context"
	listext "github.com/go-playground/pkg/v5/container/list"
	timeext "github.com/go-playground/pkg/v5/time"
	optionext "github.com/go-playground/pkg/v5/values/option"
	"sync"
	"time"
)

type builder[K comparable, V any] struct {
	lru          *Cache[K, V]
	statsCadence time.Duration
}

// New initializes a builder to create an LRU cache.
func New[K comparable, V any](capacity int) *builder[K, V] {
	return &builder[K, V]{
		lru: &Cache[K, V]{
			list:  listext.NewDoublyLinked[entry[K, V]](),
			nodes: make(map[K]*listext.Node[entry[K, V]]),
			stats: Stats{capacity: capacity},
		},
	}
}

// MaxAge sets the maximum age of an entry before it should be discarded; passively.
func (b *builder[K, V]) MaxAge(maxAge time.Duration) *builder[K, V] {
	b.lru.maxAge = int64(maxAge)
	return b
}

// Stats enables you to register a stats function that will be called periodically using the supplied duration.
//
// The Stats sent to the function will be the delta since last called.
// NOTE: In order to keep the cache blocked for as little time as possible the function call is not guaranteed to be
//
//	thread safe and is called outside of the transaction lock.
func (b *builder[K, V]) Stats(cadence time.Duration, fn func(stats Stats)) *builder[K, V] {
	b.statsCadence = cadence
	b.lru.statsFn = fn
	return b
}

// Build finalizes configuration and returns the LRU cache for use.
//
// The provided context is used for graceful shutdown of goroutines, such as stats reporting in background
// goroutine and alike.
func (b *builder[K, V]) Build(ctx context.Context) (lru *Cache[K, V]) {
	lru = b.lru
	b.lru = nil

	if lru.statsFn != nil && b.statsCadence != 0 {
		go func(ctx context.Context, cadence time.Duration) {

			var ticker = time.NewTicker(b.statsCadence)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					lru.m.Lock()
					s := lru.statsNoLock()
					lru.m.Unlock()
					lru.statsFn(s)
				}
			}
		}(ctx, b.statsCadence)
	}
	return lru
}

// Stats represents the cache statistics.
type Stats struct {
	capacity, len                       int
	hits, misses, evictions, gets, sets uint
}

type entry[K comparable, V any] struct {
	key   K
	value V
	ts    int64
}

// Cache is a configured least recently used cache ready for use.
type Cache[K comparable, V any] struct {
	m       sync.Mutex
	list    *listext.DoublyLinkedList[entry[K, V]]
	nodes   map[K]*listext.Node[entry[K, V]]
	maxAge  int64
	stats   Stats
	statsFn func(Stats)
}

// Set sets an item into the cache. It will replace the current entry if there is one.
func (cache *Cache[K, V]) Set(key K, value V) {
	cache.m.Lock()
	cache.stats.sets++

	node, found := cache.nodes[key]
	if found {
		node.Value.value = value
		if cache.maxAge > 0 {
			node.Value.ts = timeext.NanoTime()
		}
		cache.list.MoveToFront(node)
	} else {
		e := entry[K, V]{
			key:   key,
			value: value,
		}
		if cache.maxAge > 0 {
			e.ts = timeext.NanoTime()
		}
		cache.nodes[key] = cache.list.PushFront(e)
		if cache.list.Len() > cache.stats.capacity {
			entry := cache.list.PopBack()
			delete(cache.nodes, entry.Value.key)
			cache.stats.evictions++
		}
	}
	cache.m.Unlock()
}

// Get attempts to find an existing cache entry by key.
// It returns an Option you must check before using the underlying value.
func (cache *Cache[K, V]) Get(key K) (result optionext.Option[V]) {
	cache.m.Lock()
	cache.stats.gets++

	node, found := cache.nodes[key]
	if found {
		if cache.maxAge > 0 && timeext.NanoTime()-node.Value.ts > cache.maxAge {
			delete(cache.nodes, key)
			cache.list.Remove(node)
			cache.stats.evictions++
		} else {
			cache.list.MoveToFront(node)
			result = optionext.Some(node.Value.value)
			cache.stats.hits++
		}
	} else {
		cache.stats.misses++
	}
	cache.m.Unlock()
	return
}

// Remove removes the item matching the provided key from the cache, if not present is a noop.
func (cache *Cache[K, V]) Remove(key K) {
	cache.m.Lock()
	if node, found := cache.nodes[key]; found {
		cache.remove(node)
	}
	cache.m.Unlock()
}

func (cache *Cache[K, V]) remove(node *listext.Node[entry[K, V]]) {
	if node, found := cache.nodes[node.Value.key]; found {
		delete(cache.nodes, node.Value.key)
		cache.list.Remove(node)
	}
}

// Clear empties the cache.
func (cache *Cache[K, V]) Clear() {
	cache.m.Lock()
	for _, node := range cache.nodes {
		cache.remove(node)
	}
	cache.m.Unlock()
}

// statsNoLock returns the stats and reset values
func (cache *Cache[K, V]) statsNoLock() (stats Stats) {
	stats = cache.stats
	stats.len = cache.list.Len()
	cache.stats.hits = 0
	cache.stats.misses = 0
	cache.stats.evictions = 0
	cache.stats.gets = 0
	cache.stats.sets = 0
	return
}
