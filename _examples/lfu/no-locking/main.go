package main

import (
	"fmt"
	"github.com/go-playground/cache/lfu"
	"time"
)

func main() {
	// No guarding
	cache := lfu.New[string, string](100).MaxAge(time.Hour).Build()
	cache.Set("a", "b")
	cache.Set("c", "d")
	option := cache.Get("a")

	if option.IsNone() {
		return
	}
	fmt.Println("result:", option.Unwrap())

	stats := cache.Stats()
	// do things with stats
	fmt.Printf("%#v\n", stats)
}
