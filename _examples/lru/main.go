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

	cache := lru.New[string, string](100).MaxAge(time.Hour).Stats(time.Minute, func(stats lru.Stats) {
		fmt.Printf("Stats: %#v\n", stats)
	}).Build(ctx)
	cache.Set("a", "b")
	cache.Set("c", "d")

	option := cache.Get("a")
	if option.IsNone() {
		return
	}
	fmt.Println("result:", option.Unwrap())
}
