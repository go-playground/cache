# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.9.0] - 2023-03-06
### Changed
- Updated pkg dep to latest.

## [0.9.0] - 2023-03-06
### Changed
- Examples of how to protect cache documents.
- Updated pkg dep to latest.
- Updated Benchmarks.

## [0.8.0] - 2023-03-05
### Removed
- Inner Mutex to allow more flexible usage by the caller/user.
- Stats cadence helper function.

### Added
- Exposed Stats function for reporting.

## [0.7.0] - 2023-03-05
### Fixed
- Exposure of Stats fields.

## [0.6.0] - 2023-03-05
### Removed
- Previous Hit, Miss, Eviction & PercentageFull functions.
- Capacity & Len functions to reduce libraries surface area further.

### Added
- Builder `Stats` function to allow basic statistical reporting.

## [0.5.0] - 2023-02-25
### Removed
- PercentageFull helper function.

### Added
- Builder `PercentageFullReportCadence` function to abstract away periodically calling the `PercentageFullFn` is needed.

## [0.4.0] - 2023-02-25
### Added
- PercentageFull helper function to avoid the need to lock twice calling `Len` and `Capacity` separately.

## [0.3.0] - 2023-02-25
### Changed
- Changed percentage full function to be called all the time and uses a float64 instead of uint8. We were already having to cast to float64 for the math so might as well simply use it.

## [0.2.0] - 2023-02-25
### Changed
- Renamed LRU and LFU cache struct to Cache to prevent stutter in calling code. Now reads `lru.Cache` and `lfu.Cache`.
- Changed to use `int64` and monotonic time instead of `time.*` functions for speed and better space efficiency.

### Added
- Optimization when incrementing frequency and the found node is the only entry.

## [0.1.0] - 2023-02-19
### Added
- LRU & LFU cache implementations backed by a generic linked list.

[Unreleased]: https://github.com/go-playground/cache/compare/v0.10.0...HEAD
[0.10.0]: https://github.com/go-playground/cache/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/go-playground/cache/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/go-playground/cache/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/go-playground/cache/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/go-playground/cache/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/go-playground/cache/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/go-playground/cache/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/go-playground/cache/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/go-playground/cache/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/go-playground/cache/commit/v0.1.0