# cache
![Project status](https://img.shields.io/badge/version-2.0.0-green.svg)
[![GoDoc](https://godoc.org/github.com/go-playground/cache/v2?status.svg)](https://pkg.go.dev/github.com/go-playground/cache/v2)
![License](https://img.shields.io/dub/l/vibe-d.svg)

Fast, concurrent, bounded in-memory caches for Go with pluggable eviction policies.

- **Lock-free reads.** Lookups never take a lock. Eviction bookkeeping is batched and applied by whichever caller
  wins a `TryLock`, so there are no background goroutines and no `Close`.
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
| `New(capacity)` | required | Maximum entries. `0` retains nothing. |
| `NewWeighted(capacity, weigher)` | — | Maximum total weight. `nil` weigher weighs every entry 1. Entries heavier than capacity are never retained. |
| `ExpireAfterWrite(d)` | no expiry | `d <= 0` disables. |
| `ExpireAfterAccess(d)` | no expiry | Reads extend the expiry (with 1ms granularity). `d <= 0` disables. |
| `ExpireAfter(fn)` | no expiry | Per entry, computed on write; a result `<= 0` never expires. `nil` disables. |
| `OnEviction(fn)` | none | Called for capacity and expiry evictions, not `Remove`/`Clear`. Runs outside locks, possibly concurrently. |
| `RecordStats()` | off | Enables hit, miss, set and load counters. |
| `lfu.Aging(every)` | off | Halves all frequencies every `every` reads. |
| `tinylfu.Adaptive(bool)` | `true` | Hill-climbing window sizing. |

The expiry options replace one another; the last one set wins. Expired entries are never returned, and are reclaimed
in the background of normal operations.

## Benchmarks

Measured on an Apple M3 Max (16 cores), Go 1.27.2. Reproduce with `make bench` and `make hitratio` (see
[benchmarks](benchmarks), a separate module so the library stays dependency-free). Medians of 3 runs, ns/op, lower is
better.

### Throughput

Zipf(1.0) keys over 1M keys against a 100k-entry cache. `sync.Map` is an unbounded baseline that never evicts.

| reads | cache | 1 CPU | 8 CPU | 16 CPU |
|---|---|---|---|---|
| 100% | v2 s3fifo | 18.2 | **2.5** | **1.5** |
| | v2 tinylfu | 53.4 | 5.1 | 3.1 |
| | v2 lru | 37.0 | 5.2 | 3.1 |
| | otter | 39.9 | 5.1 | 4.6 |
| | ristretto | 27.4 | 11.2 | 11.8 |
| | v1 lru (mutex) | **25.1** | 208.7 | 287.6 |
| | hashicorp lru | 27.4 | 226.1 | 305.6 |
| 75% | v2 tinylfu | 97.9 | 36.2 | 35.1 |
| | v2 lru | 83.2 | **31.8** | **29.2** |
| | otter | 84.4 | 75.5 | 113.6 |
| | ristretto¹ | 51.2 | 39.0 | 40.4 |
| | v1 lru (mutex) | **30.7** | 230.8 | 300.8 |
| 50% | v2 tinylfu | 137.9 | 55.6 | 61.1 |
| | v2 lru | 117.9 | **49.1** | **55.1** |
| | otter | 127.0 | 127.8 | 224.1 |
| | ristretto¹ | 60.4 | 65.1 | 68.5 |
| | v1 lru (mutex) | **34.0** | 232.8 | 313.8 |
| 0% | v2 tinylfu | 207.5 | 101.4 | 101.4 |
| | v2 lru | 179.8 | 82.9 | 92.4 |
| | otter | 209.1 | 251.9 | 413.9 |
| | ristretto¹ | 68.5 | 80.2 | 88.0 |
| | v1 lru (mutex) | **39.3** | 246.0 | 338.6 |

¹ Ristretto applies writes asynchronously and drops them under load, so its write throughput is not comparable.

Takeaways:

- **Concurrent reads scale near-linearly** and are the fastest measured; S3-FIFO reads cost a single atomic load.
- **Mixed and write-heavy workloads scale** where mutex-based caches (v1, hashicorp) collapse past one core.
- **Single-threaded, a plain mutex-guarded LRU is faster** (v1: ~30ns vs ~80–200ns for mixed workloads). v2 pays for
  concurrency on every write: entries are immutable so reads never race, so each `Set` allocates a new entry, and
  eviction bookkeeping is buffered. If a cache is only ever used from one goroutine, v1 remains the faster choice.
- **Many goroutines writing the same key** (`BenchmarkHotKey`) is no faster than v1: ~230ns/op at 16 CPUs vs ~215–220ns.

### Hit ratio

Cache-aside replay (miss → `Set`) on synthetic traces and the [ARC paper's](https://www.usenix.org/conference/fast-03/arc-self-tuning-low-overhead-replacement-cache)
storage traces. Higher is better.

| trace | capacity | v2 tinylfu | v2 s3fifo | v2 lru | v2 lfu | v1 lfu | otter | ristretto² |
|---|---|---|---|---|---|---|---|---|
| zipf 0.8 | 10k | 38.06% | **38.46%** | 28.49% | 37.25% | 37.25% | 35.91% | 12.71% |
| zipf 1.0 | 10k | 68.89% | **68.94%** | 62.35% | 68.44% | 68.44% | 67.10% | 36.81% |
| zipf 1.2 | 10k | 90.28% | **90.47%** | 88.43% | 90.35% | 90.36% | 90.35% | 68.15% |
| zipf + scans | 10k | 38.15% | 35.99% | 31.30% | **38.38%** | 38.38% | 36.13% | 21.87% |
| loop | 10k | 70.57% | 70.49% | 0.00% | 0.00% | 0.00% | **78.99%** | 0.99% |
| shifting popularity | 10k | 73.00% | 76.93% | **78.63%** | 44.45% | 44.45% | 69.23% | 44.33% |
| ARC OLTP | 1k | 40.55% | 40.89% | 32.83% | 13.83% | 13.83% | **44.48%** | 3.52% |
| ARC P8 | 400k | 95.76% | **96.22%** | 95.65% | 91.19% | 91.21% | 94.68% | 6.01% |
| ARC S3 | 400k | **39.75%** | 30.34% | 12.04% | 29.35% | 29.36% | 37.28% | 0.47% |
| ARC DS1 | 4M | **43.55%** | 25.96% | 20.25% | 38.25% | 39.07% | 37.78% | 1.30% |

² Measured with `Wait()` after every write; Ristretto's admission policy rejects most new keys on these traces.

W-TinyLFU is the best or within a few points of the best on every trace, which is why it is the recommended default.
Otter's more mature adaptive window wins on the loop and OLTP traces. v2's LRU and LFU match v1's hit ratios.

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
