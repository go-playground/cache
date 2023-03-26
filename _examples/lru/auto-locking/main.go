package main

import (
	"context"
	"fmt"
	"github.com/go-playground/cache/lru"
	"time"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ThreadSafe cache with one operation per interaction semantics.
	cache := lru.New[string, string](100).MaxAge(time.Hour).BuildThreadSafe()

	// example of collecting/emitting stats for cache
	go func(ctx context.Context) {

		var ticker = time.NewTicker(time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stats := cache.Stats()

				// do things with stats
				fmt.Printf("%#v\n", stats)
			}
		}
	}(ctx)

	cache.Set("a", "b")
	cache.Set("c", "d")
	option := cache.Get("a")

	if option.IsNone() {
		return
	}
	fmt.Println("result:", option.Unwrap())

	// Have the ability to perform multiple operations at once by grabbing the LockGuard.
	guard := cache.LockGuard()
	guard.T.Set("c", "c")
	guard.T.Set("d", "d")
	guard.T.Remove("a")
	guard.Unlock()
}
