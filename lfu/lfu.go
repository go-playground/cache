package lfu

import (
	listext "github.com/go-playground/pkg/v5/container/list"
	optionext "github.com/go-playground/pkg/v5/values/option"
	"sync"
	"time"
)

// TODO: int can wrap around and must account for that, triple check.
// TODO: capacity must be >= 2 otherwise some logic could panic, add validation to that effect

//type builder[K comparable, V any] struct {
//	lru *LFU[K, V]
//}
//
//// New initializes a builder to create an LFU cache.
//func New[K comparable, V any](capacity int) *builder[K, V] {
//	return &builder[K, V]{
//		lru: &LFU[K, V]{
//			list:     listext.NewDoublyLinked[entry[K, V]](),
//			nodes:    make(map[K]*listext.Node[entry[K, V]]),
//			capacity: capacity,
//		},
//	}
//}
//
//// Capacity sets the maximum capacity for the cache.
//func (b *builder[K, V]) Capacity(capacity int) *builder[K, V] {
//	b.lru.capacity = capacity
//	return b
//}
//
//// MaxAge sets the maximum age of an entry before it should be discarded; passively.
//func (b *builder[K, V]) MaxAge(maxAge time.Duration) *builder[K, V] {
//	b.lru.maxAge = maxAge
//	return b
//}
//
//// HitFn sets an optional function to call upon cache hit.
//func (b *builder[K, V]) HitFn(fn func(key K, value V)) *builder[K, V] {
//	b.lru.hitFn = fn
//	return b
//}
//
//// MissFn sets an optional function to call upon cache miss.
//func (b *builder[K, V]) MissFn(fn func(key K)) *builder[K, V] {
//	b.lru.missFn = fn
//	return b
//}
//
//// EvictFn sets an optional function to call upon cache eviction.
//func (b *builder[K, V]) EvictFn(fn func(key K, value V)) *builder[K, V] {
//	b.lru.evictFn = fn
//	return b
//}
//
//// PercentageFullFn sets an optional function to call upon cache size change that will be passed the percentage full
//// as an integer with no decimals.
////
//// It will only be called if the percentage changes value from previous.
//func (b *builder[K, V]) PercentageFullFn(fn func(percentageFull uint8)) *builder[K, V] {
//	b.lru.percentageFullFn = fn
//	return b
//}
//
//// Build finalizes configuration and returns the LFU cache for use.
//func (b *builder[K, V]) Build() (lru *LFU[K, V]) {
//	lru = b.lru
//	b.lru = nil
//	return lru
//}

type entry[K comparable, V any] struct {
	key       K
	value     V
	ts        time.Time
	frequency *listext.Node[frequency[K, V]]
}

type frequency[K comparable, V any] struct {
	entries *listext.DoublyLinkedList[entry[K, V]]
	count   int
}

// LFU is a configured least frequently used cache ready for use.
type LFU[K comparable, V any] struct {
	m                  sync.Mutex
	frequencies        *listext.DoublyLinkedList[frequency[K, V]]
	entries            map[K]*listext.Node[entry[K, V]]
	capacity           int
	maxAge             time.Duration
	lastPercentageFull uint8
	hitFn              func(key K, value V)
	missFn             func(key K)
	evictFn            func(key K, value V)
	percentageFullFn   func(percentFull uint8)
}

// Set sets an item into the cache. It will replace the current entry if there is one.
func (cache *LFU[K, V]) Set(key K, value V) {
	cache.m.Lock()

	node, found := cache.entries[key]
	if found {
		node.Value.value = value
		if cache.maxAge > 0 {
			node.Value.ts = time.Now()
		}
		node.Value.frequency.Value.entries.MoveToFront(node)
	} else {

		// determine or create frequency
		freq := cache.frequencies.Back()
		if freq.Value.count != 1 {
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
			e.ts = time.Now()
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
			if cache.percentageFullFn != nil {
				pf := uint8(float64(len(cache.entries)) / float64(cache.capacity) * 100.0)
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
func (cache *LFU[K, V]) Get(key K) (result optionext.Option[V]) {
	cache.m.Lock()

	node, found := cache.entries[key]
	if found {
		freq := node.Value.frequency
		node.Value.frequency = nil // detach

		if cache.maxAge > 0 && time.Since(node.Value.ts) > cache.maxAge {
			delete(cache.entries, key)
			freq.Value.entries.Remove(node)
			if freq.Value.entries.Len() == 0 {
				cache.frequencies.Remove(freq)
			}
			if cache.evictFn != nil {
				cache.evictFn(key, node.Value.value)
			}
		} else {

			nextCount := freq.Value.count + 1
			prev := freq.Prev()

			freq.Value.entries.Remove(node)
			if freq.Value.entries.Len() == 0 {
				cache.frequencies.Remove(freq)
			}

			if prev == nil || prev.Value.count != nextCount {
				// create new node
				prev = cache.frequencies.PushFront(frequency[K, V]{
					entries: listext.NewDoublyLinked[entry[K, V]](),
					count:   nextCount,
				})
			}

			node := prev.Value.entries.PushFront(node.Value)
			node.Value.frequency = prev
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

//// Remove removes the item matching the provided key from the cache, if not present is a noop.
//func (cache *LFU[K, V]) Remove(key K) {
//	cache.m.Lock()
//	if node, found := cache.nodes[key]; found {
//		delete(cache.nodes, key)
//		cache.list.Remove(node)
//	}
//	cache.m.Unlock()
//}
//
//// Clear empties the cache.
//func (cache *LFU[K, V]) Clear() {
//	cache.m.Lock()
//	cache.list.Clear()
//	for k := range cache.nodes {
//		delete(cache.nodes, k)
//	}
//	if cache.percentageFullFn != nil {
//		pf := uint8(float64(cache.list.Len()) / float64(cache.capacity) * 100.0)
//		if pf != cache.lastPercentageFull {
//			cache.lastPercentageFull = pf
//			cache.percentageFullFn(pf)
//		}
//	}
//	cache.m.Unlock()
//}
//
//// Len returns the current size of the cache.
//// The result will include items that may be expired past the max age as they are passively expired.
//func (cache *LFU[K, V]) Len() (length int) {
//	cache.m.Lock()
//	length = cache.list.Len()
//	cache.m.Unlock()
//	return
//}
//
//// Capacity returns the current configured capacity of the cache.
//func (cache *LFU[K, V]) Capacity() (capacity int) {
//	cache.m.Lock()
//	capacity = cache.capacity
//	cache.m.Unlock()
//	return
//}
