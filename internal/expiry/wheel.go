package expiry

import (
	"math/bits"

	"github.com/go-playground/cache/v2/internal/deque"
	"github.com/go-playground/cache/v2/internal/node"
)

// Hierarchical timer wheel for per-entry expiry, after Caffeine's TimerWheel.
// Each level's span is a power of two nanoseconds so bucket selection is a shift and mask.
var (
	wheelBuckets = [...]int{64, 64, 32, 4, 1}
	wheelSpans   = [...]int64{
		1 << 30, // ~1.07s
		1 << 36, // ~1.14m
		1 << 42, // ~1.22h
		1 << 47, // ~1.63d
		4 << 47, // ~6.5d
		4 << 47, // ~6.5d
	}
	wheelShifts = func() (s [len(wheelSpans)]uint) {
		for i, span := range wheelSpans {
			s[i] = uint(bits.TrailingZeros64(uint64(span)))
		}
		return
	}()
)

// Wheel tracks nodes with individual expiry times in O(1) per operation, amortised.
type Wheel[K comparable, V any] struct {
	levels [len(wheelBuckets)][]deque.Deque[K, V]
	now    int64
}

// NewWheel returns a timer wheel whose clock starts at now.
func NewWheel[K comparable, V any](now int64) *Wheel[K, V] {
	w := &Wheel[K, V]{now: now}
	for i, n := range wheelBuckets {
		w.levels[i] = make([]deque.Deque[K, V], n)
		for j := range w.levels[i] {
			w.levels[i][j] = deque.New[K, V](node.ExpiryList)
		}
	}
	return w
}

// Add schedules n if it has an expiry.
func (w *Wheel[K, V]) Add(n *node.Node[K, V]) {
	at := n.ExpiresAt()
	if at == 0 {
		return
	}
	// A node already due is placed in the current tick's bucket rather than one the cursor has passed.
	level, idx := w.bucketFor(max(at, w.now))
	w.levels[level][idx].PushBack(n)
	n.ExpirySlot = encodeSlot(level, idx)
}

// Access is a no-op: per-entry expiry is fixed at write time.
func (w *Wheel[K, V]) Access(*node.Node[K, V]) {}

// Update replaces old with n, rescheduling at n's expiry.
func (w *Wheel[K, V]) Update(old, n *node.Node[K, V]) {
	w.Remove(old)
	w.Add(n)
}

// Remove unschedules n.
func (w *Wheel[K, V]) Remove(n *node.Node[K, V]) {
	if !isTracked(n) {
		return
	}
	level, idx := decodeSlot(n.ExpirySlot)
	w.levels[level][idx].Remove(n)
	n.ExpirySlot = 0
}

// Expire advances the wheel to now, calling fn for every expired node and rescheduling the rest of each passed bucket.
func (w *Wheel[K, V]) Expire(now int64, fn func(n *node.Node[K, V])) {
	prev := w.now
	if now <= prev {
		return
	}
	w.now = now
	for level := range wheelBuckets {
		prevTicks, curTicks := prev>>wheelShifts[level], now>>wheelShifts[level]
		delta := curTicks - prevTicks
		if delta <= 0 {
			return
		}
		w.expireLevel(level, prevTicks, delta, fn)
	}
}

func (w *Wheel[K, V]) expireLevel(level int, prevTicks, delta int64, fn func(n *node.Node[K, V])) {
	buckets := w.levels[level]
	mask := int64(len(buckets) - 1)
	steps := min(delta+1, int64(len(buckets)))
	start := prevTicks & mask
	for i := start; i < start+steps; i++ {
		w.expireBucket(&buckets[i&mask], fn)
	}
}

func (w *Wheel[K, V]) expireBucket(b *deque.Deque[K, V], fn func(n *node.Node[K, V])) {
	pending := deque.New[K, V](node.ExpiryList)
	pending.AppendBack(b)
	for n := pending.Front(); n != nil; n = pending.Front() {
		pending.Remove(n)
		n.ExpirySlot = 0
		if n.IsExpired(w.now) {
			fn(n)
			continue
		}
		w.Add(n)
	}
}

// Clear unschedules every node.
func (w *Wheel[K, V]) Clear() {
	for level := range w.levels {
		for i := range w.levels[level] {
			w.levels[level][i].Clear(func(n *node.Node[K, V]) { n.ExpirySlot = 0 })
		}
	}
}

// Len returns the number of scheduled nodes.
func (w *Wheel[K, V]) Len() int {
	total := 0
	for level := range w.levels {
		for i := range w.levels[level] {
			total += w.levels[level][i].Len()
		}
	}
	return total
}

func (w *Wheel[K, V]) bucketFor(at int64) (level, idx int) {
	duration := at - w.now
	last := len(wheelBuckets) - 1
	for level = range last {
		if duration < wheelSpans[level+1] {
			ticks := at >> wheelShifts[level]
			return level, int(ticks & int64(wheelBuckets[level]-1))
		}
	}
	return last, 0
}

func encodeSlot(level, idx int) uint32 {
	return uint32(level<<8|idx) + 1
}

func decodeSlot(slot uint32) (level, idx int) {
	s := int(slot - 1)
	return s >> 8, s & 0xff
}
