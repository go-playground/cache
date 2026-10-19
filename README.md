# cache
![Project status](https://img.shields.io/badge/version-2.0.0-green.svg)
[![GoDoc](https://godoc.org/github.com/go-playground/cache/v2?status.svg)](https://pkg.go.dev/github.com/go-playground/cache/v2)
![License](https://img.shields.io/dub/l/vibe-d.svg)

Fast, concurrent, bounded in-memory caches for Go with pluggable eviction policies.

- **Fast at any core count, automatically.** On a single core a cache uses one mutex and updates entries in place;
  with more cores reads are lock-free and eviction bookkeeping is batched so cores rarely contend. The engine is
  chosen when the cache is built, from the processors available (`GOMAXPROCS`), with nothing to configure. There are
  no background goroutines and no `Close`.
- **Four policies, one type.** Every builder produces the same `*cache.Cache[K, V]`, so switching policy is a
  one-line change.
- **Weighted capacity, expiry, eviction callbacks, single-flight loading and statistics** built in.
- **One dependency:** [go-playground/pkg](https://github.com/go-playground/pkg).

#### Requirements
- Go 1.27+

## Install

```shell
go get github.com/go-playground/cache/v2
```

## Usage

```go
users := tinylfu.New[string, User](10_000).Build()

users.Set("1", User{Name: "Ada"})

if u := users.Get("1"); u.IsSome() {
	fmt.Println(u.Unwrap().Name)
}
users.Remove("1")
```

Everything except capacity is optional:

```go
users := tinylfu.New[string, User](10_000).
	ExpireAfterWrite(time.Hour).
	OnEviction(func(id string, u User, cause cache.Cause) { log.Printf("evicted %s: %s", id, cause) }).
	RecordStats().
	Build()
```

Load on miss, with concurrent misses for the same key sharing a single load:

```go
res := users.GetOrLoad(ctx, "1", func(ctx context.Context, id string) resultext.Result[User, error] {
	return db.LoadUser(ctx, id)
})
```

Bound by total weight instead of entry count with a built-in or custom weigher:

```go
blobs := s3fifo.NewWeighted[string, []byte](64<<20, cache.WeighByLen).Build()  // len(value)
docs := tinylfu.NewWeighted[string, Doc](256<<20, cache.WeighBySize).Build()    // value.Size()
custom := lru.NewWeighted[string, Doc](256<<20, func(k string, d Doc) uint32 { return uint32(len(k) + d.Size()) }).Build()
```

More in [_examples](_examples).

## Choosing a policy

| Package | Policy | Use when |
|---|---|---|
| [tinylfu](tinylfu) | W-TinyLFU: an LRU admission window in front of a segmented LRU, with admission decided by an aging frequency sketch and a self-tuning window size | **Default.** Best hit ratio across most workloads; scan resistant. |
| [s3fifo](s3fifo) | S3-FIFO ([SOSP '23](https://dl.acm.org/doi/10.1145/3600006.3613147)): small, main and ghost FIFO queues | Read-heavy, highly concurrent workloads. Reads only bump a saturating counter, so hot keys cause no shared writes. Scan resistant. |
| [lru](lru) | Least recently used | Strongly recency-biased workloads. |
| [lfu](lfu) | Least frequently used, with optional `Aging` | Stable popularity distributions. |

All four support every option. Under concurrency LRU/LFU ordering is approximate: reads are recorded through a lossy
buffer, so a small fraction may not update recency or frequency.

## Options

| Option | Default | Notes |
|---|---|---|
| `New(capacity)` | required | Maximum entries. `0` retains nothing; above `cache.MaxEntries` (~1.07 billion) it is clamped to that. Reserves the index up front, ~17 bytes per entry of capacity (1.7MB for 100k entries), so it never resizes. |
| `NewWeighted(capacity, weigher)` | — | Maximum total weight. `nil` weigher weighs every entry 1. Entries heavier than capacity are never retained. The entry count is unknown up front, so the index grows as entries are stored. |
| `ExpireAfterWrite(d)` | no expiry | `d <= 0` disables. |
| `ExpireAfterAccess(d)` | no expiry | Reads extend the expiry (with 1ms granularity). `d <= 0` disables. |
| `ExpireAfter(fn)` | no expiry | Per entry, computed on write; a result `<= 0` never expires. `nil` disables. |
| `OnEviction(fn)` | none | Called for capacity and expiry evictions, not `Remove`/`Clear`. Runs outside locks, possibly concurrently. |
| `RecordStats()` | off | Enables hit, miss, set and load counters. |
| `lfu.Aging(every)` | off | Makes old popularity fade: halves every entry's read count after each `every` reads, so entries popular long ago can be evicted in favour of ones popular now. |
| `tinylfu.Adaptive(bool)` | `true` | Hill-climbing window sizing. |

The expiry options replace one another; the last one set wins. Expired entries are never returned. Their memory is
reclaimed by an incremental sweep that runs alongside normal operations, or by regular eviction, rather than at the
exact moment they expire, so an expiry costs only 8 bytes per entry and the cache never holds more than its capacity.
`OnEviction` reports `CauseExpired` when the sweep reclaims an entry.

## Benchmarks

Measured on an Apple M3 Max (16 cores), Go 1.27.2. Reproduce with `make bench`, `make hitratio` and
`MEMORY=1 go test -run=TestMemory -v` in [benchmarks](benchmarks), a separate module so the library stays
dependency-free. Medians of 3 runs.

### Throughput (ns/op, lower is better)

Zipf(1.0) keys over 1M keys against a 100k-entry cache. In a parallel benchmark ns/op is total time divided by every
goroutine's operations, so a smaller number with more cores means more total throughput. v1's numbers grow with cores
because its single mutex serialises every operation.

**100% reads**

| cache | 1 core | 2 cores | 4 cores | 8 cores | 16 cores |
|---|---|---|---|---|---|
| v2 lru | 25.6 | 19.3 | 11.2 | 6.0 | 2.2 |
| v1 lru | 24.1 | 79.3 | 167.7 | 219.4 | 286.9 |
| v2 lfu | 45.1 | 22.1 | 10.2 | 4.1 | 2.2 |
| v1 lfu | 73.4 | 113.5 | 204.7 | 269.1 | 350.9 |
| v2 tinylfu | 44.7 | 22.7 | 11.4 | 6.0 | 2.3 |
| v2 s3fifo | 21.1 | 10.4 | 5.5 | 2.7 | 1.6 |

**75% reads**

| cache | 1 core | 2 cores | 4 cores | 8 cores | 16 cores |
|---|---|---|---|---|---|
| v2 lru | 35.8 | 48.4 | 21.0 | 31.1 | 28.9 |
| v1 lru | 30.2 | 92.5 | 168.4 | 211.3 | 297.4 |
| v2 lfu | 50.7 | 46.1 | 19.7 | 34.1 | 31.9 |
| v1 lfu | 63.9 | 118.2 | 204.3 | 266.9 | 344.5 |
| v2 tinylfu | 52.9 | 46.7 | 24.2 | 35.5 | 34.3 |
| v2 s3fifo | 29.8 | 34.8 | 21.7 | 28.9 | 29.1 |

**50% reads**

| cache | 1 core | 2 cores | 4 cores | 8 cores | 16 cores |
|---|---|---|---|---|---|
| v2 lru | 41.8 | 70.4 | 34.8 | 48.2 | 52.8 |
| v1 lru | 33.1 | 97.0 | 184.6 | 230.3 | 316.8 |
| v2 lfu | 51.4 | 68.8 | 36.4 | 45.6 | 52.7 |
| v1 lfu | 57.1 | 126.1 | 205.5 | 269.5 | 354.3 |
| v2 tinylfu | 60.0 | 67.5 | 38.1 | 47.3 | 55.1 |
| v2 s3fifo | 34.9 | 58.6 | 35.7 | 41.9 | 46.9 |

**Writes only**

| cache | 1 core | 2 cores | 4 cores | 8 cores | 16 cores |
|---|---|---|---|---|---|
| v2 lru | 52.0 | 111.4 | 63.7 | 68.6 | 72.7 |
| v1 lru | 37.0 | 105.7 | 193.6 | 245.6 | 345.0 |
| v2 lfu | 53.5 | 113.7 | 57.0 | 70.5 | 74.0 |
| v1 lfu | 38.9 | 105.2 | 196.9 | 250.7 | 338.5 |
| v2 tinylfu | 73.5 | 108.8 | 82.2 | 86.7 | 85.1 |
| v2 s3fifo | 44.0 | 107.5 | 63.5 | 73.6 | 74.3 |

**One hot key, every goroutine reads it**

| cache | 1 core | 2 cores | 4 cores | 8 cores | 16 cores |
|---|---|---|---|---|---|
| v2 lru | 12.6 | 10.6 | 5.9 | 2.9 | 1.4 |
| v1 lru | 8.8 | 37.0 | 132.3 | 131.6 | 109.3 |
| v2 lfu | 25.1 | 15.4 | 8.7 | 6.9 | 4.7 |
| v1 lfu | 6.2 | 34.4 | 126.6 | 146.8 | 112.7 |
| v2 tinylfu | 23.0 | 11.3 | 5.6 | 2.5 | 1.2 |
| v2 s3fifo | 12.1 | 5.0 | 2.6 | 1.3 | 0.8 |

**One hot key, every goroutine sets then gets it** (v1's original benchmark)

| cache | 1 core | 2 cores | 4 cores | 8 cores | 16 cores |
|---|---|---|---|---|---|
| v2 lru | 29.5 | 181.3 | 81.9 | 151.9 | 232.3 |
| v1 lru | 17.7 | 73.8 | 269.9 | 269.0 | 224.5 |
| v2 lfu | 42.8 | 162.6 | 82.1 | 148.9 | 232.3 |
| v1 lfu | 15.0 | 73.1 | 267.0 | 266.7 | 225.9 |
| v2 tinylfu | 50.8 | 165.5 | 83.4 | 149.7 | 232.1 |
| v2 s3fifo | 27.9 | 161.6 | 97.9 | 163.6 | 264.3 |

- **2 or more cores:** v2 is faster than v1 for the same algorithm in every workload except write-only at 2 cores and
  many goroutines writing one key at 2 and 16 cores. At 16 cores it is up to 130–160x faster for reads and 4–5x for
  writes.
- **1 core:** the cache picks its single-mutex engine. On the Zipf workloads v2's LFU is faster than v1's except for
  writes only; v2's LRU is 6% slower than v1's for reads and 19–41% slower as the share of writes grows, the cost of
  v2's extra features (weights, expiry, statistics, loading) and pluggable policies. v1 is faster when every goroutine
  reads one hot key on 1 core.

### Memory

Bytes per entry for int→int caches, the same at every core count:

| cache | 10k entries | 100k entries | 1M entries | 100k after 3x churn |
|---|---|---|---|---|
| v2 lru / lfu | 85 | 84 | 84 | 93 |
| v2 s3fifo | 84 | 84 | 84 | 98 |
| v2 tinylfu | 91 | 89 | 88 | 95 |
| v1 lru / lfu | 78 | 72 | 86 | 95 |

An entry is its key and value plus 48 bytes, and v2 reserves its index up front, ~17 bytes per entry of capacity, so
it never resizes. Fixed cost of an empty cache with capacity 1,000, most of it that reserved index:

| cores | empty cache | extra once used from every core at once |
|---|---|---|
| 1 | 19.5 KB | 0.6 KB |
| 2 | 25.2 KB | 5–13 KB |
| 4 | 25.7 KB | 15–24 KB |
| 8 | 26.7 KB | 36–53 KB |
| 16 | 28.8 KB | 79–115 KB |

On one core a cache uses a single mutex and no buffers. With more cores its read and write buffers start with one
stripe and grow towards one per core only while goroutines actually contend, about 4.6 KB per core when fully grown.

### Hit ratio (higher is better)

Cache-aside replay (miss → `Set`) on synthetic traces and the
[ARC paper's](https://www.usenix.org/conference/fast-03/arc-self-tuning-low-overhead-replacement-cache) storage traces.

| trace | capacity | v2 lru | v1 lru | v2 lfu | v1 lfu | v2 tinylfu | v2 s3fifo |
|---|---|---|---|---|---|---|---|
| zipf 0.8 | 10k | 28.49% | 28.49% | 37.25% | 37.25% | 37.45% | **38.11%** |
| zipf 1.0 | 10k | 62.35% | 62.35% | 68.44% | 68.44% | 67.39% | **68.70%** |
| zipf 1.2 | 10k | 88.43% | 88.45% | 90.35% | 90.36% | 89.85% | **90.40%** |
| zipf + scans | 10k | 31.30% | 31.30% | **38.38%** | **38.38%** | 37.66% | 38.06% |
| loop | 10k | 0.00% | 0.00% | 0.00% | 0.00% | **75.76%** | 61.87% |
| shifting popularity | 10k | **78.63%** | **78.63%** | 44.45% | 44.45% | 72.75% | 76.29% |
| ARC OLTP | 1k | 32.83% | 32.83% | 13.83% | 13.83% | 39.94% | **40.78%** |
| ARC P8 | 400k | 95.65% | 95.68% | 91.19% | 91.21% | 95.41% | **96.23%** |
| ARC S3 | 400k | 12.04% | 12.04% | 29.35% | 29.36% | **39.58%** | 33.58% |
| ARC DS1 | 4M | 20.25% | 20.24% | 38.25% | 39.07% | **44.58%** | 29.40% |

v2's LRU and LFU match v1's hit ratios. W-TinyLFU is the best or within a few points of the best on every trace, which
is why it is the recommended default; S3-FIFO leads on most of the rest.

## Migrating from v1

| v1 | v2 |
|---|---|
| `lru.New[K, V](n).Build()` (not thread-safe) | `lru.New[K, V](n).Build()` (always thread-safe) |
| `lru.New[K, V](n).BuildThreadSafe()` | `lru.New[K, V](n).Build()` |
| `.MaxAge(d)` | `.ExpireAfterWrite(d)` |
| `LockGuard()` for multi-operation atomicity | Removed: incompatible with lock-free reads. Use `GetOrLoad` for load-on-miss. |
| `Stats()` reset counters on each call | `Stats()` is cumulative; subtract snapshots for deltas. Enable counters with `RecordStats()`. |
| `Capacity int` | `capacity uint64` |

## License

<sup>
Licensed under either of <a href="LICENSE-APACHE">Apache License, Version
2.0</a> or <a href="LICENSE-MIT">MIT license</a> at your option.
</sup>

<br>

<sub>
Unless you explicitly state otherwise, any contribution intentionally submitted
for inclusion in this crate by you, as defined in the Apache-2.0 license, shall be
dual licensed as above, without any additional terms or conditions.
</sub>
