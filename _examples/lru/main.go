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

	// wrapping with a Mutex, if not needed omit.
	cache := syncext.NewMutex2(lru.New[string, string](100).MaxAge(time.Hour).Build())

	// example of collecting/emitting stats for cache
	// this does require a mutex guard to collect async
	go func(ctx context.Context) {

		var ticker = time.NewTicker(time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c := cache.Lock()
				stats := c.Stats()
				cache.Unlock()

				// do things with stats
				fmt.Printf("%#v\n", stats)
			}
		}
	}(ctx)

	c := cache.Lock()
	c.Set("a", "b")
	c.Set("c", "d")
	option := c.Get("a")
	cache.Unlock()

	if option.IsNone() {
		return
	}
	fmt.Println("result:", option.Unwrap())
}
