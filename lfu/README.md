# LFU

This is a Least Frequently Used cache backed by a generic doubly linked list with O(1) time complexity.

# When to use
You would typically use an LFU cache when:

- Capacity of cache is far lower than data available.
- Entries being used are high frequency compared to others over time.
Both above will prevent the most frequently use data from flapping in and out of the cache.

## Usage

#### No Locking
```go
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
```

#### Auto Locking
```go
package main

import (
	"context"
	"fmt"
	"github.com/go-playground/cache/lfu"
	"time"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Guarding with a Mutex with one operation per interaction semantics.
	cache := lfu.New[string, string](100).MaxAge(time.Hour).BuildThreadSafe()

	// example of collecting/emitting stats for cache
	// this does require a mutex guard to collect async
	go func(ctx context.Context) {

		var ticker = time.NewTicker(time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stats := cache.Stats()

				// do things with stats
				fmt.Printf("%#v\n", stats)
			}
		}
	}(ctx)

	cache.Set("a", "b")
	cache.Set("c", "d")
	option := cache.Get("a")

	if option.IsNone() {
		return
	}
	fmt.Println("result:", option.Unwrap())

	// Have the ability to perform multiple operations at once by grabbing the LockGuard.
	guard := cache.LockGuard()
	guard.T.Set("c", "c")
	guard.T.Set("d", "d")
	guard.T.Remove("a")
	guard.Unlock()
}
```

#### Custom Locking
```go
package main

import (
	"context"
	"fmt"
	"github.com/go-playground/cache/lfu"
	syncext "github.com/go-playground/pkg/v5/sync"
	"time"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// wrapping with a Mutex, if not needed omit.
	cache := syncext.NewMutex2(lfu.New[string, string](100).MaxAge(time.Hour).Build())

	// example of collecting/emitting stats for cache
	// this does require a mutex guard to collect async
	go func(ctx context.Context) {

		var ticker = time.NewTicker(time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				guard := cache.Lock()
				stats := guard.T.Stats()
				guard.Unlock()

				// do things with stats
				fmt.Printf("%#v\n", stats)
			}
		}
	}(ctx)

	guard := cache.Lock()
	guard.T.Set("a", "b")
	guard.T.Set("c", "d")
	option := guard.T.Get("a")
	guard.Unlock()

	if option.IsNone() {
		return
	}
	fmt.Println("result:", option.Unwrap())
}
```