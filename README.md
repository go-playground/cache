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

The expiry options replace one another; the last one set wins. Expired entries are never returned. Their memory is
reclaimed by an incremental sweep that runs alongside normal operations, or by regular eviction, rather than at the
exact moment they expire, so an expiry costs only 8 bytes per entry and the cache never holds more than its capacity.
`OnEviction` reports `CauseExpired` when the sweep reclaims an entry.

## Benchmarks

Measured on an Apple M3 Max (16 cores), Go 1.27.2. Reproduce with `make bench`, `make hitratio` and
`MEMORY=1 go test -run=TestMemory -v` in [benchmarks](benchmarks), a separate module so the library stays
dependency-free. Medians of 3 runs. `sync.Map` is an unbounded baseline that never evicts.

### Throughput (ns/op, lower is better)

Zipf(1.0) keys over 1M keys against a 100k-entry cache.

**100% reads**

| cache | 1 core | 8 cores | 16 cores |
|---|---|---|---|
| v2 s3fifo | 16.0 | **2.2** | **1.3** |
| v2 lru | 28.6 | 4.0 | 2.1 |
| v2 lfu | 40.5 | 3.9 | 2.0 |
| v2 tinylfu | 40.1 | 4.0 | 2.1 |
| otter | 35.3 | 5.0 | 4.6 |
| ristretto | 29.2 | 10.6 | 11.1 |
| v1 lru | 23.2 | 193.4 | 269.7 |
| v1 lfu | 61.7 | 251.2 | 323.2 |
| hashicorp lru | 24.0 | 208.1 | 288.6 |
| sync.Map | 40.2 | 6.1 | 3.4 |

**75% reads**

| cache | 1 core | 8 cores | 16 cores |
|---|---|---|---|
| v2 s3fifo | 43.5 | **25.9** | **29.0** |
| v2 lru | 49.6 | 31.2 | 29.1 |
| v2 lfu | 61.5 | 36.2 | 33.5 |
| v2 tinylfu | 66.1 | 32.5 | 33.5 |
| otter | 76.5 | 68.4 | 109.3 |
| ristretto¹ | 46.0 | 37.9 | 39.1 |
| v1 lru | **27.1** | 210.9 | 292.6 |
| v1 lfu | 63.1 | 255.3 | 328.6 |
| hashicorp lru | 29.8 | 219.3 | 304.0 |
| sync.Map | 73.3 | 14.7 | 13.2 |

**50% reads**

| cache | 1 core | 8 cores | 16 cores |
|---|---|---|---|
| v2 s3fifo | 65.8 | **36.3** | **45.5** |
| v2 lru | 74.5 | 40.5 | 51.3 |
| v2 lfu | 80.9 | 42.0 | 54.2 |
| v2 tinylfu | 82.2 | 43.8 | 52.8 |
| otter | 119.5 | 120.1 | 216.7 |
| ristretto¹ | 57.5 | 60.2 | 64.4 |
| v1 lru | **30.6** | 207.6 | 309.1 |
| v1 lfu | 52.7 | 253.5 | 336.5 |
| hashicorp lru | 36.8 | 235.4 | 323.0 |
| sync.Map | 105.8 | 22.5 | 23.2 |

**0% reads (writes only)**

| cache | 1 core | 8 cores | 16 cores |
|---|---|---|---|
| v2 s3fifo | 116.6 | 65.9 | 74.4 |
| v2 lru | 106.7 | **60.3** | **68.0** |
| v2 lfu | 108.9 | 61.7 | 70.9 |
| v2 tinylfu | 132.7 | 77.4 | 78.8 |
| otter | 186.6 | 225.2 | 422.9 |
| ristretto¹ | 59.3 | 80.7 | 86.5 |
| v1 lru | **38.8** | 230.5 | 334.0 |
| v1 lfu | 38.0 | 234.2 | 330.7 |
| hashicorp lru | 44.0 | 254.1 | 325.3 |
| sync.Map | 161.3 | 39.0 | 44.9 |

¹ Ristretto applies writes asynchronously and drops them under load, so its write numbers are not comparable.

- **Concurrent reads scale near-linearly** and are the fastest measured; S3-FIFO reads cost a single atomic load.
- **Mixed and write-heavy workloads scale** where mutex-based caches (v1, hashicorp) collapse past one core.
- **Single-threaded, a plain mutex-guarded cache is faster for writes** (v1: ~30-40ns vs ~50-130ns). v2 pays for
  concurrency on every write: entries are immutable so reads never race, so each `Set` allocates a new entry. If a
  cache is only ever used from one goroutine, v1 remains the faster choice.
- **Many goroutines writing the same key** is no faster than v1 (~230ns/op at 16 cores vs ~220ns).

### Memory (bytes per entry, int→int)

| cache | empty cache | 10k entries | 100k entries | 1M entries | 100k after 3x churn |
|---|---|---|---|---|---|
| v2 lru / lfu | 28.8 KB | 85 | 84 | 84 | 93 |
| v2 s3fifo | 28.5 KB | 85 | 84 | 84 | 98 |
| v2 tinylfu | 33.1 KB | 92 | 90 | 88 | 95 |
| v1 lru / lfu | 184 B | 78 | 72 | 86 | 95 |
| otter | 4.3 KB | 92 | 83 | 91 | 87 |
| ristretto | 340 KB | 82 | 49 | 54 | 49 |
| hashicorp lru | 256 B | 110 | 104 | 118 | 127 |

An entry is its key and value plus 48 bytes (64 bytes for int→int), and the table is sized exactly to capacity. Most
of an empty cache is its table, presized for the configured capacity up to 64k entries; buffers start with a single
stripe and only grow per core under contention.

### Hit ratio (higher is better)

Cache-aside replay (miss → `Set`) on synthetic traces and the
[ARC paper's](https://www.usenix.org/conference/fast-03/arc-self-tuning-low-overhead-replacement-cache) storage traces.

| trace | capacity | v2 tinylfu | v2 s3fifo | v2 lru | v2 lfu | v1 lfu | otter | ristretto² |
|---|---|---|---|---|---|---|---|---|
| zipf 0.8 | 10k | 37.23% | **38.09%** | 28.49% | 37.25% | 37.25% | 35.85% | 12.79% |
| zipf 1.0 | 10k | 68.35% | **68.70%** | 62.35% | 68.44% | 68.44% | 67.69% | 36.21% |
| zipf 1.2 | 10k | 89.84% | **90.40%** | 88.43% | 90.35% | 90.36% | 90.35% | 68.23% |
| zipf + scans | 10k | 37.87% | 38.09% | 31.30% | **38.38%** | 38.38% | 36.07% | 22.00% |
| loop | 10k | 77.19% | 62.11% | 0.00% | 0.00% | 0.00% | **77.98%** | 0.99% |
| shifting popularity | 10k | 72.73% | 76.32% | **78.63%** | 44.45% | 44.45% | 69.14% | 44.27% |
| ARC OLTP | 1k | 40.09% | 40.81% | 32.83% | 13.83% | 13.83% | **44.54%** | 3.47% |
| ARC P8 | 400k | 95.51% | **96.23%** | 95.65% | 91.19% | 91.21% | 94.67% | 6.02% |
| ARC S3 | 400k | **39.60%** | 33.57% | 12.04% | 29.35% | 29.36% | 35.74% | 0.47% |
| ARC DS1 | 4M | **44.73%** | 29.40% | 20.25% | 38.25% | 39.07% | 37.04% | 1.30% |

² Measured with `Wait()` after every write; Ristretto's admission policy rejects most new keys on these traces.

W-TinyLFU is the best or within a few points of the best on every trace, which is why it is the recommended default;
Otter leads on the OLTP trace. v2's LRU and LFU match v1's hit ratios.

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
