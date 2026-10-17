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

func pow2(capacity int) uint64 {
	n := uint64(1)
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
	pos := q.tail.Load()
	for {
		s := &q.buf[pos&q.mask]
		seq := s.seq.Load()
		switch diff := int64(seq) - int64(pos); {
		case diff == 0:
			if q.tail.CompareAndSwap(pos, pos+1) {
				s.val = v
				s.seq.Store(pos + 1)
				return true
			}
			pos = q.tail.Load()
		case diff < 0:
			return false
		default:
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
// Items from different producers may be dequeued in any order relative to each other, and a goroutine migrating
// between processors may see its own items reordered; consumers must tolerate reordering.
type Striped[T any] struct {
	stripes []paddedQueue[T]
	mask    uint32
}

type paddedQueue[T any] struct {
	Queue[T]
	_ [xruntime.CacheLine]byte
}

// NewStriped creates one queue of perStripe capacity per processor, rounded up to a power of two.
func NewStriped[T any](perStripe int) *Striped[T] {
	n := uint32(1)
	for n < uint32(runtime.GOMAXPROCS(0)) {
		n <<= 1
	}
	s := &Striped[T]{stripes: make([]paddedQueue[T], n), mask: n - 1}
	for i := range s.stripes {
		s.stripes[i].init(pow2(perStripe))
	}
	return s
}

// Enqueue adds v to the calling processor's queue, reporting false if it is full. Safe for concurrent producers.
func (s *Striped[T]) Enqueue(v T) bool {
	return s.stripes[uint32(xruntime.Pid())&s.mask].Enqueue(v)
}

// Drain dequeues every published item from every queue, calling fn for each. One consumer at a time.
func (s *Striped[T]) Drain(fn func(T)) {
	for i := range s.stripes {
		q := &s.stripes[i].Queue
		for {
			v, ok := q.Dequeue()
			if !ok {
				break
			}
			fn(v)
		}
	}
}
