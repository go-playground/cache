package sketch

import (
	"math/rand/v2"
	"testing"
)

func TestIncrementIsMonotonicAndSaturates(t *testing.T) {
	s := New(1024)
	const h = 0xdeadbeefcafebabe
	if f := s.Frequency(h); f != 0 {
		t.Fatalf("fresh frequency = %d, want 0", f)
	}
	for want := uint8(1); want <= 20; want++ {
		s.Increment(h)
		if f := s.Frequency(h); f != min(want, 15) {
			t.Fatalf("after %d increments frequency = %d, want %d", want, f, min(want, 15))
		}
	}
}

func TestFrequencyNeverUnderestimatesAndRarelyOverestimates(t *testing.T) {
	const n = 4096
	s := New(n)
	r := rand.New(rand.NewPCG(1, 2))
	seen := make([]uint64, n)
	for i := range seen {
		seen[i] = r.Uint64()
		s.Increment(seen[i])
	}

	over := 0
	for _, h := range seen {
		f := s.Frequency(h)
		if f == 0 {
			t.Fatalf("false negative for %x", h)
		}
		if f > 1 {
			over++
		}
	}
	// At two entries per word, sizing for n entries yields ~3% collisions; hit ratio tests show this is harmless.
	if over*100 >= 5*n {
		t.Errorf("%d of %d inserted hashes overestimated, want < 5%%", over, n)
	}

	unseen := 0
	for range n {
		if s.Frequency(r.Uint64()) > 0 {
			unseen++
		}
	}
	if unseen*100 >= 5*n {
		t.Errorf("%d of %d unseen hashes reported, want < 5%%", unseen, n)
	}
}

func TestAgingHalvesCounters(t *testing.T) {
	s := New(1)
	if s.sampleSize != 10 {
		t.Fatalf("sampleSize = %d, want 10", s.sampleSize)
	}
	const a, b = 1, 2
	for range 6 {
		s.Increment(a)
	}
	for range 3 {
		s.Increment(b)
	}
	fa, fb := s.Frequency(a), s.Frequency(b)
	s.Increment(b)
	if got, want := s.Frequency(a), fa/2; got != want {
		t.Errorf("a after aging = %d, want %d", got, want)
	}
	if got, want := s.Frequency(b), (fb+1)/2; got != want {
		t.Errorf("b after aging = %d, want %d", got, want)
	}
}

func TestAgeCorrectsSizeForOddCounters(t *testing.T) {
	s := New(64)
	s.table[0] = 0x1 | 0x3<<4 | 0x2<<8
	s.table[1] = 0x1 << 60
	s.table[2] = 0xf
	s.table[3] = 0x1
	s.size = 41
	s.age()

	if s.table[0] != 0x1<<4|0x1<<8 || s.table[1] != 0 || s.table[2] != 0x7 || s.table[3] != 0 {
		t.Errorf("table after age = %x", s.table[:4])
	}
	// Five odd counters: (41 - 5/4) / 2.
	if s.size != 20 {
		t.Errorf("size = %d, want 20", s.size)
	}
}

func TestEnsureCapacity(t *testing.T) {
	s := New(16)
	if s.Capacity() != 16 {
		t.Fatalf("Capacity = %d, want 16", s.Capacity())
	}
	s.Increment(7)

	s.EnsureCapacity(8)
	if s.Capacity() != 16 || s.Frequency(7) != 1 {
		t.Fatalf("EnsureCapacity(8) changed the sketch: capacity %d, frequency %d", s.Capacity(), s.Frequency(7))
	}

	s.EnsureCapacity(100)
	if s.Capacity() != 128 {
		t.Errorf("Capacity = %d, want 128", s.Capacity())
	}
	if s.Frequency(7) != 0 || s.size != 0 {
		t.Errorf("grown sketch kept counts: frequency %d, size %d", s.Frequency(7), s.size)
	}
	if s.sampleSize != 1000 {
		t.Errorf("sampleSize = %d, want 1000", s.sampleSize)
	}
}

func TestWords(t *testing.T) {
	tests := []struct{ capacity, want int }{
		{-1, 8}, {0, 8}, {1, 8}, {16, 8}, {18, 16}, {1000, 512}, {1024, 512}, {1026, 1024},
		{maxCapacity, maxCapacity / 2}, {maxCapacity + 1, maxCapacity / 2}, {1 << 40, maxCapacity / 2},
	}
	for _, tt := range tests {
		if got := words(tt.capacity); got != tt.want {
			t.Errorf("words(%d) = %d, want %d", tt.capacity, got, tt.want)
		}
	}
}

func TestCountersShareOneBlock(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	for range 1000 {
		s := New(1 << 12)
		s.Increment(r.Uint64())

		var set []int
		for i, w := range s.table {
			if w == 0 {
				continue
			}
			if w&(w-1) != 0 {
				t.Fatalf("word %d holds more than one counter: %x", i, w)
			}
			set = append(set, i)
		}
		if len(set) != 4 {
			t.Fatalf("%d words set, want 4", len(set))
		}
		if block := set[0] &^ 7; set[3]&^7 != block {
			t.Fatalf("counters span words %v, want one 8-word block", set)
		}
	}
}

func TestNoAllocations(t *testing.T) {
	s := New(1024)
	var h uint64
	if a := testing.AllocsPerRun(1000, func() { h++; s.Increment(h) }); a != 0 {
		t.Errorf("Increment allocates %v", a)
	}
	if a := testing.AllocsPerRun(1000, func() { h++; s.Frequency(h) }); a != 0 {
		t.Errorf("Frequency allocates %v", a)
	}
}

func BenchmarkIncrement(b *testing.B) {
	for _, size := range []int{1 << 10, 1 << 20} {
		b.Run(sizeName(size), func(b *testing.B) {
			s, hashes := New(size), hashes(1<<16)
			for i := 0; b.Loop(); i++ {
				s.Increment(hashes[i&(len(hashes)-1)])
			}
		})
	}
}

func BenchmarkFrequency(b *testing.B) {
	for _, size := range []int{1 << 10, 1 << 20} {
		b.Run(sizeName(size), func(b *testing.B) {
			s, hashes := New(size), hashes(1<<16)
			for _, h := range hashes {
				s.Increment(h)
			}
			var sink uint8
			for i := 0; b.Loop(); i++ {
				sink += s.Frequency(hashes[i&(len(hashes)-1)])
			}
			_ = sink
		})
	}
}

func hashes(n int) []uint64 {
	r := rand.New(rand.NewPCG(5, 6))
	hs := make([]uint64, n)
	for i := range hs {
		hs[i] = r.Uint64()
	}
	return hs
}

func sizeName(size int) string {
	if size >= 1<<20 {
		return "1M"
	}
	return "1K"
}
