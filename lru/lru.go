package lru

import (
	listext "github.com/go-playground/pkg/v5/container/list"
	syncext "github.com/go-playground/pkg/v5/sync"
	timeext "github.com/go-playground/pkg/v5/time"
	optionext "github.com/go-playground/pkg/v5/values/option"
	"time"
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
		},
	}
}

// MaxAge sets the maximum age of an entry before it will be passively discarded.
//
// Default is no max age.
func (b *builder[K, V]) MaxAge(maxAge time.Duration) *builder[K, V] {
	if maxAge < 0 {
		panic("MaxAge is not permitted to be a negative value")
	}
	b.lru.maxAge = maxAge
	return b
}

// Build finalizes configuration and returns the LRU cache for use.
func (b *builder[K, V]) Build() (lru *Cache[K, V]) {
	lru = b.lru
	b.lru = nil
	return
}

// BuildThreadSafe finalizes configuration and returns an LRU cache for use guarded by a mutex.
func (b *builder[K, V]) BuildThreadSafe() ThreadSafeCache[K, V] {
	return ThreadSafeCache[K, V]{
		cache: syncext.NewMutex2(b.Build()),
	}
}

// Stats represents the cache statistics.
type Stats struct {
	// Capacity is the maximum cache capacity.
	Capacity int

	// Len is the current consumed cache capacity.
	Len int

	// Hits is the number of cache hits.
	Hits uint

	// Misses is the number of cache misses.
	Misses uint

	// Evictions is the number of cache evictions performed.
	Evictions uint

	// Gets is the number of cache gets performed regardless of a hit or miss.
	Gets uint

	// Sets is the number of cache sets performed.
	Sets uint
}

type entry[K comparable, V any] struct {
	key       K
	value     V
	timestamp timeext.Instant
}

// Cache is a configured least recently used cache ready for use.
type Cache[K comparable, V any] struct {
	list   *listext.DoublyLinkedList[entry[K, V]]
	nodes  map[K]*listext.Node[entry[K, V]]
	maxAge time.Duration
	stats  Stats
}

// Set sets an item into the cache. It will replace the current entry if there is one.
func (cache *Cache[K, V]) Set(key K, value V) {
	cache.stats.Sets++

	node, found := cache.nodes[key]
	if found {
		node.Value.value = value
		if cache.maxAge > 0 {
			node.Value.timestamp = timeext.NewInstant()
		}
		cache.list.MoveToFront(node)
	} else {
		e := entry[K, V]{
			key:   key,
			value: value,
		}
		if cache.maxAge > 0 {
			e.timestamp = timeext.NewInstant()
		}
		cache.nodes[key] = cache.list.PushFront(e)
		if cache.list.Len() > cache.stats.Capacity {
			entry := cache.list.PopBack()
			delete(cache.nodes, entry.Value.key)
			cache.stats.Evictions++
		}
	}
}

// Get attempts to find an existing cache entry by key.
// It returns an Option you must check before using the underlying value.
func (cache *Cache[K, V]) Get(key K) (result optionext.Option[V]) {
	cache.stats.Gets++

	node, found := cache.nodes[key]
	if found {
		if cache.maxAge > 0 && node.Value.timestamp.Elapsed() > cache.maxAge {
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
	// resets/empties stats
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
