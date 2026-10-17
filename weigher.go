package cache

import "math"

// Weigher returns an entry's weight towards a weighted cache's capacity.
type Weigher[K comparable, V any] func(key K, value V) uint32

// Sizer is implemented by values that report their own size, for use with WeighBySize.
type Sizer interface {
	Size() int
}

// WeighByLen weighs an entry by the length of its value, e.g. bytes for a []byte cache.
func WeighByLen[K comparable, V ~string | ~[]byte](_ K, value V) uint32 {
	return clampWeight(len(value))
}

// WeighByKeyAndLen weighs an entry by the length of its key plus the length of its value.
func WeighByKeyAndLen[K ~string, V ~string | ~[]byte](key K, value V) uint32 {
	return clampWeight(len(key) + len(value))
}

// WeighBySize weighs an entry by its value's Size method.
func WeighBySize[K comparable, V Sizer](_ K, value V) uint32 {
	return clampWeight(value.Size())
}

// clampWeight bounds a size to [1, MaxUint32] so empty values still count and huge ones cannot wrap.
func clampWeight(n int) uint32 {
	if n < 1 {
		return 1
	}
	return uint32(min(uint64(n), math.MaxUint32))
}
