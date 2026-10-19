package cache

import "github.com/go-playground/cache/v2/internal/core"

// MaxEntries is the largest entry capacity, about 1.07 billion. Builders clamp larger entry capacities to it; weighted
// capacities are total weights and are not limited.
const MaxEntries = core.MaxEntries

// Cause is why an entry was evicted.
type Cause uint8

const (
	// CauseSize means the entry was evicted to keep the cache within capacity.
	CauseSize = Cause(core.CauseSize)
	// CauseExpired means the entry's expiry passed.
	CauseExpired = Cause(core.CauseExpired)
)

// String returns the cause's name.
func (c Cause) String() string {
	switch c {
	case CauseSize:
		return "size"
	case CauseExpired:
		return "expired"
	default:
		return "unknown"
	}
}

// Stats is a snapshot of cumulative cache statistics.
type Stats struct {
	// Capacity is the maximum total weight; with unweighted caches every entry weighs 1, so it is the maximum length.
	Capacity uint64

	// Len is the number of entries.
	Len int

	// Weight is the total weight of the entries.
	Weight uint64

	// Hits is the number of Get/GetOrLoad lookups that found a live entry. Requires RecordStats.
	Hits uint64

	// Misses is the number of Get/GetOrLoad lookups that found no live entry. Requires RecordStats.
	Misses uint64

	// Sets is the number of writes, including those by GetOrLoad. Requires RecordStats.
	Sets uint64

	// Evictions is the number of entries evicted due to capacity or expiry. Always recorded.
	Evictions uint64

	// Loads is the number of GetOrLoad loader calls. Requires RecordStats.
	Loads uint64

	// LoadErrors is the number of GetOrLoad loader calls that failed or panicked. Requires RecordStats.
	LoadErrors uint64
}

// Gets returns the number of lookups, Hits + Misses.
func (s Stats) Gets() uint64 {
	return s.Hits + s.Misses
}

// HitRatio returns Hits / Gets, or 0 when there were no lookups.
func (s Stats) HitRatio() float64 {
	if s.Gets() == 0 {
		return 0
	}
	return float64(s.Hits) / float64(s.Gets())
}
