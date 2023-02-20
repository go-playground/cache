test:
	go test -cover -race ./...

bench:
	go test -run=NONE -bench=. -benchmem  ./...

lint:
	golangci-lint run

.PHONY: lint test bench