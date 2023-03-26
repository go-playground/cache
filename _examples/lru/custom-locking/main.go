package main

import (
	"context"
	"fmt"
	"github.com/go-playground/cache/lru"
	syncext "github.com/go-playground/pkg/v5/sync"
	"time"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Guarding with a Mutex to choose our own locking semantics.
	cache := syncext.NewMutex2(lru.New[string, string](100).MaxAge(time.Hour).Build())

	// example of collecting/emitting stats for cache
	go func(ctx context.Context) {

		var ticker = time.NewTicker(time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				guard := cache.Lock()
				stats := guard.T.Stats()
				guard.Unlock()

				// do things with stats
				fmt.Printf("%#v\n", stats)
			}
		}
	}(ctx)

	guard := cache.Lock()
	guard.T.Set("a", "b")
	guard.T.Set("c", "d")
	option := guard.T.Get("a")
	guard.Unlock()

	if option.IsNone() {
		return
	}
	fmt.Println("result:", option.Unwrap())
}
