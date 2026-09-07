package ahocorasick

import "fmt"

type deque[U any] struct {
	data []U // 底层存储数组
	i    int // 队首指针
	len  int // 元素个数
}

func (q *deque[U]) Reserve(size int) {
	if size <= cap(q.data) {
		return
	}
	oldData := q.data
	q.data = make([]U, size)
	if q.len == 0 {
		q.i = 0
		return
	}
	if q.i+q.len <= cap(oldData) {
		// 数据连续，直接拷贝
		copy(q.data, oldData[q.i:q.i+q.len])
	} else {
		// 数据绕回，分两段拷贝
		n := cap(oldData) - q.i
		copy(q.data[:n], oldData[q.i:cap(oldData)])
		copy(q.data[n:], oldData[:(q.i+q.len)%cap(oldData)])
	}
	q.i = 0 // 重置队首指针
}

func (q *deque[U]) Len() int {
	return q.len
}

func (q *deque[U]) checkSize() {
	if q.len < cap(q.data) {
		return
	}
	newCap := q.len * 2
	if newCap < 4 {
		newCap = 4
	}
	q.Reserve(newCap)
}

func (q *deque[U]) PushFront(v U) {
	q.checkSize()
	q.i = (q.i - 1 + cap(q.data)) % cap(q.data)
	q.data[q.i] = v
	q.len++
}

func (q *deque[U]) PushBack(v U) {
	q.checkSize()
	q.data[(q.i+q.len)%cap(q.data)] = v
	q.len++
}

func (q *deque[U]) Front() (v U) {
	if q.len == 0 {
		panic("deque is empty")
	}
	return q.data[q.i]
}

func (q *deque[U]) PopFront() {
	if q.len == 0 {
		panic("deque is empty")
	}
	var zero U
	q.data[q.i] = zero
	q.i = (q.i + 1) % cap(q.data)
	q.len--
}

func (q *deque[U]) Back() (v U) {
	if q.len == 0 {
		panic("deque is empty")
	}
	return q.data[(q.i+q.len-1)%cap(q.data)]
}

func (q *deque[U]) PopBack() {
	if q.len == 0 {
		panic("deque is empty")
	}
	var zero U
	q.data[(q.i+q.len-1)%cap(q.data)] = zero
	q.len--
}

func (q *deque[U]) Get(i int) U {
	if i < 0 || i >= q.len {
		panic(fmt.Sprintf("index %d out of range, len = %d", i, q.len))
	}
	return q.data[(q.i+i)%cap(q.data)]
}
