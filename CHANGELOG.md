# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.0] - 2023-02-25
### Changed
- Renamed LRU and LFU cache struct to Cache to prevent stutter in calling code. Now reads `lru.Cache` and `lfu.Cache`.
- Changed to use `int64` and monotonic time instead of `time.*` functions for speed and better space efficiency.

### Added
- Optimization when incrementing frequency and the found node is the only entry.

## [0.1.0] - 2023-02-19
### Added
- LRU & LFU cache implementations backed by a generic linked list.

[Unreleased]: https://github.com/go-playground/cache/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/go-playground/cache/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/go-playground/cache/commit/v0.1.0