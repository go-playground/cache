# LRU

This is a Least Recently Used cache backed by a generic doubly linked list with O(1) time complexity.

# When to use
You would typically use an LRU cache when:

- Capacity of cache will hold nearly all data.
- Entries being used are being used on a consistent frequency.

Both above will prevent large amounts of data flapping in and out of the cache.
If your cache can only hold a fraction of values being stored or data seen on a cadence but high frequency, check out using the LFU cache instead.

## Usage
```go
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
```