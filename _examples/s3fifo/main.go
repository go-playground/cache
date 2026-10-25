package main

import (
	"fmt"
	"time"

	"github.com/go-playground/cache/v2/s3fifo"
)

func main() {
	// S3-FIFO has the fastest, most scalable reads and resists one-hit-wonder pollution.
	sessions := s3fifo.New[string, []byte](100_000).
		ExpireAfterAccess(30 * time.Minute).
		Build()

	sessions.Set("session-id", []byte(`{"user":"1"}`))

	if s := sessions.Get("session-id"); s.IsSome() {
		fmt.Println(string(s.Unwrap()))
	}
}
