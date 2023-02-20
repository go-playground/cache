package main

import (
	"fmt"
	"github.com/go-playground/cache/lfu"
	"time"
)

func main() {
	cache := lfu.New[string, string](100).MaxAge(time.Hour).HitFn(func(key string, value string) {
		fmt.Printf("Hit Key: %s Value %s\n", key, value)
	}).Build()
	cache.Set("a", "b")
	cache.Set("c", "d")

	option := cache.Get("a")
	if option.IsNone() {
		return
	}
	fmt.Println("result:", option.Unwrap())
}
