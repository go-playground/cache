module github.com/go-playground/cache/v2/benchmarks

go 1.27

replace github.com/go-playground/cache/v2 => ../

require (
	github.com/dgraph-io/ristretto/v2 v2.4.2
	github.com/go-playground/cache v1.1.0
	github.com/go-playground/cache/v2 v2.0.0-00010101000000-000000000000
	github.com/hashicorp/golang-lru/v2 v2.0.7
	github.com/maypok86/otter/v2 v2.3.0
	github.com/scalalang2/golang-fifo v1.2.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-playground/pkg/v5 v5.32.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	golang.org/x/sys v0.36.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
