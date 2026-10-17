package deque

import (
	"slices"
	"testing"

	"github.com/go-playground/cache/v2/internal/node"
)

func nodes(keys ...int) []*node.Node[int, int] {
	ns := make([]*node.Node[int, int], len(keys))
	for i, k := range keys {
		ns[i] = node.New(k, k, uint64(k), 1)
	}
	return ns
}

// keys returns the deque's keys front to back, verifying link symmetry and Len on the way.
func keys(t *testing.T, d *Deque[int, int]) []int {
	t.Helper()
	var out []int
	var prev *node.Node[int, int]
	for n := d.Front(); n != nil; n = n.Next(d.list) {
		if n.Prev(d.list) != prev {
			t.Fatalf("broken prev link at %d", n.Key)
		}
		out = append(out, n.Key)
		prev = n
	}
	if prev != d.Back() {
		t.Fatal("back does not match last node")
	}
	if len(out) != d.Len() {
		t.Fatalf("len %d but %d nodes linked", d.Len(), len(out))
	}
	return out
}

func expect(t *testing.T, d *Deque[int, int], want ...int) {
	t.Helper()
	if got := keys(t, d); !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestPushAndPop(t *testing.T) {
	d := New[int, int](node.PolicyList)
	ns := nodes(1, 2, 3)
	d.PushFront(ns[0])
	d.PushFront(ns[1])
	d.PushBack(ns[2])
	expect(t, &d, 2, 1, 3)
	if n := d.PopBack(); n != ns[2] {
		t.Fatal("PopBack returned wrong node")
	}
	expect(t, &d, 2, 1)
	d.PopBack()
	d.PopBack()
	if d.PopBack() != nil {
		t.Fatal("PopBack on empty deque must return nil")
	}
	expect(t, &d)
}

func TestRemoveFromEveryPosition(t *testing.T) {
	for i := range 3 {
		d := New[int, int](node.PolicyList)
		ns := nodes(1, 2, 3)
		for _, n := range ns {
			d.PushBack(n)
		}
		d.Remove(ns[i])
		want := slices.Delete([]int{1, 2, 3}, i, i+1)
		expect(t, &d, want...)
		if ns[i].Prev(node.PolicyList) != nil || ns[i].Next(node.PolicyList) != nil {
			t.Fatal("removed node must be fully unlinked")
		}
	}
}

func TestMoves(t *testing.T) {
	d := New[int, int](node.PolicyList)
	ns := nodes(1, 2, 3)
	for _, n := range ns {
		d.PushBack(n)
	}
	d.MoveToFront(ns[2])
	expect(t, &d, 3, 1, 2)
	d.MoveToFront(ns[2])
	expect(t, &d, 3, 1, 2)
	d.MoveToBack(ns[2])
	expect(t, &d, 1, 2, 3)
	d.MoveToBack(ns[2])
	expect(t, &d, 1, 2, 3)
}

func TestReplaceKeepsPosition(t *testing.T) {
	for i := range 3 {
		d := New[int, int](node.PolicyList)
		ns := nodes(1, 2, 3)
		for _, n := range ns {
			d.PushBack(n)
		}
		repl := nodes(9)[0]
		d.Replace(ns[i], repl)
		want := []int{1, 2, 3}
		want[i] = 9
		expect(t, &d, want...)
		if ns[i].Prev(node.PolicyList) != nil || ns[i].Next(node.PolicyList) != nil {
			t.Fatal("replaced node must be fully unlinked")
		}
	}
}

func TestAppendBack(t *testing.T) {
	a, b := New[int, int](node.PolicyList), New[int, int](node.PolicyList)
	for _, n := range nodes(1, 2) {
		a.PushBack(n)
	}
	for _, n := range nodes(3, 4) {
		b.PushBack(n)
	}
	a.AppendBack(&b)
	expect(t, &a, 1, 2, 3, 4)
	expect(t, &b)

	empty := New[int, int](node.PolicyList)
	empty.AppendBack(&a)
	expect(t, &empty, 1, 2, 3, 4)
	a.AppendBack(&b)
	expect(t, &a)
}

func TestClearVisitsAndUnlinksAll(t *testing.T) {
	d := New[int, int](node.PolicyList)
	ns := nodes(1, 2, 3)
	for _, n := range ns {
		d.PushBack(n)
	}
	var visited []int
	d.Clear(func(n *node.Node[int, int]) { visited = append(visited, n.Key) })
	if !slices.Equal(visited, []int{1, 2, 3}) {
		t.Fatalf("visited %v", visited)
	}
	expect(t, &d)
	for _, n := range ns {
		if n.Next(node.PolicyList) != nil || n.Prev(node.PolicyList) != nil {
			t.Fatal("cleared node still linked")
		}
	}
	d.Clear(nil)
}

func TestLinkSetsAreIndependent(t *testing.T) {
	policy, expiry := New[int, int](node.PolicyList), New[int, int](node.ExpiryList)
	ns := nodes(1, 2, 3)
	for _, n := range ns {
		policy.PushBack(n)
		expiry.PushFront(n)
	}
	expect(t, &policy, 1, 2, 3)
	expect(t, &expiry, 3, 2, 1)
	policy.Remove(ns[1])
	expect(t, &policy, 1, 3)
	expect(t, &expiry, 3, 2, 1)
}
