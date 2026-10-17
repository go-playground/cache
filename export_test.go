package cache

// SetClockForTesting replaces c's monotonic clock; call before using c.
func SetClockForTesting[K comparable, V any](c *Cache[K, V], now func() int64) {
	c.e.SetClockForTesting(now)
}

// Drain synchronously applies c's pending maintenance.
func Drain[K comparable, V any](c *Cache[K, V]) {
	c.e.Drain()
}
