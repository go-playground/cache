package core

import (
	"runtime"

	optionext "github.com/go-playground/pkg/v5/values/option"

	"github.com/go-playground/cache/v2/internal/node"
)

// Engine modes. The mode is chosen once, when the cache is built, from the number of processors available.
//
// With few processors goroutines rarely run in parallel, so lock contention is rare while the concurrent machinery
// (lock-free reads that force an allocation per write, read and write buffers, deferred maintenance) still costs on
// every operation. The direct mode instead guards everything with one mutex and applies the policy immediately,
// updating existing entries in place. Both modes are safe for any number of goroutines and behave identically.
const (
	modeAuto uint8 = iota
	modeDirect
	modeConcurrent
)

// directMaxProcs is the most processors for which the direct mode is chosen automatically.
const directMaxProcs = 1

// modeForTesting forces a mode for engines built afterwards when not modeAuto. Only for tests and benchmarks.
var modeForTesting = modeAuto

// SetModeForTesting forces every engine built afterwards into the direct (true) or concurrent (false) mode, returning
// a func restoring automatic selection. Only for tests and benchmarks, never concurrently with building caches.
func SetModeForTesting(direct bool) (restore func()) {
	modeForTesting = modeConcurrent
	if direct {
		modeForTesting = modeDirect
	}
	return func() { modeForTesting = modeAuto }
}

func chooseDirect() bool {
	switch modeForTesting {
	case modeDirect:
		return true
	case modeConcurrent:
		return false
	}
	return runtime.GOMAXPROCS(0) <= directMaxProcs
}

// Direct reports whether the engine runs in the direct mode.
func (e *Engine[K, V]) Direct() bool {
	return e.direct
}

func (e *Engine[K, V]) getDirect(h uint64, k K) optionext.Option[V] {
	e.mu.Lock()
	n := e.table.Get(h, k)
	if n == nil || !e.liveDirect(n) {
		evs := e.takePending()
		e.mu.Unlock()
		e.recordMiss()
		e.Notify(evs)
		return optionext.None[V]()
	}
	e.policy.Access(n)
	v := n.Value
	e.mu.Unlock()
	e.recordHit()
	return optionext.Some(v)
}

// liveDirect reports whether n is unexpired, reclaiming it immediately if it expired and extending its expiry when
// expiring after access. Requires mu.
func (e *Engine[K, V]) liveDirect(n *node.Node[K, V]) bool {
	if e.expiryMode == expireNever {
		return true
	}
	now := e.now()
	if n.IsExpired(now) {
		e.policy.Remove(n)
		n.SetInPolicy(false)
		e.unmap(n, CauseExpired)
		return false
	}
	if e.expiryMode == expireAfterAccess {
		n.SetExpiresAt(addSaturating(now, e.ttl))
	}
	return true
}

func (e *Engine[K, V]) peekDirect(h uint64, k K) optionext.Option[V] {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := e.table.Get(h, k)
	if n == nil || n.IsExpired(e.now()) {
		return optionext.None[V]()
	}
	return optionext.Some(n.Value)
}

// setDirect maps k to v, updating an existing entry of the same weight in place so no allocation is needed. The
// user's weigher and expiry function run before taking the lock.
func (e *Engine[K, V]) setDirect(h uint64, k K, v V) Evictions[K, V] {
	w := e.weigh(k, v)
	var now, at int64
	if e.expiryMode != expireNever {
		now = e.now()
		at = e.expiresAt(now, k, v)
	}
	if e.stats != nil {
		e.stats.Add(statSets, 1)
	}
	e.mu.Lock()
	if n := e.table.Get(h, k); n != nil && n.Weight == w && n.InPolicy() {
		n.Value = v
		if e.expiryMode != expireNever {
			n.SetExpiresAt(at)
		}
		e.policy.Update(n, n, e.onEvictFn)
	} else {
		n := node.New(k, v, h, w)
		if e.expiryMode != expireNever {
			n.SetExpiresAt(at)
		}
		e.publishDirect(n)
	}
	return e.finishDirectAt(now)
}

// insertDirect publishes a prepared node. Used by GetOrLoad, whose node is built before its own locks are taken.
func (e *Engine[K, V]) insertDirect(n *node.Node[K, V]) Evictions[K, V] {
	if e.stats != nil {
		e.stats.Add(statSets, 1)
	}
	e.mu.Lock()
	e.publishDirect(n)
	return e.finishDirect()
}

// insertDirectIf is insertDirect only if n.Key still maps to exactly expected.
func (e *Engine[K, V]) insertDirectIf(expected, n *node.Node[K, V]) (Evictions[K, V], bool) {
	e.mu.Lock()
	// Checked under the lock: an in-place Set can revive the expected node without changing its pointer.
	if e.table.Get(n.Hash, n.Key) != expected || !e.replaceable(expected) {
		e.mu.Unlock()
		return nil, false
	}
	if e.stats != nil {
		e.stats.Add(statSets, 1)
	}
	e.publishDirect(n)
	return e.finishDirect(), true
}

// publishDirect maps n and applies it to the policy immediately. Requires mu.
func (e *Engine[K, V]) publishDirect(n *node.Node[K, V]) {
	old := e.table.Store(n)
	if old == nil {
		e.add(n)
		return
	}
	old.Retire()
	e.update(old, n)
}

// finishDirect runs a rate-limited expiry sweep, then releases mu returning pending notifications.
func (e *Engine[K, V]) finishDirect() Evictions[K, V] {
	var now int64
	if e.expiryMode != expireNever {
		now = e.now()
	}
	return e.finishDirectAt(now)
}

// finishDirectAt is finishDirect reusing a clock reading the caller already took; a slightly earlier instant only
// makes the sweep more conservative.
func (e *Engine[K, V]) finishDirectAt(now int64) Evictions[K, V] {
	if e.expiryMode != expireNever {
		e.drainNow = now
		e.sweep()
	}
	evs := e.takePending()
	e.mu.Unlock()
	return evs
}

func (e *Engine[K, V]) removeDirect(h uint64, k K) {
	e.mu.Lock()
	if old := e.table.Delete(h, k); old != nil {
		old.Retire()
		e.remove(old)
	}
	e.mu.Unlock()
}
