package main

import (
	"fmt"

	"github.com/go-playground/cache/v2/lfu"
)

func main() {
	// LFU evicts the entry read the fewest times. Without aging those counts only ever grow, so an entry that was
	// read a million times last week outranks one read a thousand times today and can never be evicted. Aging(10_000)
	// halves every entry's read count after each 10,000 reads, so old popularity fades and entries that are popular
	// now win.
	c := lfu.New[string, int](1_000).
		Aging(10_000).
		Build()

	c.Set("popular", 1)
	for range 100 {
		c.Get("popular")
	}
	fmt.Println(c.Get("popular").Unwrap())
}
