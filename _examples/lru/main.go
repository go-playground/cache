package main

import (
	"fmt"

	"github.com/go-playground/cache/v2/lru"
)

func main() {
	c := lru.New[int, string](2).Build()

	c.Set(1, "one")
	c.Set(2, "two")
	c.Get(1)          // 1 is now the most recently used
	c.Set(3, "three") // evicts 2, the least recently used

	fmt.Println("has 1:", c.Get(1).IsSome())
	fmt.Println("has 3:", c.Get(3).IsSome())
}
