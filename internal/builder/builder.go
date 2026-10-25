// Package builder holds configuration helpers shared by the public policy builder packages.
package builder

import (
	cache "github.com/go-playground/cache/v2"
	"github.com/go-playground/cache/v2/internal/core"
)

// OnEviction adapts a public eviction callback to the engine's and sets it on cfg. A nil fn disables it.
func OnEviction[K comparable, V any](cfg *core.Config[K, V], fn func(K, V, cache.Cause)) {
	if fn == nil {
		cfg.OnEviction(nil)
		return
	}
	cfg.OnEviction(func(k K, v V, c core.Cause) { fn(k, v, cache.Cause(c)) })
}
