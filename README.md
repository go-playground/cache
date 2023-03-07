# cache
![Project status](https://img.shields.io/badge/version-0.10.0-green.svg)
[![GoDoc](https://godoc.org/github.com/go-playground/cache?status.svg)](https://pkg.go.dev/github.com/go-playground/cache)
![License](https://img.shields.io/dub/l/vibe-d.svg)

Contains multiple in-memory cache implementations including LRU &amp; LFU

#### Requirements
- Go 1.18+

### Contents

Visit linked cache README's via the links below for more details.

| Cache                | Description                   |
|----------------------|-------------------------------|
| [LRU](lru/README.md) | A Least recently Used cache.  |
| [LFU](lfu/README.md) | A Lead Frequently Used cache. |

### Misc

These caches are not thread safe and this is done on purpose because of the following:
- If not needed then no additional overhead.
- Allows caller/locker/user to choose the locking strategy that best suits them. eg. can lock and do two gets and a set before unlocking.

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
