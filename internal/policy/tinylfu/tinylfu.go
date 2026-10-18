// Package tinylfu implements the W-TinyLFU eviction policy from "TinyLFU: A Highly Efficient Cache Admission Policy"
// (Einziger et al.), as refined by Caffeine.
//
// New entries enter a small LRU admission window. Entries leaving the window become candidates for the main space,
// a segmented LRU of probation and protected queues, and are admitted only if a count-min sketch estimates them to be
// more popular than the probation victim they would displace. Optionally a hill climber resizes the window to suit
// the workload's balance of recency and frequency.
package tinylfu

import (
	"math/rand/v2"

	"github.com/go-playground/cache/v2/internal/deque"
	"github.com/go-playground/cache/v2/internal/node"
	"github.com/go-playground/cache/v2/internal/policy"
	"github.com/go-playground/cache/v2/internal/sketch"
)

// Queue identifiers stored in node.Node.Slot().
const (
	window uint32 = iota
	probation
	protected
)

const (
	// hashFloodThreshold is the candidate frequency from which a losing candidate is still admitted at random, so
	// an attacker cannot pin a victim in place by inflating its frequency through hash collisions.
	hashFloodThreshold = 6
	// initialSketch caps the entries the sketch is initially sized for; it grows with the entry count.
	initialSketch = 1 << 10
)

var _ policy.Policy[int, int] = (*Policy[int, int])(nil)

// Policy is a weighted W-TinyLFU policy. The front of each queue is the most recently used node, the back the
// least.
//
// Not safe for concurrent use; see policy.Policy.
type Policy[K comparable, V any] struct {
	queues       [3]deque.Deque[K, V]
	weights      [3]uint64
	total        uint64
	count        int
	capacity     uint64
	windowTarget float64
	windowMax    uint64
	mainMax      uint64
	protectedMax uint64
	sketch       *sketch.Sketch
	sketchCap    int
	rnd          func() uint32
	adaptive     bool
	climber      climber
}

// New returns a W-TinyLFU policy retaining at most capacity weight. The admission window starts at 1% of capacity
// and the protected queue is 80% of the main space. If adaptive the window is resized by a hill climber, up to 80%
// of capacity.
func New[K comparable, V any](capacity uint64, adaptive bool) *Policy[K, V] {
	p := &Policy[K, V]{
		capacity: capacity,
		sketch:   newSketch(capacity),
		rnd:      rand.Uint32,
		adaptive: adaptive,
		climber:  newClimber(capacity),
	}
	for q := range p.queues {
		p.queues[q] = deque.New[K, V]()
	}
	p.sketchCap = p.sketch.Capacity()
	p.resetWindow()
	return p
}

func newSketch(capacity uint64) *sketch.Sketch {
	return sketch.New(int(min(capacity, initialSketch)))
}

// Add records a miss for n and tracks it in the window, then evicts until within capacity. Window overflow moves to
// probation, where each such candidate either displaces the least recently used probation node or is evicted.
func (p *Policy[K, V]) Add(n *node.Node[K, V], evict policy.EvictFunc[K, V]) {
	p.growSketch()
	p.sketch.Increment(n.Hash)
	p.record(false)
	if p.rejects(n) {
		evict(n)
		p.evictEntries(evict)
		return
	}
	p.push(n, window)
	p.evictEntries(evict)
}

// Access records a hit for n, promoting it to protected if it was in probation.
func (p *Policy[K, V]) Access(n *node.Node[K, V]) {
	p.sketch.Increment(n.Hash)
	p.record(true)
	p.reorder(n)
	p.demoteProtected()
	p.drainWindow()
}

// Update replaces old with n in old's queue and records a hit for n, then evicts until within capacity.
func (p *Policy[K, V]) Update(old, n *node.Node[K, V], evict policy.EvictFunc[K, V]) {
	n.SetSlot(old.Slot())
	p.queues[n.Slot()].Replace(old, n)
	p.weights[n.Slot()] = p.weights[n.Slot()] - uint64(old.Weight) + uint64(n.Weight)
	p.total = p.total - uint64(old.Weight) + uint64(n.Weight)
	p.sketch.Increment(n.Hash)
	p.record(true)
	if p.rejects(n) {
		p.unlink(n)
		evict(n)
	} else {
		p.reorder(n)
	}
	p.evictEntries(evict)
}

// Remove stops tracking n.
func (p *Policy[K, V]) Remove(n *node.Node[K, V]) {
	p.unlink(n)
}

// Clear stops tracking every node, calling fn for each, window first, then probation and protected, each from most
// to least recently used. The window size is reset; the frequency sketch is kept.
func (p *Policy[K, V]) Clear(fn func(n *node.Node[K, V])) {
	for q := range p.queues {
		p.queues[q].Clear(fn)
	}
	p.weights = [3]uint64{}
	p.total, p.count = 0, 0
	p.climber.reset()
	p.resetWindow()
}

// Weight returns the total weight of the tracked nodes.
func (p *Policy[K, V]) Weight() uint64 {
	return p.total
}

func (p *Policy[K, V]) rejects(n *node.Node[K, V]) bool {
	return p.capacity == 0 || uint64(n.Weight) > p.capacity
}

func (p *Policy[K, V]) growSketch() {
	if p.count+1 <= p.sketchCap {
		return
	}
	p.sketch.EnsureCapacity(p.count + 1)
	p.sketchCap = p.sketch.Capacity()
}

func (p *Policy[K, V]) record(hit bool) {
	if !p.adaptive {
		return
	}
	amount := p.climber.record(hit)
	if amount == 0 {
		return
	}
	limit := maxWindow(p.capacity)
	p.windowTarget = min(max(p.windowTarget+amount, 0), float64(limit))
	p.setWindow(min(uint64(p.windowTarget), limit))
	if amount > 0 {
		p.growWindow()
	}
}

// windowTransferQuota bounds the weight moved into the window per adjustment, as Caffeine does.
const windowTransferQuota = 1000

// growWindow fills a grown window from the least recently used end of the main region, probation first, as Caffeine
// does, so a larger window takes effect immediately rather than only as new entries arrive. A shrunk window drains
// into probation on the next Add or Access.
func (p *Policy[K, V]) growWindow() {
	for quota := uint64(windowTransferQuota); quota > 0; {
		n := p.queues[probation].Back()
		if n == nil {
			n = p.queues[protected].Back()
		}
		if n == nil || p.weights[window]+uint64(n.Weight) > p.windowMax {
			return
		}
		p.move(n, window)
		quota -= min(quota, uint64(n.Weight))
	}
}

func (p *Policy[K, V]) resetWindow() {
	w := p.capacity / 100
	p.windowTarget = float64(w)
	p.setWindow(w)
}

func (p *Policy[K, V]) setWindow(w uint64) {
	p.windowMax = w
	p.mainMax = p.capacity - w
	p.protectedMax = percent(p.mainMax, 80)
}

// maxWindow is the largest window the climber may choose, 80% of capacity.
func maxWindow(capacity uint64) uint64 {
	return percent(capacity, 80)
}

// percent returns floor(v*pct/100) without overflowing.
func percent(v, pct uint64) uint64 {
	return v/100*pct + v%100*pct/100
}

// reorder marks n as most recently used in its queue, promoting it from probation to protected.
func (p *Policy[K, V]) reorder(n *node.Node[K, V]) {
	if n.Slot() == probation {
		p.move(n, protected)
		return
	}
	p.queues[n.Slot()].MoveToFront(n)
}

// evictEntries moves overflow into probation, then evicts until within capacity. Candidates from the window form the
// front of probation and are tried oldest first against the probation victim at the back.
func (p *Policy[K, V]) evictEntries(evict policy.EvictFunc[K, V]) {
	p.demoteProtected()
	candidate := p.drainWindow()
	for p.total > p.capacity {
		victim := p.victim()
		if candidate == nil || candidate == victim {
			candidate = p.nextCandidate(candidate)
			p.evict(victim, evict)
			continue
		}
		next := p.nextCandidate(candidate)
		if p.admit(candidate, victim) {
			p.evict(victim, evict)
		} else {
			p.evict(candidate, evict)
		}
		candidate = next
	}
}

func (p *Policy[K, V]) nextCandidate(candidate *node.Node[K, V]) *node.Node[K, V] {
	if candidate == nil {
		return nil
	}
	return candidate.Prev()
}

// victim returns the least recently used node of probation, else protected, else the window.
func (p *Policy[K, V]) victim() *node.Node[K, V] {
	if v := p.queues[probation].Back(); v != nil {
		return v
	}
	if v := p.queues[protected].Back(); v != nil {
		return v
	}
	return p.queues[window].Back()
}

// admit reports whether candidate should displace victim: if it is more popular, or at random one time in 128 when
// it is at least warm, so popular victims cannot be pinned by hash flooding.
func (p *Policy[K, V]) admit(candidate, victim *node.Node[K, V]) bool {
	cf, vf := p.sketch.Frequency(candidate.Hash), p.sketch.Frequency(victim.Hash)
	if cf > vf {
		return true
	}
	if cf < hashFloodThreshold {
		return false
	}
	return p.rnd()&127 == 0
}

func (p *Policy[K, V]) demoteProtected() {
	for p.weights[protected] > p.protectedMax {
		p.move(p.queues[protected].Back(), probation)
	}
}

// drainWindow moves window overflow to the front of probation, returning the first node moved or nil.
func (p *Policy[K, V]) drainWindow() *node.Node[K, V] {
	var first *node.Node[K, V]
	for p.weights[window] > p.windowMax {
		n := p.queues[window].Back()
		p.move(n, probation)
		if first == nil {
			first = n
		}
	}
	return first
}

func (p *Policy[K, V]) evict(n *node.Node[K, V], evict policy.EvictFunc[K, V]) {
	p.unlink(n)
	evict(n)
}

func (p *Policy[K, V]) move(n *node.Node[K, V], q uint32) {
	p.unlink(n)
	p.push(n, q)
}

func (p *Policy[K, V]) push(n *node.Node[K, V], q uint32) {
	n.SetSlot(q)
	p.queues[q].PushFront(n)
	p.weights[q] += uint64(n.Weight)
	p.total += uint64(n.Weight)
	p.count++
}

func (p *Policy[K, V]) unlink(n *node.Node[K, V]) {
	p.queues[n.Slot()].Remove(n)
	p.weights[n.Slot()] -= uint64(n.Weight)
	p.total -= uint64(n.Weight)
	p.count--
}
