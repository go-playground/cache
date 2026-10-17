package main

import (
	"fmt"
	"time"

	"github.com/go-playground/cache/v2"
	"github.com/go-playground/cache/v2/s3fifo"
	"github.com/go-playground/cache/v2/tinylfu"
)

type Document struct {
	Body []byte
}

// Size reports the document's weight for cache.WeighBySize.
func (d Document) Size() int {
	return len(d.Body)
}

func main() {
	// Bound by total bytes rather than entry count using a built-in weigher.
	blobs := s3fifo.NewWeighted[string, []byte](64<<20, cache.WeighByLen).Build()
	blobs.Set("avatar.png", make([]byte, 4096))

	docs := tinylfu.NewWeighted[string, Document](256<<20, cache.WeighBySize).Build()
	docs.Set("readme", Document{Body: []byte("hello")})

	// Or any custom weigher, with per-entry expiry.
	custom := tinylfu.NewWeighted[string, Document](256<<20, func(key string, d Document) uint32 {
		return uint32(len(key) + len(d.Body))
	}).
		ExpireAfter(func(_ string, d Document) time.Duration {
			if len(d.Body) > 1<<20 {
				return time.Minute
			}
			return time.Hour
		}).
		Build()
	custom.Set("readme", Document{Body: []byte("hello")})

	fmt.Printf("blobs: %+v\ndocs: %+v\ncustom: %+v\n", blobs.Stats(), docs.Stats(), custom.Stats())
}
