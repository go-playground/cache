package lru

import (
	listext "github.com/go-playground/pkg/v5/container/list"
	timeext "github.com/go-playground/pkg/v5/time"
	optionext "github.com/go-playground/pkg/v5/values/option"
	"time"
	"log"
)

type builder[K comparable, V any] struct {
	lru *Cache[K, V]
}

// New initializes a builder to create an LRU cache.
func New[K comparable, V any](capacity int) *builder[K, V] {
	return &builder[K, V]{
		lru: &Cache[K, V]{
			list:  listext.NewDoublyLinked[entry[K, V]](),
			nodes: make(map[K]*listext.Node[entry[K, V]]),
			stats: Stats{Capacity: capacity},
			loader: optionext.None[func(K) optionext.Option[V]],
		},
	}
}

// MaxAge sets the maximum age of an entry before it should be discarded; passively.
func (b *builder[K, V]) MaxAge(maxAge time.Duration) *builder[K, V] {
	b.lru.maxAge = int64(maxAge)
	return b
}

// CacheLoader sets the loader function to put values in the cache.
func (b *builder[K, V]) CacheLoader(loader func(K) optionext.Option[V]) *builder[K, V] {
	if loader == nil {
		log.Fatal("cannot put a nil loader.")
	}
	b.lru.loader = optionext.Some(loader)
	return b
}

// Build finalizes configuration and returns the LRU cache for use.
//
// The provided context is used for graceful shutdown of goroutines, such as stats reporting in background
// goroutine and alike.
func (b *builder[K, V]) Build() (lru *Cache[K, V]) {
	lru = b.lru
	b.lru = nil
	return lru
}

// Stats represents the cache statistics.
type Stats struct {
	Capacity, Len                       int
	Hits, Misses, Evictions, Gets, Sets uint
}

type entry[K comparable, V any] struct {
	key   K
	value V
	ts    int64
}

// Cache is a configured least recently used cache ready for use.
type Cache[K comparable, V any] struct {
	list   *listext.DoublyLinkedList[entry[K, V]]
	nodes  map[K]*listext.Node[entry[K, V]]
	maxAge int64
	stats  Stats
	loader  optionext.Option[func(K) optionext.Option[V]]

}

// Set sets an item into the cache. It will replace the current entry if there is one.
func (cache *Cache[K, V]) Set(key K, value V) {
	cache.stats.Sets++

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
		if cache.list.Len() > cache.stats.Capacity {
			entry := cache.list.PopBack()
			delete(cache.nodes, entry.Value.key)
			cache.stats.Evictions++
		}
	}
}

// Get attempts to find an existing cache entry by key or if cache loader is set then get from this.
// It returns an Option you must check before using the underlying value.
func (cache *Cache[K, V]) Get(key K) (result optionext.Option[V]) {
	value := cache.get(key)

	if value.IsNone() && cache.loader.IsSome() {
		value = cache.loader.Unwrap()(key)

		if value.IsSome() {
			cache.Set(key, value.Unwrap())
		}
	}

	return value
}

// Get attempts to find an existing cache entry by key.
// It returns an Option you must check before using the underlying value.
func (cache *Cache[K, V]) Get(key K) (result optionext.Option[V]) {
	cache.stats.Gets++

	node, found := cache.nodes[key]
	if found {
		if cache.maxAge > 0 && timeext.NanoTime()-node.Value.ts > cache.maxAge {
			delete(cache.nodes, key)
			cache.list.Remove(node)
			cache.stats.Evictions++
		} else {
			cache.list.MoveToFront(node)
			result = optionext.Some(node.Value.value)
			cache.stats.Hits++
		}
	} else {
		cache.stats.Misses++
	}
	return
}

// Remove removes the item matching the provided key from the cache, if not present is a noop.
func (cache *Cache[K, V]) Remove(key K) {
	if node, found := cache.nodes[key]; found {
		cache.remove(node)
	}
}

func (cache *Cache[K, V]) remove(node *listext.Node[entry[K, V]]) {
	if node, found := cache.nodes[node.Value.key]; found {
		delete(cache.nodes, node.Value.key)
		cache.list.Remove(node)
	}
}

// Clear empties the cache.
func (cache *Cache[K, V]) Clear() {
	for _, node := range cache.nodes {
		cache.remove(node)
	}
	// reset stats
	_ = cache.Stats()
}

// Stats returns the delta of Stats since last call to the Stats function.
func (cache *Cache[K, V]) Stats() (stats Stats) {
	stats = cache.stats
	stats.Len = cache.list.Len()
	cache.stats.Hits = 0
	cache.stats.Misses = 0
	cache.stats.Evictions = 0
	cache.stats.Gets = 0
	cache.stats.Sets = 0
	return
}
