package main

import (
	"context"
	"fmt"
	"github.com/go-playground/cache/lfu"
	"time"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Guarding with a Mutex with one operation per interaction semantics.
	cache := lfu.New[string, string](100).MaxAge(time.Hour).BuildThreadSafe()

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
}
