// Package sketch implements the 4-bit count-min sketch used by TinyLFU to estimate access frequency.
//
// The layout follows Caffeine's FrequencySketch: the table is split into 64-byte blocks of eight words, each word
// holding sixteen 4-bit counters. A key's four counters live in four distinct words of a single block, so an
// increment or lookup touches one cache line. Counters saturate at 15 and are periodically halved, so the sketch
// tracks recent rather than all-time popularity. Not safe for concurrent use.
package sketch

import "math/bits"

const (
	maxCapacity = 1 << 26
	maxCount    = 15
	resetMask   = 0x7777777777777777
	oneMask     = 0x1111111111111111
)

// Sketch estimates the frequency of hashes within a sampling period.
type Sketch struct {
	table      []uint64
	blockMask  uint64
	sampleSize int
	size       int
}

// New returns a sketch sized for capacity distinct entries.
func New(capacity int) *Sketch {
	s := &Sketch{}
	s.alloc(clamp(capacity))
	return s
}

// Increment records an occurrence of hash. Once sampleSize increments have been recorded every counter is halved.
func (s *Sketch) Increment(hash uint64) {
	block, ch := s.locate(hash)
	added := false
	for i := range uint64(4) {
		w, shift := slot(block, ch, i)
		if (s.table[w]>>shift)&maxCount == maxCount {
			continue
		}
		s.table[w] += 1 << shift
		added = true
	}
	if !added {
		return
	}
	s.size++
	if s.size >= s.sampleSize {
		s.age()
	}
}

// Frequency returns the estimated number of occurrences of hash, at most 15.
func (s *Sketch) Frequency(hash uint64) uint8 {
	block, ch := s.locate(hash)
	f := uint64(maxCount)
	for i := range uint64(4) {
		w, shift := slot(block, ch, i)
		f = min(f, (s.table[w]>>shift)&maxCount)
	}
	return uint8(f)
}

// EnsureCapacity grows the sketch to fit capacity distinct entries, keeping every estimate. It is a no-op if the
// sketch is already large enough.
//
// Growth duplicates the table: a key's block is chosen by hash & blockMask and its counters' positions within the
// block do not depend on the size, so after doubling the key lands in either the original block or its copy, both of
// which hold its counters. Estimates survive exactly and warm-up loses nothing.
func (s *Sketch) EnsureCapacity(capacity int) {
	c := clamp(capacity)
	if s.Capacity() >= c {
		return
	}
	old := s.table
	s.alloc(c)
	for i := 0; i < len(s.table); i += len(old) {
		copy(s.table[i:], old)
	}
}

// Reset zeroes every counter.
func (s *Sketch) Reset() {
	clear(s.table)
	s.size = 0
}

// Capacity returns the number of distinct entries the sketch is currently sized for.
func (s *Sketch) Capacity() int {
	return len(s.table) * entriesPerWord
}

func (s *Sketch) alloc(capacity int) {
	c := clamp(capacity)
	s.table = make([]uint64, words(c))
	s.blockMask = uint64(len(s.table)>>3) - 1
	s.sampleSize = max(10, 10*c)
}

// age halves every counter, correcting size for the truncated odd counters.
func (s *Sketch) age() {
	odd := 0
	for i, w := range s.table {
		odd += bits.OnesCount64(w & oneMask)
		s.table[i] = (w >> 1) & resetMask
	}
	s.size = (s.size - odd/4) / 2
}

// locate returns the first word of hash's block and the 32 bits selecting its four counters.
func (s *Sketch) locate(hash uint64) (uint64, uint32) {
	bh := spread(hash)
	return (bh & s.blockMask) << 3, uint32(rehash(bh) >> 32)
}

// slot returns the word and bit shift of counter i, keeping each counter in its own word of the block.
func slot(block uint64, ch uint32, i uint64) (uint64, uint64) {
	b := uint64(ch >> (i << 3))
	return block + b&1 + i<<1, ((b >> 1) & 15) << 2
}

func clamp(capacity int) int {
	return min(max(capacity, 0), maxCapacity)
}

// entriesPerWord is how many distinct entries the sketch is sized for per 64-bit word of counters. Caffeine uses 1;
// measured on synthetic and ARC traces, 2 halves the sketch's memory with hit ratios within run-to-run noise, while 4
// starts losing 1-2 points on skewed workloads.
const entriesPerWord = 2

// words returns the table length for capacity: the next power of two of ceil(capacity/entriesPerWord), at least one
// block, so Capacity() is never below capacity.
func words(capacity int) int {
	c := (clamp(capacity) + entriesPerWord - 1) / entriesPerWord
	if c <= 8 {
		return 8
	}
	return 1 << bits.Len(uint(c-1))
}

func spread(h uint64) uint64 {
	h ^= h >> 31
	h *= 0x7fb5d329728ea185
	h ^= h >> 27
	return h
}

func rehash(h uint64) uint64 {
	h *= 0x81dadef4bc2dd44d
	h ^= h >> 33
	return h
}
