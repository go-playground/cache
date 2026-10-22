// Package writebuf provides bounded, lock-free, multi-producer single-consumer queues (Vyukov style).
//
// Unlike readbuf it never drops: Enqueue reports false when full and the caller must drain before retrying.
package writebuf

import (
	"runtime"
	"sync/atomic"

	"github.com/go-playground/cache/v2/internal/xruntime"
)

type slot[T any] struct {
	seq atomic.Uint64
	val T
}

// Queue is a bounded MPSC queue of T.
type Queue[T any] struct {
	tail atomic.Uint64
	_    [xruntime.CacheLine - 8]byte
	head uint64
	_    [xruntime.CacheLine - 8]byte
	mask uint64
	buf  []slot[T]
}

// New creates a queue holding at least capacity items, rounded up to a power of two.
func New[T any](capacity int) *Queue[T] {
	q := &Queue[T]{}
	q.init(pow2(capacity))
	return q
}

// pow2 rounds capacity up to a power of two, at least 2: with one slot, a drained slot's sequence would equal the next
// producer position, letting a second Enqueue overwrite an unread item.
func pow2(capacity int) uint64 {
	n := uint64(2)
	for n < uint64(capacity) {
		n <<= 1
	}
	return n
}

func (q *Queue[T]) init(n uint64) {
	q.mask, q.buf = n-1, make([]slot[T], n)
	for i := range q.buf {
		q.buf[i].seq.Store(uint64(i))
	}
}

// Enqueue adds v, reporting false if the queue is full. Safe for concurrent producers.
func (q *Queue[T]) Enqueue(v T) bool {
	ok, _ := q.enqueue(v)
	return ok
}

// enqueue adds v, reporting whether it succeeded and whether it had to retry because of a concurrent producer.
func (q *Queue[T]) enqueue(v T) (ok, contended bool) {
	pos := q.tail.Load()
	for {
		s := &q.buf[pos&q.mask]
		seq := s.seq.Load()
		switch diff := int64(seq) - int64(pos); {
		case diff == 0:
			if q.tail.CompareAndSwap(pos, pos+1) {
				s.val = v
				s.seq.Store(pos + 1)
				return true, contended
			}
			contended = true
			pos = q.tail.Load()
		case diff < 0:
			return false, contended
		default:
			contended = true
			pos = q.tail.Load()
		}
	}
}

// Dequeue removes the oldest published item. Must only be called by one consumer at a time.
func (q *Queue[T]) Dequeue() (v T, ok bool) {
	s := &q.buf[q.head&q.mask]
	if s.seq.Load() != q.head+1 {
		return v, false
	}
	v = s.val
	var zero T
	s.val = zero
	s.seq.Store(q.head + q.mask + 1)
	q.head++
	return v, true
}

// Striped is a set of per-processor queues, so concurrent producers rarely contend on the same tail.
//
// It starts with a single queue and doubles, up to one queue per processor, only when producers actually collide.
// Items from different queues may be dequeued in any order relative to each other, and a goroutine migrating between
// processors may see its own items reordered; consumers must tolerate reordering.
type Striped[T any] struct {
	stripes    atomic.Pointer[[]*paddedQueue[T]]
	growing    atomic.Bool
	perStripe  uint64
	maxStripes int
}

type paddedQueue[T any] struct {
	Queue[T]
	_ [xruntime.CacheLine]byte
}

// NewStriped creates one queue of perStripe capacity, rounded up to a power of two, that may grow to
// nextPow2(GOMAXPROCS) queues.
func NewStriped[T any](perStripe int) *Striped[T] {
	n := 1
	for n < runtime.GOMAXPROCS(0) {
		n <<= 1
	}
	s := &Striped[T]{perStripe: pow2(perStripe), maxStripes: n}
	stripes := []*paddedQueue[T]{s.newQueue()}
	s.stripes.Store(&stripes)
	return s
}

func (s *Striped[T]) newQueue() *paddedQueue[T] {
	q := &paddedQueue[T]{}
	q.init(s.perStripe)
	return q
}

// Enqueue adds v to the calling processor's queue, reporting false if it is full. Safe for concurrent producers.
func (s *Striped[T]) Enqueue(v T) bool {
	stripes := *s.stripes.Load()
	ok, contended := stripes[xruntime.Pid()&(len(stripes)-1)].enqueue(v)
	if contended {
		s.grow(len(stripes))
	}
	return ok
}

// grow doubles the queues after contention was observed with seen queues. Existing queues are carried over, so items
// enqueued through the previous slice are still drained.
func (s *Striped[T]) grow(seen int) {
	if seen >= s.maxStripes || !s.growing.CompareAndSwap(false, true) {
		return
	}
	defer s.growing.Store(false)
	cur := *s.stripes.Load()
	if len(cur) != seen {
		return
	}
	next := make([]*paddedQueue[T], 2*seen)
	copy(next, cur)
	for i := seen; i < len(next); i++ {
		next[i] = s.newQueue()
	}
	s.stripes.Store(&next)
}

// Stripes returns the current number of queues.
func (s *Striped[T]) Stripes() int {
	return len(*s.stripes.Load())
}

// Drain dequeues the published items of every queue, calling fn for each, at most one queue's capacity per queue so
// producers refilling a queue as fast as it empties cannot keep the caller, who holds the policy lock, draining
// forever. Items left behind are taken by the next drain. One consumer at a time.
func (s *Striped[T]) Drain(fn func(T)) {
	for _, q := range *s.stripes.Load() {
		for range len(q.buf) {
			v, ok := q.Dequeue()
			if !ok {
				break
			}
			fn(v)
		}
	}
}
