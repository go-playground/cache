# LFU

This is a Least Frequently Used cache backed by a generic doubly linked list with O(1) time complexity.

# When to use
You would typically use an LFU cache when:

- Capacity of cache is far lower than data available.
- Entries being used are high frequency compared to others over time.

Both above will prevent the most frequently use data from flapping in and out of the cache.

## Usage
```go
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

```