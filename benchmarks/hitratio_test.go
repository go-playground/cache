package benchmarks

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

type hitTrace struct {
	name     string
	capacity int
	keys     []int
}

func hitTraces() []hitTrace {
	const length = 2_000_000
	return []hitTrace{
		{"zipf-0.8", 10_000, ZipfTrace(length, 500_000, 0.8, 1)},
		{"zipf-1.0", 10_000, ZipfTrace(length, 500_000, 1.0, 2)},
		{"zipf-1.2", 10_000, ZipfTrace(length, 500_000, 1.2, 3)},
		{"zipf+scans", 10_000, ScanTrace(length, 100_000, 1.0, 20_000, 20_000, 4)},
		{"loop", 10_000, LoopTrace(length, 12_000)},
		{"shifting", 10_000, ShiftingTrace(length, 50_000, 1.0, 200_000, 5)},
	}
}

// replay runs a cache-aside workload: every miss is followed by a Set. Asynchronous caches are flushed after each Set
// so the comparison measures eviction policy rather than dropped writes.
func replay(c Cache, keys []int) float64 {
	hits := 0
	for _, k := range keys {
		if c.Get(k) {
			hits++
			continue
		}
		c.Set(k, k)
		c.Flush()
	}
	return float64(hits) / float64(len(keys))
}

// TestHitRatio reports the hit ratio of every bounded implementation on each trace. Set HITRATIO=1 to run it.
//
//	HITRATIO=1 go test -run=TestHitRatio -v
func TestHitRatio(t *testing.T) {
	if os.Getenv("HITRATIO") == "" {
		t.Skip("set HITRATIO=1 to run the hit-ratio comparison")
	}
	traces := append(hitTraces(), arcTraces(t)...)
	var bounded []Impl
	for _, impl := range Impls() {
		if impl.Bounded {
			bounded = append(bounded, impl)
		}
	}

	var sb strings.Builder
	sb.WriteString("| trace | capacity |")
	for _, impl := range bounded {
		fmt.Fprintf(&sb, " %s |", impl.Name)
	}
	fmt.Fprintf(&sb, "\n|---|---|%s\n", strings.Repeat("---|", len(bounded)))
	for _, tr := range traces {
		fmt.Fprintf(&sb, "| %s | %d |", tr.name, tr.capacity)
		for _, impl := range bounded {
			fmt.Fprintf(&sb, " %.2f%% |", 100*replay(impl.New(tr.capacity), tr.keys))
		}
		sb.WriteString("\n")
	}
	t.Log("\n" + sb.String())
}
