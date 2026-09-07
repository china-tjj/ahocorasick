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
