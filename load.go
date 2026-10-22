package cache

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"

	resultext "github.com/go-playground/pkg/v5/values/result"

	"github.com/go-playground/cache/v2/internal/core"
	"github.com/go-playground/cache/v2/internal/xruntime"
)

// ErrLoaderExited is returned to GetOrLoad callers waiting on a loader that called runtime.Goexit, e.g. via
// testing.T.FailNow.
var ErrLoaderExited = errors.New("cache: loader exited via runtime.Goexit")

// LoadPanicError is returned to GetOrLoad callers that were waiting on a load that panicked. The goroutine that ran
// the loader re-panics with the original value.
type LoadPanicError struct {
	// Value is the value the loader panicked with.
	Value any
	// Stack is the loader's stack trace at the time of the panic.
	Stack []byte
}

// Error implements error.
func (e *LoadPanicError) Error() string {
	return fmt.Sprintf("cache: loader panicked: %v", e.Value)
}

// Loader loads the value for a key absent from the cache.
type Loader[K comparable, V any] func(ctx context.Context, key K) resultext.Result[V, error]

// GetOrLoad returns the value for key, loading and storing it on a miss.
//
//   - Concurrent callers missing the same key share a single load.
//   - Errors are returned to every waiting caller and never cached.
//   - A caller whose ctx is done stops waiting and returns ctx.Err() without affecting the load. If a load fails
//     because the loading caller's own ctx was done, the remaining waiters retry rather than inheriting that
//     cancellation.
//   - If key is Set, Removed or the cache Cleared while the load is in flight, the loaded value is returned but not
//     stored, so a stale load never overwrites a newer write or resurrects a removed key.
//   - If load panics, waiters receive a *LoadPanicError and the panic propagates in the loading caller. If it calls
//     runtime.Goexit, waiters receive ErrLoaderExited.
//
// load must not call GetOrLoad for the same key on the same cache: it would wait on itself.
func (c *Cache[K, V]) GetOrLoad(ctx context.Context, key K, load Loader[K, V]) resultext.Result[V, error] {
	e := c.engine()
	h := e.Hash(key)
	for {
		if v := e.GetHashed(h, key); v.IsSome() {
			return resultext.Ok[V, error](v.Unwrap())
		}
		res, retry := c.inflight().do(ctx, e, h, key, load)
		if !retry {
			return res
		}
	}
}

type call[K comparable, V any] struct {
	done           chan struct{}
	res            resultext.Result[V, error]
	expected       *core.Node[K, V]
	leaderCanceled bool
	invalidated    bool
}

type loadShard[K comparable, V any] struct {
	mu    sync.Mutex
	calls map[K]*call[K, V]
	_     [xruntime.CacheLine]byte
}

type inflight[K comparable, V any] struct {
	shards []loadShard[K, V]
	mask   uint64
	active atomic.Int64
}

func newInflight[K comparable, V any]() *inflight[K, V] {
	n := uint64(1)
	for n < uint64(runtime.GOMAXPROCS(0)) {
		n <<= 1
	}
	f := &inflight[K, V]{shards: make([]loadShard[K, V], n), mask: n - 1}
	for i := range f.shards {
		f.shards[i].calls = make(map[K]*call[K, V])
	}
	return f
}

func (f *inflight[K, V]) shard(h uint64) *loadShard[K, V] {
	return &f.shards[(h>>32)&f.mask]
}

// do joins or starts the load for key, reporting whether the caller should retry from the cache lookup.
func (f *inflight[K, V]) do(ctx context.Context, e *core.Engine[K, V], h uint64, key K, load Loader[K, V]) (resultext.Result[V, error], bool) {
	s := f.shard(h)
	s.mu.Lock()
	if existing, ok := s.calls[key]; ok {
		s.mu.Unlock()
		return wait(ctx, existing)
	}
	cl := &call[K, V]{done: make(chan struct{})}
	s.calls[key] = cl
	f.active.Add(1)
	s.mu.Unlock()

	// A previous leader may have stored the value between our miss and registering this call.
	if v := e.PeekHashed(h, key); v.IsSome() {
		cl.res = resultext.Ok[V, error](v.Unwrap())
		f.release(e, s, key, cl, nil, nil)
		return cl.res, false
	}
	// Whatever the key maps to now, nil or an expired entry, is what the loaded value may replace: a write that lands
	// during the load changes the mapping, and release then leaves it alone.
	cl.expected = e.Mapped(h, key)
	f.lead(ctx, e, s, h, key, cl, load)
	return cl.res, false
}

func wait[K comparable, V any](ctx context.Context, cl *call[K, V]) (resultext.Result[V, error], bool) {
	select {
	case <-cl.done:
		if cl.leaderCanceled && ctx.Err() == nil {
			return cl.res, true
		}
		return cl.res, false
	case <-ctx.Done():
		return resultext.Err[V](ctx.Err()), false
	}
}

// lead runs the loader, distinguishing a normal return, a panic and runtime.Goexit so waiters are always released.
func (f *inflight[K, V]) lead(ctx context.Context, e *core.Engine[K, V], s *loadShard[K, V], h uint64, key K, cl *call[K, V], load Loader[K, V]) {
	var (
		n                *core.Node[K, V]
		normal, panicked bool
		panicVal         any
		stack            []byte
	)
	defer func() {
		switch {
		case normal:
			return
		case panicked:
			cl.res = resultext.Err[V, error](&LoadPanicError{Value: panicVal, Stack: stack})
			f.release(e, s, key, cl, nil, load)
			panic(panicVal)
		default:
			cl.res = resultext.Err[V](ErrLoaderExited)
			f.release(e, s, key, cl, nil, load)
		}
	}()
	func() {
		defer func() {
			if normal {
				return
			}
			if r := recover(); r != nil {
				panicked, panicVal, stack = true, r, debug.Stack()
			}
		}()
		cl.res = load(ctx, key)
		// ctx.Err is user code too (custom contexts), so it stays inside the guard: a panic here must still release.
		cl.leaderCanceled = cl.res.IsErr() && ctx.Err() != nil
		if cl.res.IsOk() {
			// Runs the user's weigher and expiry function, so it must happen before taking the shard lock.
			n = e.NewNode(h, key, cl.res.Unwrap())
		}
		normal = true
	}()
	if !normal {
		return
	}
	f.release(e, s, key, cl, n, load)
}

// release stores n unless the call was invalidated or the key's mapping changed, then wakes waiters. loaded is nil
// when the loader did not run.
//
// Two guards keep a stale load from overwriting a newer write or resurrecting a removed key. Set, Remove and Clear
// invalidate under the shard lock before writing the table, covering writes that saw the call registered. A write
// that ran its invalidation just before the call was registered is caught by InsertIf: the store only happens if the
// key still maps to what it did before the load began and that mapping is absent or expired, so a write published at
// any point after the miss wins.
func (f *inflight[K, V]) release(e *core.Engine[K, V], s *loadShard[K, V], key K, cl *call[K, V], n *core.Node[K, V], loaded Loader[K, V]) {
	if loaded != nil {
		e.RecordLoad(cl.res.IsErr())
	}
	var evs core.Evictions[K, V]
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if n != nil && !cl.invalidated {
			evs, _ = e.InsertIf(cl.expected, n)
		}
		delete(s.calls, key)
	}()
	f.active.Add(-1)
	close(cl.done)
	e.Notify(evs)
}

func (f *inflight[K, V]) invalidate(h uint64, key K) {
	if f.active.Load() == 0 {
		return
	}
	s := f.shard(h)
	s.mu.Lock()
	if cl, ok := s.calls[key]; ok {
		cl.invalidated = true
	}
	s.mu.Unlock()
}

func (f *inflight[K, V]) invalidateAll() {
	if f.active.Load() == 0 {
		return
	}
	for i := range f.shards {
		s := &f.shards[i]
		s.mu.Lock()
		for _, cl := range s.calls {
			cl.invalidated = true
		}
		s.mu.Unlock()
	}
}
