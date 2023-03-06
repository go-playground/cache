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
	"fmt"
	"github.com/go-playground/cache/lru"
	syncext "github.com/go-playground/pkg/v5/sync"
	"time"
)

func main() {
	// wrapping with a Mutex, if not needed omit.
	cache := syncext.NewMutex2(lru.New[string, string](100).MaxAge(time.Hour).Build())

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