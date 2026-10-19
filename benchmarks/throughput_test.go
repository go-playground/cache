package benchmarks

import (
	"fmt"
	"math/rand/v2"
	"runtime"
	"testing"
)

const (
	throughputCapacity = 100_000
	throughputKeys     = 1_000_000
	traceLen           = 1 << 21
)

var throughputTrace = ZipfTrace(traceLen, throughputKeys, 1.0, 42)

// BenchmarkThroughput measures concurrent operations per second on a Zipf(1.0) workload over 1M keys against a
// 100k-entry cache prefilled by replaying the trace. Reads that miss do not load.
//
//	go test -run=NONE -bench=Throughput -cpu=1,4,8,16
func BenchmarkThroughput(b *testing.B) {
	for _, readPct := range []int{100, 75, 50, 0} {
		for _, impl := range Impls() {
			// Built and prefilled on first use, since b.Run's function is invoked repeatedly while calibrating b.N and
			// filtered-out sub-benchmarks should cost nothing, and rebuilt whenever -cpu changes GOMAXPROCS: caches
			// choose their engine from GOMAXPROCS when built, as they would in a program with that many cores.
			var c Cache
			var builtFor int
			b.Run(fmt.Sprintf("reads=%d%%/%s", readPct, impl.Name), func(b *testing.B) {
				if procs := runtime.GOMAXPROCS(0); c == nil || builtFor != procs {
					c, builtFor = prefilled(impl), procs
				}
				b.ReportAllocs()
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					idx := rand.IntN(traceLen)
					for pb.Next() {
						k := throughputTrace[idx&(traceLen-1)]
						if idx%100 < readPct {
							c.Get(k)
						} else {
							c.Set(k, k)
						}
						idx++
					}
				})
			})
		}
	}
}

// prefilled builds impl and replays the trace into it, then collects the garbage so it is not charged to the
// measurement.
func prefilled(impl Impl) Cache {
	c := impl.New(throughputCapacity)
	for _, k := range throughputTrace {
		c.Set(k, k)
	}
	c.Flush()
	runtime.GC()
	return c
}

// BenchmarkHotKey reproduces v1's parallel benchmark shape: every goroutine sets and gets one shared key, the worst
// case for lock contention.
func BenchmarkHotKey(b *testing.B) {
	for _, impl := range Impls() {
		b.Run(impl.Name, func(b *testing.B) {
			c := impl.New(100)
			c.Set(1, 1)
			c.Flush()
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					c.Set(1, 1)
					c.Get(1)
				}
			})
		})
	}
}

// BenchmarkHotKeyReads has every goroutine read one shared key, isolating read-path scalability.
func BenchmarkHotKeyReads(b *testing.B) {
	for _, impl := range Impls() {
		b.Run(impl.Name, func(b *testing.B) {
			c := impl.New(100)
			c.Set(1, 1)
			c.Flush()
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					c.Get(1)
				}
			})
		})
	}
}
