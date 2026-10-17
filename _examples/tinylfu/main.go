package main

import (
	"fmt"
	"time"

	"github.com/go-playground/cache/v2"
	"github.com/go-playground/cache/v2/tinylfu"
)

type User struct {
	ID   string
	Name string
}

func main() {
	// W-TinyLFU is the recommended default: best hit ratio across most workloads.
	users := tinylfu.New[string, User](10_000).
		ExpireAfterWrite(time.Hour).
		OnEviction(func(id string, _ User, cause cache.Cause) {
			fmt.Printf("evicted %s: %s\n", id, cause)
		}).
		RecordStats().
		Build()

	users.Set("1", User{ID: "1", Name: "Ada"})

	if u := users.Get("1"); u.IsSome() {
		fmt.Println("found:", u.Unwrap().Name)
	}

	users.Remove("1")
	fmt.Printf("%+v\n", users.Stats())
}
