package expiry

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/go-playground/cache/v2/internal/node"
)

func newNode(key int, expiresAt int64) *node.Node[int, int] {
	n := node.New(key, key, uint64(key), 1)
	n.SetExpiresAt(expiresAt)
	return n
}

func collect(t Tracker[int, int], now int64) []int {
	var keys []int
	t.Expire(now, func(n *node.Node[int, int]) { keys = append(keys, n.Key) })
	return keys
}

func equal(a, b []int) bool {
	return slices.Equal(a, b)
}

func TestWriteOrderExpiresOldestFirstAndStopsAtFirstUnexpired(t *testing.T) {
	o := NewWriteOrder[int, int]()
	for i := 1; i <= 5; i++ {
		o.Add(newNode(i, int64(i*10)))
	}
	if got := collect(o, 30); !equal(got, []int{1, 2, 3}) {
		t.Fatalf("got %v", got)
	}
	if o.Len() != 2 {
		t.Fatalf("expected 2 remaining, got %d", o.Len())
	}
	if got := collect(o, 29); len(got) != 0 {
		t.Fatalf("nothing more should expire at an earlier time, got %v", got)
	}
}

func TestNodesWithoutExpiryAreNeverTracked(t *testing.T) {
	for name, tr := range map[string]Tracker[int, int]{
		"write":  NewWriteOrder[int, int](),
		"access": NewAccessOrder[int, int](),
		"wheel":  NewWheel[int, int](0),
	} {
		n := newNode(1, 0)
		tr.Add(n)
		if n.ExpirySlot != 0 {
			t.Fatalf("%s: node without expiry must not be tracked", name)
		}
		tr.Remove(n)
		tr.Access(n)
		if got := collect(tr, 1<<62); len(got) != 0 {
			t.Fatalf("%s: never-expiring node expired: %v", name, got)
		}
	}
}

func TestRemoveAndUpdateAreSafeForUntrackedNodes(t *testing.T) {
	for name, tr := range map[string]Tracker[int, int]{
		"write":  NewWriteOrder[int, int](),
		"access": NewAccessOrder[int, int](),
		"wheel":  NewWheel[int, int](0),
	} {
		untracked := newNode(1, 0)
		replacement := newNode(1, 5)
		tr.Update(untracked, replacement)
		if got := collect(tr, 1<<40); !equal(got, []int{1}) {
			t.Fatalf("%s: replacement should be tracked and expire, got %v", name, got)
		}
	}
}

func TestUpdateReplacesExpiry(t *testing.T) {
	o := NewWriteOrder[int, int]()
	old := newNode(1, 10)
	o.Add(old)
	o.Add(newNode(2, 20))
	n := newNode(1, 100)
	o.Update(old, n)
	if old.ExpirySlot != 0 {
		t.Fatal("old node must be untracked after update")
	}
	if got := collect(o, 50); !equal(got, []int{2}) {
		t.Fatalf("got %v", got)
	}
	if got := collect(o, 100); !equal(got, []int{1}) {
		t.Fatalf("got %v", got)
	}
}

func TestAccessOrderMovesReadNodesToFront(t *testing.T) {
	o := NewAccessOrder[int, int]()
	a, b := newNode(1, 10), newNode(2, 20)
	o.Add(a)
	o.Add(b)
	a.SetExpiresAt(30)
	o.Access(a)
	if got := collect(o, 25); !equal(got, []int{2}) {
		t.Fatalf("got %v", got)
	}
}

func TestAccessOrderSkipsBumpedNodesWhoseReadsWereDropped(t *testing.T) {
	o := NewAccessOrder[int, int]()
	bumped := newNode(1, 10)
	o.Add(bumped)
	o.Add(newNode(2, 20))
	bumped.SetExpiresAt(1000) // read happened but its event was dropped
	if got := collect(o, 25); !equal(got, []int{2}) {
		t.Fatalf("expected scan to look past the bumped node, got %v", got)
	}
	if o.Len() != 1 {
		t.Fatal("bumped node must remain tracked")
	}
}

func TestWriteOrderIgnoresAccess(t *testing.T) {
	o := NewWriteOrder[int, int]()
	a := newNode(1, 10)
	o.Add(a)
	o.Add(newNode(2, 20))
	o.Access(a)
	if got := collect(o, 10); !equal(got, []int{1}) {
		t.Fatalf("got %v", got)
	}
}

func TestClearUntracksEverything(t *testing.T) {
	for name, tr := range map[string]Tracker[int, int]{
		"write": NewWriteOrder[int, int](),
		"wheel": NewWheel[int, int](0),
	} {
		nodes := []*node.Node[int, int]{newNode(1, 5), newNode(2, 1<<35), newNode(3, 1<<45)}
		for _, n := range nodes {
			tr.Add(n)
		}
		tr.Clear()
		for _, n := range nodes {
			if n.ExpirySlot != 0 {
				t.Fatalf("%s: node %d still tracked", name, n.Key)
			}
		}
		if got := collect(tr, 1<<60); len(got) != 0 {
			t.Fatalf("%s: cleared nodes expired: %v", name, got)
		}
	}
}

func TestWheelExpiresOnlyExpiredNodes(t *testing.T) {
	w := NewWheel[int, int](0)
	soon, later := newNode(1, 1<<30), newNode(2, 1<<40)
	w.Add(soon)
	w.Add(later)
	if got := collect(w, 3<<30); !equal(got, []int{1}) {
		t.Fatalf("got %v", got)
	}
	if later.ExpirySlot == 0 {
		t.Fatal("later node must still be scheduled")
	}
}

func TestWheelRemove(t *testing.T) {
	w := NewWheel[int, int](0)
	n := newNode(1, 1<<31)
	w.Add(n)
	w.Remove(n)
	if w.Len() != 0 || n.ExpirySlot != 0 {
		t.Fatal("node should be unscheduled")
	}
	if got := collect(w, 1<<40); len(got) != 0 {
		t.Fatalf("removed node expired: %v", got)
	}
}

func TestWheelExpireIgnoresTimeGoingBackwards(t *testing.T) {
	w := NewWheel[int, int](1 << 40)
	w.Add(newNode(1, 1<<30))
	if got := collect(w, 1<<39); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

// TestWheelRandomizedNeverEarlyAndEventuallyExpires checks the two properties reclamation relies on: a node is never
// reported before its expiry, every node is reported exactly once, and none lingers more than one top-level span.
func TestWheelRandomizedNeverEarlyAndEventuallyExpires(t *testing.T) {
	for seed := range uint64(20) {
		rng := rand.New(rand.NewPCG(seed, seed))
		start := rng.Int64N(1 << 50)
		w := NewWheel[int, int](start)
		due := map[int]int64{}
		removed := map[int]bool{}
		nodes := map[int]*node.Node[int, int]{}
		for k := range 2000 {
			at := start + rng.Int64N(int64(1)<<uint(20+rng.IntN(32)))
			n := newNode(k, at)
			nodes[k] = n
			due[k] = at
			w.Add(n)
		}
		for k := range 2000 {
			if rng.IntN(10) == 0 {
				w.Remove(nodes[k])
				removed[k] = true
			}
		}

		now := start
		seen := map[int]bool{}
		last := start + 1<<52 + 2*wheelSpans[len(wheelSpans)-1]
		for now < last {
			now += rng.Int64N(int64(1) << uint(25+rng.IntN(26)))
			w.Expire(now, func(n *node.Node[int, int]) {
				if n.ExpiresAt() > now {
					t.Fatalf("seed %d: node %d reported early (due %d, now %d)", seed, n.Key, n.ExpiresAt(), now)
				}
				if seen[n.Key] || removed[n.Key] {
					t.Fatalf("seed %d: node %d reported twice or after removal", seed, n.Key)
				}
				seen[n.Key] = true
			})
		}
		for k := range due {
			if !removed[k] && !seen[k] {
				t.Fatalf("seed %d: node %d due %d never expired", seed, k, due[k])
			}
		}
		if w.Len() != 0 {
			t.Fatalf("seed %d: %d nodes left scheduled", seed, w.Len())
		}
	}
}

func TestWheelLagIsBoundedByLevelSpan(t *testing.T) {
	w := NewWheel[int, int](0)
	cases := []int64{1 << 20, 1 << 33, 1 << 39, 1 << 45, 1 << 49}
	for i, at := range cases {
		w.Add(newNode(i, at))
	}
	for i, at := range cases {
		lag := wheelSpans[0]
		for level := range len(wheelBuckets) - 1 {
			if at < wheelSpans[level+1] {
				break
			}
			lag = wheelSpans[level+1]
		}
		var got []int
		w.Expire(at+2*lag, func(n *node.Node[int, int]) { got = append(got, n.Key) })
		if !slices.Contains(got, i) {
			t.Fatalf("node %d due %d not expired within lag %d, got %v", i, at, lag, got)
		}
	}
}

func TestSlotEncodingRoundTrips(t *testing.T) {
	for level := range wheelBuckets {
		for idx := range wheelBuckets[level] {
			l, i := decodeSlot(encodeSlot(level, idx))
			if l != level || i != idx {
				t.Fatalf("(%d,%d) round-tripped to (%d,%d)", level, idx, l, i)
			}
			if encodeSlot(level, idx) == 0 {
				t.Fatal("encoded slot must never be 0 (untracked)")
			}
		}
	}
}

func TestZeroAllocs(t *testing.T) {
	w := NewWheel[int, int](0)
	o := NewAccessOrder[int, int]()
	n1, n2 := newNode(1, 1<<35), newNode(2, 50)
	allocs := testing.AllocsPerRun(1000, func() {
		w.Add(n1)
		w.Remove(n1)
		o.Add(n2)
		o.Access(n2)
		o.Remove(n2)
	})
	if allocs != 0 {
		t.Fatalf("expected 0 allocs, got %v", allocs)
	}
}

func TestWheelPlacesOverdueNodesInTheCurrentTick(t *testing.T) {
	w := NewWheel[int, int](10 << 30)
	n := newNode(1, 2<<30) // due 8 ticks ago
	w.Add(n)
	if got := collect(w, 11<<30); !equal(got, []int{1}) {
		t.Fatalf("overdue node must expire on the next tick, got %v", got)
	}
}
