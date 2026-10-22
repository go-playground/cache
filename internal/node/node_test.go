package node

import (
	"testing"
	"unsafe"
)

// TestNodeIsCompact pins the entry size: K + V + 48 bytes, so int→int entries fill exactly one 64-byte allocation.
func TestNodeIsCompact(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("layout pinned for 64-bit platforms")
	}
	if got := unsafe.Sizeof(Node[int, int]{}); got != 64 {
		t.Fatalf("Node[int, int] is %d bytes, want 64", got)
	}
	if got := unsafe.Sizeof(Node[string, []byte]{}); got != 88 {
		t.Fatalf("Node[string, []byte] is %d bytes, want 88", got)
	}
}

func TestSlotAndInPolicyAreIndependent(t *testing.T) {
	n := New(1, 1, 1, 1)
	n.SetSlot(7)
	n.SetInPolicy(true)
	if n.Slot() != 7 || !n.InPolicy() {
		t.Fatalf("slot %d inPolicy %v", n.Slot(), n.InPolicy())
	}
	n.SetSlot(1<<31 - 1)
	if n.Slot() != 1<<31-1 || !n.InPolicy() {
		t.Fatal("setting the slot must not clear the in-policy flag")
	}
	n.SetInPolicy(false)
	if n.Slot() != 1<<31-1 || n.InPolicy() {
		t.Fatal("clearing the in-policy flag must not change the slot")
	}
}

func TestLifecycle(t *testing.T) {
	n := New(1, 1, 1, 1)
	if !n.IsAlive() || n.IsDead() {
		t.Fatal("new node must be alive")
	}
	n.Retire()
	if n.IsAlive() || n.IsDead() {
		t.Fatal("retired node is neither alive nor dead")
	}
	n.Die()
	n.Retire()
	if !n.IsDead() {
		t.Fatal("retiring a dead node must not resurrect it")
	}
}

func TestExpiry(t *testing.T) {
	n := New(1, 1, 1, 1)
	if n.IsExpired(1 << 62) {
		t.Fatal("expiry 0 means never")
	}
	n.SetExpiresAt(10)
	if n.IsExpired(9) || !n.IsExpired(10) || n.ExpiresAt() != 10 {
		t.Fatal("unexpected expiry")
	}
}
