package lru

import (
	listext "github.com/go-playground/pkg/v5/container/list"
	optionext "github.com/go-playground/pkg/v5/values/option"
	"sync"
	"time"
)

// TODO: Benchmark using entry and &entry

type builder[K comparable, V any] struct {
	lru *LRU[K, V]
}

// New initializes a builder to create an LRU cache.
func New[K comparable, V any]() *builder[K, V] {
	return &builder[K, V]{
		lru: &LRU[K, V]{
			list:     listext.NewDoublyLinked[entry[K, V]](),
			nodes:    make(map[K]*listext.Node[entry[K, V]]),
			capacity: 1_000,
		},
	}
}

// Capacity sets the maximum capacity for the cache.
func (b *builder[K, V]) Capacity(capacity int) *builder[K, V] {
	b.lru.capacity = capacity
	return b
}

// MaxAge sets the maximum age of an entry before it should be discarded; passively.
func (b *builder[K, V]) MaxAge(maxAge time.Duration) *builder[K, V] {
	b.lru.maxAge = maxAge
	return b
}

// HitFn sets an optional function to call upon cache hit.
func (b *builder[K, V]) HitFn(fn func(key K, value V)) *builder[K, V] {
	b.lru.hitFn = fn
	return b
}

// MissFn sets an optional function to call upon cache miss.
func (b *builder[K, V]) MissFn(fn func(key K)) *builder[K, V] {
	b.lru.missFn = fn
	return b
}

// EvictFn sets an optional function to call upon cache eviction.
func (b *builder[K, V]) EvictFn(fn func(key K, value V)) *builder[K, V] {
	b.lru.evictFn = fn
	return b
}

// PercentageFullFn sets an optional function to call upon cache size change that will be passed the percentage full
// as an integer with no decimals.
//
// It will only be called if the percentage changes value from previous.
func (b *builder[K, V]) PercentageFullFn(fn func(percentageFull int)) *builder[K, V] {
	b.lru.percentageFullFn = fn
	return b
}

// Build finalizes configuration and returns the LRU cache for use.
func (b *builder[K, V]) Build() (lru *LRU[K, V]) {
	lru = b.lru
	b.lru = nil
	return lru
}

type entry[K comparable, V any] struct {
	key   K
	value V
	ts    time.Time
}

type LRU[K comparable, V any] struct {
	m                  sync.RWMutex
	list               *listext.DoublyLinkedList[entry[K, V]]
	nodes              map[K]*listext.Node[entry[K, V]]
	capacity           int
	maxAge             time.Duration
	lastPercentageFull int
	hitFn              func(key K, value V)
	missFn             func(key K)
	evictFn            func(key K, value V)
	percentageFullFn   func(percentFull int)
}

// Set sets an item into the cache. It will replace the current entry if there is one.
func (cache *LRU[K, V]) Set(key K, value V) {
	cache.m.Lock()

	e := entry[K, V]{
		key:   key,
		value: value,
		ts:    time.Now(),
	}

	node, found := cache.nodes[key]
	if found {
		node.Value = e
		cache.list.MoveToFront(node)
	} else {
		cache.nodes[key] = cache.list.PushFront(e)
		if cache.list.Len() > cache.capacity {
			entry := cache.list.PopBack()
			delete(cache.nodes, entry.Value.key)
			if cache.evictFn != nil {
				cache.evictFn(key, entry.Value.value)
			}
		} else {
			if cache.percentageFullFn != nil {
				pf := int(float64(cache.list.Len()) / float64(cache.capacity) * 100.0)
				if pf != cache.lastPercentageFull {
					cache.lastPercentageFull = pf
					cache.percentageFullFn(pf)
				}
			}
		}
	}
	cache.m.Unlock()
}

// Get attempts to find an existing cache entry by key.
// It returns an Option you must check before using the underlying value.
func (cache *LRU[K, V]) Get(key K) (result optionext.Option[V]) {
	cache.m.Lock()

	node, found := cache.nodes[key]
	if found {
		if cache.maxAge > 0 && time.Since(node.Value.ts) > cache.maxAge {
			delete(cache.nodes, key)
			cache.list.Remove(node)
			if cache.evictFn != nil {
				cache.evictFn(key, node.Value.value)
			}
		} else {
			cache.list.MoveToFront(node)
			result = optionext.Some(node.Value.value)
			if cache.hitFn != nil {
				cache.hitFn(key, node.Value.value)
			}
		}
	} else if cache.missFn != nil {
		cache.missFn(key)
	}
	cache.m.Unlock()
	return
}

// Remove removes the item matching the provided key from the cache, if not present is a noop.
func (cache *LRU[K, V]) Remove(key K) {
	cache.m.Lock()
	if node, found := cache.nodes[key]; found {
		delete(cache.nodes, key)
		cache.list.Remove(node)
	}
	cache.m.Unlock()
}

// Clear empties the cache.
func (cache *LRU[K, V]) Clear() {
	cache.m.Lock()
	cache.list.Clear()
	for k := range cache.nodes {
		delete(cache.nodes, k)
	}
	if cache.percentageFullFn != nil {
		pf := int(float64(cache.list.Len()) / float64(cache.capacity) * 100.0)
		if pf != cache.lastPercentageFull {
			cache.lastPercentageFull = pf
			cache.percentageFullFn(pf)
		}
	}
	cache.m.Unlock()
}

// Len returns the current size of the cache.
// The result will include items that may be expired past the max age as they are passively expired.
func (cache *LRU[K, V]) Len() (length int) {
	cache.m.Lock()
	length = cache.list.Len()
	cache.m.Unlock()
	return
}

// Capacity returns the current configured capacity of the cache.
func (cache *LRU[K, V]) Capacity() (capacity int) {
	cache.m.Lock()
	capacity = cache.capacity
	cache.m.Unlock()
	return
}
