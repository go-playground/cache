// Package xhash hashes comparable keys, with a fast path for integer keys.
//
// Strings deliberately use maphash.Comparable: benchmarks show it matches maphash.String.
package xhash

import (
	"hash/maphash"
	"reflect"
	"unsafe"
)

type kind uint8

const (
	kindComparable kind = iota
	kindInt
)

// Hasher hashes keys of type K with a per-instance random seed.
type Hasher[K comparable] struct {
	seed    maphash.Seed
	intSeed uint64
	kind    kind
}

// New returns a Hasher with a random seed, selecting the fastest strategy for K once up front.
func New[K comparable]() Hasher[K] {
	seed := maphash.MakeSeed()
	return Hasher[K]{
		seed:    seed,
		intSeed: maphash.Comparable(seed, uint64(0x9E3779B97F4A7C15)),
		kind:    kindOf[K](),
	}
}

func kindOf[K comparable]() kind {
	switch reflect.TypeFor[K]().Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return kindInt
	default:
		return kindComparable
	}
}

// Hash returns a well mixed 64-bit hash of k.
func (h Hasher[K]) Hash(k K) uint64 {
	switch h.kind {
	case kindInt:
		return mix(h.intSeed ^ toUint64(k))
	default:
		return maphash.Comparable(h.seed, k)
	}
}

func toUint64[K comparable](k K) uint64 {
	switch unsafe.Sizeof(k) {
	case 1:
		return uint64(*(*uint8)(unsafe.Pointer(&k)))
	case 2:
		return uint64(*(*uint16)(unsafe.Pointer(&k)))
	case 4:
		return uint64(*(*uint32)(unsafe.Pointer(&k)))
	default:
		return *(*uint64)(unsafe.Pointer(&k))
	}
}

// mix is the splitmix64 finalizer.
func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}
