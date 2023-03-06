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
	syncext "github.com/go-playground/pkg/v5/sync"
	"time"
)

func main() {
	// wrapping with a Mutex, if not needed omit.
	cache := syncext.NewMutex2(lfu.New[string, string](100).MaxAge(time.Hour).Build())

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
```