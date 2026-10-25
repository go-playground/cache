package xruntime

import (
	"runtime"
	"sync"
	"testing"
)

func TestPidIsWithinGOMAXPROCS(t *testing.T) {
	procs := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for range 4 * procs {
		wg.Go(func() {
			for range 1000 {
				if p := Pid(); p < 0 || p >= procs {
					t.Errorf("pid %d outside [0, %d)", p, procs)
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestPidIsStableWithoutPreemption(t *testing.T) {
	a, b := Pid(), Pid()
	if a != b {
		t.Skipf("goroutine migrated between calls (%d -> %d); affinity is only a hint", a, b)
	}
}

func BenchmarkPid(b *testing.B) {
	for range b.N {
		_ = Pid()
	}
}
