package lfu

import (
	listext "github.com/go-playground/pkg/v5/container/list"
	timeext "github.com/go-playground/pkg/v5/time"
	optionext "github.com/go-playground/pkg/v5/values/option"
	"sync"
	"time"
)

type builder[K comparable, V any] struct {
	lfu *Cache[K, V]
}

// New initializes a builder to create an LFU cache.
func New[K comparable, V any](capacity int) *builder[K, V] {
	return &builder[K, V]{
		lfu: &Cache[K, V]{
			frequencies: listext.NewDoublyLinked[frequency[K, V]](),
			entries:     make(map[K]*listext.Node[entry[K, V]]),
			capacity:    capacity,
		},
	}
}

// MaxAge sets the maximum age of an entry before it should be discarded; passively.
func (b *builder[K, V]) MaxAge(maxAge time.Duration) *builder[K, V] {
	b.lfu.maxAge = int64(maxAge)
	return b
}

// HitFn sets an optional function to call upon cache hit.
func (b *builder[K, V]) HitFn(fn func(key K, value V)) *builder[K, V] {
	b.lfu.hitFn = fn
	return b
}

// MissFn sets an optional function to call upon cache miss.
func (b *builder[K, V]) MissFn(fn func(key K)) *builder[K, V] {
	b.lfu.missFn = fn
	return b
}

// EvictFn sets an optional function to call upon cache eviction.
func (b *builder[K, V]) EvictFn(fn func(key K, value V)) *builder[K, V] {
	b.lfu.evictFn = fn
	return b
}

// PercentageFullFn sets an optional function to call upon cache size change that will be passed the percentage full
// as an integer with no decimals.
//
// It will only be called if the percentage changes value from previous.
func (b *builder[K, V]) PercentageFullFn(fn func(percentageFull uint8)) *builder[K, V] {
	b.lfu.percentageFullFn = fn
	return b
}

// Build finalizes configuration and returns the LFU cache for use.
func (b *builder[K, V]) Build() (lru *Cache[K, V]) {
	lru = b.lfu
	b.lfu = nil
	return lru
}

type entry[K comparable, V any] struct {
	key       K
	value     V
	ts        int64
	frequency *listext.Node[frequency[K, V]]
}

type frequency[K comparable, V any] struct {
	entries *listext.DoublyLinkedList[entry[K, V]]
	count   int
}

// Cache is a configured least frequently used cache ready for use.
type Cache[K comparable, V any] struct {
	m                  sync.Mutex
	frequencies        *listext.DoublyLinkedList[frequency[K, V]]
	entries            map[K]*listext.Node[entry[K, V]]
	capacity           int
	maxAge             int64
	lastPercentageFull uint8
	hitFn              func(key K, value V)
	missFn             func(key K)
	evictFn            func(key K, value V)
	percentageFullFn   func(percentFull uint8)
}

// Set sets an item into the cache. It will replace the current entry if there is one.
func (cache *Cache[K, V]) Set(key K, value V) {
	cache.m.Lock()

	node, found := cache.entries[key]
	if found {
		node.Value.value = value
		if cache.maxAge > 0 {
			node.Value.ts = timeext.NanoTime()
		}
		node.Value.frequency.Value.entries.MoveToFront(node)
	} else {

		// determine or create frequency
		freq := cache.frequencies.Back()
		if freq == nil || freq.Value.count != 1 {
			freq = cache.frequencies.PushBack(frequency[K, V]{
				entries: listext.NewDoublyLinked[entry[K, V]](),
				count:   1,
			})
		}
		e := entry[K, V]{
			key:       key,
			value:     value,
			frequency: freq,
		}
		if cache.maxAge > 0 {
			e.ts = timeext.NanoTime()
		}
		cache.entries[key] = freq.Value.entries.PushFront(e)
		if len(cache.entries) > cache.capacity {
			// if we just added the back frequency node
			if freq.Value.count == 1 && freq.Value.entries.Len() == 1 {
				freq = freq.Prev()
			}
			if freq != nil {
				ent := freq.Value.entries.PopBack()
				ent.Value.frequency = nil // detach
				delete(cache.entries, ent.Value.key)
				if freq.Value.entries.Len() == 0 {
					cache.frequencies.Remove(freq)
				}
				if cache.evictFn != nil {
					cache.evictFn(key, ent.Value.value)
				}
			}
		} else {
			cache.reportPercentFull()
		}
	}
	cache.m.Unlock()
}

// Get attempts to find an existing cache entry by key.
// It returns an Option you must check before using the underlying value.
func (cache *Cache[K, V]) Get(key K) (result optionext.Option[V]) {
	cache.m.Lock()

	node, found := cache.entries[key]
	if found {
		if cache.maxAge > 0 && timeext.NanoTime()-node.Value.ts > cache.maxAge {
			cache.remove(node)
			if cache.evictFn != nil {
				cache.evictFn(key, node.Value.value)
			}
		} else {
			nextCount := node.Value.frequency.Value.count + 1
			// super edge case, int can wrap around, if that's the case don't do anything but
			// mark as most recently accessed, it's already in the top tier and so want to keep it
			// there.
			if nextCount <= 0 {
				node.Value.frequency.Value.entries.MoveToFront(node)
			} else {
				prev := node.Value.frequency.Prev()
				if prev == nil && node.Value.frequency.Value.entries.Len() == 1 {
					// frequency is the head node and is the only entry, we can just increment the counter of the frequency
					// as an early optimization.
					node.Value.frequency.Value.count = nextCount
				} else {
					if prev == nil || prev.Value.count != nextCount {
						prev = cache.frequencies.PushBefore(node.Value.frequency, frequency[K, V]{
							entries: listext.NewDoublyLinked[entry[K, V]](),
							count:   nextCount,
						})
					}
					node.Value.frequency.Value.entries.Remove(node)
					if node.Value.frequency.Value.entries.Len() == 0 {
						cache.frequencies.Remove(node.Value.frequency)
					}
					node.Value.frequency = prev
					prev.Value.entries.InsertAtFront(node)
				}
			}
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
func (cache *Cache[K, V]) Remove(key K) {
	cache.m.Lock()
	if node, found := cache.entries[key]; found {
		cache.remove(node)
	}
	cache.m.Unlock()
}

func (cache *Cache[K, V]) remove(node *listext.Node[entry[K, V]]) {
	delete(cache.entries, node.Value.key)
	node.Value.frequency.Value.entries.Remove(node)
	if node.Value.frequency.Value.entries.Len() == 0 {
		cache.frequencies.Remove(node.Value.frequency)
	}
	node.Value.frequency = nil
}

// Clear empties the cache.
func (cache *Cache[K, V]) Clear() {
	cache.m.Lock()
	for _, node := range cache.entries {
		cache.remove(node)
	}
	cache.reportPercentFull()
	cache.m.Unlock()
}

// Len returns the current size of the cache.
// The result will include items that may be expired past the max age as they are passively expired.
func (cache *Cache[K, V]) Len() (length int) {
	cache.m.Lock()
	length = len(cache.entries)
	cache.m.Unlock()
	return
}

// Capacity returns the current configured capacity of the cache.
func (cache *Cache[K, V]) Capacity() (capacity int) {
	cache.m.Lock()
	capacity = cache.capacity
	cache.m.Unlock()
	return
}

func (cache *Cache[K, V]) reportPercentFull() {
	if cache.percentageFullFn != nil {
		pf := uint8(float64(len(cache.entries)) / float64(cache.capacity) * 100.0)
		if pf != cache.lastPercentageFull {
			cache.lastPercentageFull = pf
			cache.percentageFullFn(pf)
		}
	}
}
