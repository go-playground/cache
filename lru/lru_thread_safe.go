package lru

import (
	syncext "github.com/go-playground/pkg/v5/sync"
	optionext "github.com/go-playground/pkg/v5/values/option"
	"sync"
)

// ThreadSafeCache is a drop in replacement for Cache which automatically handles locking all cache interactions.
// This cache should be used when being used across threads/goroutines.
type ThreadSafeCache[K comparable, V any] struct {
	cache syncext.Mutex2[*Cache[K, V]]
}

// Set sets an item into the cache. It will replace the current entry if there is one.
func (c ThreadSafeCache[K, V]) Set(key K, value V) {
	guard := c.cache.Lock()
	guard.T.Set(key, value)
	guard.Unlock()
}

// Get attempts to find an existing cache entry by key.
// It returns an Option you must check before using the underlying value.
func (c ThreadSafeCache[K, V]) Get(key K) (result optionext.Option[V]) {
	guard := c.cache.Lock()
	result = guard.T.Get(key)
	guard.Unlock()
	return
}

// Remove removes the item matching the provided key from the cache, if not present is a noop.
func (c ThreadSafeCache[K, V]) Remove(key K) {
	guard := c.cache.Lock()
	guard.T.Remove(key)
	guard.Unlock()
}

// Clear empties the cache.
func (c ThreadSafeCache[K, V]) Clear() {
	guard := c.cache.Lock()
	guard.T.Clear()
	guard.Unlock()
}

// Stats returns the delta of Stats since last call to the Stats function.
func (c ThreadSafeCache[K, V]) Stats() (stats Stats) {
	guard := c.cache.Lock()
	stats = guard.T.Stats()
	guard.Unlock()
	return
}

// LockGuard locks the current cache and returns the Guard to Unlock. This is for when you wish to perform multiple
// operations on the cache during one lock operation.
func (c ThreadSafeCache[K, V]) LockGuard() syncext.MutexGuard[*Cache[K, V], *sync.Mutex] {
	return c.cache.Lock()
}
