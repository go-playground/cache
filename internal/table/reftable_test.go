package table

import (
	"github.com/go-playground/cache/v2/internal/xruntime"
	"sync"

	"github.com/go-playground/cache/v2/internal/node"
)

const refShards = 64

type refShard[K comparable, V any] struct {
	mu sync.RWMutex
	m  map[K]*node.Node[K, V]
	_  [xruntime.CacheLine - 32]byte
}

// refTable is the oracle: a sharded RWMutex map with the same API as Table.
type refTable[K comparable, V any] struct {
	shards [refShards]refShard[K, V]
}

func newRefTable[K comparable, V any](sizeHint int) *refTable[K, V] {
	r := &refTable[K, V]{}
	for i := range r.shards {
		r.shards[i].m = make(map[K]*node.Node[K, V], max(sizeHint, 0)/refShards)
	}
	return r
}

func (r *refTable[K, V]) shard(hash uint64) *refShard[K, V] {
	return &r.shards[hash>>58]
}

func (r *refTable[K, V]) Get(hash uint64, key K) *node.Node[K, V] {
	s := r.shard(hash)
	s.mu.RLock()
	n := s.m[key]
	s.mu.RUnlock()
	return n
}

func (r *refTable[K, V]) Store(n *node.Node[K, V]) *node.Node[K, V] {
	s := r.shard(n.Hash)
	s.mu.Lock()
	old := s.m[n.Key]
	s.m[n.Key] = n
	s.mu.Unlock()
	return old
}

func (r *refTable[K, V]) Delete(hash uint64, key K) *node.Node[K, V] {
	s := r.shard(hash)
	s.mu.Lock()
	old := s.m[key]
	delete(s.m, key)
	s.mu.Unlock()
	return old
}

func (r *refTable[K, V]) DeleteNode(n *node.Node[K, V]) bool {
	s := r.shard(n.Hash)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m[n.Key] != n {
		return false
	}
	delete(s.m, n.Key)
	return true
}

func (r *refTable[K, V]) Range(fn func(n *node.Node[K, V]) bool) {
	var buf []*node.Node[K, V]
	for i := range r.shards {
		s := &r.shards[i]
		s.mu.RLock()
		buf = buf[:0]
		for _, n := range s.m {
			buf = append(buf, n)
		}
		s.mu.RUnlock()
		for _, n := range buf {
			if !fn(n) {
				return
			}
		}
	}
}

func (r *refTable[K, V]) Len() int {
	total := 0
	for i := range r.shards {
		s := &r.shards[i]
		s.mu.RLock()
		total += len(s.m)
		s.mu.RUnlock()
	}
	return total
}
