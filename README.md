# cache
![Project status](https://img.shields.io/badge/version-1.1.0-green.svg)
[![GoDoc](https://godoc.org/github.com/go-playground/cache?status.svg)](https://pkg.go.dev/github.com/go-playground/cache)
![License](https://img.shields.io/dub/l/vibe-d.svg)

Contains multiple in-memory cache implementations including LRU &amp; LFU

#### Requirements
- Go 1.18+

### Contents

See detailed usage and docs using the links below.

| Cache                | Description                   |
|----------------------|-------------------------------|
| [LRU](lru/README.md) | A Least Recently Used cache.  |
| [LFU](lfu/README.md) | A Least Frequently Used cache. |

### Thread Safety

These caches have the option of being built with no locking and auto locking guarded via a mutex.

When to use the no locking option:

- For single threaded code.
- When you wish control your own locking semantics.


When to use auto locking:
- Ease of use, but still the ability to perform multiple operations using the LockGuard.

#### License

<sup>
Licensed under either of <a href="LICENSE-APACHE">Apache License, Version
2.0</a> or <a href="LICENSE-MIT">MIT license</a> at your option.
</sup>

<br>

<sub>
Unless you explicitly state otherwise, any contribution intentionally submitted
for inclusion in this package by you, as defined in the Apache-2.0 license, shall be
dual licensed as above, without any additional terms or conditions.
</sub>
