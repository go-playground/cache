package benchmarks

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

// heapAfter returns the live heap after building and filling a cache with n entries, keeping it reachable.
func heapAfter(impl Impl, capacity, n int) (uint64, Cache) {
	c := impl.New(capacity)
	for i := range n {
		c.Set(i, i)
	}
	c.Flush()
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc, c
}

func baseline() uint64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

// churnCapacity is the capacity used to measure steady state after evictions, when eviction metadata such as
// S3-FIFO's ghost queue and TinyLFU's sketch is populated.
const churnCapacity = 100_000

// TestMemory reports retained heap per entry for full int→int caches and the fixed overhead of an empty cache. Set
// MEMORY=1 to run it.
//
//	MEMORY=1 go test -run=TestMemory -v
func TestMemory(t *testing.T) {
	if os.Getenv("MEMORY") == "" {
		t.Skip("set MEMORY=1 to run the memory comparison")
	}
	sizes := []int{10_000, 100_000, 1_000_000}
	var sb strings.Builder
	sb.WriteString("| cache | empty cache (bytes) |")
	for _, n := range sizes {
		fmt.Fprintf(&sb, " bytes/entry @%d |", n)
	}
	fmt.Fprintf(&sb, " bytes/entry @%d after 3x churn |", churnCapacity)
	fmt.Fprintf(&sb, "\n|---|---|%s\n", strings.Repeat("---|", len(sizes)+1))
	for _, impl := range Impls() {
		before := baseline()
		after, empty := heapAfter(impl, 1000, 0)
		fmt.Fprintf(&sb, "| %s | %d |", impl.Name, int64(after)-int64(before))
		runtime.KeepAlive(empty)
		for _, n := range sizes {
			before := baseline()
			after, c := heapAfter(impl, n, n)
			fmt.Fprintf(&sb, " %.1f |", float64(int64(after)-int64(before))/float64(n))
			runtime.KeepAlive(c)
		}
		before = baseline()
		after, churned := heapAfter(impl, churnCapacity, 3*churnCapacity)
		fmt.Fprintf(&sb, " %.1f |", float64(int64(after)-int64(before))/churnCapacity)
		runtime.KeepAlive(churned)
		sb.WriteString("\n")
	}
	t.Log("\n" + sb.String())
}
