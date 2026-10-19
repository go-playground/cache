// Package core is the policy-agnostic concurrent cache engine.
//
// Reads are lock-free: a table lookup, an expiry check and either an inline atomic access (S3-FIFO) or a lossy
// buffered read event. Writes update the table immediately and enqueue a task; a single goroutine at a time applies
// buffered read events and tasks to the policy under the policy lock (maintenance), amortised onto callers via
// TryLock so no background goroutine is needed.
//
// Lock order is policy lock → table bucket lock. Writers never hold a bucket lock while acquiring the policy lock,
// and no user code (weighers, expiry functions, eviction callbacks) ever runs under the policy lock.
package core

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	timeext "github.com/go-playground/pkg/v5/time"
	optionext "github.com/go-playground/pkg/v5/values/option"

	"github.com/go-playground/cache/v2/internal/counter"
	"github.com/go-playground/cache/v2/internal/node"
	"github.com/go-playground/cache/v2/internal/policy"
	"github.com/go-playground/cache/v2/internal/readbuf"
	"github.com/go-playground/cache/v2/internal/table"
	"github.com/go-playground/cache/v2/internal/writebuf"
	"github.com/go-playground/cache/v2/internal/xhash"
	"github.com/go-playground/cache/v2/internal/xruntime"
)

const (
	drainIdle uint32 = iota
	drainRequired
	drainProcessing
)

// maxDrainRounds bounds how many consecutive maintenance rounds one caller performs, so a caller is never trapped
// draining for others under sustained writes. Remaining work is picked up by the next caller.
const maxDrainRounds = 4

// writeStripeSize is each write buffer stripe's capacity; a full stripe makes its writer drain synchronously. Smaller
// stripes (32, 64) measured 15-40% slower on concurrent writes, and stripes are only added under contention.
const writeStripeSize = 128

// Sweep pacing: at most one sweep per sweepInterval of maintenance, each scanning sweepBuckets root buckets per round
// for up to sweepMaxRounds rounds.
const (
	sweepInterval  = int64(time.Millisecond)
	sweepBuckets   = 32
	sweepMaxRounds = 8
)

// accessGranularity bounds how often expire-after-access readers write a node's expiry, avoiding a shared write on
// every read of a hot key at the cost of up to this much expiry precision.
const accessGranularity = int64(time.Millisecond)

type taskKind uint8

const (
	taskAdd taskKind = iota
	taskUpdate
	taskRemove
	// taskExpire is a remove for an entry a reader found expired and already unmapped, notified as CauseExpired.
	taskExpire
)

type task[K comparable, V any] struct {
	n, old *node.Node[K, V]
	kind   taskKind
}

// Evicted is an eviction pending notification.
type Evicted[K comparable, V any] struct {
	Key   K
	Value V
	Cause Cause
}

// Evictions are notifications collected under the policy lock, delivered after it is released.
type Evictions[K comparable, V any] []Evicted[K, V]

// Node is a cache entry prepared by NewNode for Insert.
type Node[K comparable, V any] = node.Node[K, V]

// Engine is the concurrent cache engine.
type Engine[K comparable, V any] struct {
	hasher   xhash.Hasher[K]
	table    *table.Table[K, V]
	policy   policy.Policy[K, V]
	inline   policy.InlineAccessor[K, V]
	reads    *readbuf.Buffer[node.Node[K, V]]
	writes   *writebuf.Striped[task[K, V]]
	capacity uint64
	weigher  func(K, V) uint32
	direct   bool

	expiryMode        expiryMode
	ttl               int64
	accessGranularity int64
	expireFn          func(K, V) time.Duration
	now               func() int64

	onEviction func(K, V, Cause)
	stats      *counter.Set

	onEvictFn policy.EvictFunc[K, V]
	onSweepFn func(*node.Node[K, V])
	onReadFn  func(*node.Node[K, V])
	applyFn   func(task[K, V])

	// Everything above is read-mostly and read by every operation; everything below is written during maintenance.
	// The padding keeps them on separate cache lines so maintenance does not invalidate readers' cached copies.
	_ [xruntime.CacheLine]byte

	// mu serialises all eviction bookkeeping: the policy and every call into it, draining the read and write
	// buffers (making the holder their single consumer), each node's slot and in-policy flag, and every field below.
	// In the direct mode it also guards every table access and in-place value update.
	mu     sync.Mutex
	status atomic.Uint32
	_      [xruntime.CacheLine - 12]byte

	pending   Evictions[K, V]
	evictions uint64
	drainNow  int64

	// Sweep state, owned under mu.
	sweepCursor  uint64
	nextSweep    int64
	sweepSeen    int
	sweepExpired int
}

// Statistic indexes within the engine's counter.Set.
const (
	statHits = iota
	statMisses
	statSets
	statLoads
	statLoadErrors
)

// NewEngine builds an engine from cfg.
func NewEngine[K comparable, V any](cfg Config[K, V]) *Engine[K, V] {
	if cfg.newPolicy == nil {
		panic("cache: builder must be created via its package's New or NewWeighted")
	}
	e := &Engine[K, V]{
		hasher:     xhash.New[K](),
		table:      nil,
		policy:     cfg.newPolicy(cfg.capacity),
		capacity:   cfg.capacity,
		weigher:    cfg.weigher,
		expiryMode: cfg.expiryMode,
		ttl:        int64(cfg.ttl),
		// Readers skip rewriting an expiry that would move by less than this; bounded by the TTL so short TTLs stay
		// exact instead of expiring early.
		accessGranularity: min(accessGranularity, int64(cfg.ttl)/8),
		expireFn:          cfg.expireFn,
		now:               clock,
		onEviction:        cfg.onEviction,
	}
	e.direct = chooseDirect()
	e.table = newTable(cfg, e.direct)
	if !e.direct {
		e.writes = writebuf.NewStriped[task[K, V]](writeStripeSize)
	}
	if e.direct {
		// Everything runs under mu with no buffering.
	} else if inline, ok := e.policy.(policy.InlineAccessor[K, V]); ok {
		e.inline = inline
	} else {
		e.reads = readbuf.New[node.Node[K, V]]()
	}
	if cfg.recordStats {
		e.stats = counter.NewSet()
	}
	e.onEvictFn = e.onEvict
	e.onSweepFn = e.onSweep
	e.onReadFn = e.onRead
	e.applyFn = e.apply
	return e
}

func clock() int64 {
	return int64(timeext.NewInstant())
}

// maxReservedEntries bounds how much of the index is reserved at build. Capacities up to it, which covers nearly all
// caches, get their whole index up front and never resize. Larger ones start at this size and grow towards their
// exact bound in a few doublings, so an enormous capacity used to mean "practically unbounded" does not reserve
// gigabytes (2^30 entries would be 18 GB) for entries that may never arrive.
const maxReservedEntries = 1 << 20

// newTable sizes the table for unweighted caches, whose capacity is an entry count, for capacity up front (see
// maxReservedEntries) and bounds its growth to exactly what capacity needs. Weighted caches cannot know their entry
// count, so their table starts small and grows on demand. The direct mode serialises every operation under its own
// mutex, so its table skips bucket locking.
func newTable[K comparable, V any](cfg Config[K, V], direct bool) *table.Table[K, V] {
	hint, bound := 0, 0
	if cfg.weigher == nil {
		bound = int(min(cfg.capacity, math.MaxInt32))
		hint = min(bound, maxReservedEntries)
	}
	if direct {
		return table.NewUnsynchronized[K, V](hint, bound)
	}
	return table.NewBounded[K, V](hint, bound)
}

// SetClockForTesting replaces the monotonic clock. Only for tests, before any use of the engine.
func (e *Engine[K, V]) SetClockForTesting(now func() int64) {
	e.now = now
}

// Hash returns the hash of k used by every hashed method.
func (e *Engine[K, V]) Hash(k K) uint64 {
	return e.hasher.Hash(k)
}

// Get returns the live value for k.
func (e *Engine[K, V]) Get(k K) optionext.Option[V] {
	return e.GetHashed(e.hasher.Hash(k), k)
}

// GetHashed is Get with a precomputed hash.
func (e *Engine[K, V]) GetHashed(h uint64, k K) optionext.Option[V] {
	if e.direct {
		return e.getDirect(h, k)
	}
	n := e.table.Get(h, k)
	if n == nil {
		e.recordMiss()
		return optionext.None[V]()
	}
	if e.expiryMode != expireNever && !e.touch(n) {
		e.recordMiss()
		return optionext.None[V]()
	}
	e.recordAccess(n)
	e.recordHit()
	return optionext.Some(n.Value)
}

// PeekHashed returns the live value for k without recording an access or statistics.
func (e *Engine[K, V]) PeekHashed(h uint64, k K) optionext.Option[V] {
	if e.direct {
		return e.peekDirect(h, k)
	}
	n := e.table.Get(h, k)
	if n == nil || n.IsExpired(e.now()) {
		return optionext.None[V]()
	}
	return optionext.Some(n.Value)
}

// touch reports whether n is unexpired, extending its expiry when expiring after access.
func (e *Engine[K, V]) touch(n *node.Node[K, V]) bool {
	now := e.now()
	if n.IsExpired(now) {
		e.expireOnRead(n)
		return false
	}
	if e.expiryMode == expireAfterAccess {
		if at := addSaturating(now, e.ttl); at-n.ExpiresAt() >= e.accessGranularity {
			n.SetExpiresAt(at)
		}
	}
	return true
}

// expireOnRead reclaims an entry a reader found expired, so hot expired keys are freed immediately rather than by the
// sweep, and every later read misses cheaply instead of requesting a drain. The pointer-equality delete makes a racing
// Set safe: if the key was already replaced nothing is deleted and the replacement's own task handles the old node.
func (e *Engine[K, V]) expireOnRead(n *node.Node[K, V]) {
	if !e.table.DeleteNode(n) {
		return
	}
	n.Retire()
	e.Notify(e.enqueue(task[K, V]{kind: taskExpire, n: n}))
}

func (e *Engine[K, V]) recordAccess(n *node.Node[K, V]) {
	if e.inline != nil {
		e.inline.InlineAccess(n)
		return
	}
	if e.reads.Add(n) == readbuf.Full {
		e.requestDrain()
	}
}

// Set maps k to v, replacing any existing value.
func (e *Engine[K, V]) Set(k K, v V) {
	e.Notify(e.SetHashed(e.hasher.Hash(k), k, v))
}

// SetHashed is Set with a precomputed hash, returning rather than delivering eviction notifications.
func (e *Engine[K, V]) SetHashed(h uint64, k K, v V) Evictions[K, V] {
	if e.direct {
		return e.setDirect(h, k, v)
	}
	return e.Insert(e.NewNode(h, k, v))
}

// NewNode prepares an entry for Insert, running the user's weigher and expiry function. Kept separate from Insert so
// callers holding their own locks can run user code before acquiring them.
func (e *Engine[K, V]) NewNode(h uint64, k K, v V) *Node[K, V] {
	n := node.New(k, v, h, e.weigh(k, v))
	if e.expiryMode != expireNever {
		n.SetExpiresAt(e.expiresAt(e.now(), k, v))
	}
	return n
}

// Insert publishes n, replacing any existing entry for its key, and returns rather than delivers eviction
// notifications so callers holding their own locks can deliver them after releasing those locks. Runs no user code.
func (e *Engine[K, V]) Insert(n *Node[K, V]) Evictions[K, V] {
	if e.direct {
		return e.insertDirect(n)
	}
	if e.stats != nil {
		e.stats.Add(statSets, 1)
	}
	old := e.table.Store(n)
	if old == nil {
		return e.enqueue(task[K, V]{kind: taskAdd, n: n})
	}
	old.Retire()
	return e.enqueue(task[K, V]{kind: taskUpdate, n: n, old: old})
}

// Mapped returns the node k currently maps to, expired or not, or nil. For use with InsertIf.
func (e *Engine[K, V]) Mapped(h uint64, k K) *Node[K, V] {
	if e.direct {
		e.mu.Lock()
		defer e.mu.Unlock()
	}
	return e.table.Get(h, k)
}

// InsertIf is Insert only if k still maps to exactly expected (nil meaning absent), as returned by Mapped, reporting
// whether it inserted. A value computed from an earlier lookup therefore never overwrites a newer write.
func (e *Engine[K, V]) InsertIf(expected, n *Node[K, V]) (Evictions[K, V], bool) {
	if e.direct {
		return e.insertDirectIf(expected, n)
	}
	if !e.table.StoreIf(expected, n) {
		return nil, false
	}
	if e.stats != nil {
		e.stats.Add(statSets, 1)
	}
	if expected == nil {
		return e.enqueue(task[K, V]{kind: taskAdd, n: n}), true
	}
	expected.Retire()
	return e.enqueue(task[K, V]{kind: taskUpdate, n: n, old: expected}), true
}

func (e *Engine[K, V]) weigh(k K, v V) uint32 {
	if e.weigher == nil {
		return 1
	}
	return max(e.weigher(k, v), 1)
}

func (e *Engine[K, V]) expiresAt(now int64, k K, v V) int64 {
	if e.expiryMode != expireCustom {
		return addSaturating(now, e.ttl)
	}
	d := e.expireFn(k, v)
	if d <= 0 {
		return 0
	}
	return addSaturating(now, int64(d))
}

// addSaturating adds a non-negative duration to a monotonic instant, saturating rather than wrapping to a negative
// (already expired) instant.
func addSaturating(now, d int64) int64 {
	if d > math.MaxInt64-now {
		return math.MaxInt64
	}
	return now + d
}

// Remove removes k if present.
func (e *Engine[K, V]) Remove(k K) {
	e.RemoveHashed(e.hasher.Hash(k), k)
}

// RemoveHashed is Remove with a precomputed hash.
func (e *Engine[K, V]) RemoveHashed(h uint64, k K) {
	if e.direct {
		e.removeDirect(h, k)
		return
	}
	old := e.table.Delete(h, k)
	if old == nil {
		return
	}
	old.Retire()
	e.Notify(e.enqueue(task[K, V]{kind: taskRemove, n: old}))
}

// Clear removes every entry present when Clear starts.
func (e *Engine[K, V]) Clear() {
	e.Notify(e.locked(func() {
		e.table.Range(func(n *node.Node[K, V]) bool {
			if e.table.DeleteNode(n) {
				n.Retire()
			}
			return true
		})
		e.policy.Clear(func(n *node.Node[K, V]) {
			n.SetInPolicy(false)
			n.Die()
		})
	}))
}

// Stats is a snapshot of the engine's statistics.
type Stats struct {
	Capacity   uint64
	Len        int
	Weight     uint64
	Hits       uint64
	Misses     uint64
	Sets       uint64
	Evictions  uint64
	Loads      uint64
	LoadErrors uint64
}

// Stats applies pending maintenance and returns cumulative statistics.
func (e *Engine[K, V]) Stats() Stats {
	s := Stats{Capacity: e.capacity}
	e.Notify(e.locked(func() {
		s.Len, s.Weight, s.Evictions = e.table.Len(), e.policy.Weight(), e.evictions
	}))
	if e.stats != nil {
		s.Hits, s.Misses, s.Sets = e.stats.Value(statHits), e.stats.Value(statMisses), e.stats.Value(statSets)
		s.Loads, s.LoadErrors = e.stats.Value(statLoads), e.stats.Value(statLoadErrors)
	}
	return s
}

// RecordLoad records a load and whether it failed.
func (e *Engine[K, V]) RecordLoad(failed bool) {
	if e.stats == nil {
		return
	}
	e.stats.Add(statLoads, 1)
	if failed {
		e.stats.Add(statLoadErrors, 1)
	}
}

func (e *Engine[K, V]) recordHit() {
	if e.stats != nil {
		e.stats.Add(statHits, 1)
	}
}

func (e *Engine[K, V]) recordMiss() {
	if e.stats != nil {
		e.stats.Add(statMisses, 1)
	}
}

// Drain synchronously applies all pending maintenance and reclaims every expired entry. Used by tests for
// determinism.
func (e *Engine[K, V]) Drain() {
	e.Notify(e.locked(func() {
		if e.expiryMode != expireNever {
			e.table.Range(func(n *node.Node[K, V]) bool {
				e.onSweepFn(n)
				return true
			})
		}
	}))
}

// Notify delivers eviction notifications. Must be called without holding any cache lock.
func (e *Engine[K, V]) Notify(evs Evictions[K, V]) {
	for _, ev := range evs {
		e.onEviction(ev.Key, ev.Value, ev.Cause)
	}
}

// locked blocks for the policy lock, runs maintenance then fn, and returns pending notifications. Like tryDrain it
// re-checks for drain requests made while it held the lock, so no request is stranded.
func (e *Engine[K, V]) locked(fn func()) Evictions[K, V] {
	e.mu.Lock()
	e.maintenance()
	if fn != nil {
		fn()
	}
	evs := e.takePending()
	e.mu.Unlock()
	if e.status.Load() == drainRequired {
		evs = evs.join(e.tryDrain())
	}
	return evs
}

func (e *Engine[K, V]) enqueue(t task[K, V]) Evictions[K, V] {
	var evs Evictions[K, V]
	for !e.writes.Enqueue(t) {
		evs = evs.join(e.locked(nil))
	}
	if e.status.Load() != drainRequired {
		e.status.Store(drainRequired)
	}
	return evs.join(e.tryDrain())
}

// join appends more, reusing more itself when evs is empty so the common single-batch case allocates nothing extra.
func (evs Evictions[K, V]) join(more Evictions[K, V]) Evictions[K, V] {
	if len(evs) == 0 {
		return more
	}
	return append(evs, more...)
}

// requestDrain asks for maintenance on behalf of reads, unless it is already in progress. It only writes the status
// on the idle→required transition, so readers repeatedly finding a full buffer share the status line read-only
// instead of bouncing it between cores.
func (e *Engine[K, V]) requestDrain() {
	switch e.status.Load() {
	case drainProcessing:
		return
	case drainIdle:
		e.status.CompareAndSwap(drainIdle, drainRequired)
	}
	e.Notify(e.tryDrain())
}

// tryDrain runs maintenance if the policy lock is free. If more work was requested while it held the lock it loops,
// so a request is never stranded between a holder's last check and its unlock, for at most maxDrainRounds rounds.
// A request still pending after that, which needs sustained writes racing the holder, is handed to a goroutine so it
// cannot be stranded until the next write: with a read-only workload afterwards nothing else would drain it.
func (e *Engine[K, V]) tryDrain() Evictions[K, V] {
	var evs Evictions[K, V]
	for i := range maxDrainRounds {
		if !e.mu.TryLock() {
			return evs
		}
		e.maintenance()
		evs = evs.join(e.takePending())
		e.mu.Unlock()
		if e.status.Load() != drainRequired {
			return evs
		}
		if i == maxDrainRounds-1 {
			go e.Notify(e.locked(nil))
		}
	}
	return evs
}

// maintenance must be called with mu held.
func (e *Engine[K, V]) maintenance() {
	e.status.Store(drainProcessing)
	if e.expiryMode != expireNever {
		e.drainNow = e.now()
	}
	// Reads first: a write's eviction must see accesses that happened before it, or a freshly read entry can be
	// chosen as the victim while its access is still buffered.
	if e.reads != nil {
		e.reads.Drain(e.onReadFn)
	}
	e.drainWrites()
	if e.expiryMode != expireNever {
		e.sweep()
	}
	e.status.CompareAndSwap(drainProcessing, drainIdle)
}

// sweep reclaims expired entries incrementally, Redis style. Expired entries are never returned regardless; sweeping
// only frees their capacity sooner. Checking an entry is a likely cache miss, so sweeps run at most once per
// sweepInterval and scan a bounded run of buckets from a persistent cursor, continuing for more rounds only while a
// round finds at least a quarter of its entries expired.
func (e *Engine[K, V]) sweep() {
	if e.drainNow < e.nextSweep {
		return
	}
	e.nextSweep = e.drainNow + sweepInterval
	for range sweepMaxRounds {
		e.sweepSeen, e.sweepExpired = 0, 0
		e.sweepCursor = e.table.Scan(e.sweepCursor, sweepBuckets, e.onSweepFn)
		if e.sweepSeen > 0 && e.sweepExpired*4 < e.sweepSeen {
			return
		}
	}
}

// onSweep reclaims n if it has expired. A node not yet tracked by the policy dies here and its pending add is skipped.
func (e *Engine[K, V]) onSweep(n *node.Node[K, V]) {
	e.sweepSeen++
	if !n.IsExpired(e.drainNow) || !n.IsAlive() {
		return
	}
	e.sweepExpired++
	if n.InPolicy() {
		e.policy.Remove(n)
		n.SetInPolicy(false)
	}
	e.unmap(n, CauseExpired)
}

func (e *Engine[K, V]) drainWrites() {
	if e.writes != nil {
		e.writes.Drain(e.applyFn)
	}
}

func (e *Engine[K, V]) apply(t task[K, V]) {
	switch t.kind {
	case taskAdd:
		e.add(t.n)
	case taskUpdate:
		e.update(t.old, t.n)
	case taskRemove:
		e.remove(t.n)
	case taskExpire:
		e.remove(t.n)
		e.evictions++
		if e.onEviction != nil {
			e.pending = append(e.pending, Evicted[K, V]{Key: t.n.Key, Value: t.n.Value, Cause: CauseExpired})
		}
	}
}

// add starts tracking n unless it was already removed or replaced, which happens when tasks for the same key from
// different goroutines are dequeued out of order.
func (e *Engine[K, V]) add(n *node.Node[K, V]) {
	if !n.IsAlive() {
		n.Die()
		return
	}
	if n.InPolicy() {
		return
	}
	n.SetInPolicy(true)
	e.policy.Add(n, e.onEvictFn)
}

func (e *Engine[K, V]) update(old, n *node.Node[K, V]) {
	if !old.InPolicy() {
		old.Die()
		e.add(n)
		return
	}
	if !n.IsAlive() {
		e.remove(old)
		return
	}
	n.SetInPolicy(true)
	old.SetInPolicy(false)
	e.policy.Update(old, n, e.onEvictFn)
	old.Die()
}

func (e *Engine[K, V]) remove(n *node.Node[K, V]) {
	if n.InPolicy() {
		e.policy.Remove(n)
		n.SetInPolicy(false)
	}
	n.Die()
}

func (e *Engine[K, V]) onRead(n *node.Node[K, V]) {
	if !n.InPolicy() || !n.IsAlive() {
		return
	}
	e.policy.Access(n)
}

// onEvict handles a node the policy has already unlinked.
func (e *Engine[K, V]) onEvict(n *node.Node[K, V]) {
	n.SetInPolicy(false)
	e.unmap(n, CauseSize)
}

// unmap removes n from the table if it is still mapped and records the eviction. When n is no longer mapped it was
// replaced or removed and a pending task for it will be skipped.
func (e *Engine[K, V]) unmap(n *node.Node[K, V], cause Cause) {
	if e.table.DeleteNode(n) {
		e.evictions++
		if e.onEviction != nil {
			e.pending = append(e.pending, Evicted[K, V]{Key: n.Key, Value: n.Value, Cause: cause})
		}
	}
	n.Die()
}

func (e *Engine[K, V]) takePending() Evictions[K, V] {
	evs := e.pending
	e.pending = nil
	return evs
}
