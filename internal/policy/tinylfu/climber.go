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
	initialStep float64
	step        float64
	prevHitRate float64
	hits        uint64
	misses      uint64
}

func newClimber(capacity uint64) climber {
	c := climber{
		sampleSize:  sampleSize(capacity),
		initialStep: stepPercent * float64(capacity),
	}
	c.reset()
	return c
}

// sampleSize returns 10*capacity (saturating, at least 1), as Caffeine does. Smaller samples make large caches adapt
// to noise: on the DS1 trace a 1<<20 cap cost a 4M-entry cache 7 points of hit ratio.
func sampleSize(capacity uint64) uint64 {
	if capacity > math.MaxUint64/10 {
		return math.MaxUint64
	}
	return max(1, 10*capacity)
}

func (c *climber) reset() {
	c.hits, c.misses = 0, 0
	c.prevHitRate = 0
	c.step = c.initialStep
}

// record counts a hit or miss and returns the window size adjustment, 0 until a sample completes.
func (c *climber) record(hit bool) float64 {
	if hit {
		c.hits++
	} else {
		c.misses++
	}
	n := c.hits + c.misses
	if n < c.sampleSize {
		return 0
	}
	return c.climb(float64(c.hits) / float64(n))
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
