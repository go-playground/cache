package lfu

import (
	listext "github.com/go-playground/pkg/v5/container/list"
	syncext "github.com/go-playground/pkg/v5/sync"
	timeext "github.com/go-playground/pkg/v5/time"
	optionext "github.com/go-playground/pkg/v5/values/option"
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
			stats:       Stats{Capacity: capacity},
		},
	}
}

// MaxAge sets the maximum age of an entry before it should be discarded; passively.
func (b *builder[K, V]) MaxAge(maxAge time.Duration) *builder[K, V] {
	b.lfu.maxAge = int64(maxAge)
	return b
}

// Build finalizes configuration and returns the LFU cache for use.
func (b *builder[K, V]) Build() (lfu *Cache[K, V]) {
	lfu = b.lfu
	b.lfu = nil
	return lfu
}

// BuildAutoLock finalizes configuration and returns an LRU cache for use guarded by a mutex.
//
// See Build for Cache where you may choose your own locking semantics.
func (b *builder[K, V]) BuildAutoLock() AutoLockCache[K, V] {
	return AutoLockCache[K, V]{
		cache: syncext.NewMutex2(b.Build()),
	}
}

// Stats represents the cache statistics.
type Stats struct {
	Capacity, Len                       int
	Hits, Misses, Evictions, Gets, Sets uint
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
	frequencies *listext.DoublyLinkedList[frequency[K, V]]
	entries     map[K]*listext.Node[entry[K, V]]
	maxAge      int64
	stats       Stats
}

// Set sets an item into the cache. It will replace the current entry if there is one.
func (cache *Cache[K, V]) Set(key K, value V) {
	cache.stats.Sets++

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
		if len(cache.entries) > cache.stats.Capacity {
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
				cache.stats.Evictions++
			}
		}
	}
}

// Get attempts to find an existing cache entry by key.
// It returns an Option you must check before using the underlying value.
func (cache *Cache[K, V]) Get(key K) (result optionext.Option[V]) {
	cache.stats.Gets++

	node, found := cache.entries[key]
	if found {
		if cache.maxAge > 0 && timeext.NanoTime()-node.Value.ts > cache.maxAge {
			cache.remove(node)
			cache.stats.Evictions++
		} else {
			cache.stats.Hits++
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
		}
	} else {
		cache.stats.Misses++
	}
	return
}

// Remove removes the item matching the provided key from the cache, if not present is a noop.
func (cache *Cache[K, V]) Remove(key K) {
	if node, found := cache.entries[key]; found {
		cache.remove(node)
	}
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
	for _, node := range cache.entries {
		cache.remove(node)
	}
	// reset stats
	_ = cache.Stats()
}

// Stats returns the delta of Stats since last call to the Stats function.
func (cache *Cache[K, V]) Stats() (stats Stats) {
	stats = cache.stats
	stats.Len = len(cache.entries)
	cache.stats.Hits = 0
	cache.stats.Misses = 0
	cache.stats.Evictions = 0
	cache.stats.Gets = 0
	cache.stats.Sets = 0
	return
}
