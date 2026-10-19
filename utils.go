package ahocorasick

import (
	"cmp"
	"sort"
	"unicode/utf8"
	"unsafe"
)

type sorter[T cmp.Ordered] struct {
	l    []T
	swap func(i, j int)
}

func (s *sorter[T]) Len() int {
	return len(s.l)
}

func (s *sorter[T]) Less(i, j int) bool {
	return s.l[i] < s.l[j]
}

func (s *sorter[T]) Swap(i, j int) {
	s.l[i], s.l[j] = s.l[j], s.l[i]
	s.swap(i, j)
}

// swap 不可交换 l
func sortWithSwap[T cmp.Ordered](l []T, swap func(i, j int)) {
	sort.Sort(&sorter[T]{
		l:    l,
		swap: swap,
	})
}

type uints interface {
	uint8 | uint16 | uint32 | uint64
}

type ints interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

type char interface {
	byte | rune
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

func isByte[C char]() bool {
	return unsafe.Sizeof(C(0)) == 1
}

func decodeCharInString[C char](s string) (C, int, bool) {
	if isByte[C]() {
		return C(s[0]), 1, true
	}
	r, size := utf8.DecodeRuneInString(s)
	return C(r), size, r != utf8.RuneError || size != 1
}

func charLen[C char](s string) int {
	if isByte[C]() {
		return len(s)
	}
	return utf8.RuneCountInString(s)
}

func lcpCharLen[C char](a, b string) int {
	if isByte[C]() {
		n := min(len(a), len(b))
		i := 0
		for i < n && a[i] == b[i] {
			i++
		}
		return i
	}
	// 没考虑非法 utf8 字符
	l := 0
	for len(a) > 0 && len(b) > 0 {
		ra, sa := utf8.DecodeRuneInString(a)
		rb, sb := utf8.DecodeRuneInString(b)
		if ra != rb {
			break
		}
		l++
		a, b = a[sa:], b[sb:]
	}
	return l
}
