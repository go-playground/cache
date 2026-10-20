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
| `New(capacity)` | required | Maximum entries. `0` retains nothing; above `cache.MaxEntries` (~1.07 billion) it is clamped to that. Reserves the index up front, ~17 bytes per entry of capacity (1.7MB for 100k entries), so it never resizes; capacities above 1M entries reserve 1M and grow towards their bound as they fill. |
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
exact moment they expire, so an expiry costs only 8 bytes per entry; until reclaimed they still count towards
capacity and `Stats().Len`. `OnEviction` reports `CauseExpired` when the sweep reclaims an entry.

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
| v2 lru | 26.3 | 20.6 | 11.3 | 5.6 | 3.9 |
| v1 lru | 25.2 | 81.4 | 160.4 | 201.3 | 270.6 |
| v2 lfu | 42.0 | 20.6 | 8.1 | 4.8 | 2.1 |
| v1 lfu | 64.0 | 106.1 | 191.6 | 246.4 | 329.6 |
| v2 tinylfu | 41.1 | 20.7 | 9.0 | 4.4 | 2.2 |
| v2 s3fifo | 18.2 | 8.7 | 4.5 | 3.1 | 2.7 |

**75% reads**

| cache | 1 core | 2 cores | 4 cores | 8 cores | 16 cores |
|---|---|---|---|---|---|
| v2 lru | 32.4 | 45.4 | 14.3 | 34.4 | 31.4 |
| v1 lru | 28.3 | 82.5 | 161.3 | 204.8 | 285.9 |
| v2 lfu | 45.4 | 43.7 | 15.2 | 36.2 | 34.1 |
| v1 lfu | 65.7 | 108.1 | 197.1 | 240.9 | 326.0 |
| v2 tinylfu | 48.5 | 43.1 | 18.0 | 30.8 | 33.6 |
| v2 s3fifo | 26.6 | 32.9 | 15.2 | 27.6 | 30.3 |

**50% reads**

| cache | 1 core | 2 cores | 4 cores | 8 cores | 16 cores |
|---|---|---|---|---|---|
| v2 lru | 39.8 | 66.8 | 27.1 | 39.5 | 51.5 |
| v1 lru | 33.5 | 83.9 | 168.7 | 211.3 | 299.9 |
| v2 lfu | 47.6 | 65.9 | 27.7 | 40.3 | 53.2 |
| v1 lfu | 57.0 | 104.0 | 192.4 | 243.4 | 335.0 |
| v2 tinylfu | 55.4 | 62.8 | 36.7 | 43.9 | 47.9 |
| v2 s3fifo | 31.1 | 59.0 | 27.6 | 38.6 | 46.4 |

**Writes only**

| cache | 1 core | 2 cores | 4 cores | 8 cores | 16 cores |
|---|---|---|---|---|---|
| v2 lru | 55.8 | 115.2 | 56.2 | 70.2 | 81.0 |
| v1 lru | 40.9 | 101.4 | 190.9 | 243.5 | 337.5 |
| v2 lfu | 55.2 | 114.5 | 59.1 | 78.2 | 82.5 |
| v1 lfu | 41.5 | 102.7 | 194.4 | 249.5 | 335.0 |
| v2 tinylfu | 72.5 | 105.3 | 80.5 | 88.1 | 88.9 |
| v2 s3fifo | 46.2 | 108.8 | 66.5 | 79.4 | 82.1 |

**One hot key, every goroutine reads it**

| cache | 1 core | 2 cores | 4 cores | 8 cores | 16 cores |
|---|---|---|---|---|---|
| v2 lru | 12.6 | 10.5 | 6.8 | 3.6 | 2.5 |
| v1 lru | 9.1 | 37.5 | 132.9 | 128.7 | 94.1 |
| v2 lfu | 24.3 | 15.3 | 8.7 | 3.7 | 4.6 |
| v1 lfu | 6.2 | 35.3 | 129.0 | 138.4 | 100.0 |
| v2 tinylfu | 23.2 | 11.3 | 7.0 | 2.3 | 1.2 |
| v2 s3fifo | 12.0 | 5.2 | 2.6 | 1.9 | 0.8 |

**One hot key, every goroutine sets then gets it** (v1's original benchmark)

| cache | 1 core | 2 cores | 4 cores | 8 cores | 16 cores |
|---|---|---|---|---|---|
| v2 lru | 28.5 | 181.1 | 85.5 | 150.6 | 233.4 |
| v1 lru | 18.3 | 74.0 | 259.9 | 243.1 | 200.5 |
| v2 lfu | 42.4 | 168.2 | 84.3 | 149.5 | 234.1 |
| v1 lfu | 15.1 | 74.0 | 257.7 | 273.8 | 208.0 |
| v2 tinylfu | 50.3 | 167.5 | 83.8 | 148.5 | 232.2 |
| v2 s3fifo | 27.5 | 170.4 | 99.8 | 165.7 | 264.5 |

- **2 or more cores:** v2 is faster than v1 for the same algorithm in every workload except write-only at 2 cores and
  many goroutines writing one key at 2 and 16 cores. At 16 cores it is up to 70–160x faster for reads and 4x for
  writes.
- **1 core:** the cache picks its single-mutex engine. On the Zipf workloads v2's LFU is faster than v1's except for
  writes only; v2's LRU is 4% slower than v1's for reads and 14–36% slower as the share of writes grows, the cost of
  v2's extra features (weights, expiry, statistics, loading) and pluggable policies. v1 is faster when every goroutine
  reads one hot key on 1 core.

### Memory

Bytes per entry for int→int caches, the same at every core count:

| cache | 10k entries | 100k entries | 1M entries | 100k after 3x churn |
|---|---|---|---|---|
| v2 lru / lfu | 85 | 84 | 84 | 87 |
| v2 s3fifo | 85 | 84 | 84 | 93 |
| v2 tinylfu | 91 | 89 | 88 | 90 |
| v1 lru / lfu | 78 | 72 | 86 | 95 |

An entry is its key and value plus 48 bytes, and v2 reserves its index up front, ~17 bytes per entry of capacity
(up to 1M entries), so it never resizes. Fixed cost of an empty cache with capacity 1,000, most of it that reserved
index:

| cores | empty cache | extra once used from every core at once |
|---|---|---|
| 1 | 19.5 KB | 0–0.6 KB |
| 2 | 25.2 KB | 5–14 KB |
| 4 | 25.7 KB | 16–20 KB |
| 8 | 26.7 KB | 38–45 KB |
| 16 | 28.8 KB | 81–101 KB |

On one core a cache uses a single mutex and no buffers. With more cores its read and write buffers start with one
stripe and grow towards one per core only while goroutines actually contend, about 4.6 KB per core when fully grown.

### Hit ratio (higher is better)

Cache-aside replay (miss → `Set`) on synthetic traces and the
[ARC paper's](https://www.usenix.org/conference/fast-03/arc-self-tuning-low-overhead-replacement-cache) storage traces.

| trace | capacity | v2 lru | v1 lru | v2 lfu | v1 lfu | v2 tinylfu | v2 s3fifo |
|---|---|---|---|---|---|---|---|
| zipf 0.8 | 10k | 28.49% | 28.49% | 37.25% | 37.25% | 36.83% | **38.07%** |
| zipf 1.0 | 10k | 62.35% | 62.35% | 68.44% | 68.44% | 67.83% | **68.70%** |
| zipf 1.2 | 10k | 88.43% | 88.45% | 90.35% | 90.36% | 90.08% | **90.39%** |
| zipf + scans | 10k | 31.30% | 31.30% | **38.38%** | **38.38%** | 37.63% | 38.09% |
| loop | 10k | 0.00% | 0.00% | 0.00% | 0.00% | **77.72%** | 61.97% |
| shifting popularity | 10k | **78.63%** | **78.63%** | 44.45% | 44.45% | 73.13% | 76.33% |
| ARC OLTP | 1k | 32.83% | 32.83% | 13.83% | 13.83% | 40.16% | **40.78%** |
| ARC P8 | 400k | 95.65% | 95.68% | 91.19% | 91.21% | 95.48% | **96.23%** |
| ARC S3 | 400k | 12.04% | 12.04% | 29.35% | 29.36% | **38.83%** | 33.59% |
| ARC DS1 | 4M | 20.25% | 20.24% | 38.25% | 39.07% | **43.26%** | 29.40% |

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
