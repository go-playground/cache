package lfu

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/go-playground/cache/v2/internal/node"
)

// TestInPlaceUpdateMatchesReplacement checks that updating a node in place, Update(n, n), behaves exactly like
// replacing it with a fresh node of the same weight: the same keys are evicted in the same order.
func TestInPlaceUpdateMatchesReplacement(t *testing.T) {
	for _, capacity := range []uint64{1, 3, 10, 64} {
		for seed := range uint64(5) {
			inPlace, replaced := New[int, int](capacity, 7), New[int, int](capacity, 7)
			inNodes, reNodes := map[int]*node.Node[int, int]{}, map[int]*node.Node[int, int]{}
			var inEvicted, reEvicted []int
			inEvict := func(n *node.Node[int, int]) { inEvicted = append(inEvicted, n.Key); delete(inNodes, n.Key) }
			reEvict := func(n *node.Node[int, int]) { reEvicted = append(reEvicted, n.Key); delete(reNodes, n.Key) }
			rng := rand.New(rand.NewPCG(seed, capacity))
			for range 3000 {
				k := rng.IntN(int(capacity) * 3)
				h := uint64(k)*0x9E3779B97F4A7C15 + 1
				switch n, ok := inNodes[k]; {
				case !ok:
					a, b := node.New(k, k, h, 1), node.New(k, k, h, 1)
					inNodes[k], reNodes[k] = a, b
					inPlace.Add(a, inEvict)
					replaced.Add(b, reEvict)
				case rng.IntN(2) == 0:
					inPlace.Access(n)
					replaced.Access(reNodes[k])
				default:
					inPlace.Update(n, n, inEvict)
					old, fresh := reNodes[k], node.New(k, k, h, 1)
					fresh.Meta.Store(old.Meta.Load())
					reNodes[k] = fresh
					replaced.Update(old, fresh, reEvict)
				}
				if !slices.Equal(inEvicted, reEvicted) || inPlace.Weight() != replaced.Weight() {
					t.Fatalf("capacity %d seed %d: in-place evicted %v (weight %d), replacement evicted %v (weight %d)",
						capacity, seed, inEvicted, inPlace.Weight(), reEvicted, replaced.Weight())
				}
			}
		}
	}
}
