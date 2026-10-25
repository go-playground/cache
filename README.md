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
| v2 lru | 26.5 | 20.5 | 10.1 | 4.0 | 3.8 |
| v1 lru | 25.1 | 79.2 | 161.2 | 212.4 | 283.8 |
| v2 lfu | 44.6 | 22.0 | 9.4 | 3.9 | 3.6 |
| v1 lfu | 74.6 | 117.8 | 200.2 | 261.6 | 344.6 |
| v2 tinylfu | 44.3 | 21.6 | 10.5 | 4.1 | 2.3 |
| v2 s3fifo | 20.7 | 9.6 | 5.1 | 2.6 | 1.5 |

**75% reads**

| cache | 1 core | 2 cores | 4 cores | 8 cores | 16 cores |
|---|---|---|---|---|---|
| v2 lru | 34.8 | 47.4 | 21.2 | 34.3 | 31.6 |
| v1 lru | 30.8 | 90.5 | 174.4 | 223.7 | 308.1 |
| v2 lfu | 49.9 | 46.3 | 19.8 | 37.9 | 35.0 |
| v1 lfu | 68.5 | 121.1 | 205.4 | 271.0 | 347.0 |
| v2 tinylfu | 53.5 | 47.0 | 22.9 | 35.1 | 34.3 |
| v2 s3fifo | 30.0 | 36.1 | 20.3 | 28.4 | 30.4 |

**50% reads**

| cache | 1 core | 2 cores | 4 cores | 8 cores | 16 cores |
|---|---|---|---|---|---|
| v2 lru | 41.0 | 71.0 | 35.6 | 45.6 | 53.1 |
| v1 lru | 34.0 | 93.7 | 179.8 | 232.8 | 322.5 |
| v2 lfu | 51.5 | 69.2 | 33.5 | 45.6 | 54.5 |
| v1 lfu | 60.8 | 117.8 | 206.9 | 263.6 | 344.7 |
| v2 tinylfu | 60.2 | 66.9 | 42.4 | 52.3 | 55.9 |
| v2 s3fifo | 35.5 | 60.4 | 31.9 | 45.0 | 49.9 |

**Writes only**

| cache | 1 core | 2 cores | 4 cores | 8 cores | 16 cores |
|---|---|---|---|---|---|
| v2 lru | 54.8 | 119.3 | 57.4 | 77.2 | 80.1 |
| v1 lru | 41.6 | 102.7 | 187.4 | 243.2 | 329.3 |
| v2 lfu | 56.3 | 114.6 | 55.0 | 81.2 | 81.2 |
| v1 lfu | 42.3 | 104.7 | 192.0 | 243.1 | 339.0 |
| v2 tinylfu | 74.5 | 109.6 | 83.1 | 85.4 | 90.1 |
| v2 s3fifo | 45.5 | 110.7 | 65.6 | 80.3 | 80.7 |

**One hot key, every goroutine reads it**

| cache | 1 core | 2 cores | 4 cores | 8 cores | 16 cores |
|---|---|---|---|---|---|
| v2 lru | 13.6 | 10.9 | 6.5 | 2.8 | 1.5 |
| v1 lru | 8.8 | 35.6 | 132.5 | 128.9 | 101.5 |
| v2 lfu | 26.2 | 15.6 | 9.1 | 3.7 | 3.0 |
| v1 lfu | 6.2 | 33.9 | 127.4 | 142.3 | 102.4 |
| v2 tinylfu | 23.9 | 11.9 | 6.0 | 3.2 | 1.3 |
| v2 s3fifo | 13.1 | 5.6 | 2.8 | 1.4 | 0.9 |

**One hot key, every goroutine sets then gets it** (v1's original benchmark)

| cache | 1 core | 2 cores | 4 cores | 8 cores | 16 cores |
|---|---|---|---|---|---|
| v2 lru | 31.2 | 178.1 | 84.9 | 154.7 | 234.3 |
| v1 lru | 17.7 | 73.2 | 263.0 | 240.0 | 197.7 |
| v2 lfu | 43.7 | 168.1 | 84.0 | 154.1 | 233.0 |
| v1 lfu | 15.0 | 73.7 | 255.9 | 272.7 | 207.6 |
| v2 tinylfu | 51.8 | 168.0 | 85.1 | 148.3 | 232.2 |
| v2 s3fifo | 29.7 | 170.4 | 99.5 | 165.3 | 264.8 |

- **2 or more cores:** v2 is faster than v1 for the same algorithm in every workload except write-only at 2 cores and
  many goroutines writing one key at 2 and 16 cores. At 16 cores it is up to 75–95x faster for reads and 4x for
  writes.
- **1 core:** the cache picks its single-mutex engine. On the Zipf workloads v2's LFU is faster than v1's except for
  writes only; v2's LRU is 6% slower than v1's for reads and 13–32% slower as the share of writes grows, the cost of
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
| 4 | 25.7 KB | 15–20 KB |
| 8 | 26.7 KB | 36–49 KB |
| 16 | 28.8 KB | 78–116 KB |

On one core a cache uses a single mutex and no buffers. With more cores its read and write buffers start with one
stripe and grow towards one per core only while goroutines actually contend, about 4.6 KB per core when fully grown.

### Hit ratio (higher is better)

Cache-aside replay (miss → `Set`) on synthetic traces and the
[ARC paper's](https://www.usenix.org/conference/fast-03/arc-self-tuning-low-overhead-replacement-cache) storage traces.

| trace | capacity | v2 lru | v1 lru | v2 lfu | v1 lfu | v2 tinylfu | v2 s3fifo |
|---|---|---|---|---|---|---|---|
| zipf 0.8 | 10k | 28.49% | 28.49% | 37.25% | 37.25% | 36.72% | **38.09%** |
| zipf 1.0 | 10k | 62.35% | 62.35% | 68.44% | 68.44% | 68.35% | **68.69%** |
| zipf 1.2 | 10k | 88.43% | 88.45% | 90.35% | 90.36% | 89.95% | **90.39%** |
| zipf + scans | 10k | 31.30% | 31.30% | **38.38%** | **38.38%** | 37.07% | 38.09% |
| loop | 10k | 0.00% | 0.00% | 0.00% | 0.00% | **76.89%** | 61.78% |
| shifting popularity | 10k | **78.63%** | **78.63%** | 44.45% | 44.45% | 73.22% | 76.32% |
| ARC OLTP | 1k | 32.83% | 32.83% | 13.83% | 13.83% | 40.18% | **40.80%** |
| ARC P8 | 400k | 95.65% | 95.68% | 91.19% | 91.21% | 95.50% | **96.23%** |
| ARC S3 | 400k | 12.04% | 12.04% | 29.35% | 29.36% | **38.84%** | 33.58% |
| ARC DS1 | 4M | 20.25% | 20.24% | 38.25% | 39.07% | **43.24%** | 29.40% |

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
