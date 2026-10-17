package core

import (
	"time"

	"github.com/go-playground/cache/v2/internal/policy"
)

// Cause is why an entry was evicted.
type Cause uint8

const (
	// CauseSize means the entry was evicted to stay within capacity.
	CauseSize Cause = iota + 1
	// CauseExpired means the entry's expiry passed.
	CauseExpired
)

type expiryMode uint8

const (
	expireNever expiryMode = iota
	expireAfterWrite
	expireAfterAccess
	expireCustom
)

// Config is the policy-agnostic cache configuration assembled by the public builders.
// It can only be constructed via NewConfig, so the public cache.New is uncallable outside this module.
type Config[K comparable, V any] struct {
	capacity    uint64
	weigher     func(K, V) uint32
	newPolicy   func(capacity uint64) policy.Policy[K, V]
	expiryMode  expiryMode
	ttl         time.Duration
	expireFn    func(K, V) time.Duration
	onEviction  func(K, V, Cause)
	recordStats bool
}

// NewConfig returns a Config with defaults: no expiry, no eviction callback and no statistics.
// A nil weigher weighs every entry as 1.
func NewConfig[K comparable, V any](capacity uint64, weigher func(K, V) uint32, newPolicy func(capacity uint64) policy.Policy[K, V]) Config[K, V] {
	return Config[K, V]{capacity: capacity, weigher: weigher, newPolicy: newPolicy}
}

// ExpireAfterWrite expires entries d after they were last written. d <= 0 disables expiry.
func (c *Config[K, V]) ExpireAfterWrite(d time.Duration) {
	c.setTTL(expireAfterWrite, d)
}

// ExpireAfterAccess expires entries d after they were last read or written. d <= 0 disables expiry.
func (c *Config[K, V]) ExpireAfterAccess(d time.Duration) {
	c.setTTL(expireAfterAccess, d)
}

// ExpireAfter expires each entry after the duration fn returns when it is written; a result <= 0 means that entry
// never expires. A nil fn disables expiry.
func (c *Config[K, V]) ExpireAfter(fn func(K, V) time.Duration) {
	c.ttl, c.expireFn, c.expiryMode = 0, fn, expireCustom
	if fn == nil {
		c.expiryMode = expireNever
	}
}

func (c *Config[K, V]) setTTL(mode expiryMode, d time.Duration) {
	c.ttl, c.expireFn, c.expiryMode = d, nil, mode
	if d <= 0 {
		c.ttl, c.expiryMode = 0, expireNever
	}
}

// OnEviction sets a callback invoked, outside any lock, for every entry evicted due to capacity or expiry.
// A nil fn disables it.
func (c *Config[K, V]) OnEviction(fn func(K, V, Cause)) {
	c.onEviction = fn
}

// RecordStats enables hit, miss, set and load counters.
func (c *Config[K, V]) RecordStats() {
	c.recordStats = true
}
