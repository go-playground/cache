GOFLAGS ?=
FUZZTIME ?= 30s
ARC_TRACES ?=

test:
	go test -count=1 -cover -race ./...
	CACHE_TEST_ENGINE=direct go test -count=1 -race ./...
	CACHE_TEST_ENGINE=concurrent go test -count=1 -race ./...

lint:
	golangci-lint run ./...

fuzz:
	go test -run=NONE -fuzz=FuzzTableAgainstReference -fuzztime=$(FUZZTIME) ./internal/table/
	go test -run=NONE -fuzz=FuzzLRUAgainstModel -fuzztime=$(FUZZTIME) ./internal/policy/lru/
	go test -run=NONE -fuzz=FuzzLFUAgainstModel -fuzztime=$(FUZZTIME) ./internal/policy/lfu/
	go test -run=NONE -fuzz=FuzzS3FIFOAgainstModel -fuzztime=$(FUZZTIME) ./internal/policy/s3fifo/
	go test -run=NONE -fuzz=FuzzTinyLFUAgainstModel -fuzztime=$(FUZZTIME) ./internal/policy/tinylfu/

bench:
	cd benchmarks && go test -run=NONE -bench=. -benchmem -cpu=1,2,4,8,16 -count=3 -timeout=120m . | tee bench.txt
	cd benchmarks && REPORT=bench.txt go test -run=TestReport -v .

hitratio:
	cd benchmarks && HITRATIO=1 ARC_TRACES=$(ARC_TRACES) go test -run=TestHitRatio -v -timeout=60m .

.PHONY: test lint fuzz bench hitratio
