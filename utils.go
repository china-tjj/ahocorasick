package ahocorasick

import (
	"cmp"
	"sort"
)

type kvSorter[K cmp.Ordered, V any] struct {
	kList []K
	vList []V
}

func (s *kvSorter[K, V]) Len() int {
	return len(s.kList)
}

func (s *kvSorter[K, V]) Less(i, j int) bool {
	return s.kList[i] < s.kList[j]
}

func (s *kvSorter[K, V]) Swap(i, j int) {
	s.kList[i], s.kList[j] = s.kList[j], s.kList[i]
	s.vList[i], s.vList[j] = s.vList[j], s.vList[i]
}

func sortKVList[K cmp.Ordered, V any](kList []K, vList []V) {
	sort.Sort(&kvSorter[K, V]{
		kList: kList,
		vList: vList,
	})
}

type uints interface {
	uint8 | uint16 | uint32 | uint64
}

type ints interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

// i, j >= 0 时与 (i + j) / 2 一致
func mid[T ints](i, j T) T {
	return (i & j) + ((i ^ j) >> 1)
}

func reverse[U any](s []U) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func binSearch[U ints, E cmp.Ordered](s []E, left, right U, target E) (U, bool) {
	for left < right {
		m := mid(left, right)
		if s[m] == target {
			return m, true
		} else if s[m] < target {
			left = m + 1
		} else {
			right = m
		}
	}
	return 0, false
}
