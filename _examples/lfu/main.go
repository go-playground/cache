package main

import (
	"fmt"

	"github.com/go-playground/cache/v2/lfu"
)

func main() {
	// Aging halves frequencies every 10k reads so formerly popular entries can cool down.
	c := lfu.New[string, int](1_000).
		Aging(10_000).
		Build()

	c.Set("popular", 1)
	for range 100 {
		c.Get("popular")
	}
	fmt.Println(c.Get("popular").Unwrap())
}
