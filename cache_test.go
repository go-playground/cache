package cache_test

import (
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cache "github.com/go-playground/cache/v2"
	"github.com/go-playground/cache/v2/lfu"
	"github.com/go-playground/cache/v2/lru"
	"github.com/go-playground/cache/v2/s3fifo"
	"github.com/go-playground/cache/v2/tinylfu"
)

type build func(capacity uint64) *cache.Cache[string, int]

func builders() map[string]build {
	return map[string]build{
		"lru":     func(c uint64) *cache.Cache[string, int] { return lru.New[string, int](c).Build() },
		"lfu":     func(c uint64) *cache.Cache[string, int] { return lfu.New[string, int](c).Build() },
		"tinylfu": func(c uint64) *cache.Cache[string, int] { return tinylfu.New[string, int](c).Build() },
		"s3fifo":  func(c uint64) *cache.Cache[string, int] { return s3fifo.New[string, int](c).Build() },
	}
}

func TestEveryPolicyBuildsAWorkingCache(t *testing.T) {
	for name, b := range builders() {
		t.Run(name, func(t *testing.T) {
			c := b(100)
			c.Set("a", 1)
			if v := c.Get("a"); v.IsNone() || v.Unwrap() != 1 {
				t.Fatalf("got %v", v)
			}
			c.Remove("a")
			if c.Get("a").IsSome() {
				t.Fatal("removed key visible")
			}
			for i := range 1000 {
				c.Set(fmt.Sprint(i), i)
			}
			if s := c.Stats(); s.Len > 100 || s.Capacity != 100 {
				t.Fatalf("capacity not enforced: %+v", s)
			}
			c.Clear()
			if s := c.Stats(); s.Len != 0 {
				t.Fatalf("not cleared: %+v", s)
			}
		})
	}
}

func TestZeroCapacityRetainsNothing(t *testing.T) {
	for name, b := range builders() {
		t.Run(name, func(t *testing.T) {
			c := b(0)
			c.Set("a", 1)
			cache.Drain(c)
			if c.Get("a").IsSome() || c.Stats().Len != 0 {
				t.Fatal("zero capacity cache retained an entry")
			}
		})
	}
}

func TestEachBuildIsIndependent(t *testing.T) {
	b := lru.New[string, int](10)
	c1, c2 := b.Build(), b.Build()
	c1.Set("a", 1)
	if c2.Get("a").IsSome() {
		t.Fatal("caches built from one builder must not share state")
	}
}

func TestZeroValueCachePanicsWithGuidance(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(fmt.Sprint(r), "policy builder") {
			t.Fatalf("expected guidance panic, got %v", r)
		}
	}()
	var c cache.Cache[string, int]
	c.Get("a")
}

// TestBuildersExposeTheSameCommonOptions keeps the hand-written builders in sync.
func TestBuildersExposeTheSameCommonOptions(t *testing.T) {
	common := []string{"Build", "ExpireAfter", "ExpireAfterAccess", "ExpireAfterWrite", "OnEviction", "RecordStats"}
	types := map[string]reflect.Type{
		"lru":     reflect.TypeFor[*lru.Builder[string, int]](),
		"lfu":     reflect.TypeFor[*lfu.Builder[string, int]](),
		"tinylfu": reflect.TypeFor[*tinylfu.Builder[string, int]](),
		"s3fifo":  reflect.TypeFor[*s3fifo.Builder[string, int]](),
	}
	ref := types["lru"]
	for name, typ := range types {
		for _, m := range common {
			got, ok := typ.MethodByName(m)
			if !ok {
				t.Fatalf("%s builder lacks %s", name, m)
			}
			want, _ := ref.MethodByName(m)
			if params(got.Type) != params(want.Type) {
				t.Fatalf("%s.%s parameters %s differ from lru's %s", name, m, params(got.Type), params(want.Type))
			}
		}
	}
}

// params describes a method's parameters, excluding the receiver.
func params(fn reflect.Type) string {
	var sb strings.Builder
	for i := 1; i < fn.NumIn(); i++ {
		sb.WriteString(fn.In(i).String() + ";")
	}
	return sb.String()
}

func TestPolicySpecificOptions(t *testing.T) {
	c := lfu.New[string, int](2).Aging(1).Build()
	c.Set("a", 1)
	c.Set("b", 2)
	for range 10 {
		c.Get("a")
	}
	c.Set("c", 3)
	cache.Drain(c)
	if c.Stats().Len != 2 {
		t.Fatal("aging lfu must still enforce capacity")
	}

	tc := tinylfu.New[string, int](100).Adaptive(false).Build()
	tc.Set("a", 1)
	if tc.Get("a").IsNone() {
		t.Fatal("non-adaptive tinylfu must work")
	}
}

type blob []byte

type sized struct{ n int }

func (s sized) Size() int { return s.n }

func TestBuiltInWeighers(t *testing.T) {
	byLen := s3fifo.NewWeighted[string, []byte](10, cache.WeighByLen).Build()
	byLen.Set("a", make([]byte, 6))
	byLen.Set("b", make([]byte, 6))
	cache.Drain(byLen)
	if s := byLen.Stats(); s.Weight > 10 || s.Len != 1 {
		t.Fatalf("WeighByLen not applied: %+v", s)
	}

	named := lru.NewWeighted[string, blob](100, cache.WeighByLen).Build()
	named.Set("a", blob("12345"))
	if w := named.Stats().Weight; w != 5 {
		t.Fatalf("named byte slice weight %d, want 5", w)
	}

	keyAndLen := lru.NewWeighted[string, string](100, cache.WeighByKeyAndLen).Build()
	keyAndLen.Set("key", "value")
	if w := keyAndLen.Stats().Weight; w != 8 {
		t.Fatalf("WeighByKeyAndLen weight %d, want 8", w)
	}

	bySize := tinylfu.NewWeighted[string, sized](100, cache.WeighBySize).Build()
	bySize.Set("a", sized{40})
	bySize.Set("b", sized{0})
	if w := bySize.Stats().Weight; w != 41 {
		t.Fatalf("WeighBySize weight %d, want 41 (empty values weigh 1)", w)
	}

	custom := lfu.NewWeighted[string, int](100, func(_ string, v int) uint32 { return uint32(v) }).Build()
	custom.Set("a", 7)
	if w := custom.Stats().Weight; w != 7 {
		t.Fatalf("custom weigher weight %d, want 7", w)
	}

	unit := lru.NewWeighted[string, int](100, nil).Build()
	unit.Set("a", 7)
	if w := unit.Stats().Weight; w != 1 {
		t.Fatalf("nil weigher weight %d, want 1", w)
	}
}

func TestExpiryOptions(t *testing.T) {
	var now atomic.Int64
	now.Store(1)
	c := lru.New[string, int](10).ExpireAfterWrite(time.Second).Build()
	cache.SetClockForTesting(c, now.Load)
	c.Set("a", 1)
	now.Add(int64(2 * time.Second))
	if c.Get("a").IsSome() {
		t.Fatal("ExpireAfterWrite not applied")
	}

	last := lru.New[string, int](10).ExpireAfterWrite(time.Second).ExpireAfterAccess(time.Hour).Build()
	cache.SetClockForTesting(last, now.Load)
	last.Set("a", 1)
	now.Add(int64(2 * time.Second))
	if last.Get("a").IsNone() {
		t.Fatal("the last expiry setting must win")
	}

	custom := s3fifo.New[string, int](10).ExpireAfter(func(_ string, v int) time.Duration {
		return time.Duration(v) * time.Second
	}).Build()
	cache.SetClockForTesting(custom, now.Load)
	custom.Set("short", 1)
	custom.Set("long", 100)
	now.Add(int64(5 * time.Second))
	if custom.Get("short").IsSome() || custom.Get("long").IsNone() {
		t.Fatal("ExpireAfter not applied per entry")
	}
}

func TestOnEvictionReportsCause(t *testing.T) {
	var now atomic.Int64
	now.Store(1)
	var sizeEvictions, expiryEvictions atomic.Int64
	c := tinylfu.New[string, int](1).
		ExpireAfterWrite(time.Second).
		OnEviction(func(_ string, _ int, cause cache.Cause) {
			switch cause {
			case cache.CauseSize:
				sizeEvictions.Add(1)
			case cache.CauseExpired:
				expiryEvictions.Add(1)
			}
		}).
		Build()
	cache.SetClockForTesting(c, now.Load)
	c.Set("a", 1)
	c.Set("b", 2)
	cache.Drain(c)
	now.Add(int64(2 * time.Second))
	cache.Drain(c)
	if sizeEvictions.Load() != 1 || expiryEvictions.Load() != 1 {
		t.Fatalf("size %d expiry %d, want 1 and 1", sizeEvictions.Load(), expiryEvictions.Load())
	}
	if cache.CauseSize.String() != "size" || cache.CauseExpired.String() != "expired" || cache.Cause(0).String() != "unknown" {
		t.Fatal("unexpected Cause strings")
	}
}

func TestStats(t *testing.T) {
	c := lru.New[string, int](10).RecordStats().Build()
	c.Set("a", 1)
	c.Get("a")
	c.Get("b")
	s := c.Stats()
	if s.Hits != 1 || s.Misses != 1 || s.Gets() != 2 || s.HitRatio() != 0.5 || s.Sets != 1 || s.Len != 1 {
		t.Fatalf("unexpected stats %+v", s)
	}
	if (cache.Stats{}).HitRatio() != 0 {
		t.Fatal("hit ratio with no lookups must be 0")
	}
}
