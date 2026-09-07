package ahocorasick

import "math/bits"

// bitmap 对比 big.Int 当做 bitmap 差别为扩容策略不一样
// big.Int 为每次 +4
// bitmap 为用 append，大约是每次 *2
type bitmap struct {
	arr []uint
}

// Reserve 容量不足时扩容到指定的容量
func (b *bitmap) Reserve(size int) {
	baseSize := (size-1)/bits.UintSize + 1
	if baseSize <= len(b.arr) {
		return
	}
	arr := make([]uint, baseSize)
	copy(arr, b.arr)
	b.arr = arr
}

// Ensure 确保索引i不会越界，通过append扩容
func (b *bitmap) Ensure(i int) {
	baseSize := i/bits.UintSize + 1
	if baseSize <= len(b.arr) {
		return
	}
	b.arr = append(b.arr, make([]uint, baseSize-len(b.arr))...)
}

func (b *bitmap) Get(i int) bool {
	idx := i / bits.UintSize
	return idx < len(b.arr) && b.arr[idx]&(1<<(i%bits.UintSize)) != 0
}

func (b *bitmap) Set(i int, v bool) {
	b.Ensure(i)
	if v {
		b.arr[i/bits.UintSize] |= 1 << (i % bits.UintSize)
	} else {
		b.arr[i/bits.UintSize] &= ^(1 << (i % bits.UintSize))
	}
}
