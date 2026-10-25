package main

import (
	"context"
	"fmt"
	"sync"

	resultext "github.com/go-playground/pkg/v5/values/result"

	"github.com/go-playground/cache/v2/tinylfu"
)

func main() {
	c := tinylfu.New[string, string](10_000).RecordStats().Build()

	fetch := func(_ context.Context, key string) resultext.Result[string, error] {
		// Expensive lookup, e.g. a database query. Concurrent misses for the same key share one call.
		return resultext.Ok[string, error]("value for " + key)
	}

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			res := c.GetOrLoad(context.Background(), "key", fetch)
			if res.IsErr() {
				fmt.Println("load failed:", res.Err())
				return
			}
			_ = res.Unwrap()
		})
	}
	wg.Wait()

	s := c.Stats()
	fmt.Printf("loads: %d, hits: %d, misses: %d\n", s.Loads, s.Hits, s.Misses)
}
