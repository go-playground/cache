package lru

import (
	syncext "github.com/go-playground/pkg/v5/sync"
	optionext "github.com/go-playground/pkg/v5/values/option"
)

// AutoLockCache is a drop in replacement for Cache which automatically handles locking all cache interactions.
// This is for ease of use when the flexibility of locking scemantics are not required.
type AutoLockCache[K comparable, V any] struct {
	cache syncext.Mutex2[*Cache[K, V]]
}

// Set sets an item into the cache. It will replace the current entry if there is one.
func (c AutoLockCache[K, V]) Set(key K, value V) {
	guard := c.cache.Lock()
	guard.T.Set(key, value)
	guard.Unlock()
}

// Get attempts to find an existing cache entry by key.
// It returns an Option you must check before using the underlying value.
func (c AutoLockCache[K, V]) Get(key K) (result optionext.Option[V]) {
	guard := c.cache.Lock()
	result = guard.T.Get(key)
	guard.Unlock()
	return
}

// Remove removes the item matching the provided key from the cache, if not present is a noop.
func (c AutoLockCache[K, V]) Remove(key K) {
	guard := c.cache.Lock()
	guard.T.Remove(key)
	guard.Unlock()
}

// Clear empties the cache.
func (c AutoLockCache[K, V]) Clear() {
	guard := c.cache.Lock()
	guard.T.Clear()
	guard.Unlock()
}

// Stats returns the delta of Stats since last call to the Stats function.
func (c AutoLockCache[K, V]) Stats() (stats Stats) {
	guard := c.cache.Lock()
	stats = guard.T.Stats()
	guard.Unlock()
	return
}
