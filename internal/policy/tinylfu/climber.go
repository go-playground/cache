package tinylfu

import "math"

const (
	stepPercent      = 0.0625
	stepDecay        = 0.98
	restartThreshold = 0.05
)

// climber is a hill climber choosing the admission window size: after each sample of requests it keeps moving the
// window in the same direction while the hit rate improves and reverses when it worsens. Steps decay geometrically
// and restart at full size when the hit rate shifts sharply, which signals a workload change.
type climber struct {
	sampleSize  uint64
	byEntries   bool
	minEntries  uint64
	initialStep float64
	step        float64
	prevHitRate float64
	hits        uint64
	misses      uint64
}

// newClimber returns a climber stepping by a fraction of capacity, which may be a weight, and sampling by entries:
// capacity in entries for unweighted caches, or the current entry count for weighted ones, see resample.
func newClimber(capacity uint64) climber {
	c := climber{
		sampleSize:  sampleSize(capacity),
		initialStep: stepPercent * float64(capacity),
	}
	c.reset()
	return c
}

// newEntryClimber returns a climber for a weighted cache, whose capacity is a total weight: each completed sample sizes
// the next from the entries the cache holds, never fewer than min(capacity, initialSketch), so the period stays
// meaningful however few, heavy entries it holds.
func newEntryClimber(capacity uint64) climber {
	c := newClimber(capacity)
	c.byEntries, c.minEntries = true, min(capacity, initialSketch)
	c.sampleSize = sampleSize(c.minEntries)
	return c
}

// sampleSize returns 10*entries (saturating, at least 1), as Caffeine does. Smaller samples make large caches adapt
// to noise: on the DS1 trace a 1<<20 cap cost a 4M-entry cache 7 points of hit ratio.
func sampleSize(entries uint64) uint64 {
	if entries > math.MaxUint64/10 {
		return math.MaxUint64
	}
	return max(1, 10*entries)
}

// resample sets the sample size from the number of entries the cache holds. A weighted cache's capacity is a total
// weight, so sampling 10x that would mean the climber never adapts; sampling by entries keeps the period meaningful.
func (c *climber) resample(entries uint64) {
	c.sampleSize = sampleSize(entries)
}

func (c *climber) reset() {
	c.hits, c.misses = 0, 0
	c.prevHitRate = 0
	c.step = c.initialStep
}

// record counts a hit or miss and returns the window size adjustment, 0 until a sample completes. entries is the
// number the cache holds, used by an entry climber to size its next sample.
func (c *climber) record(hit bool, entries uint64) float64 {
	if hit {
		c.hits++
	} else {
		c.misses++
	}
	n := c.hits + c.misses
	if n < c.sampleSize {
		return 0
	}
	amount := c.climb(float64(c.hits) / float64(n))
	if c.byEntries {
		c.sampleSize = sampleSize(max(entries, c.minEntries))
	}
	return amount
}

func (c *climber) climb(hitRate float64) float64 {
	change := hitRate - c.prevHitRate
	amount := c.step
	if change < 0 {
		amount = -amount
	}
	c.step = stepDecay * amount
	if math.Abs(change) >= restartThreshold {
		c.step = math.Copysign(c.initialStep, amount)
	}
	c.prevHitRate = hitRate
	c.hits, c.misses = 0, 0
	return amount
}
