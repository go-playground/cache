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
